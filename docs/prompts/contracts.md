# Контракты между разработчиками

Источник истины для сигнатур, форматов JSON и имён. Реализуй **буквально**. Если реальный код не позволяет (другое имя типа и т. п.), сохрани смысл, поправь контракт отдельным PR владельца и сообщи затронутым.

Условные обозначения: **(D<N>)** — владелец. `[Фаза 0]` — входит в скелет-PR.

Во всех местах, где нужны точные типы существующего кода (`Confidence`, `Fact`, `MissingContext`, версии документов), бери их из репозитория: `internal/diagnose/types.go`, `internal/incident/model.go`.

## 1. `internal/depgraph` (D2) [Фаза 0]

```go
package depgraph

// Типы чтения — точная копия diagnose/reader.go (типы полей взять оттуда). После скелета diagnose заменяет
// свои объявления псевдонимами на эти (D3), поэтому адаптер в internal/app остаётся один.
type ListOptions struct { Namespace, LabelSelector, FieldSelector string; Limit int64 }
type ListResult struct { Items []unstructured.Unstructured; Truncated bool; ObservedAt time.Time }
type Reader interface {
    Get(ctx context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error)
    List(ctx context.Context, gvk schema.GroupVersionKind, o ListOptions) (*ListResult, error)
}
var ErrNotFound, ErrForbidden, ErrTooLarge, ErrUnavailable error // errors.New(...), тексты — как в diagnose

// Ref — идентификатор вершины; Key() = "group/kind/namespace/name"; у core-ресурсов группа в ключе — "core" ("core/Pod/ns/p").
type Ref struct{ Group, Kind, Namespace, Name string }
func (r Ref) Key() string

type Health string // "Healthy","Degraded","Progressing","Suspended","Missing","Unknown"
type UnknownReason string // "forbidden","unavailable","too_large","limit","policy"
type EdgeType string // "owns","selects","backed_by","routes","mounts","binds","runs_on","scales"
type EdgeState string // "ok","broken","missing","unknown"

type Node struct {
    Ref
    UID         string
    Health      Health
    Reason      string        // код состояния, без значений полей
    Unknown     UnknownReason // заполнено только при Health == Unknown
    Placeholder bool          // ссылка по имени на несуществующий объект
    Object      *unstructured.Unstructured // только в памяти (json:"-"); для ConfigMap data/binaryData уже отброшены
}
type Edge struct{ From, To Ref; Type EdgeType; State EdgeState; Reason string }

type Graph struct{ /* ... */ }
func New(seed Ref) *Graph
func (g *Graph) Seed() Ref
func (g *Graph) AddNode(n Node)
func (g *Graph) AddEdge(e Edge)
func (g *Graph) Node(r Ref) (Node, bool)
func (g *Graph) Nodes() []Node            // отсортировано по Key()
func (g *Graph) Edges() []Edge            // отсортировано по (From, To, Type)
func (g *Graph) EdgesFrom(r Ref, t EdgeType) []Edge
func (g *Graph) EdgesTo(r Ref, t EdgeType) []Edge
func (g *Graph) Owners(r Ref) []Node      // вверх по owns, до корня
func (g *Graph) Dependents(r Ref) []Node  // область влияния r (кто пострадает при его отказе), транзитивно, ближайшие первыми: входящие mounts/binds/runs_on/selects/routes/scales и исходящие owns. Node → Pod'ы на ней → Service → Ingress; Deployment → ReplicaSet → Pod'ы
func (g *Graph) SetTruncated(code string) // пометить граф усечённым (+ код предупреждения); для построителя и тестовых графов
func (g *Graph) AddWarning(code string)   // короткий код без имён ресурсов и значений; повторы схлопываются
func (g *Graph) Truncated() bool
func (g *Graph) Warnings() []string
func (g *Graph) Snapshot() Snapshot

type Snapshot struct {
    Nodes     []SnapshotNode `json:"nodes"`
    Edges     []SnapshotEdge `json:"edges"`
    Truncated bool           `json:"truncated,omitempty"`
    Warnings  []string       `json:"warnings,omitempty"`
}
type SnapshotNode struct { Group, Kind, Namespace, Name string; Health string; Reason, Unknown string; Placeholder bool } // JSON: group,kind,namespace,name,health,reason,unknown,placeholder (все, кроме kind/name/health, omitempty)
type SnapshotEdge struct { From, To string; Type, State, Reason string }                                               // From/To — Ref.Key()

type Limits struct{ MaxNodes, MaxEdges, MaxDepth, MaxReads int }
var DefaultLimits = Limits{MaxNodes: 200, MaxEdges: 600, MaxDepth: 4, MaxReads: 40} // ADR-0031 §3
type Options struct { Limits Limits; ExpandIngress, ExpandHPA, ExpandConfigMapRefs bool } // этап 1: все флаги false
func Build(ctx context.Context, r Reader, seed Ref, o Options) (*Graph, error) // ошибка — только программная; всё остальное в Unknown/Warnings
```

`internal/depgraph/graphtest` [Фаза 0, D2]: `type FakeReader struct{...}` (конструктор `NewFakeReader()`, нулевое значение тоже работает) реализует `depgraph.Reader`; методы `Put(*unstructured.Unstructured)`, `PutYAML(t, string)`, `Fail(gvk, err)`, `Forbid(gvk)`, `FailObject(gvk, ns, name, err)`, `Calls() int`, `CallLog() []string`. `List` понимает namespace, label-селектор, field-селектор по пути через точку (`spec.nodeName=n1`) и `Limit` (с `Truncated`). Потребители (D3, D4) встраивают его в свои фейки `diagnose.Reader` (добавляя `PodLogs`).

## 2. `internal/signals` и `opsplatform.MetricsReader` (D1) [Фаза 0]

```go
package signals

type Signal string
const (
    CPUThrottlingRatio Signal = "cpu_throttling_ratio"
    CPUUsageVsLimit    Signal = "cpu_usage_vs_limit"
    MemoryUsageVsLimit Signal = "memory_usage_vs_limit"
    ContainerRestarts  Signal = "container_restarts"
    OOMEvents          Signal = "oom_events"
    NodeMemoryAvailableRatio Signal = "node_memory_available_ratio"
    NodeCPUUtilization Signal = "node_cpu_utilization"
    NodeDiskAvailableRatio Signal = "node_disk_available_ratio"
    NodePressureHistory Signal = "node_pressure_history"
)
type Family string // "container","kube_state","node","volume","ingress"
type Reason string // "not_installed","forbidden","unavailable","family_missing","no_series","history_short","invalid_ref"
type Subject struct{ Namespace, Pod, Container, Node, PVC string }
type Want struct{ Signal Signal; Subject Subject }
type Request struct{ At time.Time; Want []Want }
type Value struct { Signal Signal; Subject Subject; Value float64; Unit string; Window time.Duration; Agg string; Limit *float64; ObservedAt time.Time }
type Gap struct{ Signal Signal; Subject Subject; Reason Reason }

type Set struct{ /* ... */ }
func (s *Set) Get(sig Signal, sub Subject) (Value, bool) // nil-безопасно
func (s *Set) Values() []Value                           // отсортировано
func (s *Set) Gaps() []Gap                               // отсортировано
func (s *Set) StackReason() Reason                       // "" — стек доступен; иначе not_installed|forbidden|unavailable
func (s *Set) PartialSource() string                     // "metrics" только при unavailable у существующего стека, иначе ""

type Provider interface{ Fetch(ctx context.Context, req Request) (*Set, error) } // ошибка только программная

type Sample struct{ Labels map[string]string; Value float64; At time.Time }
type Querier interface{ Query(ctx context.Context, expr string, at time.Time) ([]Sample, error) }
var ErrNotFound, ErrForbidden, ErrUnavailable, ErrTooLarge error
func NewPrometheus(q Querier, opts ...Option) Provider
```

```go
package opsplatform

type MetricSample struct{ Labels map[string]string; Value float64; At time.Time }
type MetricsReader interface{ Query(ctx context.Context, expr string, at time.Time) ([]MetricSample, error) }
// Op получает поле: Metrics MetricsReader // nil при StoredOnly или если метрики не настроены
// Config получает: Metrics MetricsConfig{ Namespace, Service string } // заполняет слой связки
```

Аддитивные дополнения скелета D1 (старый код не ломают; сообщить D3 и D7):

```go
// signals
func NewSet(stack Reason, values []Value, gaps []Gap) *Set // для Provider и тестов потребителей: Set иначе не построить
func (s *Set) GapOf(sig Signal, sub Subject) (Gap, bool)   // причина отсутствия значения по сигналу и цели
type Option func(*prometheus)                              // публичных опций пока нет
// opsplatform
func (c MetricsConfig) Configured() bool                   // координаты стека заполнены
func (o *Op) MetricsQueries() int                          // число запросов метрик в операции (для Op.Audit; ни выражений, ни значений)
```

Дополнение к замороженному контракту платформы записывается в `NOTES.md` (как для `StoredOnly`).

## 3. `internal/diagnose` (D3) [Фаза 0: ядро]

```go
// псевдонимы (reader.go): diagnose использует типы depgraph
type ListOptions = depgraph.ListOptions
type ListResult  = depgraph.ListResult
var ErrNotFound, ErrForbidden, ErrTooLarge, ErrUnavailable = depgraph.ErrNotFound, depgraph.ErrForbidden, depgraph.ErrTooLarge, depgraph.ErrUnavailable

// types.go
ResourceRef: + Group string `json:"group,omitempty"`
Hypothesis:  + RuleID string `json:"ruleId,omitempty"`; RuleEpoch int `json:"ruleEpoch,omitempty"`; Explains []string `json:"explains,omitempty"`
Recommendation: + RuleID string `json:"ruleId,omitempty"`; RuleEpoch int `json:"ruleEpoch,omitempty"`
const SourceMetric SourceKind = "metric"
Source: + Signal string `json:"signal,omitempty"`
Report: + Graph *depgraph.Snapshot `json:"graph,omitempty"`; Chains []Chain `json:"chains,omitempty"`
type Chain struct { Path []ChainStep `json:"path"`; Candidate string `json:"candidate,omitempty"` } // Candidate — ID гипотезы-кандидата
type ChainStep struct { Ref ResourceRef `json:"ref"`; Health string `json:"health"`; Reason string `json:"reason,omitempty"` }
func (r *Report) RootCandidates() []string // ID гипотез, которые объясняют другие и сами не объясняются
const RuleSetVersion = 2

// context.go
Context: + Graph *depgraph.Graph; Signals *signals.Set; ObservedAt time.Time
type CollectOptions struct { Graph *depgraph.Options; Signals signals.Provider }
func CollectWith(ctx context.Context, r Reader, t Target, o CollectOptions) (*Context, error) // Collect(...) остаётся и вызывает CollectWith с нулевыми опциями

// hooks.go (реестры; порядок — по возрастанию order)
type CollectHook func(ctx context.Context, r Reader, c *Context) // выполняется после базового сбора и графа; без ошибки: сбой → c.Missing / c.Partial
func RegisterCollectHook(order int, name string, h CollectHook)  // order: 100 — граф (D3), 200 — сигналы (D1), 300+ зарезервировано
func registerPostHook(order int, name string, h func(b *builder))   // после правил: 100 — цепочки/Explains (D3), 200 — метрики (D1)
func registerRule(id string, epoch int, apply func(b *builder))    // таблица правил: у каждого id эпоха >= 1
type TargetCollector func(ctx context.Context, r Reader, t Target, o CollectOptions) (*Context, error)
func RegisterTarget(kind string, c TargetCollector)
```

`runRule` запоминает текущее правило в `builder`; `builder.hypothesis` и `builder.recommend` сами проставляют `RuleID` и `RuleEpoch` (ADR-0034 §3). Хуки (D1) живут в пакете `diagnose` и могут пользоваться неэкспортируемым `builder`.

Коды MissingContext (D1): `metrics.unavailable` (`Params.reason`), `metrics.signal_unavailable` (`Params.signal`, `Params.reason`). Факты метрик: код `metric.<signal>`, `Source{Kind: SourceMetric, Signal: <signal>, Resource: <Pod/Node>}`, `Params`: `value`, `unit`, `window`, `agg`, при необходимости `limit` (числа — строками с фиксированной точностью).

## 4. `internal/correlation` (D4) [Фаза 0]

```go
package correlation

const Version = 1
type Kind string // "shared_node","shared_storage","shared_dependency","shared_root","shared_symptom" (этап A: первые два)
type WorkloadRef struct { Kind, Namespace, Name string; ProblemPods int; Symptoms []string; Since time.Time }
type Candidate struct {
    ID         string
    Kind       Kind
    Source     diagnose.ResourceRef // общий источник
    Confidence diagnose.Confidence
    Since      time.Time
    Affected   []WorkloadRef // до 50, отсортировано
    Total      int           // всего затронуто (точно в пределах прочитанного)
    Truncated  bool
    Potential  []WorkloadRef // зависимые, но пока не сломанные (из графа), до 50
    Supporting []string      // ID фактов в Result.Facts
}
func (c Candidate) Key() string // "<kind>:<group/kind/namespace/name источника>"
type Result struct {
    Version    int
    ObservedAt time.Time
    Candidates []Candidate // до 5, по убыванию уверенности, затем Total
    Facts      []diagnose.Fact
    Missing    []diagnose.MissingContext
    Partial    []string
}
func (r *Result) Best() *Candidate
const (
    CorrelationWindow = 30 * time.Minute
    MaxCandidates = 5; MaxAffected = 50; MaxExtraReads = 10
    MinDeadlineReserve = 5 * time.Second
)
func Run(ctx context.Context, r diagnose.Reader, c *diagnose.Context, now time.Time) (*Result, error)
```

Коды: факты `correlation.<kind>.<признак>`, `MissingContext` `correlation.unavailable` (`Params.reason`). Locale — `incidentsCorrelation.json`.

## 5. `internal/incident` (D5 — владелец; D4, D6 — файлы-заглушки) [Фаза 0: контракт]

```go
// model.go
type RuleRef struct { RuleID string `json:"ruleId"`; Epoch int `json:"epoch"`; Code string `json:"code"` }
type CorrelationFeedback struct { CandidateID string `json:"candidateId"`; Verdict string `json:"verdict"` } // confirmed|rejected|unknown
type CorrelationVerdict struct { Kind, Confidence, Verdict string }

Incident: + Correlation *correlation.Result `json:"correlation,omitempty"`; ProblemID, Signature, RecurrenceOf string (omitempty)
Outcome:  + RejectedHypothesisIDs, HelpfulRecommendationIDs, UselessRecommendationIDs []string; OtherAction bool; ResolvedAt *time.Time; Correlation *CorrelationFeedback (все omitempty)
Summary:  + ProblemID, Signature, RecurrenceOf string; ResolvedAt *time.Time; RuleSetVersion int; Symptoms []string; Confirmed *RuleRef; Rejected, Helpful, Useless []RuleRef; Features []string; CorrelationVerdict *CorrelationVerdict (все omitempty)

// Caller (на операцию): + Signals signals.Provider // nil, если метрики недоступны
// Service: функция WithProblems(pb ProblemBackend) *Service
```

Хуки в `Service.Open` и других местах (вставляет **один раз** D5 в скелете; дальше каждый правит только свой файл):

| Заглушка | Файл-владелец | Где вызывается |
|---|---|---|
| `func (s *Service) correlate(ctx context.Context, dc *diagnose.Context, in *Incident) []string` | `correlate.go` (D4) | в `Open` после `Analyze`, до сохранения; возвращает имена частичных источников |
| `func signatureOf(in *Incident, rep *diagnose.Report) string` | `signature.go` (D6) | в `Open`, до сохранения (`in.Signature`) |
| `func (s *Service) detectRecurrence(ctx context.Context, in *Incident) []string` | `recurrence.go` (D6) | в `Open` до сохранения (`in.RecurrenceOf`) |
| `func (s *Service) autoAttach(ctx context.Context, in *Incident) []string` | `problem_hook.go` (D5) | в `Open` после успешного `PutIncident` |
| `func (s *Service) prepareOutcome(in *Incident, o *Outcome) error` | `outcome.go` (D6) | в `MarkOutcome` после базовой проверки |
| `func (s *Service) onOutcomeSaved(ctx context.Context, in *Incident)` | `problem_hook.go` (D5) | в `MarkOutcome` после сохранения |
| `func (in *Incident) summarizeFeedback(sum *Summary)` | `summarize_feedback.go` (D6) | из `Summarize()` |
| `func (s *Service) purgeProblems(ctx context.Context, orgID string, before time.Time)` | `problem_hook.go` (D5) | из `Purge` |

`Open` вызывает `diagnose.CollectWith(..., CollectOptions{Signals: caller.Signals})`.

```go
// problem.go (D5)
type ProblemStatus string // "open","closed"
type ProblemOrigin string // "auto","manual"
type ProblemMember struct { IncidentID string; AddedAt time.Time; AddedBy string } // AddedBy: user ID или "auto"
type Problem struct {
    ID string; Scope tenant.Scope; Key, Kind string; Source diagnose.ResourceRef
    Status ProblemStatus; Origin ProblemOrigin
    Members []ProblemMember; Detached []string; MergedInto string
    CreatedAt, UpdatedAt time.Time; Version <тот же тип, что у Incident.Version>
}
type ProblemQuery struct{ Status ProblemStatus; Limit int; Cursor string }
type ProblemPage struct{ Items []Problem; NextCursor string }
type ProblemBackend interface {
    PutProblem(ctx context.Context, p *Problem) error                          // Version==0 создаёт (ErrConflict, если есть), иначе CAS по версии
    GetProblem(ctx context.Context, scope tenant.Scope, id string) (*Problem, error)
    ListProblems(ctx context.Context, scope tenant.Scope, q ProblemQuery) (ProblemPage, error) // новые первыми
    DeleteProblem(ctx context.Context, scope tenant.Scope, id string) error
    PurgeProblems(ctx context.Context, orgID string, before time.Time) (int, error)
}
// service: AttachToProblem, DetachFromProblem, MergeProblems, CreateProblem, GetProblem, ListProblems
```

Пределы: окно проблемы 6 ч, участников до 100, просматривается до 100 открытых проблем, автоприсоединение при уверенности кандидата `medium` и выше (ADR-0033 §6).

## 6. `internal/feedback` (D6) [Фаза 0]

```go
package feedback

const ( MinSample = 5; UselessShare = 0.7; RecurrenceWindow = 30 * 24 * time.Hour; StatsWindow = 200 )
type Ref struct{ RuleID string; Epoch int; Code string }
type Record struct { // строится адаптером из incident.Summary (лежит в internal/incident, D6)
    Result string; OpenedAt time.Time; ResolvedAt *time.Time; RuleSetVersion int
    Symptoms []string; Confirmed *Ref; Rejected, Helpful, Useless []Ref
    RecurrenceOf string; ID string; Features []string
    CorrelationKind, CorrelationConfidence, CorrelationVerdict string
}
type HypothesisStat struct { Ref; Confirmed, Rejected int; Precision *float64; Sample int; Trusted bool; MedianResolveSeconds *int64; Resolved, Recurred int }
type RecommendationStat struct { Ref; Helpful, Useless int; Ratio *float64; Sample int; Trusted, PreviouslyUnhelpful bool }
type SymptomStat struct { Code string; Closed, WithConfirmed int; Coverage *float64 }
type CorrelationStat struct { Kind, Confidence string; Confirmed, Rejected int; Precision *float64 }
// Добавлено D6 (ADR-0034 §5, §8; аддитивно): точность гипотез в расследованиях с источником (graph, metrics, correlation) и без него.
type FeatureCounts struct { Records, Confirmed, Rejected int; Precision *float64 }
type FeatureStat struct { Feature string; With, Without FeatureCounts }
type Stats struct {
    Records, Unevaluated int; Truncated bool; ComputedAt time.Time; RuleSetVersions []int
    Hypotheses []HypothesisStat; Recommendations []RecommendationStat; Symptoms []SymptomStat; Correlations []CorrelationStat
    Features []FeatureStat
}
func Compute(recs []Record, now time.Time) Stats
func Export(st Stats, ruleSetVersion int) ([]byte, error) // анонимный JSON, без имён ресурсов, namespace, идентификаторов, комментариев и точных времён
type SignatureInput struct { WorkloadKind, WorkloadNamespace, WorkloadName string; Symptoms []string; Hypotheses []Ref }
func Signature(in SignatureInput) string // hex от стабильной сериализации, 32 символа

// internal/incident (D6, новые файлы)
func (s *Service) RuleStats(ctx context.Context, scope tenant.Scope) (feedback.Stats, error)
func (s *Service) ExportRuleStats(ctx context.Context, scope tenant.Scope) ([]byte, error)
```

Семантика (D6, реализовано): JSON всех типов — camelCase, доли `omitempty` (нет ключа = нет процента, показывать только число случаев); срезы в `Stats` никогда не `null`.
`Unevaluated` — закрытые расследования без явных оценок. Точность гипотез и корреляции считается только по результату `resolved`; `unresolved`/`inconclusive` учитываются в покрытии симптомов и в пользе рекомендаций (явные отметки). В статистику входят строки с `RuleSetVersion >= 2`. Повторение (`Recurred`) считается, когда расследование-повторение уже закрыто. `Truncated` — закрытых расследований больше `StatsWindow`.
`incident.MarkOutcome` теперь отвечает `ErrClosed` на повторную отметку закрытого расследования; `MaxOutcomeRefs = 20` — предел списков в отметке и в строке истории.

## 7. RPC и DTO (D7 владеет именами; D8 использует) [Фаза 0 D7]

Имена, если нельзя иначе по существующему образцу, D7 уточняет в скелет-PR и правит этот раздел. Права: чтение — `read`; изменение состава проблем — `operate`; выгрузка — право смены настроек хранения расследований.

| RPC | Вход | Выход | Права |
|---|---|---|---|
| `IncidentProblemList` | `clusterID`, `status`, `cursor` | `{items: ProblemSummaryDTO[], nextCursor}` | read |
| `IncidentProblemGet` | `clusterID`, `problemID` | `ProblemDetailDTO` | read |
| `IncidentProblemAttach` | `clusterID`, `problemID`, `incidentID` | `ProblemDetailDTO` | operate |
| `IncidentProblemDetach` | `clusterID`, `incidentID` | `{}` | operate |
| `IncidentProblemMerge` | `clusterID`, `fromID`, `toID` | `ProblemDetailDTO` | operate |
| `IncidentProblemCreate` | `clusterID`, `incidentIDs[]` | `ProblemDetailDTO` | operate |
| `IncidentRuleStats` | `clusterID` | `feedback.Stats` (JSON, camelCase) | read |
| `IncidentRuleStatsExport` | `clusterID` | `{filename, mime, dataBase64}` | настройки хранения |
| `IncidentMarkOutcome` (расширен) | `clusterID`, `incidentID`, `OutcomeInput` | как сейчас | как сейчас |

```jsonc
// ProblemSummaryDTO
{ "id": "", "key": "", "kind": "shared_node", "source": {"group":"","kind":"Node","namespace":"","name":"n1"},
  "status": "open", "origin": "auto", "memberCount": 3, "affectedTotal": 12, "updatedAt": "" }
// ProblemDetailDTO = ProblemSummaryDTO + { "members": [{"incidentId":"","addedAt":"","addedBy":"auto", "summary": IncidentSummary}], "affected": WorkloadRef[], "affectedTruncated": false, "detached": [] }
// OutcomeInput = существующие поля + rejectedHypothesisIds, helpfulRecommendationIds, uselessRecommendationIds, otherAction, resolvedAt, correlation{candidateId,verdict}
```

Событие `problem.changed` — адресное, несёт только `problemID` (ADR-0009); подробности клиент загружает RPC с повторной проверкой прав.

## 8. Фронтенд (D8) [Фаза 0]

- Типы: `frontend/src/types/incident.ts` — зеркало JSON разделов 3–7 (camelCase, все новые поля необязательны).
- Заготовки locale-файлов (en/ru/cn) и регистрация в `registry.ts`: `incidentsMetrics.json` (наполняет D1), `incidentsChain.json` (D3), `incidentsCorrelation.json` (D4). Ключи — по той же схеме, что у существующих кодов диагностики (D8 смотрит, как сейчас подключаются коды вида `pending.unbound_pvc`, и описывает схему в `frontend/src/i18n/locales/README.md` или в PR скелета).
- Фикстуры JSON отчётов и ответов RPC: `frontend/src/components/incidents/__fixtures__/`.

## 9. Кто кого уведомляет при изменении контракта

| Раздел | Владелец | Уведомить |
|---|---|---|
| 1 depgraph | D2 | D3, D4, D8 |
| 2 signals/MetricsReader | D1 | D3, D7 |
| 3 diagnose | D3 | D1, D4, D5, D8 |
| 4 correlation | D4 | D5, D8 |
| 5 incident | D5 | D4, D6, D7, D8 |
| 6 feedback | D6 | D5, D7, D8 |
| 7 RPC/DTO | D7 | D8 |
| 8 фронтенд | D8 | D1, D3, D4 |
