# D2 — HorizontalPodAutoscaler (ADR-0031, этап 2)

Перед началом прочитай `00-common.md`, `README.md` и `contracts.md` (§1 про HPA и §2–3). Ветка: `intel2/d2-hpa` от `refactor`. Ты **ни от кого не зависишь** и никого не ждёшь: все точки расширения уже в `refactor`, чужие результаты берёшь из `contracts.md` и подменяешь фейками.

## Цель

HPA — вид графа и цель расследования: «не масштабируется» объясняется по `scaleTargetRef` и conditions. Правило `chain.hpa`; гипотезы `hpa.target_missing`, `hpa.metrics_unavailable`, `hpa.max_replicas_reached`.

## Твои файлы (создаёшь и правишь только их)

- `internal/depgraph/kind_hpa.go` — `registerKind` для `autoscaling/HorizontalPodAutoscaler` (`Seedable: true`; здоровье по conditions `AbleToScale`, `ScalingActive`, `ScalingLimited`: `Degraded`, если `ScalingActive=False` по причине, отличной от `ScalingDisabled`) и `registerExpander`: ребро `scales` HPA → workload (`Deployment`, `StatefulSet`, `ReplicaSet`), заглушка `Missing`; неподдерживаемый `kind` в `scaleTargetRef` (например custom resource) — `Unknown`/`policy`, не ошибка.
- `internal/depgraph/hpa_test.go`.
- `internal/diagnose/targets_hpa.go` — `RegisterTarget("HorizontalPodAutoscaler", collectHPA)`.
- `internal/diagnose/rules_chain_hpa.go` — `registerChainRule("chain.hpa", 1, …)`; `internal/diagnose/hpa_chain_test.go`.
- `internal/diagnose/testdata/chain/hpa_*`.
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsChainHpa.json`.

## Не трогай

- Общие файлы из списка «Заморожено» в `README.md`, в частности `kind_registry.go`, `kinds.go`, `build.go`, `engine.go`, `hooks.go`, `reader.go`, `types.go` (в т. ч. `RuleSetVersion`).
- Файлы других разработчиков (`kind_*.go`, `rules_chain_*.go`, `targets_*.go`, `testdata/chain/*`, `incidentsChain*.json` других видов), общие тесты первой итерации.
- `go.mod`, `go.sum`, `vendor/`.

## Задачи

1. **Граф.** Раскрытие HPA в workload, дальше — стандартные раскрытия (Deployment → ReplicaSet → Pod). Для HPA нужно читать только сам HPA и цель.
2. **Цель HPA.** Сбор: HPA, целевой workload (через граф), Pod'ы цели для контекста, события HPA.
3. **`hpa.target_missing`** (`high`): цель — `Missing`.
4. **`hpa.metrics_unavailable`**: `ScalingActive=False` с причиной `FailedGetResourceMetric`/`FailedGetPodsMetric`/`InvalidMetricSourceType` и подобными — **по `reason` условия**, а не по свободному тексту; сообщение используется только как запасной путь с пониженной уверенностью. Рекомендация — шаг для человека (проверить metrics-server/адаптер), без обещаний по Prometheus.
5. **`hpa.max_replicas_reached`**: `ScalingLimited=True` (`TooManyReplicas`) и `status.currentReplicas == spec.maxReplicas`; рекомендация — поднять `maxReplicas` или оптимизировать нагрузку (код `hpa.raise_max_replicas`).
6. Не дублируй проблемы Deployment: если цель HPA нездорова по причине Pod'ов, выдай ссылку-цепочку на цель, а не копию её гипотез.
7. **Переводы** en/ru/cn.

## Тесты и фикстуры

Фикстуры `hpa_healthy`, `hpa_target_missing`, `hpa_metrics_unavailable`, `hpa_max_replicas`, `hpa_forbidden`, `hpa_truncated`, `hpa_unsupported_target`. Таблица conditions → гипотеза. Golden для основных. `npm test` после генерации `report.json`.

## Не входит

Метрики `hpa_at_max_replicas` (D5), корреляция (D6), карта (D7).

## Готово, когда

- Все пункты «Задач» выполнены и покрыты тестами; `gofmt`, `go build ./...`, `go vet`, тесты своих пакетов, `go test ./internal/app/... ./internal/k8s/...` зелёные, `cd frontend && npm test && npm run build`.
- `git diff --stat origin/refactor` содержит только файлы из раздела «Твои файлы».
- Переводы en/ru/cn с паритетом ключей; нет сырых значений Kubernetes в переводах.
- Описание PR по `00-common.md` (в том числе строка для CHANGELOG).
