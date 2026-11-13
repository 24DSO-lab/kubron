// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package opsplatform — общая платформа операционных модулей Kubron Intelligence и Lifecycle (ADR-0026).
//
// Пакет содержит контракт, которым пользуются домены (incident, diagnose, readiness, baseline, drift):
//
//   - Reader — безопасное чтение кластера (read-only, allowlist, deadline, лимит ответа, лимитер на кластер);
//   - Provider.Begin — серверная проверка права и принадлежности кластера команде ДО чтения, tenant.Scope из каталога
//     Kubron и контекст выполнения Op;
//   - Op — контекст одной операции: повторная проверка права, аудит, адресное событие, технический журнал.
//
// Правила для потребителей:
//
//   - домены не строят rest.Config и не создают клиентов Kubernetes: конфигурацию даёт только ConfigFor (ADR-0018),
//     а доступ к кластеру — только Reader из Op. Это проверяет статический тест в internal/k8s;
//   - домен объявляет у себя узкий интерфейс чтения (подмножество Reader) и получает tenant.Scope параметром;
//     пакет opsplatform импортирует только слой связки (делегаты App);
//   - Secret не входит в allowlist; логи читаются только через PodLogs и редактируются до сохранения;
//   - ошибки Kubernetes Reader переводит в ErrNotFound, ErrForbidden, ErrUnavailable, ErrTooLarge — домены
//     сравнивают их через errors.Is и не разбирают текст.
package opsplatform

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Ошибки Reader. Реализация переводит в них ошибки Kubernetes; детали остаются в цепочке через %w.
var (
	// ErrNotFound — ресурса нет (или вид ресурса не существует в кластере).
	ErrNotFound = errors.New("resource not found")
	// ErrForbidden — запрос вне allowlist платформы либо отказ Kubernetes RBAC технической учётной записи.
	ErrForbidden = errors.New("access to the resource is forbidden")
	// ErrUnavailable — источник недоступен: таймаут, сеть, перегруженный API, отмена. Это не пустой успех.
	ErrUnavailable = errors.New("cluster source is unavailable")
	// ErrTooLarge — ответ превысил лимит размера (в том числе лог больше LimitBytes).
	ErrTooLarge = errors.New("response is too large")
)

// ListOptions — параметры выборки. Limit — размер страницы и верхняя граница числа объектов результата;
// 0 — значение платформы по умолчанию. Платформа не отдаёт больше своего максимума независимо от Limit.
type ListOptions struct {
	Namespace     string
	LabelSelector string
	FieldSelector string
	Limit         int64
}

// ListResult — результат List. Truncated=true — объектов больше, чем вернулось (достигнут лимит или размера,
// или числа объектов): потребитель обязан показать неполноту, а не считать результат исчерпывающим.
type ListResult struct {
	Items      []unstructured.Unstructured
	Truncated  bool
	ObservedAt time.Time
}

// LogRequest — запрос лога контейнера. TailLines и LimitBytes ограничиваются платформой сверху;
// 0 — значения по умолчанию. Previous — предыдущий экземпляр контейнера.
type LogRequest struct {
	Namespace, Pod, Container string
	Previous                  bool
	TailLines, LimitBytes     int64
}

// Reader — read-only чтение кластера в контексте одной операции. Все методы принимают ctx первым аргументом:
// отмена и deadline прерывают запрос. Число одновременных чтений на кластер ограничено общим лимитером.
// Secret чтению не подлежит ни при каких параметрах (ErrForbidden).
type Reader interface {
	Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
	List(ctx context.Context, gvk schema.GroupVersionKind, opts ListOptions) (*ListResult, error)
	// PodLogs возвращает сырой лог: редактирование (logredact) и сохранение — забота домена и права diagnostics.logs.
	PodLogs(ctx context.Context, req LogRequest) ([]byte, error)
	ServerVersion(ctx context.Context) (version.Info, error)
	ServerResources(ctx context.Context) ([]*metav1.APIResourceList, error)
}

// OpRequest — запрос на начало операции. ClusterID приходит от клиента лишь как указание цели:
// принадлежность кластера команде и tenant.Scope платформа определяет сама по каталогу.
type OpRequest struct {
	Module, Operation, ClusterID string
	Permission                   auth.Permission
	// StoredOnly — операция работает только с уже сохранёнными данными (история, экспорт): подключение к кластеру не
	// строится, Op.Reader == nil, права и принадлежность кластера команде проверяются как обычно. Дополнение к
	// замороженному контракту (нулевое значение — прежнее поведение); зафиксировано в NOTES.md.
	StoredOnly bool
}

// Hooks — побочные эффекты Op. Заполняются платформой (Provider) или тестом; nil-поле безопасно:
// Require без реализации отказывает (fail closed), остальные ничего не делают.
type Hooks struct {
	Require func(p auth.Permission) error
	Audit   func(action, target string, err error, attrs ...slog.Attr)
	Notify  func(event, id string)
	End     func(err error, partial ...string)
}

// Op — контекст выполнения одной операции (ADR-0026 §3). Это логический контекст, а не доменный DTO и не таблица.
// Создаёт Provider.Begin; операцию обязательно закрыть через End (defer op.End(err)).
type Op struct {
	Scope         tenant.Scope
	Initiator     string
	SessionID     string
	CorrelationID string
	StartedAt     time.Time
	Reader        Reader

	hooks Hooks
	ended atomic.Bool
}

// NewOp собирает Op из полей шаблона base (копируются только данные, не состояние закрытия) и Hooks.
// Для платформы и тестов доменов; домены Op не конструируют.
func NewOp(base *Op, h Hooks) *Op {
	return &Op{
		Scope:         base.Scope,
		Initiator:     base.Initiator,
		SessionID:     base.SessionID,
		CorrelationID: base.CorrelationID,
		StartedAt:     base.StartedAt,
		Reader:        base.Reader,
		hooks:         h,
	}
}

// Require повторно проверяет право на сервере (например, diagnostics.logs перед выдачей сохранённого фрагмента).
// Наличие ранее открытого экрана или ID ресурса доступом не считается (ADR-0026 §1).
func (o *Op) Require(p auth.Permission) error {
	if o.hooks.Require == nil {
		return auth.ErrForbidden
	}
	return o.hooks.Require(p)
}

// Audit пишет запись аудита о значимом действии: кто, кластер, действие, цель, результат, correlation ID.
// В аудит не попадают содержимое объектов и логов: attrs содержат только идентификаторы и счётчики.
func (o *Op) Audit(action, target string, err error, attrs ...slog.Attr) {
	if o.hooks.Audit != nil {
		o.hooks.Audit(action, target, err, attrs...)
	}
}

// Notify посылает адресное событие сессии/кластера (ADR-0009). Событие несёт только имя и ID:
// клиент перезагружает данные через RPC, который повторно проверяет права.
func (o *Op) Notify(event, id string) {
	if o.hooks.Notify != nil {
		o.hooks.Notify(event, id)
	}
}

// End закрывает операцию: запись технического журнала (модуль, операция, длительность, correlation ID, статус).
// partial — описания частично недоступных источников: статус «частично», а не успех. Повторный вызов игнорируется.
func (o *Op) End(err error, partial ...string) {
	if !o.ended.CompareAndSwap(false, true) {
		return
	}
	if o.hooks.End != nil {
		o.hooks.End(err, partial...)
	}
}

// Provider начинает операции. Реализует платформа поверх существующих Manager, сессий и каталога.
//
// Begin проверяет право req.Permission и принадлежность кластера команде ДО любого чтения, определяет
// tenant.Scope по каталогу и возвращает контекст с deadline и correlation ID. Чужой или неизвестный кластер
// даёт auth.ErrClusterNotFound, отсутствие права — auth.ErrForbidden.
type Provider interface {
	Begin(ctx context.Context, req OpRequest) (context.Context, *Op, error)
}
