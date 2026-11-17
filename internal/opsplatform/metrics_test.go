// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/24DSO-lab/kubron/internal/auth"
)

const (
	testMetricsNS  = "kubron-metrics"
	testMetricsSvc = "prometheus:web"
	testQueryPath  = "/api/v1/namespaces/" + testMetricsNS + "/services/" + testMetricsSvc + "/proxy/api/v1/query"
)

var testMetricsCfg = MetricsConfig{Namespace: testMetricsNS, Service: testMetricsSvc}

// metricsClient поднимает «apiserver» с обработчиком h и возвращает настоящий клиент core/v1 к нему: запрос идёт тем же
// путём, что и в кластере (services/proxy), без подставок клиента.
func metricsClient(t *testing.T, h http.HandlerFunc) *kubernetes.Clientset {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL, QPS: -1})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func vectorBody(series ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(series, ",") + `]}}`
}

func serve(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

func newTestMetricsReader(t *testing.T, h http.HandlerFunc, l metricsLimits) *metricsReader {
	t.Helper()
	return newMetricsReader("c1", metricsClient(t, h), NewClusterLimiter(4), testMetricsCfg, l)
}

func TestMetricsQuerySuccess(t *testing.T) {
	at := time.Date(2026, 11, 16, 7, 0, 0, 500_000_000, time.UTC)
	var gotPath, gotQuery, gotTime string
	r := newTestMetricsReader(t, func(w http.ResponseWriter, req *http.Request) {
		gotPath, gotQuery, gotTime = req.URL.Path, req.URL.Query().Get("query"), req.URL.Query().Get("time")
		serve(200, vectorBody(
			`{"metric":{"pod":"b","namespace":"ns"},"value":[1700000000.25,"2"]}`,
			`{"metric":{"pod":"a","namespace":"ns"},"value":[1700000000.25,"0.5"]}`,
			`{"metric":{},"value":[1700000000,"NaN"]}`,
		))(w, req)
	}, defaultMetricsLimits)

	got, err := r.Query(t.Context(), `up{job="x"}`, at)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != testQueryPath {
		t.Errorf("path = %q, want %q", gotPath, testQueryPath)
	}
	if gotQuery != `up{job="x"}` {
		t.Errorf("query = %q", gotQuery)
	}
	if gotTime != "1794812400.500" {
		t.Errorf("time = %q, want the operation moment with millisecond precision", gotTime)
	}
	if len(got) != 3 {
		t.Fatalf("got %d samples, want 3", len(got))
	}
	// Серии упорядочены по меткам: пустой набор, затем pod=a, затем pod=b.
	if len(got[0].Labels) != 0 || got[1].Labels["pod"] != "a" || got[2].Labels["pod"] != "b" {
		t.Errorf("samples are not ordered by labels: %v", got)
	}
	if got[1].Value != 0.5 || got[2].Value != 2 {
		t.Errorf("values = %v, %v", got[1].Value, got[2].Value)
	}
	if v := got[0].Value; v == v { // NaN передаётся как есть: решает каталог сигналов
		t.Errorf("NaN was changed to %v", v)
	}
	if !got[1].At.Equal(time.Unix(1700000000, 250_000_000)) {
		t.Errorf("sample time = %v", got[1].At)
	}
	if r.Queries() != 1 {
		t.Errorf("Queries() = %d, want 1", r.Queries())
	}
}

func TestMetricsQueryOmitsTimeForZeroMoment(t *testing.T) {
	var hasTime bool
	r := newTestMetricsReader(t, func(w http.ResponseWriter, req *http.Request) {
		hasTime = req.URL.Query().Has("time")
		serve(200, vectorBody())(w, req)
	}, defaultMetricsLimits)
	got, err := r.Query(t.Context(), "up", time.Time{})
	if err != nil || len(got) != 0 || hasTime {
		t.Errorf("got %v, %v, time param sent: %v", got, err, hasTime)
	}
}

func TestMetricsQueryInfinities(t *testing.T) {
	r := newTestMetricsReader(t, serve(200, vectorBody(
		`{"metric":{"a":"1"},"value":[1,"+Inf"]}`, `{"metric":{"a":"2"},"value":[1,"-Inf"]}`)), defaultMetricsLimits)
	got, err := r.Query(t.Context(), "up", time.Now())
	if err != nil || len(got) != 2 || !(got[0].Value > 1e308) || !(got[1].Value < -1e308) {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestMetricsQueryErrorMapping(t *testing.T) {
	notFound := `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404,"message":"services \"prometheus:web\" not found"}`
	forbidden := `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403,"message":"services/proxy is forbidden"}`
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"service not found", 404, notFound, ErrNotFound},
		{"prometheus 404 without status", 404, "404 page not found", ErrNotFound},
		{"forbidden by RBAC", 403, forbidden, ErrForbidden},
		{"unauthorized", 401, `{"kind":"Status","status":"Failure","reason":"Unauthorized","code":401}`, ErrForbidden},
		{"no endpoints", 503, "no endpoints available for service", ErrUnavailable},
		{"server error", 500, "boom", ErrUnavailable},
		{"too many requests", 429, "slow down", ErrUnavailable},
		{"payload too large", 413, `{"kind":"Status","status":"Failure","reason":"RequestEntityTooLarge","code":413}`, ErrTooLarge},
		{"prometheus rejected the query", 400, `{"status":"error","errorType":"bad_data","error":"parse error"}`, ErrUnavailable},
		{"status error in a 200 reply", 200, `{"status":"error","errorType":"execution","error":"boom"}`, ErrUnavailable},
		{"not a vector", 200, `{"status":"success","data":{"resultType":"matrix","result":[]}}`, ErrUnavailable},
		{"scalar", 200, `{"status":"success","data":{"resultType":"scalar","result":[1,"2"]}}`, ErrUnavailable},
		{"not json", 200, "<html>", ErrUnavailable},
		{"bad sample value", 200, vectorBody(`{"metric":{},"value":[1,"abc"]}`), ErrUnavailable},
		{"bad sample shape", 200, vectorBody(`{"metric":{},"value":["x",1]}`), ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestMetricsReader(t, serve(tc.status, tc.body), defaultMetricsLimits)
			got, err := r.Query(t.Context(), "up", time.Now())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got != nil {
				t.Errorf("an error must not come with partial data: %v", got)
			}
			for _, other := range []error{ErrNotFound, ErrForbidden, ErrUnavailable, ErrTooLarge} {
				if other != tc.want && errors.Is(err, other) {
					t.Errorf("err also matches %v", other)
				}
			}
		})
	}
}

// Текст ошибки может попасть в журнал и аудит: ни выражения, ни текста ответа сервера там быть не должно.
func TestMetricsErrorsDoNotLeakQueryOrResponse(t *testing.T) {
	const secret = "S3CRET-EXPR"
	expr := `up{pod="` + secret + `"}`
	echo := `{"status":"error","errorType":"bad_data","error":"parse error in ` + secret + `"}`
	for name, h := range map[string]http.HandlerFunc{
		"prometheus echoes the query": serve(400, echo),
		"apiserver echoes the body":   serve(503, "upstream said: "+secret),
		"status error echoes":         serve(200, echo),
		"forbidden message":           serve(403, `{"kind":"Status","status":"Failure","reason":"Forbidden","code":403,"message":"`+secret+`"}`),
		"not found message":           serve(404, `{"kind":"Status","status":"Failure","reason":"NotFound","code":404,"message":"`+secret+`"}`),
	} {
		t.Run(name, func(t *testing.T) {
			r := newTestMetricsReader(t, h, defaultMetricsLimits)
			_, err := r.Query(t.Context(), expr, time.Now())
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Errorf("error leaks the query or the response: %v", err)
			}
		})
	}
	t.Run("transport failure carries no URL", func(t *testing.T) {
		srv := httptest.NewServer(serve(200, vectorBody()))
		cs, _ := kubernetes.NewForConfig(&rest.Config{Host: srv.URL, QPS: -1})
		srv.Close() // соединение отказывает: client-go вернёт url.Error с полным адресом запроса
		r := newMetricsReader("c1", cs, NewClusterLimiter(1), testMetricsCfg, defaultMetricsLimits)
		_, err := r.Query(t.Context(), expr, time.Now())
		if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "/proxy/") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestMetricsQueryResponseSizeLimit(t *testing.T) {
	big := vectorBody(`{"metric":{"x":"` + strings.Repeat("a", 400) + `"},"value":[1,"1"]}`)
	l := defaultMetricsLimits
	l.maxBytes = int64(len(big)) - 1
	if _, err := newTestMetricsReader(t, serve(200, big), l).Query(t.Context(), "up", time.Now()); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a response over the limit: err = %v, want ErrTooLarge (not a truncated success)", err)
	}
	l.maxBytes = int64(len(big)) // ровно в пределе — допустимо
	if got, err := newTestMetricsReader(t, serve(200, big), l).Query(t.Context(), "up", time.Now()); err != nil || len(got) != 1 {
		t.Errorf("a response at the limit: %v, %v", got, err)
	}
}

func TestMetricsQuerySeriesLimit(t *testing.T) {
	series := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"metric":{"i":"%d"},"value":[1,"1"]}`, i)
		}
		return vectorBody(parts...)
	}
	l := defaultMetricsLimits
	l.maxSeries = 5
	if got, err := newTestMetricsReader(t, serve(200, series(5)), l).Query(t.Context(), "up", time.Now()); err != nil || len(got) != 5 {
		t.Errorf("5 series: %d, %v", len(got), err)
	}
	if _, err := newTestMetricsReader(t, serve(200, series(6)), l).Query(t.Context(), "up", time.Now()); !errors.Is(err, ErrTooLarge) {
		t.Errorf("6 series: err = %v, want ErrTooLarge", err)
	}
	if MetricsMaxSeries != 500 || MetricsMaxResponseBytes != 1<<20 || MetricsQueryTimeout != 10*time.Second {
		t.Error("platform limits differ from ADR-0032 §3")
	}
}

func TestMetricsQueryDeadline(t *testing.T) {
	release := make(chan struct{})
	r := newTestMetricsReader(t, func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-release:
		case <-req.Context().Done():
		}
	}, metricsLimits{timeout: 50 * time.Millisecond, maxBytes: MetricsMaxResponseBytes, maxSeries: MetricsMaxSeries})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	_, err := r.Query(t.Context(), "up", time.Now())
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want ErrUnavailable wrapping DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the query outlived its deadline: %v", d)
	}
}

// Deadline запроса не длиннее deadline операции: более короткий из двух.
func TestMetricsQueryRespectsOperationDeadline(t *testing.T) {
	r := newTestMetricsReader(t, func(w http.ResponseWriter, req *http.Request) { <-req.Context().Done() }, defaultMetricsLimits)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := r.Query(ctx, "up", time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the operation deadline did not cut the query")
	}
}

func TestMetricsQueryTakesLimiterSlot(t *testing.T) {
	var inflight, peak atomic.Int32
	h := func(w http.ResponseWriter, req *http.Request) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inflight.Add(-1)
		serve(200, vectorBody())(w, req)
	}
	lim := NewClusterLimiter(2)
	r := newMetricsReader("c1", metricsClient(t, h), lim, testMetricsCfg, defaultMetricsLimits)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := r.Query(t.Context(), "up", time.Now()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if p := peak.Load(); p > 2 {
		t.Errorf("peak concurrency = %d, want <= 2 (the shared cluster limiter)", p)
	}
	if lim.tracked() != 0 {
		t.Error("slots were not released")
	}
	if r.Queries() != 8 {
		t.Errorf("Queries() = %d, want 8", r.Queries())
	}
}

// Слот делится с Reader: пока он занят чтением, запрос метрик ждёт и уступает отмене контекста.
func TestMetricsQueryWaitsForSlotAndStopsOnCancel(t *testing.T) {
	lim := NewClusterLimiter(1)
	release, err := lim.Acquire(t.Context(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var calls atomic.Int32
	r := newMetricsReader("c1", metricsClient(t, func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		serve(200, vectorBody())(w, req)
	}), lim, testMetricsCfg, defaultMetricsLimits)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = r.Query(ctx, "up", time.Now())
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
	if calls.Load() != 0 {
		t.Error("the request was sent without a limiter slot")
	}
}

func TestMetricsQueryRejectsExpressionsOutsideTheAllowedSet(t *testing.T) {
	var calls atomic.Int32
	r := newTestMetricsReader(t, func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		serve(200, vectorBody())(w, req)
	}, defaultMetricsLimits)
	for name, expr := range map[string]string{
		"empty":           "",
		"blank":           " \t\n",
		"too long":        strings.Repeat("a", maxMetricsExprBytes+1),
		"control char":    "up\x00",
		"escape sequence": "up\x1b[31m",
		"invalid utf-8":   "up\xff",
		"delete":          "up\x7f",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Query(t.Context(), expr, time.Now()); !errors.Is(err, ErrForbidden) {
				t.Errorf("err = %v, want ErrForbidden", err)
			}
		})
	}
	if calls.Load() != 0 || r.Queries() != 0 {
		t.Errorf("rejected expressions must not be sent or counted: calls=%d queries=%d", calls.Load(), r.Queries())
	}
	// Многострочное выражение допустимо: каталог может переносить строки.
	if _, err := r.Query(t.Context(), "sum(\n up\n)", time.Now()); err != nil {
		t.Errorf("a multiline expression was rejected: %v", err)
	}
}

// Подставка клиента Kubernetes отдаёт типизированный nil вместо REST-клиента: это ошибка, а не паника.
func TestMetricsQueryWithoutRESTClientIsUnavailable(t *testing.T) {
	r := newMetricsReader("c1", nil, NewClusterLimiter(1), testMetricsCfg, defaultMetricsLimits)
	if _, err := r.Query(t.Context(), "up", time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil client: err = %v", err)
	}
	fakeEnv := newFakeEnv()
	c, _ := fakeEnv.Clients(t.Context(), "c1")
	r = newMetricsReader("c1", c.Core, NewClusterLimiter(1), testMetricsCfg, defaultMetricsLimits)
	if _, err := r.Query(t.Context(), "up", time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("fake client without a REST client: err = %v", err)
	}
}

func TestMetricsConfigConfigured(t *testing.T) {
	for _, tc := range []struct {
		cfg  MetricsConfig
		want bool
	}{
		{MetricsConfig{}, false},
		{MetricsConfig{Namespace: "ns"}, false},
		{MetricsConfig{Service: "prometheus:web"}, false},
		{testMetricsCfg, true},
	} {
		if got := tc.cfg.Configured(); got != tc.want {
			t.Errorf("%+v.Configured() = %v, want %v", tc.cfg, got, tc.want)
		}
	}
}

// --- Provider.Begin ---

func beginWith(t *testing.T, cfg MetricsConfig, stored bool) (*Op, *fakeEnv) {
	t.Helper()
	env := newFakeEnv()
	prov := NewProvider(env, Config{Limiter: NewClusterLimiter(2), OpTimeout: 5 * time.Second, Metrics: cfg})
	rq := req("c1", auth.PermRead)
	rq.StoredOnly = stored
	_, op, err := prov.Begin(t.Context(), rq)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { op.End(nil) })
	return op, env
}

func TestBeginBuildsMetricsOnlyWhenConfiguredAndConnected(t *testing.T) {
	if op, _ := beginWith(t, testMetricsCfg, false); op.Metrics == nil {
		t.Error("Metrics must be set for a connected operation with a configured stack")
	}
	if op, _ := beginWith(t, MetricsConfig{}, false); op.Metrics != nil {
		t.Error("metrics are not configured: Op.Metrics must stay nil")
	}
	if op, _ := beginWith(t, MetricsConfig{Namespace: testMetricsNS}, false); op.Metrics != nil {
		t.Error("incomplete coordinates must not produce a reader")
	}
}

func TestBeginStoredOnlyHasNoMetrics(t *testing.T) {
	op, env := beginWith(t, testMetricsCfg, true)
	if op.Metrics != nil || op.Reader != nil {
		t.Error("a StoredOnly operation must have neither Reader nor Metrics")
	}
	if env.clientCalls != 0 {
		t.Error("a StoredOnly operation must not connect to the cluster")
	}
	if op.MetricsQueries() != 0 {
		t.Error("no metrics, no queries")
	}
}

func TestOpMetricsQueriesCountsAndGoesToTechnicalLog(t *testing.T) {
	env := newFakeEnv()
	srv := httptest.NewServer(serve(200, vectorBody()))
	defer srv.Close()
	cs, _ := kubernetes.NewForConfig(&rest.Config{Host: srv.URL, QPS: -1})
	// Окружение отдаёт клиент с настоящим REST: платформа должна построить читатель поверх него.
	prov, buf := newProviderWithClients(t, env, cs)
	_, op, err := prov.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := op.Metrics.Query(t.Context(), "up", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if op.MetricsQueries() != 3 {
		t.Errorf("MetricsQueries() = %d, want 3", op.MetricsQueries())
	}
	op.End(nil)
	if log := buf.String(); !strings.Contains(log, "metric_queries=3") {
		t.Errorf("technical log has no query count: %s", log)
	}
}

// Платформа с настроенными метриками, но без ответа стека: операция не ломается, а ошибка — из контракта.
func TestOpMetricsFailureDoesNotBreakOperation(t *testing.T) {
	env := newFakeEnv()
	cs := metricsClient(t, serve(404, `{"kind":"Status","status":"Failure","reason":"NotFound","code":404}`))
	prov, _ := newProviderWithClients(t, env, cs)
	ctx, op, err := prov.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	defer op.End(nil)
	if _, err := op.Metrics.Query(ctx, "up", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

type clientsEnv struct {
	*fakeEnv
	core kubernetes.Interface
}

func (e clientsEnv) Clients(ctx context.Context, id string) (Clients, error) {
	c, err := e.fakeEnv.Clients(ctx, id)
	c.Core = e.core
	return c, err
}

func newProviderWithClients(t *testing.T, env *fakeEnv, core kubernetes.Interface) (Provider, *strings.Builder) {
	t.Helper()
	var buf strings.Builder
	prov := NewProvider(clientsEnv{env, core}, Config{
		Limiter: NewClusterLimiter(2), OpTimeout: 5 * time.Second, Metrics: testMetricsCfg, Logger: newTestLogger(&buf),
	})
	return prov, &buf
}

func newTestLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
