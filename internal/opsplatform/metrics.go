// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

// Пределы одного запроса метрик (ADR-0032 §3). Заданы кодом, а не настройками: изменение — новым ADR.
const (
	// MetricsQueryTimeout — deadline одного запроса (в пределах deadline операции).
	MetricsQueryTimeout = 10 * time.Second
	// MetricsMaxResponseBytes — предельный размер ответа Prometheus.
	MetricsMaxResponseBytes = 1 << 20
	// MetricsMaxSeries — предельное число серий в ответе.
	MetricsMaxSeries = 500

	// maxMetricsExprBytes — предельная длина выражения: каталог сигналов строит короткие запросы, длинное — не оттуда.
	maxMetricsExprBytes = 4096
)

// MetricSample — одна серия мгновенного вектора Prometheus: метки и одно значение. NaN и ±Inf передаются как есть:
// решает потребитель (каталог сигналов трактует их как отсутствие данных, а не как 0).
type MetricSample struct {
	Labels map[string]string
	Value  float64
	At     time.Time
}

// MetricsReader — безопасная передача мгновенного PromQL-запроса к Prometheus встроенного стека в контексте одной
// операции (ADR-0032 §3). Это отдельный интерфейс, а не методы Reader: контракт Reader заморожен, а добавление
// методов ломает его реализации и подставки. Выражение принимает только код каталога сигналов (internal/signals),
// интерфейсу пользователя произвольные запросы не доступны. Ошибки — ErrNotFound, ErrForbidden, ErrUnavailable,
// ErrTooLarge (errors.Is).
type MetricsReader interface {
	Query(ctx context.Context, expr string, at time.Time) ([]MetricSample, error)
}

// MetricsConfig — координаты стека мониторинга для API-прокси Kubernetes. Заполняет слой связки из единственного
// источника правды в internal/k8s (k8s.MetricsNamespace и k8s.PrometheusService); платформа их не дублирует.
type MetricsConfig struct {
	// Namespace — namespace стека (kubron-metrics).
	Namespace string
	// Service — имя «сервис:порт» для services/proxy (prometheus:web).
	Service string
}

// Configured сообщает, что координаты стека заполнены; иначе Op.Metrics остаётся nil.
func (c MetricsConfig) Configured() bool { return c.Namespace != "" && c.Service != "" }

// queryCounter — необязательная возможность MetricsReader: число выполненных запросов (реализация платформы её имеет).
type queryCounter interface{ Queries() int }

// MetricsQueries возвращает число запросов метрик, выполненных в операции, для Op.Audit (ADR-0032 §8): в аудит идёт
// только число, без выражений и значений. Метрик нет или читатель счётчика не ведёт — 0.
func (o *Op) MetricsQueries() int {
	if o == nil {
		return 0
	}
	if c, ok := o.Metrics.(queryCounter); ok {
		return c.Queries()
	}
	return 0
}

// metricsLimits — пределы запроса; в тестах уменьшаются, в платформе равны константам выше.
type metricsLimits struct {
	timeout   time.Duration
	maxBytes  int64
	maxSeries int
}

var defaultMetricsLimits = metricsLimits{
	timeout: MetricsQueryTimeout, maxBytes: MetricsMaxResponseBytes, maxSeries: MetricsMaxSeries,
}

// metricsReader — MetricsReader поверх клиентов подключения ЭТОЙ сессии: запрос идёт через services/proxy сервиса
// стека тем же механизмом, что k8s.Manager.promQuery, но внутри платформы — с общим лимитером кластера и deadline
// (пакет internal/k8s не импортируется). Один экземпляр живёт одну операцию.
type metricsReader struct {
	clusterID string
	core      kubernetes.Interface
	cfg       MetricsConfig
	lim       *ClusterLimiter
	l         metricsLimits
	queries   atomic.Int64
}

// NewMetricsReader создаёт MetricsReader кластера clusterID. lim — общий лимитер процесса (nil — SharedLimiter()).
// Платформа вызывает его из Provider.Begin; домены читателя не создают.
func NewMetricsReader(clusterID string, c Clients, lim *ClusterLimiter, cfg MetricsConfig) MetricsReader {
	return newMetricsReader(clusterID, c.Core, lim, cfg, defaultMetricsLimits)
}

func newMetricsReader(clusterID string, core kubernetes.Interface, lim *ClusterLimiter, cfg MetricsConfig, l metricsLimits) *metricsReader {
	if lim == nil {
		lim = SharedLimiter()
	}
	return &metricsReader{clusterID: clusterID, core: core, cfg: cfg, lim: lim, l: l}
}

// Queries — число запросов, принятых к выполнению в операции (включая неудачные); только число, без выражений.
func (r *metricsReader) Queries() int { return int(r.queries.Load()) }

// Query выполняет мгновенный запрос на момент at (нулевой at — «сейчас» по часам Prometheus) и возвращает вектор.
// Ошибки Prometheus и сети — ErrUnavailable, отсутствие сервиса — ErrNotFound, отказ RBAC — ErrForbidden, ответ больше
// предела — ErrTooLarge. Текст ошибки не содержит выражения и значений ответа: он может попасть в журнал.
func (r *metricsReader) Query(ctx context.Context, expr string, at time.Time) ([]MetricSample, error) {
	if err := checkMetricsExpr(expr); err != nil {
		return nil, err
	}
	r.queries.Add(1)

	ctx, cancel := context.WithTimeout(ctx, r.l.timeout)
	defer cancel()
	release, err := r.lim.Acquire(ctx, r.clusterID)
	if err != nil {
		return nil, err
	}
	defer release()

	// У подставок клиента Kubernetes (fake) REST-клиент — типизированный nil: без проверки вызов Get() привёл бы к
	// панике, а не к ошибке. Тип rest.Interface не называем: ops-пакеты не импортируют client-go/rest (ADR-0026).
	if r.core == nil || isNilValue(r.core.CoreV1().RESTClient()) {
		return nil, fmt.Errorf("%w: metrics transport is not available", ErrUnavailable)
	}
	req := r.core.CoreV1().RESTClient().Get().
		Namespace(r.cfg.Namespace).
		Resource("services").
		Name(r.cfg.Service).
		SubResource("proxy").
		Suffix("api/v1/query").
		Param("query", expr)
	if !at.IsZero() {
		req = req.Param("time", strconv.FormatFloat(float64(at.UnixMilli())/1000, 'f', 3, 64))
	}
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, metricsTransportError(err)
	}
	defer stream.Close()
	// Читаем на байт больше предела: так видно, что ответ не поместился (ErrTooLarge), а не молча обрезан.
	body, err := io.ReadAll(io.LimitReader(stream, r.l.maxBytes+1))
	if err != nil {
		return nil, metricsTransportError(err)
	}
	if int64(len(body)) > r.l.maxBytes {
		return nil, ErrTooLarge
	}
	return parseVector(body, r.l.maxSeries)
}

// isNilValue сообщает, что интерфейс пуст или хранит nil-указатель.
func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// checkMetricsExpr отсекает выражения вне допустимого набора: пустые, слишком длинные, не UTF-8 или с управляющими
// символами. Содержательную проверку делает каталог сигналов (internal/signals): платформа лишь не даёт передать мусор.
func checkMetricsExpr(expr string) error {
	switch {
	case strings.TrimSpace(expr) == "":
		return fmt.Errorf("%w: empty query", ErrForbidden)
	case len(expr) > maxMetricsExprBytes:
		return fmt.Errorf("%w: query is too long", ErrForbidden)
	case !utf8.ValidString(expr):
		return fmt.Errorf("%w: query is not valid text", ErrForbidden)
	}
	for _, r := range expr {
		if r < 0x20 && r != '\t' && r != '\n' || r == 0x7f {
			return fmt.Errorf("%w: query has control characters", ErrForbidden)
		}
	}
	return nil
}

// metricsTransportError приводит ошибку запроса к ошибкам контракта. В отличие от translate, исходная ошибка в цепочку
// не входит: client-go вкладывает в её текст полный URL с выражением (url.Error) и тело ответа apiserver, а Prometheus
// в тексте ошибки нередко повторяет запрос. Такой текст попал бы в журнал и аудит операции (ADR-0032 §8), поэтому
// остаются только вид ошибки и код статуса. Отмена и deadline сохраняются в цепочке: errors.Is с ними работает.
func metricsTransportError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrUnavailable, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: %w", ErrUnavailable, context.Canceled)
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%w: metrics service was not found", ErrNotFound)
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return fmt.Errorf("%w: access to the metrics service was denied", ErrForbidden)
	case apierrors.IsRequestEntityTooLargeError(err):
		return ErrTooLarge
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		return fmt.Errorf("%w: http status %d", ErrUnavailable, status.Status().Code)
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return fmt.Errorf("%w: transport error: %v", ErrUnavailable, ue.Err)
	}
	return fmt.Errorf("%w: request failed", ErrUnavailable)
}

// promResponse — ответ /api/v1/query. Поле error (текст Prometheus) намеренно не читается: оно может содержать
// выражение запроса.
type promResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Data      struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

type promSeries struct {
	Metric map[string]string  `json:"metric"`
	Value  [2]json.RawMessage `json:"value"`
}

// parseVector разбирает ответ Prometheus: только status=success и resultType=vector. Всё остальное — ErrUnavailable
// (источник ответил не так, как ожидается), превышение числа серий — ErrTooLarge. Результат упорядочен по меткам.
func parseVector(body []byte, maxSeries int) ([]MetricSample, error) {
	var resp promResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("%w: malformed response", ErrUnavailable)
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("%w: prometheus status %q (%s)", ErrUnavailable, clipRunes(resp.Status, 16), clipRunes(resp.ErrorType, 32))
	}
	if resp.Data.ResultType != "vector" {
		return nil, fmt.Errorf("%w: unsupported result type %q", ErrUnavailable, clipRunes(resp.Data.ResultType, 16))
	}
	var series []promSeries
	if err := json.Unmarshal(resp.Data.Result, &series); err != nil {
		return nil, fmt.Errorf("%w: malformed vector", ErrUnavailable)
	}
	if len(series) > maxSeries {
		return nil, ErrTooLarge
	}
	out := make([]MetricSample, 0, len(series))
	for _, s := range series {
		var ts float64
		var raw string
		if json.Unmarshal(s.Value[0], &ts) != nil || json.Unmarshal(s.Value[1], &raw) != nil {
			return nil, fmt.Errorf("%w: malformed sample", ErrUnavailable)
		}
		v, err := strconv.ParseFloat(raw, 64) // понимает NaN, +Inf и -Inf
		if err != nil {
			return nil, fmt.Errorf("%w: malformed sample value", ErrUnavailable)
		}
		labels := make(map[string]string, len(s.Metric))
		for k, val := range s.Metric {
			labels[k] = val
		}
		out = append(out, MetricSample{Labels: labels, Value: v, At: time.UnixMilli(int64(math.Round(ts * 1000))).UTC()})
	}
	slices.SortStableFunc(out, func(a, b MetricSample) int {
		return cmp.Compare(labelsKey(a.Labels), labelsKey(b.Labels))
	})
	return out, nil
}

// labelsKey — стабильный ключ набора меток для упорядочивания серий.
func labelsKey(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(m[k])
		b.WriteByte(0)
	}
	return b.String()
}
