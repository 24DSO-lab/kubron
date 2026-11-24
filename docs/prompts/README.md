# Промпты второй итерации Kubron Intelligence (ADR-0031…0033)

Рабочие задания для **9 разработчиков**. Первая итерация (граф этапа 1, метрики этапа A, корреляция A+B, обратная связь) слита в `refactor`; здесь — всё, что осталось по ADR.

| ADR | Объём второй итерации | Кто |
|---|---|---|
| 0031, этап 2 | Ingress + IngressClass, HPA, существование ConfigMap, интерактивная карта зависимостей | D1, D2, D3, D7 |
| 0031, этап 3 | StatefulSet, Job, CronJob | D3, D4 |
| 0032, этап B | сигналы `pvc_usage_ratio`, `hpa_at_max_replicas`, `deployment_unavailable_replicas`, `ingress_5xx_ratio`, `ingress_latency_p95` и правила на них | D5 |
| 0033, этап C | правила `shared_dependency`, `shared_root`, `shared_symptom`, действие «пересчитать корреляцию» | D6 |
| сквозное | интерфейс остальных видов цели, пересчёт, документация | D8, D9 |

## Как устроено, чтобы никто никого не ждал

Раньше работа шла фазами (скелеты → реализация → интеграция). Теперь **все точки расширения уже в `refactor`**, а работа нарезана **вертикально по виду ресурса**: каждый разработчик целиком владеет своим видом — граф, цель, правила, фикстуры, переводы.

1. **Реестры вместо общих правок.** Граф: `registerKind`/`registerExpander` (`internal/depgraph/kind_registry.go`). Диагностика: `RegisterTarget`, `registerChainRule`. Корреляция: `registerRule`. Все регистрации — в `init()` собственных файлов, поэтому порядок слияния PR не важен.
2. **Контракты зафиксированы заранее** (`contracts.md`): формы вершин и рёбер, ID правил, коды гипотез, ID сигналов, виды корреляции, RPC. Код, который читает чужой результат (метрические правила читают узлы HPA/Ingress, корреляция читает «заглушки Missing», карта читает любой граф), опирается на контракт и на ручные фейки, а не на чужую реализацию.
3. **Один файл — один владелец, файлы разные у всех.** Общие файлы заморожены (список ниже). У каждого вида свои: `kind_<вид>.go`, `rules_chain_<вид>.go`, `targets_<вид>.go`, `testdata/chain/<вид>_*`, `incidentsChain<Вид>.json`.
4. **Тесты не общие.** Кто-то обновляет общий список в тесте — конфликт; поэтому общие тесты уже переведены на «подмножество» (`TestTargetRegistry`, `TestAllowedKindsExcludeSecret` читает `allowedReads`, `chainForbidGVK`), а свои проверки каждый кладёт в свои новые `_test.go`.
5. **`RuleSetVersion` не трогает никто.** Интегратор поднимает его до 3 один раз, после слияния всех PR (см. «Интеграция»).
6. **Права и allowlist готовы.** `diagnose.AllowedKinds` уже включает Ingress, IngressClass, HorizontalPodAutoscaler, ConfigMap; RBAC чарта — D9 проверяет.

## Состав команды

| № | Файл | Область |
|---|---|---|
| D1 | [d1-ingress.md](d1-ingress.md) | Ingress и IngressClass: граф, цель, правила |
| D2 | [d2-hpa.md](d2-hpa.md) | HorizontalPodAutoscaler: граф, цель, правила |
| D3 | [d3-config-statefulset.md](d3-config-statefulset.md) | ConfigMap-ссылки Pod и StatefulSet |
| D4 | [d4-batch.md](d4-batch.md) | Job и CronJob |
| D5 | [d5-metrics-b.md](d5-metrics-b.md) | Метрики, этап B: сигналы, стек, метрические правила |
| D6 | [d6-correlation-c.md](d6-correlation-c.md) | Корреляция, этап C, и RPC пересчёта |
| D7 | [d7-dependency-map.md](d7-dependency-map.md) | Интерактивная карта зависимостей (фронтенд) |
| D8 | [d8-frontend.md](d8-frontend.md) | Интерфейс: формы, типы, пересчёт, отображение новых данных |
| D9 | [d9-docs-deploy.md](d9-docs-deploy.md) | Документация, CHANGELOG, RBAC и чарт, ADR |

Общее: [00-common.md](00-common.md) (окружение, правила, коммиты, реестры). Контракты: [contracts.md](contracts.md).

## Матрица владения файлами

| Путь | Владелец |
|---|---|
| `internal/depgraph/kind_ingress.go`, `*_test.go` с префиксом `ingress_` | D1 |
| `internal/diagnose/targets_ingress.go`, `rules_chain_ingress.go`, `ingress_*_test.go` | D1 |
| `internal/diagnose/testdata/chain/ingress_*` | D1 |
| `frontend/src/i18n/locales/*/incidentsChainIngress.json` | D1 |
| то же с `hpa` / `kind_hpa.go` / `incidentsChainHpa.json` | D2 |
| `internal/depgraph/kind_statefulset.go`, `internal/diagnose/{targets,rules_chain}_statefulset.go`, `testdata/chain/sts_*`, `incidentsChainStatefulSet.json` | D3 |
| `internal/depgraph/configmap.go`, `expand_pod.go` (только блок ConfigMap), `internal/diagnose/graph_hook.go` (только опции графа), `rules_chain_config.go`, `testdata/chain/config_*`, `incidentsChainConfig.json` | D3 |
| `internal/depgraph/kind_job.go`, `kind_cronjob.go`, `internal/diagnose/{targets,rules_chain}_job.go`, `_cronjob.go`, `testdata/chain/job_*`, `cronjob_*`, `incidentsChainBatch.json` | D4 |
| `internal/signals/**`, `internal/diagnose/metrics_*.go`, `collect_signals.go`, `internal/k8s/metrics_stack.go`, `internal/k8s/manifests/metrics-stack.yaml`, `internal/k8s/metrics_signals_test.go`, `incidentsMetrics.json` | D5 |
| `internal/correlation/**`, `internal/incident/recorrelate*.go`, `internal/app/app_incident_correlate.go`, `internal/server/methods.go` (одна строка), `incidentsCorrelation.json` | D6 |
| `frontend/src/components/incidents/map/**`, `frontend/src/composables/dependencyMap*.ts`, `incidentsMap.json` | D7 |
| `frontend/**` кроме файлов D1–D7 (формы, типы, `incidents.json`, `incidentsIntel.json`, golden-тест) | D8 |
| `docs/**` (кроме `docs/prompts/`, `docs/adr/*` тела решений), `docs-site/**`, `CHANGELOG.md`, `deploy/**` | D9 |

**Заморожено (не править никому в этой итерации):** `internal/depgraph/{kind_registry,kinds,build,expand,health,model}.go`, `internal/diagnose/{engine,hooks,reader,types,collect}.go`, `internal/incident/{service,model}.go`, `frontend/src/composables/incidentModel.ts`, общие locale-файлы. Если без правки не обойтись — вопрос в PR; тимлид правит после слияния.

## Нужная помощь вне своих файлов

| Нужно | Как |
|---|---|
| Метрическому правилу (D5) нужны узлы HPA/Ingress | `contracts.md` §1: форма узлов и рёбер зафиксирована; в тестах D5 граф собирается вручную (`depgraph.New/AddNode/AddEdge`) |
| Корреляции (D6) нужны «заглушки Missing» ConfigMap и Service без endpoints | форма фиксирована; тесты на ручных графах |
| Карте (D7) нужен граф разных видов | фикстуры JSON отчёта с графом D7 пишет сам по `contracts.md` §1 |
| Интерфейсу (D8) нужен RPC пересчёта | сигнатура в `contracts.md` §5; в тестах `callApp` подменяется |

## Интеграция (после слияния всех PR, делает тимлид или назначенный интегратор)

1. `git pull` `refactor`; `RuleSetVersion = 3` в `internal/diagnose/types.go`; обновить golden-отчёты (`KUBRON_UPDATE_CHAIN=1`), просмотреть diff.
2. Полный прогон `go test ./...`, `npm test`, `npm run build`, линтер, CI.
3. Приёмка (ниже). Затем — CHANGELOG из строк PR.

### Приёмка (E2E)

1. **Ingress.** Backend-Service отсутствует → цепочка Ingress → Service (`Missing`), кандидат на Ingress; IngressClass не найден; TLS Secret — шаг для человека.
2. **HPA.** Цель отсутствует; достигнут `maxReplicas` (по conditions); с метриками — `hpa_at_max_replicas` повышает уверенность не выше чем на один уровень.
3. **ConfigMap.** Pod в `CreateContainerConfigError`, ConfigMap нет в кластере → `config.missing_reference` подтверждён графом; значения ConfigMap нигде не появляются (тест на утечку).
4. **StatefulSet / Job / CronJob.** По одному случаю на каждую гипотезу из `contracts.md`.
5. **Метрики B.** Без стека — полный отчёт и подсказка только для нужных сигналов; с ингресс-метриками — `ingress_5xx_ratio` в доказательствах Ingress.
6. **Корреляция C.** Десять расследований из-за одного отсутствующего ConfigMap → `shared_dependency`; цепочка Ingress → Service → Deployment → Pod → `shared_root`; пять workloads namespace с одним кодом → `shared_symptom` строго `low`; «пересчитать корреляцию» обновляет снимок и не трогает Events.
7. **Карта.** Для расследования с графом открывается карта: вершины по состоянию, `Missing`/`Unknown` различимы, выбор вершины показывает факты, клавиатурная навигация, пустой/усечённый граф без падений.
8. Старые отчёты (RuleSetVersion 1–2) открываются; детерминизм: один и тот же снимок → одинаковый отчёт.

## Ветки и PR

- Ветка: `intel2/dN-<кратко>` от `refactor`; PR в `refactor`; принимает тимлид.
- Порядок слияния **любой**: файлы не пересекаются, регистрация через `init()`.
- Коммиты — по `00-common.md` (подпись `Signed-off-by`, без `Co-Authored-By`).
