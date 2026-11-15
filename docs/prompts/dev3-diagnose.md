# D3. Ядро `diagnose` и цепочечные правила (этап 1)

Ты — разработчик **D3**, владелец `internal/diagnose`. Делаешь две вещи: (1) ядро контракта для остальных (**ADR-0031 §4–§5, ADR-0034 §3**), (2) цепочечные правила этапа 1 (**ADR-0031 §6**).

## Прочитай сначала

- `docs/prompts/00-common.md`, `README.md`, `contracts.md` (разделы 1, 2, 3, 8).
- `docs/adr/0031-dependency-graph.md` — целиком; `0034` §3 (RuleID, эпоха); `0032` §6 (точки встраивания D1); `0024` §3–4.
- Код целиком: `internal/diagnose/*.go` (особенно `types.go`, `engine.go`, `context.go`, `collect.go`, `reader.go`, `rules.go`, `rollout.go`, `podstate.go`, `i18n_test.go`), `internal/incident/service.go` (как вызывается `Collect` и `Analyze`).

## Ты владеешь

- Весь `internal/diagnose/**`, **кроме** `metrics_evidence.go`, `metrics_missing.go`, `collect_signals.go` (D1) и их тестов.
- Новые файлы: `hooks.go`, `targets.go`, `chain_*.go`, `rules_chain_*.go`, `collect_chain_*.go`, `testdata/chain/**`.
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsChain.json` (заготовки создаёт D8; ты заполняешь).

**Не трогай:** `internal/incident/**` (D5–D6), `internal/depgraph/**` (D2), `internal/signals/**` (D1), `internal/app/**` (D7).

## Фаза 0: ядро (PR `[skeleton]`, после скелетов D2 и D1)

Только контракт и реестры, без новой диагностики. **Старые тесты должны остаться зелёными; отчёт по тем же входам равен прежнему, кроме новых полей** (`RuleID`, `RuleEpoch`, `RuleSetVersion = 2`).

1. `reader.go`: `ListOptions`, `ListResult`, `ErrNotFound`, `ErrForbidden`, `ErrTooLarge`, `ErrUnavailable` — **псевдонимы/переназначения** на `depgraph` (`contracts.md` §3). Адаптер в `internal/app/incident_adapters.go` должен компилироваться без правок (проверь `go build ./...`: если нет — верни правку типов, а не адаптера).
2. `types.go`: новые поля и типы (`Group`, `RuleID`, `RuleEpoch`, `Explains`, `SourceMetric`, `Source.Signal`, `Report.Graph`, `Report.Chains`, `Chain`, `ChainStep`, `RootCandidates`, `RuleSetVersion = 2`).
3. `context.go`: `Context.Graph`, `Context.Signals`, `Context.ObservedAt`; `CollectOptions`, `CollectWith`; `Collect` делегирует в `CollectWith`.
4. `hooks.go`, `targets.go`: `CollectHook`, `RegisterCollectHook`, `registerPostHook`, `registerRule` (таблица правил с `epoch`), `TargetCollector`, `RegisterTarget`; `runRule` запоминает текущее правило, `builder.hypothesis`/`builder.recommend` ставят `RuleID` и `RuleEpoch`. Все существующие правила получают `epoch = 1`.
5. Тесты: (а) чтение старого JSON отчёта (`RuleSetVersion` 1); (б) каждый выданный `RuleID` есть в таблице правил и у него `epoch >= 1`; (в) старые золотые отчёты не изменились, кроме новых полей.

D5 ждёт этот PR для своего скелета (`CollectWith`) — делай его первым.

## Задачи (Фаза 1)

### 1. Сбор через граф (ADR-0031 §4)

- `collect.go`: вместо фиксированного `switch` — регистр целей. Уже естьные цели (`Pod`, `Deployment`, `ReplicaSet`) регистрируются через `RegisterTarget` без изменения поведения. Добавь `Service`, `PersistentVolumeClaim`, `Node` (для Node `Namespace` пуст — допустимо только для Node).
- После базового сбора: хук 100 строит граф `depgraph.Build` от цели (опции из `CollectOptions.Graph`, по умолчанию `DefaultLimits`, флаги этапа 2 выключены), кладёт в `Context.Graph`; `Truncated` и `Unknown` → `Partial` и `MissingContext` по уже действующим правилам `Collect` (ошибка графа расследование не роняет). `Report.Graph = Context.Graph.Snapshot()`.
- `AllowedKinds` расширяется до видов этапа 1 (Service, EndpointSlice, PVC, PV, StorageClass). Существующий страж «виды диагностики ⊆ allowlist платформы» остаётся зелёным.
- Обязательный инвариант: если граф недоступен целиком, отчёт совпадает с прежним (без цепочек), кроме `MissingContext`.

### 2. Цепочечные правила этапа 1 (ADR-0031 §6)

Новые файлы `rules_chain_*.go`, правила через `registerRule(id, epoch, apply)`; **существующие правила не переписывай**, кроме минимальной правки `pending.unbound_pvc` (пониженная уверенность текстового пути, когда граф дал цепочку) — вынеси её в аккуратный хук, не переписывая правило.

| Цель / цепочка | Что диагностируется |
|---|---|
| Pod → PVC → PV/StorageClass | `Pending` из-за PVC: класс не найден (`Missing`), нет класса по умолчанию, `WaitForFirstConsumer` без потребителя, `ProvisioningFailed` по Events, PVC `Lost`; `step.check_pvc` заменяется фактами |
| Service | селектор не совпал ни с одним Pod; Pod'ы не `Ready` (endpoints пусты); порт/`targetPort` не совпали с портами контейнера |
| PVC как цель | Pending/Lost, ошибки attach/mount по Events потребителя |
| Node как цель и звено | `NotReady`, давление на память/диск/PID, `Unschedulable`, затронутые Pod'ы; гипотеза `oom.node_memory_pressure` опирается на это звено |

- Цепочки пишутся через граф (`Owners`, `Dependents`, `EdgesFrom`), не через разбор текста. Каждый факт — с источником, полем и `ObservedAt`.
- **`Explains`**: гипотеза «PVC не привязан: класс не найден» объясняет «Pod не запланирован: PVC не привязан». Пост-хук 100 проставляет связи и строит `Report.Chains` (путь по графу от затронутого ресурса до кандидата). Значения «первопричина установлена» и поля `root_cause` **нет** (ADR-0024 §3).
- Уверенность — по правилу; недостающие данные (`Unknown`) — `MissingContext`, не факт «проблемы нет».

### 3. Тесты

- Фикстуры на `graphtest.FakeReader` в `testdata/chain/` для каждой цепочки в вариантах: всё есть / объект отсутствует (`Missing`) / **запрещён** (`Unknown`) / граф усечён. Полные JSON-отчёты как золотые файлы.
- Детерминизм: тот же `Context` дважды → побайтно одинаковый JSON.
- Старые золотые отчёты не меняются.
- `RootCandidates` и `Chains`: цепочка из трёх звеньев, две независимые цепочки, цикл (не зацикливаться).
- `i18n_test.go`: все новые коды имеют переводы (если тест не покрывает фронтенд — добавь свой тест на совпадение ключей в en/ru/cn).

### 4. Локализация

`incidentsChain.json` en/ru/cn: коды фактов, гипотез, рекомендаций и шагов твоих правил + словарь причин вершин графа (коды от D2) — для компактной цепочки в UI. Тексты: что наблюдается, без утверждения «это точно причина».

## Чего не делаешь

Этапы 2–3 ADR-0031 (Ingress, HPA, ConfigMap-ссылки, StatefulSet, Job, CronJob); метрики (D1 через хуки); корреляцию (D4); правки `incident` (D5/D6); визуализацию.

## Стыки

- **D2:** взять `graphtest.FakeReader`, список кодов `HealthOf`; сообщать, если графу чего-то не хватает (правит только D2).
- **D1:** хуки `RegisterCollectHook(200)` и `registerPostHook(200)`, поля `Context.Signals`, `SourceMetric`. Тест «без метрик отчёт не меняется» — у D1, но ты не ломаешь его порядком хуков.
- **D4:** для корреляции нужен `Context.Graph` и `ResourceRef.Group`.
- **D5:** ждёт `CollectWith`/`CollectOptions`; не меняй их сигнатуры после слияния.
- **D8:** отдай пример JSON-отчёта с `chains` и `graph` (золотый файл) — он построит на нём фикстуры.

## Приёмка

- E2E-1 из README проходит на фикстурах: PVC с несуществующим StorageClass → цепочка с `Missing`, кандидат на PVC, `step.check_pvc` заменён.
- `go test ./internal/diagnose/... ./internal/incident/... ./internal/app/...` зелёный.
- В PR — строка для CHANGELOG.
