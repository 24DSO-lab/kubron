# Smoke-e2e веб-версии

Проверяет сквозной путь: браузер → `kubron-server` → (опционально) кластер. Файл: `frontend/e2e/smoke.spec.ts`.

## Что проверяется
1. Первый запуск: создание администратора, вход в интерфейс.
2. Cookie сессии (`HttpOnly`, `SameSite=Lax`), экран входа после потери cookie.
3. Защита API: 401 без сессии, 403 без CSRF-токена, 403 при чужом Origin и для закрытых методов, `AuthStatus` без сессии не раскрывает пользователя.
4. (Нужен `E2E_KUBECONFIG`) добавление кластера текстом через диалог, `ConnectCluster`, `GetNamespaces` содержит `kube-system`.
5. (Нужны `E2E_KUBECONFIG` и `E2E_TERMINAL_POD=namespace/pod/container`, в образе должен быть `/bin/sh`) терминал: `OpenTerminal` выдаёт
   серверный id, по бинарному WebSocket `/api/terminal/{id}` команда выполняется в поде; `StartTerminalSession` с id клиента закрыт.

6. (`two-users.spec.ts`, gate этапа 2) два пользователя одновременно, у каждого своя сессия: оба видят себя в `AuthStatus`, права по роли
   (`ListUsers`: admin 200, user 403), в журнале аудита входы записаны от имени каждого с адресом, активный кластер у каждого свой
   (нужен `E2E_KUBECONFIG`), чужой терминал недоступен (нужен `E2E_TERMINAL_POD`), события логов приходят только владельцу потока
   (нужен `E2E_LOG_POD=namespace/pod/container` — под, пишущий в stdout), выход одного не затрагивает другого.
   Файл выполняется после `smoke.spec.ts` (по алфавиту) и использует созданные им администратора и кластер `e2e-kind`.

## Этап 5: port-forward, терминалы и флаги (`pf-terminals.spec.ts`)
Нужны два дополнительных сервера с пустыми каталогами данных (в CI их поднимает `ci.yml`):
- `E2E_PF_URL` — `KUBRON_PF_BASE_DOMAIN=localhost:8081 KUBRON_PF_SCHEME=http KUBRON_TERMINAL_IDLE_TIMEOUT=10s`, `--listen 127.0.0.1:8081`
  (браузер открывает `http://localhost:8081`; `*.localhost` Chromium разрешает в loopback);
- `E2E_LOCKED_URL` — `KUBRON_DISABLE_EXEC=1 KUBRON_DISABLE_PORT_FORWARD=1`, `--listen 127.0.0.1:8082`.
Также `E2E_KUBECONFIG`, `E2E_TERMINAL_POD` и `E2E_HTTP_POD=namespace/pod` (HTTP на порту 8080 отвечает `kubron-pf-ok`, в CI — `busybox httpd`).
Проверяется: `GetCapabilities` в режиме proxy; `StartPortForward` не открывает порт на хосте (`mode=proxy`, `localPort=0`), адрес из
`GetPortForwardURL` открывается в браузере на `{id}.pf.localhost`, cookie потока `kpf` есть, cookie сессии Kubron на хосте потока нет, API Kubron
там недоступен; адрес одноразовый, чужой контекст и чужой пользователь потока не получают; остановка закрывает поток; `portforward.start/stop`
в аудите; `terminal.start/stop` в аудите с причиной и длительностью, закрытие по idle-таймауту; флаги `KUBRON_DISABLE_*` отражены в
`GetCapabilities` и дают понятную ошибку.

### Argo CD за прокси потоков (`pf-argocd.spec.ts`)
Нужен кластер с Argo CD в namespace `argocd`, `E2E_PF_URL` и `E2E_ARGO_PASSWORD` (или `E2E_ARGO_PASSWORD_FILE`; пароль admin из
`argocd-initial-admin-secret`). В CI Argo CD v2.13.3 ставится шагом «Argo CD». Файл идёт до `pf-terminals.spec.ts` и сам создаёт
администратора и кластер. Проверяется: `OpenArgoUI` в режиме proxy (`mode=proxy`, `localPort=0`, сервис `argocd-server`), UI на
origin потока открывается по https с самоподписанным сертификатом пода, `/api/version`, вход `admin` (`/api/v1/session`), список
приложений с токеном, поток событий `/api/v1/stream/applications` отдаётся как `text/event-stream` и приносит первое событие (CI создаёт приложение `e2e-guestbook`), остановка потока его владельцем закрывает доступ.

## Gate этапа 3: команды на PostgreSQL (`frontend/e2e-pg`)
Отдельный набор: сервер работает на PostgreSQL с **пустой** базой, первым пользователем становится администратор организации.
`teams.spec.ts` проверяет:
1. Каждый видит только кластеры своей команды (`GetClusterStatuses`).
2. Чужой кластер неотличим от несуществующего: `ConnectCluster`, `GetClusterKubeconfig`, `SetClusterOIDCCredentials`, `MoveCluster`,
   `DeleteCluster` отвечают на чужой id так же, как на выдуманный (статус, код и текст без учёта id).
3. Администратор организации без членства в команде не получает данные кластера (и всё же управляет командами и составом).
4. Администратор команды управляет своей командой (добавляет участников) и не может ни читать состав чужой, ни добавлять в неё кластеры;
   обычный участник не управляет ни составом, ни кластерами.
5. Исключённый участник теряет кластер сразу; (нужен `E2E_KUBECONFIG`) уже открытое подключение закрывается в течение ~10 с и клиент получает
   событие `cluster:access-revoked`.
6. Журнал: администратор команды видит только события своей команды, администратор организации — все.
7. Интерфейс: в полосе кластеров только свои, раздел «Команды» есть у администратора команды.

Без `E2E_KUBECONFIG` кластеры указывают на недоступный адрес — проверки прав от этого не зависят, пропускается только тест с настоящим подключением.

```bash
createdb kubron_e2e   # пустая база
KUBRON_DATABASE_URL="postgres://…/kubron_e2e" KUBRON_SECRET_KEY="k1:$(openssl rand -base64 32)" \
  ./kubron-server --listen 127.0.0.1:8081 &
cd frontend && E2E_KUBECONFIG=/path/to/kubeconfig npm run test:e2e:pg
```
Адрес сервера — `E2E_PG_BASE_URL` (по умолчанию `http://127.0.0.1:8081`). Повторный запуск требует пустой базы: тесты меняют состав команд.
CI: job `e2e-pg` в `.github/workflows/ci.yml` (PostgreSQL 16 как сервис и kind), блокирующий.

## Gate HA: две реплики на одной PostgreSQL (`frontend/e2e-ha`)
Две реплики `kubron-server` (`ha-a` на 8081, `ha-b` на 8082) на одной **пустой** базе с общим ключом шифрования. Cookie у адресов
с разными портами общие, поэтому один браузерный контекст ходит на обе реплики, как за балансировщиком без привязки. `ha.spec.ts`:
1. Вход на реплике A, запрос на реплике B; любая реплика обслуживает любой запрос, пользователь, созданный на одной, появляется на другой сразу по сообщению `access.changed` (страховка — перечитывание раз в 2 с).
2. `Logout` на A закрывает сокет событий этой сессии на B за секунды (не за 10 с сверки); сокет другой сессии не затрагивается.
3. Блокировка входа общая: пять неверных паролей, поделённых между репликами, блокируют логин на обеих (обход не работает).
4. 50 сессий входят через обе реплики и работают на обеих (p95 `AuthStatus` < 2 с; цифры печатаются в журнал теста).
5. Гибель реплики: процесс A убивается `SIGKILL`; cookie, выданная A, действует на B; устаревший терминал A на B даёт `409 stream_elsewhere`,
   новый терминал открывается на B и выполняет команду (нужны `E2E_KUBECONFIG` и `E2E_TERMINAL_POD`).

```bash
createdb kubron_ha   # пустая база
export KUBRON_DATABASE_URL="postgres://…/kubron_ha" KUBRON_SECRET_KEY="k1:$(openssl rand -base64 32)" KUBRON_DRAIN_DELAY=0s
KUBRON_REPLICA_ID=ha-a ./kubron-server --listen 127.0.0.1:8081 & echo $! > /tmp/ha-a.pid
KUBRON_REPLICA_ID=ha-b ./kubron-server --listen 127.0.0.1:8082 &
cd frontend && E2E_HA_A_PID=$(cat /tmp/ha-a.pid) E2E_KUBECONFIG=… E2E_TERMINAL_POD=ns/pod/container npm run test:e2e:ha
```
Адреса — `E2E_HA_A_URL` и `E2E_HA_B_URL`. Повторный запуск требует пустой базы и живой реплики A. CI: job `e2e-ha` в `.github/workflows/ci.yml`.

## Локальный запуск
```bash
cd frontend && npm ci && npm run build:web && cd ..
go build -o kubron-server ./cmd/kubron-server
KUBRON_DATA_DIR="$(mktemp -d)" ./kubron-server --listen 127.0.0.1:8080 &
cd frontend && npx playwright install chromium
E2E_KUBECONFIG=/path/to/kubeconfig npm run test:e2e   # без переменной тест кластера пропускается
```
PowerShell: `$env:KUBRON_DATA_DIR = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "kubron-e2e-$(Get-Random)")`.
Каталог данных должен быть пустым — первый тест создаёт администратора.

## CI
Job `e2e` в `.github/workflows/ci.yml`: собирает веб-фронтенд и сервер, поднимает kind, запускает тесты.
Job блокирующий (после первого зелёного прогона `continue-on-error` убран).
