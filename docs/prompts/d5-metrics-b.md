# D5 — Метрики как доказательства, этап B (ADR-0032)

Перед началом прочитай `00-common.md`, `README.md` и `contracts.md` (§4 (сигналы) и §5 (контракт сигналов)). Ветка: `intel2/d5-metrics-b` от `refactor`. Ты **ни от кого не зависишь** и никого не ждёшь: все точки расширения уже в `refactor`, чужие результаты берёшь из `contracts.md` и подменяешь фейками.

## Цель

Пять новых сигналов и правила на них: `pvc_usage_ratio`, `hpa_at_max_replicas`, `deployment_unavailable_replicas`, `ingress_5xx_ratio`, `ingress_latency_p95`. Метрики остаются **необязательным** источником: нет стека — отчёт полный.

## Твои файлы (создаёшь и правишь только их)

- `internal/signals/**` — каталог (`catalog.go`: новые `spec`, константы окон, `FamilyVolume`/`FamilyIngress`), `Subject` (если нужны поля), запросы PromQL (закрытый список; значения только из `Subject` и графа), `internal/signals/*_test.go`.
- `internal/diagnose/metrics_*.go`, `collect_signals.go` — сбор новых сигналов (хук 200) и пост-правила; `internal/diagnose/metrics_stageb_test.go`.
- `internal/k8s/metrics_stack.go`, `internal/k8s/manifests/metrics-stack.yaml`, `internal/k8s/metrics_signals_test.go` — если стеку нужны новые задачи сбора (`kubelet_volume_stats_*` из cAdvisor/kubelet, kube-state-metrics `--resources` для `horizontalpodautoscalers`, `ingress-nginx` как опциональная задача сбора **только** при обнаружении).
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsMetrics.json` — новые факты, подсказки, причины.
- Фикстуры `internal/diagnose/testdata/metrics/*` (по образцу существующих).

## Не трогай

- Файлы видов D1–D4 (`kind_*`, `targets_*`, `rules_chain_*`, `testdata/chain/*`, `incidentsChain*`): их правила ты **не правишь**, а читаешь результат (гипотезы по коду из `contracts.md` §2).
- Общие: `engine.go`, `hooks.go`, `types.go`, `kind_registry.go`. `RuleSetVersion`.
- `go.mod`, `go.sum`, `vendor/`.

## Задачи

1. **Каталог.** Добавь 5 сигналов по таблице `contracts.md` §4: окна и единицы — константами с комментарием «ADR-0032 §4»; PromQL агрегирует за окно на стороне Prometheus; метки (`namespace`, `persistentvolumeclaim`, `horizontalpodautoscaler`, `ingress`, `exported_namespace`…) сверь с метриками из `internal/k8s/manifests/metrics-stack.yaml`; тест согласованности `metrics_signals_test.go` (`metrics`, `jobs`, `ksmKinds`, `labels`) ловит расхождение.
2. **Ингресс-метрики опциональны.** `ingress_5xx_ratio` и `ingress_latency_p95` выполняются только если семейство `FamilyIngress` обнаружено лениво (ADR-0032 §5); иначе — причина `family_missing`, без ошибки.
3. **Субъекты.** Адрес сигнала берётся из `ResourceRef` цели и узлов графа (`Kind`, `Group` по `contracts.md` §1). Добавь в `Subject` нужные поля. Субъект, которого нет в графе (Ingress не найден), — сигнал не запрашивается.
4. **Правила.**
   - `metrics.pvc_full`: `pvc_usage_ratio ≥ 0.9` (константа «ADR-0032 §4») → факт `metric.pvc_usage_ratio`; усиливает `sts.pvc_pending`/существующие гипотезы хранилища, если они есть, иначе гипотеза `metrics.pvc_nearly_full`.
   - `metrics.hpa_saturated`: `hpa_at_max_replicas=1` → усиливает `hpa.max_replicas_reached`, иначе `metrics.hpa_saturated`.
   - `metrics.unavailable_replicas`: `deployment_unavailable_replicas > 0` в окне → факт и `metrics.deployment_unavailable`.
   - `metrics.ingress_errors`: `ingress_5xx_ratio ≥ 0.05` или `ingress_latency_p95 ≥ 2s` → усиливает `ingress.backend_no_endpoints`, иначе `metrics.ingress_degraded`.
   Правило ищет чужую гипотезу по коду в `b.hypotheses` (пост-хук 200); отсутствие гипотезы — штатный случай (D1–D4 могут быть ещё не слиты). Уверенность чужой гипотезы меняется **не более чем на один уровень**, `high` метриками не достигается.
5. **Недоступность.** Причины `forbidden` (подсказка про `prometheus-proxy-access`), `no_stack`, `family_missing`, `unavailable`; подсказка «с мониторингом точнее» — только для сигналов найденного симптома, операция не «частичная».
6. **Фиксатор запросов.** Запросы не содержат значений полей объектов; имена ресурсов подставляются через экранирование PromQL (существующий helper).
7. **Переводы** en/ru/cn.

## Тесты и фикстуры

Таблица «сигнал → факт/гипотеза»; тест «нет стека»; тест «нет RBAC»; тест «чужая гипотеза есть/нет» (в тесте добавь гипотезу вручную через `Context`/`builder`, не импортируя D1–D4); golden-запросы PromQL; согласованность с манифестом стека; детерминизм.

## Не входит

Правила графа видов (D1–D4), корреляция (D6), вывод метрик в интерфейсе (общий компонент фактов уже показывает `metric.*`; D8 проверит).

## Готово, когда

- Все пункты «Задач» выполнены и покрыты тестами; `gofmt`, `go build ./...`, `go vet`, тесты своих пакетов, `go test ./internal/app/... ./internal/k8s/...` зелёные, `cd frontend && npm test && npm run build`.
- `git diff --stat origin/refactor` содержит только файлы из раздела «Твои файлы».
- Переводы en/ru/cn с паритетом ключей; нет сырых значений Kubernetes в переводах.
- Описание PR по `00-common.md` (в том числе строка для CHANGELOG).
