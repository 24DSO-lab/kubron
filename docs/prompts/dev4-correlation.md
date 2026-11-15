# D4. Корреляция расследований по живым данным `internal/correlation`

Ты — разработчик **D4**. Реализуешь **ADR-0033 §2–§5, этап A**: при открытии расследования ищешь по живым данным общий источник и затронутое множество нагрузок, пишешь снимок в расследование.

## Прочитай сначала

- `docs/prompts/00-common.md`, `README.md`, `contracts.md` (разделы 1, 3, 4, 5).
- `docs/adr/0033-correlation-problems.md` — целиком; `0031` §2–3 (граф, `Dependents`); `0032` §1 (метрики — только опциональный источник); `0024` §4–5.
- Код: `internal/diagnose/{types,context,reader,podstate}.go`, `internal/incident/{model,service}.go` (как вызывается сбор и как строится `Partial`), `internal/depgraph` (после скелета D2).

## Ты владеешь

- `internal/correlation/**` (+ тесты, `testdata/`).
- `internal/incident/correlate.go` — файл-заглушка, созданный D5; после его слияния **ты** реализуешь тело `Service.correlate`.
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsCorrelation.json` (заготовки создаёт D8; ты заполняешь).

**Не трогай:** остальные файлы `internal/incident/**` (D5, D6), `internal/diagnose/**` (D3), `internal/depgraph/**` (D2), `internal/app/**` (D7).

## Фаза 0: скелет (PR `[skeleton]`, после D2 и D1)

- `internal/correlation`: `Version`, `Kind`, `WorkloadRef`, `Candidate` (+ `Key()`), `Result` (+ `Best()`), константы, `Run` (заглушка, возвращающая `&Result{Version: Version}`). Строго по `contracts.md` §4. Типы `diagnose.Confidence`, `Fact`, `MissingContext`, `ResourceRef` — из реального кода.

Твоё отдельное зависимое для D5: `Candidate`, `Result`, `Best()`, `Key()`. Следи, чтобы их JSON-имена совпадали с `contracts.md` (camelCase, `omitempty` там, где уместно).

## Задачи (Фаза 1)

### 1. `correlation.Run` (ADR-0033 §2–§4)

Вход: уже собранный `diagnose.Context` (граф, Pod'ы, Node, Events) и `diagnose.Reader` для дополнительных чтений. `now` берётся параметром (не `time.Now()`).

**Правила этапа A:**

| Вид | Общий источник | Затронутое множество (живые чтения) | Источник ненормален | Временная связь |
|---|---|---|---|---|
| `shared_node` | Node (из `runs_on` цели или сама цель-Node) | `List` Pod по `spec.nodeName`, без namespace, с `Limit`; Pod'ы с симптомами → владельцы верхнего уровня | условия `NotReady`, `MemoryPressure`, `DiskPressure`, `PIDPressure`; `Unschedulable`; taint `NoExecute`; предупреждающие Events Node | `lastTransitionTime` условия предшествует первым сбоям не более чем на `CorrelationWindow` |
| `shared_storage` | StorageClass | PVC с таким `storageClassName` в `Pending`/`Lost` (`List` PVC без namespace, `Limit`) и их Pod'ы-потребители | нет provisioner / нет класса по умолчанию / `ProvisioningFailed` у ≥ 2 PVC | время событий в окне |

- «Симптом Pod'а» определи **внутри `correlation`** по данным объекта (`CrashLoopBackOff`, `OOMKilled` в `lastState`, `Ready=false`, `Evicted`), не завися от внутренностей `diagnose`. Время сбоя — по меткам объекта (завершение последнего запуска, `lastTransitionTime`), не по времени открытия расследования.
- Затронутая нагрузка = **владелец верхнего уровня** (Deployment, StatefulSet, DaemonSet, CronJob, Job, одиночный Pod). `ProblemPods`, `Symptoms` (коды), `Since`. В `Affected` до 50, `Total` и `Truncated` честные.
- `Potential` — зависимые от источника, но пока без симптомов (из `Graph.Dependents`).
- Уверенность (ADR-0033 §4): признаки (а) структурная связь, (б) источник ненормален, (в) временная связь, (г) ≥ 3 разных workload. `low` = (а)+(г); `medium` = (а) + (б) или (в); `high` = все четыре. Программа однозначна, без весов.
- Бюджет (в коде): до 10 дополнительных чтений; запуск **только** если до deadline `ctx` осталось ≥ 5 секунд (иначе результат с `Partial` `correlation`, без кандидатов). Списки читаются с `Limit`.
- Недоступное (`forbidden` на список Pod'ов Node при ограниченном RBAC) → `MissingContext` `correlation.unavailable` с причиной и неполное множество, а **не** ложная полнота. Список затронутых не показывает больше, чем доступно сессии (то, что вернул `Reader`).
- Кандидаты: до 5, по убыванию уверенности, потом `Total`, потом `Key()` (детерминированно). Факты — `diagnose.Fact` с источником `derived`, собственная нумерация; `Supporting` ссылается на них.
- **Повышение версии:** любое изменение правил — `Version++` и обновление золотых файлов.

Предусмотри реестр правил (`registerRule` в пакете), чтобы этап C (`shared_dependency`, `shared_root`, `shared_symptom`) добавлялся новым файлом, без правки ядра. Сами правила этапа C **не реализуй**.

### 2. `Service.correlate` (`internal/incident/correlate.go`)

После слияния контракта D5. Тело: вызывает `correlation.Run`, кладёт результат в `in.Correlation`, возвращает имена частичных источников. Сбой корреляции **не роняет `Open`** (вторичный источник, `Partial`). Размер снимка ограничен константами; в снимок не попадают поля объектов, метки, аннотации, тексты логов (ADR-0022).

### 3. Тесты

- Табличные на `graphtest.FakeReader` (обёрнутый в фейк `diagnose.Reader` с пустым `PodLogs`): E2E-2 из README — 12 Pod'ов на Node с `MemoryPressure`, владельцы разные; тот же набор без условий Node (`low`, без группы); условие появилось после сбоев → нет временной связи; `forbidden` на список Pod'ов; усечённый список (`Truncated`); мало времени до deadline; два PVC на одном классе без provisioner.
- Детерминизм (JSON побайтно), порядок кандидатов, лимиты, отсутствие запрещённых данных в JSON.
- Чтения ≤ 10; никаких обращений к Secret.
- Тест `Service.correlate` с фейковым `Backend` (см. тесты `internal/incident/service_test.go`).

### 4. Локализация

`incidentsCorrelation.json` en/ru/cn: коды фактов `correlation.<kind>.*`, `correlation.unavailable` и причины, названия видов `shared_node`, `shared_storage`. Тон — «возможный общий источник», не «причина».

## Чего не делаешь

`Problem` и автоприсоединение (D5); правила этапа C; фоновое обнаружение; индекс расследований; «пересчитать корреляцию»; правки `types.go` и `model.go`.

## Стыки

- **D2/D3:** граф и `Context.Graph`; коды причин вершин.
- **D5:** `Incident.Correlation *correlation.Result` и хук `correlate`; `Candidate.Key()` и `Best()` использует `autoAttach` — не меняй их семантику без уведомления.
- **D8:** отдай золотой JSON `Result` для фикстур.

## Приёмка

- E2E-2 на фикстурах: снимок с `shared_node`, уверенность не ниже `medium`, число затронутых и область влияния.
- `go test ./internal/correlation/... ./internal/incident/...` зелёный; статический тест платформы зелёный.
- В PR — строка для CHANGELOG.
