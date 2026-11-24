# D3 — ConfigMap-ссылки Pod и StatefulSet (ADR-0031, этапы 2 и 3)

Перед началом прочитай `00-common.md`, `README.md` и `contracts.md` (§1 про ConfigMap и StatefulSet, §2 (правило про `data`) и §3). Ветка: `intel2/d3-config-statefulset` от `refactor`. Ты **ни от кого не зависишь** и никого не ждёшь: все точки расширения уже в `refactor`, чужие результаты берёшь из `contracts.md` и подменяешь фейками.

## Цель

Существование ConfigMap, на который ссылается Pod, проверяется графом (`config.missing_reference` подтверждён графом, а не только сообщением kubelet), **без чтения содержимого**. StatefulSet — цель и вид графа: застревание упорядоченного запуска, PVC шаблонов, `partition`.

## Твои файлы (создаёшь и правишь только их)

- `internal/depgraph/configmap.go` (санитайзер уже есть), блок ConfigMap в `internal/depgraph/expand_pod.go` (флаг `ExpandConfigMapRefs`) — доведи до рабочего состояния; `internal/depgraph/config_refs_test.go`.
- `internal/diagnose/graph_hook.go` — **только** строку опций графа: включить `ExpandConfigMapRefs: true`.
- `internal/diagnose/rules_chain_config.go` — `registerChainRule("chain.config_refs", 1, …)`; `internal/diagnose/config_chain_test.go`.
- `internal/depgraph/kind_statefulset.go` — `registerKind` для `apps/StatefulSet` (`Seedable: true`; здоровье по `readyReplicas/currentRevision/updateRevision`) и `registerExpander`: рёбра `owns` на Pod и на PVC по `volumeClaimTemplates`.
- `internal/diagnose/targets_statefulset.go`, `rules_chain_statefulset.go` (`chain.statefulset`), `statefulset_chain_test.go`.
- `internal/diagnose/testdata/chain/config_*`, `testdata/chain/sts_*`.
- `frontend/src/i18n/locales/{en,ru,cn}/incidentsChainConfig.json` и `incidentsChainStatefulSet.json`.

## Не трогай

- Общие файлы из списка «Заморожено» в `README.md`, в частности `kind_registry.go`, `kinds.go`, `build.go`, `engine.go`, `hooks.go`, `reader.go`, `types.go` (в т. ч. `RuleSetVersion`).
- Файлы других разработчиков (`kind_*.go`, `rules_chain_*.go`, `targets_*.go`, `testdata/chain/*`, `incidentsChain*.json` других видов), общие тесты первой итерации.
- `go.mod`, `go.sum`, `vendor/`.

## Задачи

1. **ConfigMap в графе.** Ссылки из `volumes[].configMap`, `projected`, `envFrom[].configMapRef`, `env[].valueFrom.configMapKeyRef`; `optional: true` — ребро `ok`. Чтение — только существование: сразу после чтения вызови `SanitizeConfigMap`, `data`/`binaryData`/`managedFields`/`last-applied-configuration` не должны выйти из `depgraph` (**обязательный тест** на утечку: ни в `Node`, ни в `Context`, ни в JSON отчёта нет значений).
2. **Правило `chain.config_refs`.** Для `config.missing_reference` из текстового правила: если граф показывает ребро `missing` — гипотеза `high`, цепочка `Pod > ConfigMap`, текстовое правило теряет уверенность (штатный `chainRuleIDs`); если ConfigMap существует, а kubelet жалуется на ключ — уверенность не повышается (ключи не читаются): шаг для человека `step.check_configmap_key`.
3. **Бюджет чтения.** ConfigMap читаются по имени (`Get`), число ограничено общими лимитами `MaxReads`; при превышении — `Unknown/limit`.
4. **StatefulSet — граф и цель.** Pod'ы по ordinal, PVC шаблонов (`<claim>-<sts>-<n>`), ревизии (`controllerrevisions` НЕ читаются — используй `status.currentRevision/updateRevision` и метки Pod `controller-revision-hash`).
5. **`sts.stuck_ordered_start`**: `podManagementPolicy: OrderedReady`, Pod N не `Ready`, Pod'ов > N не создано — кандидат на Pod N (по цепочке — существующие правила Pod объясняют его причину; не копируй их).
6. **`sts.pvc_pending`**: PVC шаблона не связан — цепочка `StatefulSet > PVC`, причины хранилища — существующее `chain.storage` (не дублировать).
7. **`sts.update_partitioned`**: `updateStrategy.rollingUpdate.partition > 0` и `updatedReplicas < replicas` — информативная гипотеза `medium` (это может быть намеренно).
8. **Переводы** en/ru/cn в двух файлах.

## Тесты и фикстуры

`config_*`: `config_missing_volume`, `config_missing_envfrom`, `config_optional_missing`, `config_present_key_missing`, `config_forbidden`, `config_truncated`, `config_no_leak` (манифест ConfigMap с «секретными» значениями; проверь, что строки значений не встречаются в сериализованных `Context` и отчёте). `sts_*`: `sts_healthy`, `sts_stuck_ordered`, `sts_pvc_pending`, `sts_partitioned`, `sts_forbidden`, `sts_truncated`. Тест старого поведения: без графа/с отключённым флагом отчёт первой итерации не меняется (первая итерация — существующие тесты зелёные).

## Не входит

Правило корреляции `shared_dependency` (D6), карта (D7), метрики (D5).

## Готово, когда

- Все пункты «Задач» выполнены и покрыты тестами; `gofmt`, `go build ./...`, `go vet`, тесты своих пакетов, `go test ./internal/app/... ./internal/k8s/...` зелёные, `cd frontend && npm test && npm run build`.
- `git diff --stat origin/refactor` содержит только файлы из раздела «Твои файлы».
- Переводы en/ru/cn с паритетом ключей; нет сырых значений Kubernetes в переводах.
- Описание PR по `00-common.md` (в том числе строка для CHANGELOG).
