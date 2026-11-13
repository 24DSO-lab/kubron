# NOTES — Dev 4: дрейф конфигурации и baseline (ADR-0027)

Ветка: `feat/adr-0027-drift` (от `origin/refactor`), 9 коммитов; общие файлы правятся только в последнем — `chore(wire): baseline`.
ADR и их статус не менялись. Патчи: `git format-patch origin/refactor..HEAD` (эта папка). Автор и подпись во всех коммитах:
`Vadim <vmilyash@mts.ru>`, `Signed-off-by` есть, трейлеров `Co-Authored-By:` и `Claude-Session:` нет.

## Что сделано

| Пакет / место | Содержание |
|---|---|
| `internal/baseline` | Типы (`Baseline`, неизменяемая `Version`, `Object`/`Field`/`HashedValue`, `Exception`, `CheckResult`), интерфейс `Backend`, файловое хранилище (десктоп), общий контракт `baselinetest.Run` |
| `internal/drift` | Нормализация (`RulesVersion = 1`, 7 типов), keyed HMAC чувствительных значений, `Engine` (Snapshot / Preview / Check), источники изменений, исключения, `Service` без импорта платформы |
| `internal/pgstore/baselines.go`, миграция `0013_baselines` | PostgreSQL: область в ключе, содержимое версий шифруется secretbox (AAD: область+baseline+версия), исключения отдельной таблицей, retention, перешифровка, проверка ключа |
| wire (`chore(wire): baseline`) | `Store.Baselines()` (file/PG/ext), `RekeyReport.Baselines`, keycheck, `App`-делегаты `Baseline*`/`Drift*`, адаптер `Reader`, `AuditLookup`, `methods.go`, `exactPermission`, retention в `jobs.go`, локали, меню, CHANGELOG, индексы docs |
| UI | `BaselineView.vue` + `components/baseline/*`, `composables/useBaseline.ts`, `locales/{en,ru,cn}/baseline.json`, vitest, e2e |
| docs | `docs/baseline.md`, `docs-site` (en, ru), `docs/decisions/2026-11-12-baseline-exceptions.md` |

Требования ТЗ, которые выполнены: Secret не читается и не нормализуется (отдельный тест — чтений Secret у кластера нет);
чувствительные значения не попадают ни в результат, ни в предпросмотр, ни в журнал, ни в ошибки, ни в аудит
(тест с «канарейками» по всем представлениям: JSON, `%v`, `%+v`, `%#v`, slog); «не проверено» никогда не «соответствует»;
версия создаётся только с комментарием, подтверждением и токеном предпросмотра; baseline без версии не остаётся;
источник изменения — только с доказательством; нормализация идемпотентна (свойство + fuzz), golden-файлы на каждый тип.

## Что НЕ сделано (по ТЗ или из-за окружения)

- По ТЗ не делалось: применение baseline к кластеру, автоисправление, периодические проверки, GitOps-источник, сравнение кластеров,
  compliance, режим «новые ресурсы вне baseline» (`StatusNew` в типах зарезервирован, движок его не выдаёт).
- **vitest и e2e не запускались**: в песочнице нет сети, `npm ci` недоступен. Проверено вручную: чистые функции composable
  (запуск через `node --experimental-strip-types`), строгая проверка типов `useBaseline.ts` обычным `tsc`, `npm run i18n:check`-логика
  (`node scripts/i18n-check.mjs` — OK). `vue-tsc` и сборка фронтенда не запускались. e2e (`frontend/e2e/baseline.spec.ts`) проверяет
  только закрытость RPC без сессии: сценарии с живым кластером в e2e не входят.
- Wails-привязки `wailsjs/go/main/App.d.ts` генерируются при сборке: перед `vue-tsc` нужен `wails generate module`.
- Проверок на реальном кластере не было: чтение кластера покрыто двойниками (`fakeCluster`) и тестами адаптера ошибок.

## Команды проверки

```sh
mkdir -p ~/sdk && tar xzf .test/go1.27.3.linux-amd64.tar.gz -C ~/sdk && tar xzf .test/vendor.tar.gz
sed -i 's|^# github.com/coreos/go-oidc/v3 v3.14.1|# github.com/coreos/go-oidc/v3 v3.21.0|' vendor/modules.txt
export PATH=$HOME/sdk/go/bin:$PATH GOFLAGS=-mod=vendor GOTOOLCHAIN=local
go test -count=1 -race ./internal/baseline/... ./internal/drift/...
go test -count=1 ./internal/store/ ./internal/auth/ ./internal/server/ ./internal/app/ ./internal/jobs/ ./internal/k8s/
go test -count=1 -run BaselineRetention ./cmd/kubron-server/        # нужен frontend/dist (например, пустой index.html)
go test -run '^$' -fuzz FuzzNormalizeTree -fuzztime 30s -fuzzminimizetime 0 ./internal/drift/
# PostgreSQL (не от root): initdb, pg_ctl start, создать БД kubron_test
KUBRON_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:54329/kubron_test?sslmode=disable' go test -count=1 ./internal/pgstore/
gofmt -l . ; python3 scripts/license-headers.py --check ; node scripts/i18n-check.mjs
cd frontend && npm ci && npm test && npm run i18n:check
```

Результат на момент сдачи: все перечисленные Go-команды, gofmt, license-headers и i18n-check зелёные; `scripts/check-migrations.sh`
(up/down/up на пустой БД) проходит. Флаг `-fuzzminimizetime 0` нужен потому, что минимизация корпуса движком fuzz на этом
пакете занимает минуты; 221 тыс. запусков без нарушений.

## Допущения

1. **Нумерация миграций.** `0013_baselines` закреплена в ТЗ, а `migrate.Load` запрещает пропуски. Пока `0011` и `0012` других срезов
   не влиты, `pgstore` на одной этой ветке не стартует; для проверки я клал временные пустые `0011/0012` (в патчи они не входят).
2. **Новая версия сразу активная.** ADR не говорит, активируется ли версия при создании. Принято: да (иначе «зафиксировал и ничего
   не изменилось»); смена активной — отдельное действие (откат к старой версии, без изменения содержимого).
3. **Исключения** хранятся списком на версию, при создании новой версии копируются из активной (см. `docs/decisions/2026-11-12-baseline-exceptions.md`).
4. **Токен предпросмотра** — HMAC от отпечатка снятого состояния и id baseline ключом процесса, TTL 10 мин. Отпечаток считается
   отдельным ключом, не солью версии. **В HA-режиме предпросмотр и подтверждение, попавшие на разные реплики, не совпадут** (ключ
   у каждого процесса свой): вернётся «cluster state changed since the preview», нужно повторить предпросмотр. Для устранения нужен общий
   ключ (например, производный от `KUBRON_SECRET_KEY`); я не стал расширять конфигурацию без решения.
5. **Источник изменения.** Имя менеджера Kubron не уникально (Update без `FieldManager`), поэтому «применено из Kubron» ставится
   только при совпадении времени записи `managedFields` с записью аудита `ApplyYAML` (допуск 30 с). Это зависит от формата `detail`
   у `ApplyYAML` (`namespace=… kind=… name=…`) — если он изменится, источник перестанет устанавливаться (без ложных срабатываний).
   Имя человека не отдаётся: оно требует права на аудит.
6. **Что считается чувствительным:** `ConfigMap.data/binaryData`, `env[].value`, а также любая строка, которую меняет `logging.Redact`
   (иначе два разных секрета после редакции выглядели бы одинаково). В `ConfigMap` значения всегда хешируются, то есть показ «было/стало»
   для них невозможен — осознанная цена безопасности.
7. **HPA:** `spec.replicas` исключается у нагрузок под HPA (`autoscaling/v2`, список по пространству имён). Не прочитался HPA — оговорка
   `hpa-unreadable` в результате, replicas сравниваются как есть.
8. **Пределы:** 1000 ресурсов в версии, лимит `List` 1000, 200 исключений на версию, 100 пространств имён / 20 типов / 500 ссылок во входе,
   селектор до 1000 символов, комментарий до 2000. Выборка, обрезанная лимитом, помечается пропуском `truncated`.
9. **Десктоп и сервер различаются по объёму истории.** Файловое хранилище хранит не более 500 последних результатов на baseline, а версии
   на десктопе не чистятся (задач `jobs` там нет). Сервер: результаты 90 дней, версии — не менее 20 последних + активная (`KUBRON_BASELINE_*`).
   Для ext-хранилища (`pkg/ext` не менялся) baseline лежат в каталоге данных Kubron, как у файлового.
10. **Файловое хранилище:** путь `<корень>/<org>/<team>/<cluster>/<baseline>/…`; на десктопе область — как её отдаёт платформа. Идентификаторы
    проверяются (`^[A-Za-z0-9_-]{1,64}$`), документ чужой области в своём каталоге считается «не найден».
11. **Права.** Права не менялись (`baseline.manage` уже заведено шагом 0). Делегаты вызывают `a.authz(...)` первой строкой (требование
    `policy_test`) и затем `Provider.Begin` + `Op.Require`. Предпросмотр требует `baseline.manage` (он читает кластер для будущего изменения).
12. **Меню:** пункт «Baseline конфигурации» помещён в существующую группу «Config» (`navigation.ts`), а не в отдельную группу.
13. **Китайские тексты** (`cn`) написаны мной без носителя языка — стоит показать переводчику.
14. Запись аудита `ApplyYAML` в десктопе без `ClusterID`: такие записи принимаются для любого кластера (на десктопе аудит один на профиль).

## Просьбы к другим

- **Всем, кто добавляет миграции:** после слияния `0011` и `0012` проверить, что `0013_baselines` не конфликтует по именам таблиц (`baselines`, `baseline_*`).
допущение 5. Если ключ предпросмотра нужно сделать общим для реплик
  (допущение 4), удобнее всего вывести его из `KUBRON_SECRET_KEY` рядом с остальными ключами.
- **Ревьюеру:** решение об исключениях (`docs/decisions/2026-11-12-baseline-exceptions.md`) ждёт ревью человеком.
- **Frontend:** запустить `npm ci && npm test && npm run build` и `wails generate module` — в моей песочнице этого не было.

## Новые строки ошибок Go

Добавлены в `frontend/src/i18n/locales/{en,ru,cn}/backend.json` (ключи с префиксом `baseline…`), `i18n-check` зелёный. Все 53 строки идут из
`internal/baseline`, `internal/drift`, `internal/pgstore/baselines.go`, `internal/app/app_baseline.go`; обёртки вида `%w: …`
переводятся рекурсивно (`{p0}: a comment is required`). Ошибки Go — на английском, комментарии — на русском.
Добавлены также названия действий аудита (`audit.json`: `BaselineCreate`, `BaselineVersionAdd`, `BaselineActivate`,
`BaselineExceptionAdd`, `BaselineExceptionRemove`, `BaselineDelete`, `DriftCheck`) и `nav.views.baseline`.

## Строки для CHANGELOG (уже внесены в `[Unreleased]` → Added)

- **Configuration baseline and drift:** record the current configuration of selected Deployments, StatefulSets, DaemonSets, Services, Ingresses,
  NetworkPolicies and ConfigMaps as a baseline and run a check to see what has drifted. Resources are reported as match, changed, missing or
  not checked (never silently a match); versions are immutable and created only after a preview, a comment and an explicit confirmation;
  exceptions are kept separately; Secrets are never read and sensitive values are stored only as keyed hashes. Kubron only reads the cluster.
- **Baseline permission and settings:** new `baseline.manage` permission (administrators by default) for creating versions, changing the active
  version, exceptions and deletion; viewing and running a check need `read`. Server: results are kept 90 days
  (`KUBRON_BASELINE_RESULTS_RETENTION_DAYS`), at least 20 latest versions per baseline, the active one always (`KUBRON_BASELINE_MIN_VERSIONS`);
  PostgreSQL migration `0013_baselines`, version content is encrypted and included in key rotation.
