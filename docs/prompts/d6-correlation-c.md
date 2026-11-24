# D6 — Корреляция, этап C (ADR-0033) и RPC пересчёта

Перед началом прочитай `00-common.md`, `README.md` и `contracts.md` (§1 (формы заглушек Missing), §5 (виды и RPC)). Ветка: `intel2/d6-correlation-c` от `refactor`. Ты **ни от кого не зависишь** и никого не ждёшь: все точки расширения уже в `refactor`, чужие результаты берёшь из `contracts.md` и подменяешь фейками.

## Цель

Три новых правила корреляции — `shared_dependency`, `shared_root`, `shared_symptom` (только `low`) — и явное действие «пересчитать корреляцию» (RPC `IncidentCorrelationRecompute`). Правила работают поверх графа любых видов и **не зависят** от реализаций D1–D4.

## Твои файлы (создаёшь и правишь только их)

- `internal/correlation/rule_dependency.go`, `rule_root.go`, `rule_symptom.go` — `registerRule(name, fn)` в `init()`; тесты `internal/correlation/rule_dependency_test.go` и т. п.; фикстуры `internal/correlation/testdata/*` (новые каталоги).
- `internal/incident/recorrelate.go` (+ `_test.go`) — метод `Service.RecomputeCorrelation(ctx, c Caller, id string)`: перечитывает живые данные через существующий `correlate`, пишет снимок, не трогает Events, закрытые расследования не пересчитывает.
- `internal/app/app_incident_correlate.go` — RPC `IncidentCorrelationRecompute` (права `operate`, аудит `incident.correlation_recompute`, ошибка кластера как у `IncidentRefresh`); `internal/app/app_incident_correlate_test.go`.
- `internal/server/methods.go` — **одна строка** `"IncidentCorrelationRecompute"` (ты единственный, кто её правит в этой итерации).
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsCorrelation.json` — тексты новых видов кандидата, факты, причины.

## Не трогай

- `internal/incident/{service,model}.go` и прочие файлы `incident`, кроме новых твоих. Если для пересчёта нужна приватная функция `Service`, вызывай существующий экспортируемый путь или опиши в PR, что нужна правка `service.go` (после слияния её внесёт тимлид) — **а в своём файле сделай временную обёртку через публичный API**.
- `internal/depgraph/**`, `internal/diagnose/**`, `internal/signals/**`.
- Фронтенд кроме `incidentsCorrelation.json` (кнопка — D8).
- `go.mod`, `go.sum`, `vendor/`.

## Задачи

1. **`shared_dependency`.** Для каждого расследования окна читается граф из сохранённого отчёта (`Report.graph`) **и** (если есть источник живых данных) вершины `Missing`/Service без endpoints; группируй расследования по общей вершине-заглушке `Missing` (любой вид из `contracts.md` §1) или общему Service без готовых endpoints. Минимум — 2 workloads; уверенность — по четырём признакам ADR-0033 §4 (структурный, состояние, время, ≥ 3 нагрузок).
2. **`shared_root`.** Источник — `Hypothesis.Explains` кандидата первопричины в отчётах расследований разных слоёв цепочки (Ingress → Service → Deployment → Pod): общий workload-владелец кандидата ⇒ одна проблема; минимум 2 расследования.
3. **`shared_symptom`.** Один namespace, ≥ 5 workloads с одним кодом симптома и одним кодом гипотезы; **уверенность строго `low`** при любых условиях (тест на невозможность поднять выше), структурной связи нет, поэтому не участвует в автоприсоединении.
4. **Автоприсоединение.** Проверь, что кандидаты новых видов `low` не создают проблем автоматически; `shared_dependency`/`shared_root` с `medium`+ — по существующей логике автоприсоединения (она не меняется).
5. **Пересчёт.** `RecomputeCorrelation`: права `operate`; Events не перечитываются; результат — новый снимок с новым временем; повторный вызов идемпотентен; для закрытого — ошибка как у `Refresh`.
6. **RPC + аудит + whitelist.** Сигнатура из `contracts.md` §5; тест в `app` (образец — `incident_e2e_test.go`): неизвестный кластер → `ErrClusterNotFound`; пользователь без `operate` → `ErrForbidden`.
7. **Ограничение по namespace.** Список затронутых нагрузок не показывает больше, чем доступно сессии (как для `shared_node`).
8. **Переводы** en/ru/cn.

## Тесты и фикстуры

Табличные тесты на ручных графах (`depgraph.New/AddNode/AddEdge`) — без реализации видов D1–D4: заглушки `Missing` ConfigMap/Ingress-backend, Service без endpoints. Golden корреляции — по образцу `golden_test.go`. Старый снимок (без новых видов) читается. Детерминизм.

## Не входит

Кнопка и отображение в интерфейсе (D8), карта (D7), ручное управление проблемами (уже есть).

## Готово, когда

- Все пункты «Задач» выполнены и покрыты тестами; `gofmt`, `go build ./...`, `go vet`, тесты своих пакетов, `go test ./internal/app/... ./internal/k8s/...` зелёные, `cd frontend && npm test && npm run build`.
- `git diff --stat origin/refactor` содержит только файлы из раздела «Твои файлы».
- Переводы en/ru/cn с паритетом ключей; нет сырых значений Kubernetes в переводах.
- Описание PR по `00-common.md` (в том числе строка для CHANGELOG).
