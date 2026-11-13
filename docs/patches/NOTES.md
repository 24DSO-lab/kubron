# NOTES — Dev 1: платформа (ADR-0026), редактор логов (ADR-0024 §5 п.4), интеграция

Ветка: `feat/adr-0026-platform` (от `origin/refactor`). ADR и их статус не менялись.

## Что сделано

**Шаг 0 — контракт** (коммиты `feat(platform): tenant.Scope…`, `feat(auth): diagnostics.logs…`, `chore(i18n)…`; патчи: `patches/contract/`)
- `internal/tenant` (`Scope`, `Valid()`), `internal/opsplatform/contract.go` (типы, `Reader`, `Op`, `Provider`, ошибки).
- Права `diagnostics.logs` (admin, operator — явной записью) и `baseline.manage` (только admin); оба кластерные (`ClusterScoped`).
- Тесты прав — `internal/auth/permissions_ops_test.go`; строки прав — `auth.json` (см. «Допущения», п. 1).

**Шаг 1 — платформа** (патчи: `patches/platform/`)
- `Reader` (`reader.go`, `allowlist.go`, `errors.go`, `partial.go`): get/list по allowlist видов, Secret запрещён
  (в таблице его нет + отдельный запрет по `Kind`), deadline на вызов, пагинация List с `Truncated` (лимит числа
  объектов и байт), `PodLogs` с потолком байт, discovery с `ErrPartial`, перевод ошибок k8s → `ErrNotFound/Forbidden/
  Unavailable/TooLarge`, `ObservedAt`.
- `ClusterLimiter` (`limiter.go`): 4 чтения на кластер, `KUBRON_OPS_CLUSTER_CONCURRENCY` (1…32), учёт отмены, без утечек.
- `Provider.Begin` (`provider.go`): право и принадлежность кластера команде проверяются ДО чтения; `tenant.Scope` берётся
  из каталога; `Op.Require` перепроверяет; `Op.Audit` без содержимого (только скаляры, редактирование, усечение);
  `Op.End` — технический журнал (slog) и метрики, отдельно от аудита; `Op.Notify` — адресно владельцу вида.
- `Manager.ConnectionFor` (`internal/k8s/opsconn.go`): подключение вида к кластеру через `PlanFor`/`ConfigFor`, кластер
  не становится активным. `ConnectToCluster` вынесен на тот же код (`connect`), поведение не менялось.
- Адаптер `internal/app/ops_env.go`: `opsEnv` (реализация `opsplatform.Env`), `a.beginOp(req)` — единая точка входа
  для делегатов Dev 2–4. Экспортированных методов нет, RPC-политики не затронуты.
- Метрики: `kubron_ops_operations_total{module,operation,status}`, `kubron_ops_operation_duration_seconds{module,operation}`;
  значения вне формы `^[a-z][a-z0-9_]{0,31}$` сворачиваются в `other`. Подключение: `opsplatform.SetObserver(m)` в
  `cmd/kubron-server/metrics.go`.
- Статические тесты (`internal/k8s/opsplatform_static_test.go`): пакеты `opsplatform, incident, diagnose, readiness,
  baseline, drift` не импортируют `rest`/`clientcmd`/`internal/k8s`/`internal/app`/`credential`/`store`/`pgstore`, не
  вызывают `NewForConfig*`/`ConfigFor`/`PlanFor`/`NewManager`, не читают Secret (`.Secrets(`, GVK/GVR-литералы).
  Детекторы проверены на нарушающем коде. Общий `TestRestConfigIsBuiltOnlyInConfigFor` уже обходил весь `internal/`.

**Шаг 2 — `internal/logredact`**
- `Redact([]byte) (Result{Text,Replacements,Version}, error)`, `Version = 1`, `MaxInputBytes = 1 MiB`.
- Девять правил: `pem`, `auth-header`, `bearer`, `jwt`, `url-credentials`, `cloud-key`, `key-value`,
  `high-entropy-hex`, `high-entropy-base64`. Fail-closed: `ErrBinary` (не UTF-8/NUL), `ErrTooLarge`, `ErrRedaction`
  (паника, нет неподвижной точки, проверка результата не пройдена). Пустой `Result` при любой ошибке.
- Корпус `testdata/<правило>.positive|negative.txt` (≥3 случая каждого вида), тест покрытия, тест отпечатка правил
  (смена правил без подъёма `Version` — красный тест), fuzz (не паникует, идемпотентен, в результате не остаётся
  обнаруживаемых секретов; прогон 40 с без находок).

## Что НЕ сделано
- **Шаг 3 (интеграция)** — ждёт патчей Dev 2, 3, 4. Общего отчёта интеграции и списка расхождений срезов пока нет.
- Фронтенд-тесты (`npm ci && npm test`) и e2e не запускались: в окружении нет `node_modules` и сети. `npm run i18n:check`
  аналог — `node scripts/i18n-check.mjs` — зелёный.
- Тесты с PostgreSQL (`pgstore`, `check-migrations.sh`, e2e-pg) не запускались: PostgreSQL в окружении нет. Платформа
  своих таблиц не вводит, миграции 0011–0013 — у Dev 2–4.
- В `vendor/` новых зависимостей не потребовалось (`client-go/.../fake`, `discovery/fake`, `dynamic/fake` уже были).

## Команды проверки
```
export PATH=$HOME/sdk/go/bin:$PATH GOFLAGS=-mod=vendor GOTOOLCHAIN=local
go vet -tags webkit2_41 ./internal/{auth,tenant,opsplatform,logredact,k8s,metrics,app,pgstore}/... ./cmd/kubron-server/...
go test -tags webkit2_41 -race ./internal/{tenant,opsplatform,logredact}/...
go test -tags webkit2_41 ./internal/{auth,k8s,metrics,app,server,pgstore}/... ./cmd/kubron-server/...
go test -tags webkit2_41 -run xxx -fuzz FuzzRedact -fuzztime 40s ./internal/logredact/
gofmt -l . ; python3 scripts/license-headers.py --fix ; node scripts/i18n-check.mjs
git log --format=full origin/refactor..HEAD
```
(для `cmd/kubron-server` нужен `frontend/dist`: локально создана заглушка `frontend/dist/index.html`, в git её нет.)

## Допущения
1. **Строки прав — `auth.json`, не `roles.json`.** Подписи прав (`auth.perm.*`) и список прав на фронте
   (`types/auth.ts`: `Perm`, `PERM_LABEL_KEYS`) живут там; `roles.json` содержит только названия ролей. Добавлены
   `auth.perm.diagnosticsLogs` и `auth.perm.baselineManage` (en, ru, cn) и `Perm.DiagnosticsLogs`/`Perm.BaselineManage`.
2. **Обе новых права кластерные** (`auth.ClusterScoped`): действуют по роли в команде-владельце кластера.
3. **Существующие тесты изменены минимально**: `permissions_test.go` (оператору добавлен `diagnostics.logs` в
   ожидаемый набор) и `policy_test.go` (`permByIdent` — две новые записи, иначе `TestPermIdentTableCoversAllPermissions`
   красный). Новые тесты — в новом файле.
4. **`NewOp(base *Op, h Hooks)`**: контракт описывал только поля и методы `Op`; для работы методов добавлены `Hooks`
   и `NewOp` (для платформы и тестов доменов; `nil`-хуки безопасны: `Require` отказывает, остальное no-op). `Op.End`
   идемпотентен; `End` обязателен — он освобождает контекст операции.
5. **`ErrPartial` и `PartialError`/`PartialDetails`** — дополнение к замороженным ошибкам (в `partial.go`). `ServerResources`
   при частичном ответе discovery возвращает данные И ошибку с `errors.Is(err, ErrPartial)`; потребитель использует данные
   и передаёт `PartialDetails(err)` в `Op.End(err, partial...)`.
6. **Ошибки Reader**: 401 Unauthorized → `ErrForbidden`; неизвестный вид, Secret, пустая версия — `ErrForbidden` без обращения
   к кластеру; для `PodLogs` 400 (контейнер не запускался/нет предыдущего экземпляра) → `ErrNotFound`; лог больше
   `LimitBytes` → `ErrTooLarge` (не молчаливое усечение; домен может повторить с меньшим `TailLines`); истёкший токен
   продолжения List при уже прочитанных страницах → результат с `Truncated=true`.
7. **Лимиты Reader по умолчанию**: вызов 20 с; страница 200; объектов 500 (максимум 1000); ответ List 8 МиБ; лог 200 строк
   (макс. 2000), 256 КиБ (макс. 1 МиБ). Операция целиком — 60 с (`DefaultOpTimeout`). Значения в `ReaderLimits`/`Config`.
8. **Allowlist видов** (`allowlist.go`): core (Pod, Namespace, Node, Event, Service, Endpoints, ConfigMap, PVC, PV,
   ResourceQuota, LimitRange), apps (Deployment, ReplicaSet, StatefulSet, DaemonSet), batch (Job, CronJob), Ingress,
   IngressClass, NetworkPolicy, EndpointSlice, HPA, PDB, events.k8s.io/Event, StorageClass, PriorityClass, CRD, Validating/
   MutatingWebhookConfiguration. Версия API не ограничивается (нужны устаревшие версии). **Нужен другой вид — просьба к
   Dev 1** (запись в таблицу + тест). ConfigMap в allowlist: нужен drift (ADR-0027); редактирование значений — забота домена.
9. **Scope**: сервер — `OrgID` из каталога PostgreSQL (`pgstore.Store.OrgID()`), `TeamID` — команда-владелец из `Actor`;
   десктоп — `local` / `default`. Пустая часть scope → `auth.ErrClusterNotFound` (fail closed).
10. **Отказ по праву** пишет сам `Provider`: действие названо по модулю и операции (`incident`+`open` → `IncidentOpen`),
    `Result=denied`, `need=<право>`. Для этих действий (и для действий, которые домены передают в `Op.Audit`) нужны
    строки `audit.actions.*` — это wire-файлы доменов (Dev 2–4).
11. **Атрибуты `Op.Audit`**: допустимы только скалярные `slog.Attr` (string/int/uint/bool/float/duration/time); `Any`,
    группы и значения-структуры отбрасываются; строки редактируются `logging.Redact`, усекаются до 128 символов,
    пробелы → `_`. Паника/отказ приёмника аудита результат операции не меняет.
12. **Статический запрет импорта `internal/k8s`, `store`, `pgstore`, `credential`, `app`** в пакетах модулей: домен
    объявляет свой узкий интерфейс, платформу импортирует только слой связки. Нужно исключение — через NOTES.
13. **Метрики операций** подключаются только в сервере (`SetObserver`); в десктопе наблюдателя нет.
14. **logredact — осознанное избыточное скрытие**: `token: expired` в тексте ошибки, `secretName: …`, 40-символьные
    git-SHA и строки base64 (в т. ч. тела сертификатов длиннее 40 символов с высокой энтропией) будут скрыты. Дайджесты
    образов (`sha256:…`, `sha512:…`) сохраняются. Счётчики `max_tokens: 4096` не трогаются.
15. **logredact — отпечаток правил** ловит изменение шаблонов и `params`; изменение логики внутри функций правил он не
    видит — тогда `Version` поднимается вручную.

## Просьбы к другим
- **Dev 2 (Intelligence)**: логи — только через `Op.Reader.PodLogs`, затем `logredact.Redact`; при любой ошибке
  редактора фрагмент НЕ сохраняется; в запись сохраните `logredact.Result.Version`. Перед выдачей сохранённого фрагмента —
  `op.Require(auth.PermDiagnosticsLogs)`. Через свой consumer-side интерфейс (в доменном пакете) принимайте `logredact`.
- **Dev 2–4**: в делегатах App — `ctx, op, err := a.beginOp(opsplatform.OpRequest{Module, Operation, ClusterID, Permission})`,
  затем `defer func(){ op.End(err, partial...) }()`; домен получает `op.Scope`, `op.Reader`. Имя модуля и операции —
  `^[a-z][a-z0-9_]{0,31}$`. Для дрейфа/readiness `ServerResources` может вернуть `ErrPartial` вместе с данными.
- **Dev 2–4**: в wire-коммит добавьте `audit.actions.<Действие>` для своих действий (en/ru/cn) и вызов
  `op.Notify` только с ID.
- **Dev 3, 4**: домены `readiness`, `baseline`, `drift` не импортируют `internal/k8s`/`store`/`pgstore` (статический тест).
  Если нужен вид ресурса вне allowlist (п. 8) — напишите Dev 1.
- Права: Dev 4 для записи baseline требует `auth.PermBaselineManage`, для чтения drift — `auth.PermRead`.

## Новые строки ошибок Go (backend.json en/ru/cn — добавлены)
`resource not found`, `access to the resource is forbidden`, `cluster source is unavailable`, `response is too large`,
`source is partially available`, `invalid operation request`, `resource name is required`,
`namespace is required for a namespaced resource`, `invalid label selector: {p0}`, `invalid field selector: {p0}`,
`cluster manager is not available`, `cluster connection is incomplete`, `log is not valid UTF-8 text`,
`log is too large to redact`, `log redaction failed`.

## Строки для CHANGELOG
- Added: права `diagnostics.logs` (admin, operator) и `baseline.manage` (admin) — подготовка к Intelligence и Lifecycle.
- Added: общая платформа операционных модулей — безопасное чтение кластера (allowlist, без Secret), лимит одновременных
  чтений на кластер (`KUBRON_OPS_CLUSTER_CONCURRENCY`, по умолчанию 4), аудит и технический журнал операций.
- Added: метрики `kubron_ops_operations_total` и `kubron_ops_operation_duration_seconds` (без пользователей, кластеров и
  namespace в метках).
- Added: версионированный редактор логов (`internal/logredact`), скрывает токены, пароли, ключи и высокоэнтропийные строки.

## Интеграция (шаг 3) — отчёт

**Статус: код и документы интеграции закрыты; открыты только пункты «Не запускалось» и «Решения по допущениям» ниже.**

Ветка `integrate/adr-0026` от `origin/refactor` (срезы Dev 2–4 уже влиты). Замечания Dev 2–4 из `docs/patches/` разобраны.

### Проверено
gofmt, `go vet`, лицензионные шапки, `i18n-check`; Go-тесты всех пакетов (в т.ч. PostgreSQL 16: `pgstore`), `check-migrations.sh`
(0011–0013 без дыр), `TestEveryAppMethodIsClassified`, Rekey/keycheck с incident fragments и baselines, статические проверки
«нет обхода платформы / нет чтения Secret». **Не запускалось** (нет сети, нет `node_modules`): `npm test`, `vue-tsc`, e2e,
`wails generate module`; перед релизом выполнить на машине с сетью.

### Закрыто по просьбам к Dev 1
- **Dev 3, allowlist.** Добавлены виды матрицы readiness (`internal/opsplatform/allowlist.go`); страж
  `internal/app/allowlist_matrix_test.go` падает, если вид матрицы не в allowlist и не в списке «виртуальных» (Review, Scale и т.п.).
  Secret по-прежнему запрещён дважды.
- **Dev 3, `beginOp` без подключения.** Аддитивное поле `OpRequest.StoredOnly`: без `Env.Clients`, `Op.Reader == nil`, права и
  принадлежность кластера проверяются как обычно; существование кластера проверяет окружение (`ClusterKnown`, десктоп —
  `LoadClusterMeta`). Переведены History/Get/Compare/Export readiness.

### Решения по допущениям
- **Dev 2, п. 1 (права).** Решение остаётся за ADR-0024/ревьюером; платформа ничего не добавляла и права не расширяла.
  Рекомендация: фрагменты инцидента — `diagnostics.logs`, настройки — `users.manage`, как в срезе.
- **Dev 4, п. 4 (ключ предпросмотра в HA).** Не менял конфигурацию. Рекомендация: вывести ключ из `KUBRON_SECRET_KEY`
  (HKDF с меткой `baseline-preview`); до этого повтор предпросмотра при смене реплики — известное ограничение.
- **Dev 4, п. 5 (формат `detail` у `ApplyYAML`).** Формат не менялся; платформа его не трогает. Зафиксировать тестом на стороне drift.

### Расхождения между срезами (для ревью)
1. Имена действий аудита: `IncidentOpen`/`BaselineCreate`/`DriftCheck` (CamelCase) против `readiness.run`/`readiness.export`.
   Не унифицировано: переименование ломает уже записанный аудит и переводы `audit.json`.
2. `maintenanceJobs` у Dev 2 принимает variadic `extra ...*jobs.Job`; Dev 3/4 добавляют задачи retention в `jobs.go` напрямую.
3. NOTES: Dev 3 — отдельный файл `NOTES-dev3.md`, Dev 2 — `NOTES-dev2.md`, Dev 4 — внутри `patches/`.
4. Меню: readiness и baseline — в группе «Конфигурация/Config»; инциденты — по решению Dev 2.
5. Китайские тексты (`cn`) всех срезов написаны без носителя языка.
6. Источники чтения: baseline (Dev 4) и readiness (Dev 3) идут через адаптер `Reader` платформы; домены платформу не импортируют.
