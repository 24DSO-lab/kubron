# D1 — Ingress и IngressClass (ADR-0031, этап 2)

Перед началом прочитай `00-common.md`, `README.md` и `contracts.md` (§1 про Ingress и §2–3). Ветка: `intel2/d1-ingress` от `refactor`. Ты **ни от кого не зависишь** и никого не ждёшь: все точки расширения уже в `refactor`, чужие результаты берёшь из `contracts.md` и подменяешь фейками.

## Цель

Ingress становится видом графа и целью расследования: «почему не открывается сайт» объясняется цепочкой Ingress → Service → Pod по графу. Правила — `chain.ingress`; гипотезы `ingress.backend_service_missing`, `ingress.backend_no_endpoints`, `ingress.class_not_found`.

## Твои файлы (создаёшь и правишь только их)

- `internal/depgraph/kind_ingress.go` — `registerKind` для `networking.k8s.io/Ingress` и `IngressClass` (`Seedable: true` у Ingress; здоровье: `Degraded`, если backend или класс отсутствуют — по рёбрам, не по тексту) и `registerExpander("Ingress", …)`: рёбра `routes` (backend правил и `defaultBackend`, порт по имени/номеру) и `binds` (IngressClass), заглушки `Missing`; раскрытие Service-backend дальше стандартным расширением Service (endpoints и Pod'ы уже умеет первая итерация).
- `internal/depgraph/ingress_test.go` — тесты графа на `graphtest.FakeReader` (+ `InstallFakeKind` в `export_test.go` как образец регистрации).
- `internal/diagnose/targets_ingress.go` — `RegisterTarget("Ingress", collectIngress)` в `init()` по образцу `collectService`.
- `internal/diagnose/rules_chain_ingress.go` — `registerChainRule("chain.ingress", 1, …)`: три гипотезы из `contracts.md` §2, симптомы `ingress.*`, шаги для человека (TLS Secret не читается: `step.check_tls_secret`), рекомендации.
- `internal/diagnose/ingress_chain_test.go` — тесты правила на собранном контексте.
- `internal/diagnose/testdata/chain/ingress_*` — фикстуры.
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsChainIngress.json` — переводы всех кодов.

## Не трогай

- Общие файлы из списка «Заморожено» в `README.md`, в частности `kind_registry.go`, `kinds.go`, `build.go`, `engine.go`, `hooks.go`, `reader.go`, `types.go` (в т. ч. `RuleSetVersion`).
- Файлы других разработчиков (`kind_*.go`, `rules_chain_*.go`, `targets_*.go`, `testdata/chain/*`, `incidentsChain*.json` других видов), общие тесты первой итерации.
- `go.mod`, `go.sum`, `vendor/`.

## Задачи

1. **Граф.** Раскрытие Ingress: все хосты/пути, `defaultBackend`; backend типа `resource` (не Service) — вершина `Unknown`, причина `policy`/`unsupported` с фактом, а не падение. Ограничения графа — общие (лимиты не повышать).
2. **Цель Ingress.** Сбор: Ingress, события, Pod'ы backend-Service для связности (по графу — хук 100).
3. **Правило `chain.ingress`.** Candidate первопричины — на Ingress (`Explains` — симптомы Service/Pod ниже по цепочке); backend Service без endpoints указывает на Service/Pod (цепочка `Ingress > Service > Pod`), текстовые правила Service/Pod не дублируются (используй `chainRuleIDs`-механику `registerChainRule`).
4. **Ingress как звено цепочки.** При цели Service граф уже может найти Ingress-потребителя: проверь, что `chain.ingress` корректно молчит, если цель — не Ingress и Ingress не в графе.
5. **IngressClass.** Класс не найден → `ingress.class_not_found:high`; класс не указан и нет класса по умолчанию → факт `ingress.class_unspecified`, гипотеза уровнем ниже.
6. **TLS.** Имя Secret берётся только из `spec.tls[].secretName`; Secret не читается, в отчёте — шаг для человека. Значения Secret/сертификатов нигде.
7. **Переводы.** Все новые коды en/ru/cn в `incidentsChainIngress.json`.

## Тесты и фикстуры

Фикстуры `ingress_*` (golden только для основных): `ingress_healthy`, `ingress_backend_missing`, `ingress_backend_no_endpoints`, `ingress_class_missing`, `ingress_class_unspecified`, `ingress_forbidden` (`"forbid": ["networking.k8s.io/v1/Ingress"]`), `ingress_truncated` (малые `limits`), `ingress_default_backend`, `ingress_resource_backend`. Тест `TestChainLocalesHaveTheSameKeys` проходит. Тест «ConfigMap/Secret не читаются»: у фейка-читателя нет их запросов от твоего кода. Фронтенд golden-тест подхватит `report.json`: прогони `npm test`.

## Не входит

Метрики Ingress (D5), правило корреляции по общему backend (D6), карта (D7), права RBAC в чарте (D9).

## Готово, когда

- Все пункты «Задач» выполнены и покрыты тестами; `gofmt`, `go build ./...`, `go vet`, тесты своих пакетов, `go test ./internal/app/... ./internal/k8s/...` зелёные, `cd frontend && npm test && npm run build`.
- `git diff --stat origin/refactor` содержит только файлы из раздела «Твои файлы».
- Переводы en/ru/cn с паритетом ключей; нет сырых значений Kubernetes в переводах.
- Описание PR по `00-common.md` (в том числе строка для CHANGELOG).
