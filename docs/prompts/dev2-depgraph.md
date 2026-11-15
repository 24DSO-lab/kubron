# D2. Граф зависимостей `internal/depgraph`

Ты — разработчик **D2**. Реализуешь модель и построитель графа зависимостей (**ADR-0031 §1–§3, §7 — снимок**, этап 1). Твой пакет — фундамент для D3 и D4: скелет нужен первым.

## Прочитай сначала

- `docs/prompts/00-common.md`, `README.md`, `contracts.md` (раздел 1).
- `docs/adr/0031-dependency-graph.md` — целиком; `0026` §1–2; `0024` §2–4.
- Код: `internal/k8s/graphbuild.go` (образец модели; код не переиспользуется), `internal/diagnose/{reader,collect,context,podstate}.go`, `internal/opsplatform/allowlist.go`, `internal/k8s/opsplatform_static_test.go`.

## Ты владеешь

- `internal/depgraph/**` (включая `graphtest`).
- `internal/k8s/opsplatform_static_test.go` — **только** добавление четырёх пакетов в `opsPackages`.

**Не трогай:** `internal/k8s/graphbuild.go` (остаётся для Argo/Flux), `internal/diagnose/**` (D3 сам сделает псевдонимы типов чтения).

## Фаза 0: скелет (самый первый PR всего проекта, `[skeleton]`)

Цель — в первые часы. В этом PR:

1. `internal/depgraph`: типы чтения (`ListOptions`, `ListResult`, `Reader`, ошибки) — **точная копия** из `internal/diagnose/reader.go` (типы полей, тексты ошибок), модель графа, конструкторы `New`, `AddNode`, `AddEdge`, аксессоры, `Snapshot`, `Limits`, `DefaultLimits`, `Options`, `Build` (заглушка: строит граф из одной вершины-цели) — строго по `contracts.md` §1. Если тип в `diagnose.Reader` отличается от контракта, **бери тип из кода** и поправь `contracts.md` отдельным коммитом.
2. `internal/depgraph/graphtest`: `FakeReader` (`Put`, `PutYAML`, `Fail`, `Forbid`, `Calls`, `CallLog`).
3. Каталоги с `doc.go` (только комментарий пакета) для `internal/signals`, `internal/correlation`, `internal/feedback`.
4. `opsplatform_static_test.go`: `internal/depgraph`, `internal/signals`, `internal/correlation`, `internal/feedback` в `opsPackages`. Тест должен остаться зелёным на пустых пакетах.

## Задачи (Фаза 1)

### 1. Модель (ADR-0031 §2)

- Вершины: `Health` из существующего словаря + `Unknown`; **`Missing` только при `NotFound`**; отказ RBAC, недоступность, лимит размера, предел графа и Secret → `Unknown` с причиной (`forbidden`, `unavailable`, `too_large`, `limit`, `policy`).
- Рёбра: все восемь типов, состояния `ok/broken/missing/unknown`.
- **Отсутствующая связь — часть графа:** ссылка по имени на несуществующий объект — вершина-заглушка `Missing` + ребро `missing`; селектор Service без Pod — `Degraded` у Service с кодом причины.
- Secret: вершина `Unknown/policy`, **никакого чтения**. ConfigMap (функция отбрасывания `data`/`binaryData` делается сейчас с тестом, чтение — за флагом `ExpandConfigMapRefs`, по умолчанию выключено).
- `Node.Object` держит объект только в памяти (`json:"-"`); в `Snapshot` — только идентификаторы, состояния, коды причин (никаких меток, аннотаций, хостов Ingress, данных ConfigMap).
- Детерминизм: `Nodes()`/`Edges()`/`Snapshot()` всегда отсортированы; одинаковый вход → побайтно одинаковый JSON.

### 2. Построение от цели (ADR-0031 §3)

Стартовые цели этапа 1: `Pod`, `Deployment`, `ReplicaSet`, `Service`, `PVC`, `Node`. Расширения:

- вверх по `ownerReferences` (Pod → ReplicaSet → Deployment; StatefulSet/Job/CronJob по мере наличия в allowlist);
- Pod → PVC → PV/StorageClass (`Get` по имени; для нескольких PVC — один `List` PVC namespace с `Limit`);
- Pod → Node (`runs_on`); Node → Pod'ы по `FieldSelector spec.nodeName` (без namespace, с `Limit`; `forbidden` → `Unknown`);
- Service ↔ Pod по селектору (список Service namespace с `Limit`, сопоставление локально), Service → EndpointSlice (`kubernetes.io/service-name`);
- Ingress/HPA/ConfigMap-расширения — **за флагами** `ExpandIngress`, `ExpandHPA`, `ExpandConfigMapRefs` (этап 2; реализуй каркас и тесты заглушки, без включения по умолчанию).

Пределы: `DefaultLimits` (200 вершин, 600 рёбер, глубина 4, 40 запросов). Превышение — `Truncated` + `Warnings`; не ошибка. Ошибка чтения одной связи не роняет построение. Без informers и кэша. Учитывай отмену `ctx`.

### 3. Здоровье и состояние рёбер

`HealthOf(obj)` для Pod, ReplicaSet/Deployment, Service, PVC, PV, StorageClass, Node, EndpointSlice → `(Health, reasonCode)`; коды — короткие стабильные строки (дальше их используют D3 и D4: `pending_unscheduled`, `crash_loop`, `oom_killed`, `not_ready`, `unbound`, `lost`, `no_endpoints`, `selector_matches_nothing`, `node_not_ready`, `node_pressure_memory|disk|pid` и т. п.). Состояние ребра выводится из обеих сторон и факта связи (selector не совпал → `broken`, цель не найдена → `missing`). **Выпиши в `doc.go` список кодов причин** и сообщи D3/D4 после слияния.

### 4. Тесты

- Табличные на `graphtest.FakeReader`: полная цепочка Pod → RS → Deployment; Pod → PVC → StorageClass; **несуществующий StorageClass** (`Missing`); **forbidden** на PV (`Unknown/forbidden`, не `Missing`); Service без Pod; Node с Pod'ами и `forbidden` на список; `Truncated` на пределах (200/600/глубина/40 запросов); отмена `ctx`; ссылка на Secret без чтения (в `CallLog` нет обращения к Secret).
- Детерминизм снимка (golden JSON). Снимок не содержит меток, аннотаций и данных ConfigMap (тест на подстроки).
- Тест отбрасывания `data`/`binaryData` ConfigMap.
- Бенчмарк построения на графе в пределах лимитов (не должен делать больше 40 чтений).
- Статический: пакет не импортирует `opsplatform`, `internal/k8s`, `diagnose`.

## Твоя помощь другим

- D3 и D4 ждут `graphtest.FakeReader` для фикстур — делай его удобным: `PutYAML` принимает обычные YAML-манифесты, несколько документов через `---`.
- Расскажи D3 про список `HealthOf`-кодов, а D4 — про `Dependents`.

## Чего не делаешь

Расширения этапа 2 (включение Ingress, HPA, ConfigMap), этапа 3 (StatefulSet, Job, CronJob); информеры и кэш графа; живое обновление; переписывание `k8s/graphbuild.go`; визуализацию.

## Приёмка

- `go test ./internal/depgraph/... ./internal/k8s/...` зелёный; покрытие пакета не ниже 85%.
- D3 может писать правила на твоём `Graph` без твоих правок (полный список кодов причин в `doc.go`).
- В PR — строка для CHANGELOG.
