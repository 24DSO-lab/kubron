# Контракты второй итерации

Единый источник имён и форм. Реализуй **буквально**. Менять контракт может только его владелец отдельным PR с уведомлением затронутых (указаны в таблицах). Типы `Report`, `Fact`, `Hypothesis`, `MissingContext` — как в коде (`internal/diagnose/types.go`) и `docs/intelligence-contracts.md`.

## 1. Граф: формы вершин и рёбер

Идентификатор вершины — `Ref{Group, Kind, Namespace, Name}`. Рёбра — `depgraph.EdgeType`: `owns`, `selects`, `backed_by`, `routes`, `mounts`, `binds`, `runs_on`, `scales`. Состояние ребра: `ok`, `broken`, `missing`, `unknown`. Отсутствующая ссылка — вершина-заглушка `Missing` и ребро `missing` (ADR-0031 §2).

| Вид (Group/Kind) | Ребро | Условия | Владелец, потребители |
|---|---|---|---|
| `networking.k8s.io/Ingress` | `routes`: Ingress → `Service` (backend правил и `defaultBackend`) | Service нет → заглушка `Missing`, ребро `missing`; Service есть, endpoints нет → у Service своё состояние `Degraded`, ребро Ingress→Service `ok` | D1; читают D5, D6, D7 |
| `networking.k8s.io/Ingress` | `binds`: Ingress → `networking.k8s.io/IngressClass` (`spec.ingressClassName`, затем аннотация `kubernetes.io/ingress.class`) | класса нет → заглушка `Missing`, ребро `missing`; класс не указан и нет класса по умолчанию — вершины нет, факт отчёта `ingress.class_unspecified` | D1 |
| `autoscaling/HorizontalPodAutoscaler` | `scales`: HPA → workload (`spec.scaleTargetRef`: Deployment, StatefulSet, ReplicaSet) | цели нет → заглушка `Missing`, ребро `missing`; HPA `Degraded`, если `ScalingActive=False` (кроме `ScalingDisabled`) | D2; читают D5, D6, D7 |
| core `ConfigMap` | `mounts`: Pod → ConfigMap (`volumes`, `envFrom`, `env.valueFrom`); флаг `optional: true` → ребро `ok` даже при отсутствии | ConfigMap нет → заглушка `Missing`, ребро `missing`. **`data`/`binaryData` не покидают `depgraph`** | D3; читают D6, D7 |
| `apps/StatefulSet` | `owns`: StatefulSet → Pod; `owns`: StatefulSet → PVC по шаблону `<claim>-<sts>-<ordinal>` (`volumeClaimTemplates`) | PVC нет → заглушка `Missing` | D3; читают D5, D6, D7 |
| `batch/Job` | `owns`: Job → Pod | Job с условием `Failed` → `Degraded`; `Suspended` по `spec.suspend` | D4 |
| `batch/CronJob` | `owns`: CronJob → Job | `spec.suspend` → `Suspended` | D4 |

Секрет остаётся вершиной `Unknown` с причиной `policy` (без изменений). Лимиты графа — прежние (ADR-0031 §3); новый вид не повышает лимиты.

## 2. Правила диагностики и коды

ID правил (`registerChainRule`), эпоха 1 у всех:

| ID правила | Владелец | Цель |
|---|---|---|
| `chain.ingress` | D1 | Ingress (и Ingress как звено цепочки Service/Pod) |
| `chain.hpa` | D2 | HorizontalPodAutoscaler |
| `chain.config_refs` | D3 | Pod (и всё, что ведёт к Pod) со ссылками на ConfigMap |
| `chain.statefulset` | D3 | StatefulSet |
| `chain.job` | D4 | Job |
| `chain.cronjob` | D4 | CronJob |
| `metrics.pvc_full` | D5 | PVC / Pod с PVC |
| `metrics.hpa_saturated` | D5 | HPA и workload под HPA |
| `metrics.unavailable_replicas` | D5 | Deployment |
| `metrics.ingress_errors` | D5 | Ingress |

Коды гипотез (**зафиксированы**; новые коды владелец добавляет с тем же префиксом без согласования):

| Код | Владелец | Смысл |
|---|---|---|
| `ingress.backend_service_missing` | D1 | Service backend не найден |
| `ingress.backend_no_endpoints` | D1 | у backend нет готовых endpoints |
| `ingress.class_not_found` | D1 | IngressClass не найден |
| `hpa.target_missing` | D2 | `scaleTargetRef` указывает на несуществующий объект |
| `hpa.metrics_unavailable` | D2 | условие `ScalingActive=False` по причине недоступных метрик (по условию, не по тексту сообщения, где возможно) |
| `hpa.max_replicas_reached` | D2 | `ScalingLimited=True`, текущее = `maxReplicas` |
| `config.missing_reference` | D3 | ссылка Pod на отсутствующий ConfigMap подтверждена графом (код существующий; D3 повышает уверенность и добавляет цепочку) |
| `sts.stuck_ordered_start` | D3 | упорядоченный запуск застрял на Pod N |
| `sts.pvc_pending` | D3 | PVC шаблона не связан |
| `sts.update_partitioned` | D3 | обновление остановлено `partition` |
| `job.backoff_limit_exceeded` | D4 | |
| `job.deadline_exceeded` | D4 | |
| `cronjob.suspended` | D4 | |
| `cronjob.missed_schedule` | D4 | пропущены запуски (`startingDeadlineSeconds`) |
| `cronjob.concurrency_blocked` | D4 | `concurrencyPolicy: Forbid` блокирует запуск |

Метрические правила D5 **усиливают** перечисленные гипотезы, если они есть в отчёте (подтверждающий `Fact` `metric.*`; уверенность — не более чем на один уровень, `high` метриками не достигается), а при их отсутствии выдают собственные гипотезы с префиксом `metrics.` (список в промпте D5). Они не зависят от реализации D1–D4: ищут гипотезу по коду во время анализа (пост-хук 200), а если её нет — работают самостоятельно.

Каждое правило пишет коды симптомов, фактов, рекомендаций и шагов с префиксом своей области (`ingress.`, `hpa.`, `config.`, `sts.`, `job.`, `cronjob.`). Все — с переводом en/ru/cn.

## 3. Цели

`diagnose.RegisterTarget(kind, collect)`: `Ingress`, `HorizontalPodAutoscaler`, `StatefulSet`, `Job`, `CronJob` (D1–D4). Namespace обязателен (как у Service). ConfigMap целью **не** является (звено цепочки Pod). Для каждой цели: базовый сбор — сама цель, затронутые Pod'ы, события; остальное — из графа (хук 100).

## 4. Сигналы метрик (D5)

ID сигналов (закрытый каталог `internal/signals/catalog.go`):

| ID | Семейство | Субъект | Значение |
|---|---|---|---|
| `pvc_usage_ratio` | `volume` (`kubelet_volume_stats_*`) | `Subject{Namespace, PVC}` | доля занятого места 0…1, окно `windowMedium` |
| `hpa_at_max_replicas` | `kube_state` | `Subject{Namespace, Name HPA}` | 1, если текущее = max, окно `windowShort` |
| `deployment_unavailable_replicas` | `kube_state` | Deployment | число недоступных реплик |
| `ingress_5xx_ratio` | `ingress` (`ingress-nginx`) | Ingress | доля ответов 5xx 0…1, окно `windowShort`; только при обнаружении семейства |
| `ingress_latency_p95` | `ingress` | Ingress | секунды, окно `windowShort` |

Если в `Subject` не хватает поля для нового субъекта (например имя HPA/Ingress/Deployment), D5 добавляет поле в `signals.Subject` в своём файле (пакет `signals` целиком его). Адрес сигналов — узлы графа: имя Ingress/HPA берётся из `ResourceRef` цели или узла графа (`Kind`, `Group` по §1).

Новые факты: `metric.pvc_usage_ratio`, `metric.hpa_at_max_replicas`, `metric.deployment_unavailable_replicas`, `metric.ingress_5xx_ratio`, `metric.ingress_latency_p95`. Недоступность — как в первой итерации: `metrics.unavailable` с причиной (`forbidden`, `no_stack`, `family_missing`…), подсказки — только для сигналов найденного симптома.

## 5. Корреляция и RPC (D6)

Виды кандидата (`Candidate.Kind`): `shared_dependency`, `shared_root`, `shared_symptom` (добавляются к `shared_node`, `shared_storage`).

| Вид | Источник | Минимум | Уверенность |
|---|---|---|---|
| `shared_dependency` | вершина `Missing` (ConfigMap и др.) или Service без endpoints, на которые ссылаются графы нескольких расследований за окно корреляции | ≥ 2 workloads | по четырём признакам ADR-0033 §4 |
| `shared_root` | `Hypothesis.Explains` кандидата первопричины, дающий общий workload-владелец у расследований разных слоёв цепочки | ≥ 2 расследования | по четырём признакам |
| `shared_symptom` | один namespace, ≥ 5 workloads с одним кодом симптома и одним кодом гипотезы | 5 | **только `low`**, структурной связи нет |

RPC (метод `App`, имя в `internal/server/methods.go`):

```go
// IncidentCorrelationRecompute пересчитывает снимок корреляции расследования по живым данным (не затрагивает Events).
// Права: operate на кластер. Аудит: incident.correlation_recompute. Возвращает обновлённое расследование.
func (a *App) IncidentCorrelationRecompute(clusterID, id string) (d *incident.Detail, err error)
```

Тот же `incident.Detail`, что у `IncidentGet`; ошибки — как у `IncidentRefresh`. Закрытое (`resolved`/`closed`) расследование не пересчитывается (`ErrIncidentClosed`-аналог первой итерации).

## 6. Интерфейс

- `types/incident.ts`: `WorkloadKind` расширяется до `TargetKind = 'Pod' | 'Deployment' | 'ReplicaSet' | 'StatefulSet' | 'Job' | 'CronJob' | 'Ingress' | 'HorizontalPodAutoscaler' | 'Service' | 'PersistentVolumeClaim' | 'Node'`; `WorkloadKind` остаётся алиасом для обратной совместимости (D8).
- Форма открытия: список видов из `TargetKind`, `Node` — без namespace (D8).
- Карта (D7): вход — `Report.graph` (`nodes`, `edges`, `partial`), `Report.chains`, факты по ресурсам; выход — компонент `DependencyMap.vue`, встраиваемый в `IncidentView` одной строкой (**D8 добавляет строку**, D7 готовит компонент и пропсы `report`, `selected`).
- Кнопка «Пересчитать корреляцию» (D8) вызывает `callApp('IncidentCorrelationRecompute', clusterID, id)`.

## 7. Что принадлежит кому в переводах

Каждый владелец заполняет свой `incidentsChain<Вид>.json` (для D5 — `incidentsMetrics.json`, для D6 — `incidentsCorrelation.json`, для D7 — `incidentsMap.json`); ключи — по схеме `frontend/src/i18n/locales/README.md`.
