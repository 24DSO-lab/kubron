# D6. Обратная связь и статистика правил

Ты — разработчик **D6**. Реализуешь **ADR-0034, этапы A, B, C**: расширенная отметка результата, сигнатура и повторение, детерминированная статистика, анонимная выгрузка.

## Прочитай сначала

- `docs/prompts/00-common.md`, `README.md`, `contracts.md` (разделы 5, 6, 7).
- `docs/adr/0034-feedback-loop.md` — целиком; `0024` §3, §5; `0031` §5 (RuleID); `0033` §4 (оценка корреляции).
- Код: `internal/incident/{model,service,backend,settings}.go`, `internal/pgstore/incidents.go` (как строится `summary`), `internal/diagnose/types.go` (`Hypothesis`, `Recommendation`, `Report.Symptom`), `internal/diagnose/engine.go`.

## Ты владеешь

- `internal/feedback/**` (+ тесты, `testdata/`).
- `internal/incident/outcome.go`, `signature.go`, `recurrence.go`, `summarize_feedback.go` — файлы-заглушки создаёт D5; после его контракт-PR **ты владеешь их телами**. Новые файлы там же: `feedback_stats.go` (`Service.RuleStats`, `Service.ExportRuleStats`).

**Не трогай:** `model.go`, `service.go`, `backend.go` (D5) — всё, что нужно там, уже сделал D5 в контракт-PR; `internal/diagnose/**` (D3), `internal/app/**` (D7).

## Фаза 0: скелет (PR `[skeleton]`, после D2)

`internal/feedback`: константы (`MinSample`, `UselessShare`, `RecurrenceWindow`, `StatsWindow`), типы `Ref`, `Record`, `HypothesisStat`, `RecommendationStat`, `SymptomStat`, `CorrelationStat`, `Stats`, функции `Compute`, `Export`, `Signature` (заглушки) — строго по `contracts.md` §6. Пакет импортирует только стандартную библиотеку (и при необходимости `diagnose`); не `incident`.

## Задачи

### 1. Этап A: сбор данных (ADR-0034 §2, §3, §5)

- **`prepareOutcome(in, o)`** (`outcome.go`): валидация новых полей `Outcome`: все идентификаторы должны быть в отчёте (иначе `ErrInvalidOutcome`, как для `ConfirmedHypothesisID`); списки ≤ 20; `Rejected` не пересекается с подтверждённой; `Helpful` не пересекается с `Useless`; `ResolvedAt` только для `resolved`, не раньше `OpenedAt`, не позже момента отметки (если не задано — момент отметки); `Correlation.CandidateID` должен быть в снимке корреляции; `Verdict` из трёх значений. Комментарий редактируется как сейчас. Отметка по-прежнему однократна и закрывает расследование.
- **`summarizeFeedback(sum)`** (`summarize_feedback.go`): из `Report` и `Outcome` строит `Confirmed`, `Rejected`, `Helpful`, `Useless` (`RuleRef{RuleID, Epoch, Code}`), `Features` (`graph`, `metrics`, `correlation` — по наличию `Report.Graph`, фактов `SourceMetric`, `Incident.Correlation`), `Symptoms` (коды симптомов отчёта), `CorrelationVerdict` (вид и уверенность кандидата + вердикт). Списки ≤ 20; **никаких текстов, имён ресурсов, параметров**. Записи без `RuleID` (старые инциденты) пропускаются, без догадок по `Code`. Без паники на пустом `Report`.
- **Сигнатура** (`feedback.Signature`, `signatureOf`): хэш от владельца-workload цели (вид, namespace, имя), отсортированных кодов симптомов и отсортированных пар `(RuleID, Code)` гипотез с уверенностью ≥ `medium`. Стабильная сериализация (фиксированный разделитель, без map-порядка), hex 32 символа. **В хэш не входят тексты, параметры и значения из кластера.** Тест на стабильность (золотые значения) и на порядок входов.

### 2. Этап B: повторение и статистика (ADR-0034 §4, §6, §7)

- **`detectRecurrence(ctx, in)`**: один `ListIncidents` (`closed`, `Limit` 100); среди `Summary` ищется запись с той же `Signature`, результатом `resolved` и `ResolvedAt` не старше 30 дней; пишет `in.RecurrenceOf`. Пустая сигнатура — нет поиска. Сбой поиска не роняет `Open` (вторичный источник). **Закрытое предыдущее расследование не меняется.**
- **`feedback.Compute(recs, now)`**: чистая функция, детерминированная, отсортированный вывод. Показатели: точность гипотезы (подтверждена ÷ (подтверждена + отвергнута)), польза рекомендации (помогла ÷ (помогла + не помогла)), **медиана** `ResolvedAt − OpenedAt` по `resolved` с этой подтверждённой гипотезой, повторяемость (доля «устранённых с этой гипотезой», за которыми последовало расследование с `RecurrenceOf` на них), покрытие симптома, точность корреляции по виду и уверенности. **Неоценённые случаи в знаменатель не входят**; `unresolved` и `inconclusive` в точность не входят, но учитываются в покрытии. Порог доверия `MinSample = 5`: ниже — `Precision == nil`, `Trusted == false`, только число случаев. `PreviouslyUnhelpful` — при `Sample ≥ 5` и доле непомогавших ≥ 70%. Ключ — `(RuleID, Epoch, Code)`.
- **`Service.RuleStats(ctx, scope)`** (`feedback_stats.go`): читает до 200 закрытых расследований кластера (две страницы по 100), строит `[]feedback.Record` (адаптер `Summary → Record` живёт здесь), записи без новых полей пропускает (старые строки PostgreSQL и не мигрируются) и возвращает `Stats` с `Unevaluated`, `Truncated`. Вычисляется по запросу, не при `Open`.
- Тесты: фикстуры записей — порог 5, все неоценённые, пустая выборка, много эпох одного правила, повторение, медиана при чётном и нечётном числе, детерминизм, случай «все записи без RuleID».

### 3. Этап C: выгрузка для мантейнеров (ADR-0034 §8)

- `feedback.Export(st, ruleSetVersion)` — JSON только из агрегатов: `RuleSetVersion`, `(RuleID, Epoch, код)`, счётчики, медианы, признаки `Features`, покрытие симптомов, точность корреляции по видам. Числа ниже порога выгружаются как есть. **В файле нет:** имён ресурсов, namespace, идентификаторов (расследований, организации, команды, кластера), пользователей, комментариев, сигнатур, точных моментов времени.
- **Тест на отсутствие данных:** входные записи заполнены уникальными маркерами (имена, комментарии, ID), в выгрузке их нет; детерминизм; валидный JSON.
- `Service.ExportRuleStats(ctx, scope)` — обёртка над `RuleStats` + `Export`.
- Процесс улучшения правил (ADR-0034 §9) — короткий раздел в `docs/` для мантейнеров — **пишет D7** (ты присылаешь тезисы в PR).

## Чего не делаешь

Новое хранилище счётчиков; автоматическую калибровку уверенности; телеметрию и автоотправку; статистику по организации (в `Backend` нет списка по организации); правки чужих файлов; правку закрытого расследования; AI-ранжирование.

## Стыки

- **D3:** `RuleID`, `RuleEpoch` в `Hypothesis` и `Recommendation`; коды симптомов — посмотри `Report`, как они представлены.
- **D4:** `Incident.Correlation` и `Candidate.Kind/Confidence`.
- **D5:** типы в `model.go` и вызовы хуков; если нужна дополнительная строка в `service.go` — проси D5.
- **D7:** `Service.RuleStats`, `ExportRuleStats`, расширенный `OutcomeInput`.
- **D8:** отдай пример JSON `Stats` для фикстур.

## Приёмка

- E2E-4 и E2E-5 из README на тестах сервиса с фейковым `Backend`.
- `go test ./internal/feedback/... ./internal/incident/...` зелёный; пакет `feedback` покрыт не ниже 90%.
- В PR — строка для CHANGELOG и тезисы для раздела о процессе мантейнеров.
