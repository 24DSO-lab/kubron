# Модель угроз Kubron (серверный режим)

> Статус: черновик для ревью вторым человеком. Область — веб-сервер `kubron-server` (PostgreSQL, несколько реплик, Helm-чарт).
> Десктоп использует часть тех же механизмов, но в модель не входит. Сценарии проверки — `docs/security-review.md`.

## Активы

| Актив | Где хранится | Чем защищён |
| --- | --- | --- |
| Ключ шифрования (`KUBRON_SECRET_KEY`) | Secret Kubernetes, вне БД | AES-256-GCM (`internal/secretbox`), ротация через `KUBRON_SECRET_KEY_PREVIOUS`, ADR-0008, `docs/deploy.md` «Ключ шифрования» |
| Учётные данные кластеров (kubeconfig, токены, client-key) | PostgreSQL, зашифрованы ключом выше | `internal/secretbox`, `internal/k8s/kubeconfig_mask.go` (наружу секреты не отдаются) |
| Сессии и CSRF-токены | PostgreSQL (`internal/websession`, ADR-0004) | cookie `__Host-kubron_session` по HTTPS, CSRF-токен, автовыход по бездействию (по умолчанию 15 минут) |
| Пароли пользователей | PostgreSQL | PBKDF2-HMAC-SHA256, 600 000 итераций (`internal/auth/password.go`) |
| Пароли и токены IdP (oidc-password) | PostgreSQL, зашифрованы | `internal/oidcpassword`, политика адресов IdP |
| Данные кластеров, доступные через Kubernetes API | кластеры | права общей учётки кластера и роли Kubron |
| Журнал аудита | PostgreSQL (`internal/audit`) | запись на сервере, пользователь не правит |

## Границы доверия

1. Браузер ↔ сервер (HTTPS, TLS завершает прокси).
2. Сервер ↔ PostgreSQL.
3. Сервер ↔ API-серверы кластеров.
4. Сервер ↔ IdP (oidc-password, OIDC-кластеры).
5. Цепочка поставки (исходники → образ и чарт → кластер оператора).
6. Прокси port-forward: проксируемое приложение на отдельном origin `*.pf.<baseDomain>`.

## STRIDE

Обозначения: S — подмена личности, T — искажение, R — отказ от действий, I — раскрытие, D — отказ в обслуживании, E — повышение привилегий.

### 1. Браузер ↔ сервер

| # | Угроза | Мера | Где |
| --- | --- | --- | --- |
| S | Захват первого запуска: первый, кто достиг пустой БД, становится администратором | `SetupAdmin` в сервисном режиме требует токен, заголовок `X-Kubron-Setup-Token`; чарт генерирует токен | `internal/server/setup.go`, `docs/http-security.md` «Первичная настройка», `docs/deploy.md` «Первый запуск» |
| S | Подмена cookie из соседнего поддомена (cookie tossing) | по HTTPS принимается только `__Host-kubron_session`; `baseDomain` на отдельном домене | `internal/server/session.go`, `docs/deploy.md` |
| S | Перебор пароля | экспоненциальная блокировка (15 с … 5 мин) и token bucket по адресу клиента; блокировка общая между репликами | `internal/auth/service.go`, `internal/server/ratelimit.go`, `docs/http-security.md` |
| T | CSRF | проверка Origin/`Sec-Fetch-Site` и CSRF-токен для всех методов, кроме публичных | `internal/server/http.go` |
| T/S | Подмена заголовка Host (ссылки, cookie, кэш) | allowlist Host: `KUBRON_ALLOWED_HOSTS` (чарт собирает из Ingress и `baseDomain`) | `internal/server/allowedhosts.go`, `deploy/helm/kubron/templates/_helpers.tpl` |
| S | Подделка адреса клиента через `X-Forwarded-*` | `KUBRON_TRUST_PROXY` включается явно и только за доверенным прокси | `internal/server/clientip.go`, `docs/deploy.md` |
| R | Отрицание действий | аудит каждого изменяющего вызова с пользователем и request id | `internal/audit`, `internal/server/requestid.go` |
| I | Внутренние ошибки раскрывают устройство сервера | в ответ 500 идёт общий текст и request id, подробности — в журнал | `internal/server/errsafe.go`, `internal/publicerr` |
| I | Секреты в журналах и ответах API | ключ не логируется (проверка в kind-job), секреты kubeconfig маскируются | `internal/k8s/kubeconfig_mask.go`, `internal/logging` |
| D | Заливка запросами | rate limit, лимит размера тела RPC (`maxRPCBody`) | `internal/server/ratelimit.go`, `internal/server/http.go` |
| E | Обход authz, IDOR по id команды/кластера | каждый экспортируемый метод `App` начинается с `a.authz*`; методы классифицированы в `allowedMethods` (тест `TestEveryAppMethodIsClassified`) | `internal/app/app_auth.go`, `internal/server/methods.go` |
| E | WebSocket/терминал без проверки источника | веб-сессия и обязательный Origin того же хоста при апгрейде (CSRF-заголовок для WebSocket недоступен); терминал — право `exec` и право на кластер | `internal/server/ws.go` (`newUpgrader`), `internal/server/terminal_ws.go` |
| E | XSS | CSP `script-src 'self'` без `unsafe-inline`/`unsafe-eval` | `internal/server/headers.go` |

### 2. Сервер ↔ PostgreSQL

| # | Угроза | Мера | Где |
| --- | --- | --- | --- |
| I | Чтение БД даёт учётки кластеров | значения зашифрованы ключом из отдельного Secret, не лежащим в БД | `internal/secretbox`, ADR-0008 |
| I | Перехват трафика | `sslmode=verify-full` в примере; egress ограничивается NetworkPolicy | `deploy/helm/kubron/values.yaml`, `docs/deploy.md` |
| T | Гонка при первом запуске/миграции | advisory lock PostgreSQL | `internal/migrate`, `internal/auth/setup_race_test.go` |
| D | Потеря БД | резервное копирование и проверка восстановления | `docs/backup.md` |

### 3. Сервер ↔ кластеры

| # | Угроза | Мера | Где |
| --- | --- | --- | --- |
| S/E | Общая учётка кластера: любой участник с `write` действует от её имени | права людей ограничивает app-RBAC Kubron (ADR-0006); см. остаточные риски | `docs/adr/0006-k8s-access-model.md` |
| E | Подключение kubeconfig, исполняющего чужой код (`exec`) или читающего файлы сервера | в веб-режиме exec-плагины и локальные файлы запрещены | `internal/k8s/connection.go` (`ErrExecForbidden`, `ErrLocalFilesForbidden`) |
| E | SSRF: адрес API-сервера указывает на metadata, loopback, внутренние сервисы | политика адресов проверяется при каждом соединении (в том числе после DNS), по умолчанию включена; NetworkPolicy закрывает `169.254.0.0/16` и `fd00:ec2::254/128` | `internal/netpolicy`, `internal/k8s/cluster_addr_policy.go`, `deploy/helm/kubron/templates/networkpolicy.yaml` |
| I | Вынос сохранённого токена подменой адреса сервера в kubeconfig | подстановка секрета отказывает при смене адреса, `proxy-url` или пользователя | `internal/k8s/kubeconfig_mask.go`, `docs/http-security.md` |
| E | Избыточные права in-cluster ServiceAccount | профиль `readonly` по умолчанию, Secret недоступны | `deploy/helm/kubron/templates/rbac.yaml`, `docs/deploy.md` «RBAC режима in-cluster» |

### 4. Сервер ↔ IdP

| # | Угроза | Мера | Где |
| --- | --- | --- | --- |
| S | Подмена IdP, SSRF через адрес IdP | политика адресов действует и для IdP; egress ограничивается `networkPolicy.egress.idp` | `internal/oidcpassword`, `internal/netpolicy`, `docs/deploy.md` |
| I | Пароль пользователя IdP попадает в журналы | пароль используется только для обмена токеном, не логируется и не хранится | `internal/oidcpassword` |

### 5. Цепочка поставки

| # | Угроза | Мера | Где |
| --- | --- | --- | --- |
| T | Подмена образа или чарта | подпись cosign (keyless) и аттестации происхождения для образа и чарта; проверка перед установкой | `.github/workflows/publish.yml`, `docs/deploy.md` «Проверка образа и чарта» |
| T | Уязвимая зависимость | Trivy (ворота перед push и еженедельный скан), osv-scanner, `npm audit`, Dependabot; исключения только в `.trivyignore` и `osv-scanner.toml` с обоснованием и сроком | `.github/workflows/ci.yml`, `scripts/check-ignores.py` |
| T | Скомпрометированный action в CI | actions закреплены по SHA, минимальные `permissions` | `.github/workflows/*` |
| I | Неизвестный состав | SBOM образа, чарта и десктопа в релизе и как аттестация | `.github/workflows/publish.yml`, `release.yml` |
| T | Публикация чужой версии | guard «версия уже опубликована», версия из тега, сверка с файлами | `docs/releasing.md` |

### 6. Прокси port-forward

| # | Угроза | Мера | Где |
| --- | --- | --- | --- |
| I/S | Проксируемое приложение получает cookie или CSRF Kubron | поток живёт на отдельном origin `{id}.pf.<baseDomain>`; доступ даёт одноразовый ticket и собственная cookie потока | `internal/pfproxy/doc.go`, `internal/pfproxy/signer.go` |
| S | Повторное использование ticket | ticket одноразовый (HMAC, jti в памяти реплики) и короткоживущий | `internal/pfproxy/signer.go`, `internal/pfproxy/proxy.go` |
| S | JS проксированного приложения подменяет cookie Kubron | `__Host-` у cookie сессии; `baseDomain` на отдельном регистрируемом домене (чарт предупреждает) | `docs/deploy.md`, `deploy/helm/kubron/templates/NOTES.txt` |
| E | Поток открыт на другой реплике | `421`/`409`, привязка по cookie на Ingress | `internal/pfproxy/proxy.go`, `docs/ha.md` |
| D | Зависшие потоки | `idleTimeout` и `maxDuration` | `deploy/helm/kubron/values.yaml` |

## Остаточные риски

1. **Общая учётка кластера.** Токен или сертификат кластера один на всех. Участник команды с правом `write` и выше действует в кластере от имени этой учётки, ограничений по namespace Kubron не вводит. Снижать: выдавать учётке только нужные права в самом кластере, ограничивать `write` командами, включать `disableExec`/`disableNodeShell`.
2. **Билет port-forward одноразовый только в пределах реплики.** Использованные ticket хранятся в памяти реплики; на другой реплике тот же ticket сработает повторно в пределах TTL. Снижать: sticky-сессии на Ingress, короткий TTL.
3. **`style-src 'unsafe-inline'`.** Monaco и xterm создают стили на лету (`internal/server/headers.go`). Скрипты при этом `script-src 'self'`, но внедрение CSS остаётся возможным.
4. **PBKDF2-SHA256 вместо Argon2id.** 600 000 итераций соответствуют рекомендации OWASP для PBKDF2, но слабее по памяти, чем Argon2id; переход возможен через версию схемы хеша (`hashScheme`).
5. **Минимальная длина пароля 8 символов** (`minPassRunes`). Снижать: SSO/OIDC, внешняя политика паролей.
6. **Секреты чарта-генератора.** Токен первичной настройки, сгенерированный чартом, лежит в Secret `<release>-setup` до удаления; GitOps требует `setup.existingSecret`.

## Пересмотр

Модель обновляется при изменении границ (новый тип аутентификации, новый вид прокси), при каждом найденном инциденте и перед мажорным релизом.
