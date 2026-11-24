# Общие правила для всех разработчиков (вторая итерация Kubron Intelligence)

Файл читают все D1–D9. Он дополняет ADR-0031…0034 (`docs/adr/`), `README.md` и `contracts.md` этого каталога.

## Что прочитать

1. ADR-0031 (этапы 2–3, §6), ADR-0032 (этап B), ADR-0033 (этап C) — только свои разделы, остальное по необходимости; ADR-0024, 0026 как база.
2. `docs/prompts/README.md` (матрица владения файлами) и `docs/prompts/contracts.md` (имена, формы графа, коды).
3. `docs/intelligence-contracts.md` — справочник первой итерации (типы `Report`, `Fact`, `MissingContext`…).
4. Свой промпт `dN-*.md`.

Работай от ветки `refactor`. Первая итерация слита: образцы для подражания — `internal/diagnose/targets_chain.go`, `rules_chain_storage.go`, `rules_chain_service.go`, `internal/depgraph/expand_net.go`, фикстуры `internal/diagnose/testdata/chain/`.

## Главный принцип: никто никого не ждёт

Все точки расширения уже есть в `refactor`. Ты работаешь **только в своих новых файлах**, подключаешься через реестры из `init()` и тестируешь на фейках и фикстурах. Общие файлы (`engine.go`, `service.go`, `kinds.go`, `build.go`, `reader.go`, `incidentModel.ts`…) не правятся. Нужная правка общего файла — вопрос в PR и разбор после слияния, а не ожидание.

Реестры:

| Что | Как подключить |
|---|---|
| Вид в графе | `registerKind(kindExt{...})` и `registerExpander(kind, fn)` из `init()` файла `internal/depgraph/kind_<вид>.go` (`kind_registry.go`; образец использования в тестах — `export_test.go`) |
| Цель расследования | `diagnose.RegisterTarget("Kind", collect)` в `init()` |
| Правило | `registerRule(id, epoch, apply)` в `init()` (диагностика) / `registerRule(name, fn)` (корреляция) |
| Сигнал метрик | запись в `internal/signals/catalog.go` (только D5) |
| Хук сбора / пост-хук | `RegisterCollectHook`, `registerPostHook` (`hooks.go`); порядок: граф 100, сигналы 200 |
| Переводы | свой файл `incidentsChain<Вид>.json` в `frontend/src/i18n/locales/{en,ru,cn}/` (уже созданы пустыми `{}`) |
| RPC | `callApp('Имя')` на фронтенде; на бэкенде — метод `App` и строка в `internal/server/methods.go` (только D6) |

Чего **не делаем**: не меняем `RuleSetVersion` (в конце один интегратор поднимает его до 3), `go.mod`/`go.sum`, не коммитим `vendor/`, не добавляем зависимости, не правим чужие тесты — свои тесты лежат в своих новых файлах.

## Окружение

```bash
mkdir -p ~/go-1.27.3 && tar -C ~/go-1.27.3 -xzf .test/go1.27.3.linux-amd64.tar.gz
export PATH=~/go-1.27.3/go/bin:$PATH GOTOOLCHAIN=local
tar -xzf .test/vendor.tar.gz                  # vendor/ в корне репозитория: НЕ коммитить (добавляй в коммит только свои файлы, не `git add -A`)
export GOFLAGS="-mod=vendor -tags=webkit2_41"
```

Если сборка пишет «vendor/modules.txt … not explicitly required» — vendor старее `go.mod`: выполни `GOFLAGS=-tags=webkit2_41 go mod vendor` (нужен доступ к прокси модулей). Без сети временно возьми `go.mod`/`go.sum` из коммита, под который собран vendor, и верни их перед коммитом (`git checkout go.mod go.sum`).

Проверки перед PR:

```bash
gofmt -l internal/ ; go build ./... ; go vet ./internal/...
go test ./internal/depgraph/... ./internal/diagnose/... ./internal/correlation/... ./internal/signals/... ./internal/incident/... ./internal/app/... ./internal/k8s/...
cd frontend && npm ci && npm test && npm run build     # npm test читает фикстуры цепочек: golden-тест рендерит каждый отчёт
```

## Правила кода

- Заголовок каждого нового Go-файла: `// Copyright 2026 The Kubron Authors` и `// SPDX-License-Identifier: Apache-2.0`.
- Комментарии — на русском, коротко; идентификаторы — английские.
- Доступ к кластеру — только через `Reader` пакета; `depgraph`, `diagnose`, `signals`, `correlation`, `feedback` не импортируют `internal/k8s`/`client-go` и не читают Secret (страж: `internal/k8s/opsplatform_static_test.go`).
- **Значения не утекают.** Содержимое ConfigMap, тексты логов, значения полей, имена ресурсов в журнал, аудит, метрики Kubron и выгрузки не пишутся. В отчёте — коды, ссылки на ресурсы, параметры, числа.
- **Детерминизм.** Всё, что идёт в отчёт, сортируется; время — из `Context.ObservedAt`.
- **«Нет» ≠ «не знаем».** `Missing` — только при `NotFound`; отказ RBAC, лимиты, недоступность — `Unknown` с причиной (`forbidden`, `unavailable`, `too_large`, `limit`, `policy`).
- Новые поля JSON — `omitempty`; старые документы читаются.
- Каждый новый код (симптом, факт, гипотеза, рекомендация, шаг, missing) — перевод в **en, ru и cn** в своём locale-файле; паритет ключей проверяют `TestChainLocalesHaveTheSameKeys` и `npm run i18n:check`. В переводах нет сырых значений Kubernetes (`Unknown`, `False` как слова интерфейса и т. п.).
- Тесты табличные, на фикстурах; без кластера и сети.

## Фикстуры цепочек (общий харнесс)

Формат: `internal/diagnose/testdata/chain/<вид>_<случай>/{manifests.yaml,case.json,report.json}`; описание — `internal/diagnose/chain_test.go`. Для случая с `"golden": true` отчёт регенерируется `KUBRON_UPDATE_CHAIN=1 go test ./internal/diagnose -run TestChain`, затем просматривается глазами. Префикс каталога — твой вид (`ingress_`, `hpa_`, `config_`, `sts_`, `job_`, `cronjob_`): чужие каталоги не трогай. Минимум на вид: здоровый случай, каждая гипотеза из `contracts.md`, `forbidden`, усечение графа.

## Коммиты и PR

Формат: `тип(область): описание` на русском, далее пустая строка и подпись **последней строкой**:

```
Signed-off-by: Vadim <vmilyash@mts.ru>
```

`Co-Authored-By:` и `Claude-Session:` не добавлять. Ветка: `intel2/dN-<кратко>` от `refactor`, перед push `git rebase origin/refactor`. В описании : что сделано (ADR и раздел), какие файлы затронуты, как проверить, что не вошло, **одна строка для CHANGELOG**, вопросы тимлиду.

## Definition of Done

- Сценарии твоего промпта покрыты тестами; `gofmt`, `go build`, `go vet`, тесты, `npm test` (если трогал фронтенд) зелёные; CI зелёный.
- Правки только в своих файлах из матрицы; новых зависимостей нет.
- Отчёт детерминирован; старые отчёты и инциденты открываются без ошибок.
- Лимиты и пороги — константами с комментарием «ADR-00XX §N».
- Нет логирования значений и чтения Secret; переводы в трёх языках.
