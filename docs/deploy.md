# Развёртывание kubron-server

Веб-сервер Kubron поставляется как контейнерный образ и Helm-чарт `deploy/helm/kubron`.
Модель угроз и требования к безопасности: раздел 8 плана разработки, решения — ADR-0004
(сессии), ADR-0007 (хранилище), ADR-0008 (секреты).

## Что нужно заранее

| Что | Зачем |
| --- | --- |
| PostgreSQL 14+ (внешний) | Пользователи, кластеры, журнал аудита, веб-сессии, состояние Helm. Миграции сервер применяет сам при старте |
| Ключ шифрования | Шифрует kubeconfig и токены Argo CD в БД (`KUBRON_SECRET_KEY`) |
| Обратный прокси с TLS | Сервер сам TLS не терминирует; без TLS cookie сессии идёт открытым текстом |

## Ключ шифрования

Формат: `id:base64`, где base64 кодирует ровно 32 случайных байта, а `id` — метка ключа (буквы,
цифры, `_ . -`, до 32 символов). Метка хранится в БД открытым текстом и нужна при ротации.

```sh
echo "k1:$(openssl rand -base64 32)"
```

Сохраните результат в менеджере паролей или KMS **до** первого запуска. Потеря ключа означает
потерю всех сохранённых секретов (kubeconfig, токены): прочитать их без ключа нельзя, подбор не
поможет. Приложение и чарт ключ не генерируют именно поэтому.

Ротация: новый ключ — в `KUBRON_SECRET_KEY`, прежний — в `KUBRON_SECRET_KEY_PREVIOUS` (через
запятую, если их несколько); затем `kubron-server rekey` перешифрует записи новым ключом.
Прежний ключ можно убирать только после `rekey`.

## Публичный URL для входа через браузер

Для OIDC-сценария `browser-callback` серверу нужен внешний адрес, на который IdP вернёт браузер.
Задайте `KUBRON_PUBLIC_URL` как публичный origin Kubron:

```sh
KUBRON_PUBLIC_URL=https://kubron.example.com
```

В production разрешён только `https`. Схема `http` допускается исключительно для локальной разработки
с `localhost`, доменом в зоне `.localhost` или loopback IP (например, `http://127.0.0.1:8080`).
Значение должно быть абсолютным URL без пути, учётных данных, query-параметров и fragment.
Пустое значение означает, что `browser-callback` не настроен; сервер проверяет URL при старте и завершает
запуск с ошибкой при небезопасном или некорректном значении. В текущем патче добавлена валидация конфигурации;
сам HTTP callback будет подключён следующим этапом B.3.

В Docker передайте переменную окружения через `-e KUBRON_PUBLIC_URL=...`. В Helm используйте
`kubron.extraEnv`, например:

```yaml
kubron:
  extraEnv:
    - name: KUBRON_PUBLIC_URL
      value: "https://kubron.example.com"
```

Адрес должен совпадать с origin, доступным браузеру пользователя, и указывать на тот же сервер,
который принимает callback. TLS завершается на обратном прокси; настройте прокси на передачу
запросов callback без изменения query-параметров.

## Запуск через Docker

Образ: distroless, пользователь `65532`, порт `8080`, настройка только через переменные окружения.

```sh
docker build -t kubron:dev --build-arg VERSION=0.3.0 .

docker run -d --name kubron -p 127.0.0.1:8080:8080 \
  --read-only --tmpfs /home/nonroot:uid=65532,gid=65532 --tmpfs /tmp \
  --cap-drop ALL --security-opt no-new-privileges \
  -e KUBRON_ALLOW_REMOTE=1 \
  -e KUBRON_SECRET_KEY='k1:…' \
  -e KUBRON_DATABASE_URL='postgres://kubron:…@db.example.com:5432/kubron?sslmode=verify-full' \
  kubron:dev
```

- Образ слушает `0.0.0.0:8080` (`KUBRON_LISTEN`), поэтому нужен `KUBRON_ALLOW_REMOTE=1`: без него
  сервер откажется стартовать на не-loopback адресе. Порт публикуйте только на `127.0.0.1` или
  во внутреннюю сеть, где стоит прокси с TLS.
- Строку подключения и ключ передавайте через окружение или секреты оркестратора, а не флагами:
  аргументы видны в списке процессов.
- Без `KUBRON_DATABASE_URL` сервер работает с файловым хранилищем в `HOME` (`/home/nonroot/.kubron`).
  Это режим для знакомства: данные пропадут вместе с tmpfs.
- Helm и клиентские библиотеки Kubernetes пишут в `HOME`, `XDG_*` и `TMPDIR`. Образ задаёт их
  внутри `/home/nonroot` и `/tmp`; при `--read-only` монтируйте оба каталога как tmpfs/том.

## Установка через Helm

```sh
kubectl create namespace kubron

# 1. Secret с ключом и строкой подключения (создаёт человек, не чарт)
kubectl -n kubron create secret generic kubron-secrets \
  --from-literal=KUBRON_SECRET_KEY='k1:…' \
  --from-literal=KUBRON_DATABASE_URL='postgres://kubron:…@db.example.com:5432/kubron?sslmode=verify-full'

# 2. Установка (образ по умолчанию — ghcr.io/24dso-labs/kubron)
helm install kubron deploy/helm/kubron -n kubron \
  --set secrets.existingSecret=kubron-secrets \
  --set image.tag=0.3.0
```

При теге `vX.Y.Z` workflow `.github/workflows/publish.yml` публикует образ
(`ghcr.io/24dso-labs/kubron`) и чарт (`oci://ghcr.io/24dso-labs/charts/kubron`) в ghcr.io — это и есть
`image.repository` чарта по умолчанию. Для собственной сборки соберите образ из `Dockerfile`,
загрузите в свой реестр и задайте `--set image.repository=…`.

Основные настройки — в `values.yaml` (с комментариями), допустимые значения проверяет
`values.schema.json`. Готовые наборы: `deploy/helm/kubron/ci/*-values.yaml`.

| Задача | Значения |
| --- | --- |
| Доступ снаружи с TLS | `ingress.enabled=true`, `ingress.className`, `ingress.hosts`, `ingress.tls` |
| Другое имя ключей в Secret | `secrets.keys.secretKey`, `secrets.keys.databaseUrl` |
| Работа с кластером, где запущен сам Kubron | `inCluster.enabled=true`, при необходимости `rbac.create=true` |
| Ограничить сеть | `networkPolicy.ingress.from`, `networkPolicy.egress.*` |

`secrets.create=true` создаёт Secret из values и годится только для разработки: ключ окажется в
истории релиза Helm. Ключ при этом всё равно не генерируется.

### Ingress и WebSocket

Чарту нужен ingress с TLS, а при включённом ingress с TLS чарт сам ставит `KUBRON_TRUST_PROXY=1`.
Это значит, что сервер верит заголовку `X-Forwarded-Proto` и помечает cookie как `Secure`.
Включайте доверие к прокси, только если под доступен лишь через прокси (NetworkPolicy чарта это
обеспечивает при заданном `networkPolicy.ingress.from`).

События и терминал идут по WebSocket (`/api/ws`, `/api/terminal/*`). Для ingress-nginx нужны
большие таймауты, иначе соединения рвутся через 60 секунд; готовый набор аннотаций, включая
привязку по cookie, лежит в `ci/ingress-values.yaml`.

### Первый запуск и токен первичной настройки

Пока в БД нет пользователей, анонимный `SetupAdmin` превратился бы в захват сервера, поэтому в сервисном режиме он
принимает только запросы с токеном (`X-Kubron-Setup-Token`, переменная `KUBRON_SETUP_TOKEN`, не короче 16 символов).
Чарт включает токен по умолчанию (`setup.enabled`):

- без настроек чарт создаёт Secret `<release>-setup` со случайным токеном (`setup.tokenLength`, по умолчанию 32).
  Значение сохраняется при `helm upgrade` (чарт читает его через `lookup`). Прочитать:

  ```sh
  kubectl -n <namespace> get secret <release>-setup -o jsonpath='{.data.KUBRON_SETUP_TOKEN}' | base64 -d
  ```

- `setup.existingSecret` и `setup.tokenKey` — свой Secret с токеном. **Для GitOps обязательно**: Argo CD и
  `helm template` рендерят чарт без доступа к кластеру, `lookup` пуст, и сгенерированный токен менялся бы при каждом
  рендере;
- `secrets.keys.setupToken` — токен лежит в основном Secret (`secrets.existingSecret`) под этим ключом.

Токен вводится на экране «Добро пожаловать». Без сети администратора можно создать прямо в БД; пароль передавайте
через stdin или `KUBRON_ADMIN_PASSWORD`, не аргументом командной строки (он попал бы в историю оболочки и список
процессов):

```sh
kubectl -n <namespace> exec -i deploy/<release> -- /kubron-server create-admin --username admin < password.txt
```

После создания первого администратора токен не используется; Secret `<release>-setup` можно удалить вместе с
`setup.enabled=false`.

### Домены, допустимые Host и привязка к реплике

- **Допустимые Host.** Чарт задаёт `KUBRON_ALLOWED_HOSTS` сам: это хосты из `ingress.hosts` (при `ingress.enabled`),
  `*.pf.<portForward.baseDomain>` (при port-forward) и `kubron.allowedHosts`. Сервер обслуживает только запросы с этими
  значениями заголовка `Host`: так нельзя подменить адрес в ссылках, cookie и кэше прокси. Если доступ идёт без Ingress
  (через Service или LoadBalancer), перечислите свой адрес в `kubron.allowedHosts`, иначе список останется пустым (чарт
  предупредит в `NOTES.txt`).
- **Доверие к прокси.** `KUBRON_TRUST_PROXY=1` чарт включает сам при Ingress с TLS (`kubron.trustProxy` задаёт значение
  явно). Включайте только когда под достижим лишь через прокси: иначе клиент подделает `X-Forwarded-Proto` и адрес для
  ограничения попыток входа. Число доверенных прокси — `KUBRON_TRUSTED_PROXY_HOPS` или `KUBRON_TRUSTED_PROXIES`
  (`kubron.extraEnv`; `docs/http-security.md`).
- **Привязка по cookie на Ingress** (`nginx.ingress.kubernetes.io/affinity: cookie`, пример в `ci/ingress-values.yaml`)
  рекомендуется для обоих хостов: основного и `*.pf.<baseDomain>`. Любая реплика обслуживает любой запрос, но терминалы,
  логи и потоки port-forward живут на реплике, которая их открыла: запрос к чужой реплике получает `409 stream_elsewhere`
  (или `421` для потока port-forward) и повторяется. Билет port-forward одноразовый в пределах одной реплики, поэтому
  привязка потока к реплике обязательна, а не оптимизация. Привязка уменьшает число таких повторов.

### NetworkPolicy

По умолчанию входящий трафик разрешён из namespace `ingress-nginx`, исходящий — DNS, PostgreSQL (порт 5432),
API-серверы кластеров и провайдеры OIDC. Политика требует CNI с поддержкой NetworkPolicy.

| Правило egress | По умолчанию | Настройка |
| --- | --- | --- |
| DNS (53) | kube-dns в `kube-system` | `networkPolicy.egress.dns.to` |
| PostgreSQL (5432) | куда угодно: **сузьте** до своей БД | `postgres.cidrs` (список CIDR) или `postgres.to` (готовые правила), `postgres.ports` |
| API-серверы кластеров (443, 6443) | любые адреса **кроме** `169.254.0.0/16` и `fd00:ec2::254/128`, плюс любые поды кластера | `clusterApi.cidrs` (рекомендуется), `clusterApi.to`, `clusterApi.except`, `clusterApi.ports`, `clusterApi.allowCluster` |
| IdP на нестандартных портах | порт 443 уже входит в правило API-серверов; отдельного правила нет | `idp.ports` (например `[8443]`), `idp.cidrs`, `idp.to` |

**Исключения по умолчанию.** Адреса `169.254.0.0/16` (link-local, в том числе метаданные облака `169.254.169.254`) и
`fd00:ec2::254/128` (метаданные AWS по IPv6) закрыты всегда, пока вы не заменили правило своим `to`: пользователь Kubron
не должен суметь направить сервер на metadata-сервис. Это **второй рубеж**: первый — политика адресов приложения
(`kubron.clusterAddrPolicy`, раздел «Адрес API-сервера кластера»), и он проверяет адрес на каждом соединении, а не
только в сети. Исключения IPv4 и IPv6 пишутся одним списком, чарт раскладывает их по `0.0.0.0/0` и `::/0`.

**Сузьте до своих сетей.** Лучший вариант — перечислить сети, где живут ваши кластеры и IdP:

```yaml
networkPolicy:
  egress:
    postgres:
      cidrs: [10.30.0.0/24]
    clusterApi:
      cidrs: [10.20.0.0/16, 192.168.50.0/24]
    idp:
      ports: [8443]
      cidrs: [10.40.0.0/24]
```

Приоритет внутри правила: `to` (готовые правила NetworkPolicy, заменяют всё) > `cidrs` > «любые адреса, кроме `except`».
Готовый пример — `deploy/helm/kubron/ci/restricted-values.yaml`.

**CNI.** `ipBlock` по спецификации описывает внешние адреса, и часть CNI (например Cilium) не относит к нему адреса
узлов и подов своего кластера. Поэтому правило API-серверов по умолчанию содержит и `namespaceSelector: {}` (любые поды
на тех же портах): оно нужно, когда API-сервер или IdP работают подом в этом же кластере. Если API-сервер вашего
кластера — адрес узла управляющего слоя (режим `in-cluster`) и CNI его не пропускает, добавьте правило своей CNI через
`networkPolicy.egress.extra` или задайте `clusterApi.to`. Какие именно правила нужны, зависит от CNI; проверьте
подключение к кластеру после установки.

### Метрики

`metrics.enabled=true` включает `/metrics` на отдельном порту (`metrics.port`, 9090), отдельный Service
`<release>-metrics` и правило NetworkPolicy, пускающее на этот порт только `networkPolicy.metrics.from`
(по умолчанию namespace `monitoring`). Ingress порт метрик не получает. Для Prometheus Operator
`metrics.serviceMonitor.enabled=true`. Список метрик и запросы — `docs/metrics.md`.

### Журнал и аудит

Журнал процесса — JSON в stderr (`kubron.logLevel`, `kubron.logFormat`), у каждого запроса есть
`request_id` (заголовок `X-Request-Id`). `kubron.auditStdout=true` дублирует аудит JSON-строками в stdout
для внешнего сборщика. Подробности и гарантии по секретам — `docs/logging.md`.

### RBAC режима in-cluster

`inCluster.enabled=true` монтирует токен ServiceAccount (по умолчанию он не монтируется): Kubron ходит в кластер, где
запущен сам, под этим ServiceAccount. `rbac.create=true` создаёт ClusterRole и привязывает её к ServiceAccount.

**Профиль — потолок прав для всех пользователей.** Токен один на всех: подмены личности нет (ADR-0006), а права людей
ограничивают только роли и команды самого Kubron. Поэтому RBAC ServiceAccount определяет максимум, который сможет сделать
в этом кластере любой пользователь, включая администратора Kubron: если профиль не разрешает запись, запись не получится
ни у кого. Выбирайте минимально достаточный профиль (`rbac.profile`):

| Профиль | Что разрешено ServiceAccount | Что работает в Kubron |
| --- | --- | --- |
| `readonly` (по умолчанию) | `get/list/watch` по группам API, которые читает Kubron (workload'ы, сеть, хранилище, RBAC-объекты, CRD, метрики), **без `secrets`** | просмотр (`read`) |
| `operator` | `readonly` плюс: `patch/update` Deployment, StatefulSet, DaemonSet (restart, rollback), `scale` Deployment и StatefulSet, `create/delete` Job (повтор), `patch` узлов (cordon) | просмотр и безопасные операции (`operate`): restart, scale, rollback, повтор Job, cordon |
| `custom` | только `rbac.customRules` либо существующая ClusterRole (`rbac.existingClusterRole`) | то, что вы выдали |

Правила профилей выведены из вызовов Kubernetes API в коде (`internal/k8s`) для прав `read` и `operate`; шаблоны лежат в
`values.yaml` (`rbac.readonlyRules`, `rbac.operatorRules`).

**Что профили не покрывают намеренно.** Терминал пода (`pods/exec`, `pods/attach`), port-forward, оболочка узла (создаёт
привилегированный под), drain (`pods/eviction`), правка и удаление произвольных объектов, Helm (релизы хранятся в Secret'ах
и применяют произвольные манифесты) требуют широких прав, и выдавать их по умолчанию нельзя. Включайте их осознанно через
`custom`. Права Kubron `exec`, `nodeshell`, `portforward`, `write` при этом всё равно можно отключить для всех:
`kubron.disableExec`, `kubron.disableNodeShell`, `kubron.disablePortForward`.

- **Значения Secret.** По умолчанию ServiceAccount не читает `secrets`, поэтому значения Secret недоступны никому.
  `rbac.allowSecretsRead=true` добавляет `get/list/watch` на `secrets` кластера для любого профиля: после этого их увидят
  все, у кого в Kubron есть право `secrets.read` (роли `operator` и `admin`).
- **Пользовательские ресурсы** (CRD, Argo CD `Application` и др.) в профили не входят: добавьте нужные группы в
  `rbac.extraRules`.
- **Существующая роль.** `rbac.profile=custom` и `rbac.existingClusterRole=view` привязывают ServiceAccount к встроенной
  роли `view` без собственной ClusterRole. Встроенная `view` не включает `secrets`, а `edit` и `admin` включают и дают запись:
  привязывайте их только когда это действительно нужно.
- **Смена профиля** применяется сразу при `helm upgrade`; перезапуск подов не нужен, права проверяет API-сервер.

## Несколько реплик (HA)

Сервер рассчитан на две и более реплик: веб-сессии и пользователи хранятся в PostgreSQL и общие для
всех реплик, изменения (выход, смена пароля, права, состояния кластеров) расходятся между репликами
через `LISTEN/NOTIFY`, а остановка реплики — корректный draining. Эксплуатация — `docs/ha.md`,
сессии — `docs/ha-sessions.md`, решения — ADR-0016.

- **Прямое соединение с PostgreSQL для слушателя.** pgbouncer в режиме `transaction` не поддерживает
  `LISTEN`: прямой адрес задаётся `KUBRON_DB_LISTEN_URL` (только окружение; публикация событий идёт
  через обычный пул).
- **Идентификатор реплики.** `KUBRON_REPLICA_ID` / `--replica-id`; без него — «имя узла + случайный
  суффикс процесса». Заданный вручную должен быть уникален среди реплик.
- **Остановка (SIGTERM).** Реплика перестаёт принимать новые WebSocket и терминалы (`503`), ждёт
  `KUBRON_DRAIN_DELAY` (за Ingress/Service рекомендуется `5s`; меньше `terminationGracePeriodSeconds`
  пода минус 10 с) и закрывает соединения кодом `1012`; веб-клиент переподключается сам.
- **Потери при гибели реплики.** Открытые терминалы, логи и port-forward пропадают вместе с
  процессом и открываются заново; входы, настройки и права не теряются.
- **Чарт.** По умолчанию `replicaCount: 2`: создаются PodDisruptionBudget (`minAvailable: 1`) и мягкое
  правило размещения реплик на разных узлах (`topologySpreadConstraints` и `affinity` заменяют его).
  `kubron.drainDelay` (по умолчанию `5s`) задаёт `KUBRON_DRAIN_DELAY`; `secrets.keys.dbListenUrl` — ключ Secret
  с прямым адресом PostgreSQL для `LISTEN`. Привязка по cookie на Ingress (пример в `values.yaml`) уменьшает
  число повторных открытий терминалов и логов, но не обязательна. `replicaCount: 1` допустим; тогда PDB не
  создаётся. Готовые наборы — `ci/replicas-values.yaml`, `ci/single-values.yaml`.

## Port-forward в веб-режиме

В сервисном режиме port-forward работает через HTTP-прокси (ADR-0017): порт на хосте сервера не
открывается, каждый поток получает свой origin `https://{id}.pf.<base-domain>`. Что нужно:

- `KUBRON_PF_BASE_DOMAIN` — базовый домен (например `kubron.example.com`; поток —
  `{id}.pf.kubron.example.com`). **Используйте отдельный регистрируемый домен** (не поддомен домена Kubron): JS проксированного
  приложения работает на `*.pf.<base-domain>` и на вложенном поддомене может подменить (cookie tossing) cookie родительского
  домена. Префикс `__Host-` у cookie сессии защищает при HTTPS, но отдельный домен убирает класс атак целиком;
- DNS-запись `*.pf.<base-domain>`, wildcard-сертификат и правило Ingress для этого хоста;
- HTTPS: режим `proxy` требует TLS (`KUBRON_PF_SCHEME=http` допускается только на loopback).

В Helm-чарте это блок `portForward` (`baseDomain`, `scheme`, `idleTimeout`, `maxDuration`): при `ingress.enabled` чарт сам
добавляет в Ingress правило для хоста `*.pf.<baseDomain>` на тот же сервис (хост выбирает сам сервер). Wildcard-сертификат
нужно указать в `ingress.tls` (`*.pf.<baseDomain>`), DNS-запись `*.pf.<baseDomain>` создаётся вне чарта; пример значений —
`deploy/helm/kubron/ci/portforward-values.yaml`. Привязка по cookie (`nginx.ingress.kubernetes.io/affinity: cookie`) действует
и на этот хост: поток живёт на реплике, открывшей его, чужая реплика отвечает `421`, и браузер повторяет запрос.

Если Argo CD опубликован (Ingress), укажите его адрес в настройках кластера («Argo CD → Адрес»): `OpenArgoUI`
откроет его напрямую, поток не создаётся и порты не нужны — это предпочтительный вариант, а прокси остаётся запасным.
Принимаются только `http(s)`-адреса без логина, query и fragment.

Несколько реплик: поток port-forward живёт на реплике, которая его открыла. Для хоста `*.pf.<base-domain>` включите на
Ingress привязку по cookie (`nginx.ingress.kubernetes.io/affinity: cookie`); запрос, попавший на другую реплику,
получает `421`, а `GetPortForwardURL` и `StopPortForward` — `409 stream_elsewhere` (интерфейс повторяет их сам).

Без `KUBRON_PF_BASE_DOMAIN` port-forward в сервисном режиме выключен (предупреждение в логе);
явно отключается `KUBRON_DISABLE_PORT_FORWARD`. Длительность потока ограничена
`KUBRON_PF_IDLE_TIMEOUT` и `KUBRON_PF_MAX_DURATION`; начало и конец потока попадают в аудит
(`portforward.start/stop`). Трафик потока идёт через API-сервер кластера (SPDY), поэтому
дополнительного egress в NetworkPolicy не нужно.

## Терминалы и оболочка узла

Терминал пода (exec/attach) требует права `exec`. Оболочка на узле — привилегированный под с оболочкой
хоста — требует отдельного права `nodeshell`, по умолчанию его есть только у роли администратора.
Администратор сервера может отключить возможности для всех независимо от ролей:
`KUBRON_DISABLE_EXEC` (терминалы подов и оболочка узла), `KUBRON_DISABLE_NODE_SHELL`,
`KUBRON_DISABLE_PORT_FORWARD`; интерфейс скрывает недоступное по `GetCapabilities`, а методы отвечают
понятной ошибкой. Терминал закрывается по абсолютному сроку `KUBRON_TERMINAL_MAX_DURATION` (по умолчанию
8h) и после простоя без ввода `KUBRON_TERMINAL_IDLE_TIMEOUT` (30m); `0` снимает ограничение. Начало и конец
сессии пишутся в аудит (`terminal.start/stop`, `nodeshell.start/stop`: пользователь, кластер, namespace/под,
причина и длительность).

## Helm: состояние и доступ к сети

**Где хранится состояние.** В сервисном режиме (кластеры в PostgreSQL) репозитории Helm, OCI-реестры
и журнал операций хранятся в БД (миграция `0005`) и принадлежат **команде**: участники команды видят
репозитории и журнал кластеров своей команды, чужие — нет (чужое имя или идентификатор операции
отвечает «не найдено»). Репозиторий с тем же именем может быть у каждой команды. Пароли репозиториев
и реестров шифруются тем же ключом, что и секреты кластеров (`secretbox`), поэтому `kubron-server rekey`
перешифровывает и их; они не возвращаются через API и не пишутся в логи. Журнал хранит 200 последних
операций на команду и переживает удаление кластера. Операции Helm попадают в журнал аудита с `teamId`
и `clusterId`. Файлы `helm/repositories.yaml`, `oci.yaml`, `registry-config.json` и `operations.json`
в сервисном режиме не используются; существующие файлы в БД автоматически не переносятся
(репозитории нужно добавить заново). Десктоп и режим без БД работают с файлами, как раньше.

**Несколько реплик.** Кэш индексов репозиториев и тегов OCI лежит в `emptyDir` каждой реплики. Репозиторий,
добавленный на одной реплике, на другой докачивается сам при первом поиске, просмотре версий или установке; список
репозиториев и проверка обновлений не ждут сеть и подгружают кэш в фоне (до этого показывается подсказка
«запустите Update»). «Обновить» обновляет кэш только реплики, принявшей запрос, поэтому реплики могут ненадолго
показывать разные версии чартов. Решение и ограничения: `docs/decisions/2026-11-01-helm-state-followup.md` (D-03).

**Защита от SSRF.** Адреса репозиториев и реестров задаёт пользователь, поэтому сервер проверяет
адрес **на каждом соединении** (а не только разбором URL): подмена DNS между проверкой и соединением
и редиректы не обходят запрет. Всегда запрещены loopback, link-local (в том числе `169.254.169.254`
и `fd00:ec2::254`), `0.0.0.0/8`, multicast; адреса вида `2130706433`, `0177.0.0.1`, `::ffff:127.0.0.1`
и IPv4 внутри NAT64/6to4 приводятся к обычному виду. Допустимы только схемы `http` и `https`.
Запрос к запрещённому адресу выглядит для пользователя как обычная «connection refused»;
причина (адрес и класс, без URL и учётных данных) пишется только в серверный лог. Прокси из окружения
(`HTTP_PROXY`) для запросов Helm не используется.

| Переменная | Значение |
| --- | --- |
| `KUBRON_HELM_ADDR_POLICY` | `strict` (по умолчанию): дополнительно запрещены частные диапазоны `10/8`, `172.16/12`, `192.168/16`, `fc00::/7`, `100.64/10`; `permissive`: только «всегда запрещено»; `off`: проверка выключена |
| `KUBRON_HELM_ALLOW_CIDRS` | Исключения: CIDR или адреса через запятую, например `10.20.0.0/16`. Снимают запрет с loopback, link-local и частных диапазонов, но **не** с адресов метаданных облака, unspecified и multicast |

Опечатка в значении не выключает защиту: сервер не запустится с понятной ошибкой.

**Внимание при обновлении.** По умолчанию действует `strict`: репозиторий чартов на внутреннем адресе
(`10.x`, `192.168.x`) перестанет открываться, пока его сеть не добавлена в `KUBRON_HELM_ALLOW_CIDRS`.
Задайте переменные через `kubron.extraEnv` значений чарта (список в формате `container.env`).

### Адрес API-сервера кластера

Адрес API-сервера берётся из kubeconfig, который прислал пользователь, поэтому им тоже можно воспользоваться
как SSRF (направить сервер на metadata-сервис или внутреннюю службу). В сервисном режиме (БД или адрес не на
loopback) проверка **включена по умолчанию** (`permissive`); режим одного пользователя на loopback по умолчанию без
проверки. Отключить её в сервисном режиме можно только явно (`off`), сервер пишет предупреждение в журнал.

| Переменная | Значение |
| --- | --- |
| `KUBRON_CLUSTER_ADDR_POLICY` | `permissive` (по умолчанию в сервисном режиме: запрещены loopback, link-local и метаданные облака; частные сети разрешены), `strict` (дополнительно закрыты частные диапазоны) или `off` (по умолчанию вне сервисного режима) |
| `KUBRON_CLUSTER_ADDR_ALLOW_CIDRS` | Исключения, как у Helm: снимают запрет с loopback и link-local, но не с метаданных облака; для CI на kind: `127.0.0.0/8` |

Что проверяется при включении: адрес в kubeconfig при добавлении и при каждом подключении, пробное подключение,
а также каждое соединение клиента (запросы к API, discovery, informers, Helm, терминал и port-forward).
`proxy-url` в kubeconfig не допускается (запрос ушёл бы на прокси, а проверялся бы только его адрес),
прокси из окружения не используется. Подключение `in-cluster` проверке не подлежит: адрес сервера задаёт сам
кластер. Тем же правилам подчиняется и адрес провайдера аутентификации (IdP) при входе через брокер, включая `token_endpoint` из его ответа; IdP во внутренней сети при `strict` нужно добавить в исключения. Рекомендуемый профиль для IdP — `permissive`: loopback, link-local и метаданные облака закрыты, внутренний IdP работает без настройки.

## Проверка образа и чарта

Каждый релиз (тег `vX.Y.Z`) собирается в GitHub Actions (`release.yml` → `publish.yml`), и перед `push` образ проходит
Trivy (HIGH/CRITICAL с готовым исправлением останавливают публикацию). К опубликованным образу и чарту прилагаются:

| Что | Где | Чем проверять |
| --- | --- | --- |
| Подпись cosign (keyless, Sigstore): личность — workflow `publish.yml` на теге | OCI-реестр рядом с образом и чартом | `cosign verify`, политики admission-контроллеров (Kyverno и др.) |
| Аттестация происхождения (SLSA provenance) | GitHub и реестр | `gh attestation verify` |
| Аттестация SBOM (образ — SPDX, чарт — CycloneDX) | GitHub и реестр | `gh attestation verify --predicate-type …` |
| Файлы SBOM `kubron-sbom_<версия>_{image,chart,desktop}.*.json` | GitHub Release | скачать из релиза; суммы — в `SHA256SUMS.txt` |

Проверяйте **digest**, а не только тег: тег `latest` и теги версий в реестре изменяемы, подпись относится к digest.

```sh
VERSION=0.7.0                       # версия без «v»
IMAGE=ghcr.io/24dso-labs/kubron
CHART=ghcr.io/24dso-labs/charts/kubron

# 1. Происхождение образа: собран workflow publish.yml этого репозитория
gh attestation verify "oci://$IMAGE:$VERSION" --repo 24DSO-labs/kubron \
  --signer-workflow 24DSO-labs/kubron/.github/workflows/publish.yml

# 2. SBOM образа (SPDX) и чарта (CycloneDX) как проверенные аттестации
gh attestation verify "oci://$IMAGE:$VERSION" --repo 24DSO-labs/kubron \
  --predicate-type https://spdx.dev/Document/v2.3
gh attestation verify "oci://$CHART:$VERSION" --repo 24DSO-labs/kubron \
  --predicate-type https://cyclonedx.org/bom

# 3. Подпись cosign образа и чарта. Личность: workflow publish.yml на теге vX.Y.Z
IDENTITY='^https://github\.com/24DSO-labs/kubron/\.github/workflows/publish\.yml@refs/tags/v[0-9].*$'
cosign verify "$IMAGE:$VERSION" \
  --certificate-identity-regexp "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify "$CHART:$VERSION" \
  --certificate-identity-regexp "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Регулярное выражение сознательно требует тег `refs/tags/v…`: ручные пробные сборки (`0.0.0-dev.<sha>`) подписываются с ветки
и проверку не пройдут. Подпись сделана cosign 3.x.

Для установки по digest (рекомендуется в production): `helm install … --set image.digest=sha256:…` (значение имеет приоритет
над `image.tag`).

**Политика admission-контроллера.** Пример для Kyverno: запретить запуск образов Kubron без подписи нашего workflow
(проверьте поля под версию вашего Kyverno):

```yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: verify-kubron-image
spec:
  webhookTimeoutSeconds: 30
  rules:
    - name: verify-signature
      match:
        any:
          - resources:
              kinds: [Pod]
      verifyImages:
        - imageReferences:
            - "ghcr.io/24dso-labs/kubron*"
          failureAction: Enforce
          mutateDigest: true
          attestors:
            - entries:
                - keyless:
                    issuer: https://token.actions.githubusercontent.com
                    subjectRegExp: '^https://github\.com/24DSO-labs/kubron/\.github/workflows/publish\.yml@refs/tags/v[0-9].*$'
                    rekor:
                      url: https://rekor.sigstore.dev
```

Раз в неделю workflow `scheduled-scan.yml` сканирует последний стабильный образ Trivy и открывает issue с меткой `security`,
если для HIGH/CRITICAL уже есть исправление. Сторонние зависимости (Go, npm) проверяют `govulncheck`, `npm audit` и
osv-scanner в CI; исключения сканеров — только в `.trivyignore` и `osv-scanner.toml`, с обоснованием и сроком до 180 дней
(`scripts/check-ignores.py`).

## Вход в кластеры: что настроить в IdP

Пользователи входят в кластеры через окно входа (см. [docs/cluster-sign-in.md](cluster-sign-in.md)). От
администратора требуется следующее.

* **Device grant (RFC 8628).** Для входа «по коду устройства» у OIDC-клиента в IdP должен быть включён
  Device Authorization Grant (в Keycloak: клиент → «OAuth 2.0 Device Authorization Grant Enabled»). Если IdP
  отвечает `unauthorized_client`, пользователь переходит на вход через браузер (`paste-redirect`), если он
  настроен.
* **Redirect URI.** Для входа «через браузер» (`paste-redirect`) регистрировать новые redirect URI **не нужно**:
  используется тот, что указан в kubeconfig (например, `http://localhost:8000`). Пользователь копирует адрес,
  на который перешёл браузер, из адресной строки. IdP должен принимать этот адрес как есть.
* **Веб-клиент для возврата на сервер (`browser-callback`).** Задайте `KUBRON_PUBLIC_URL` (см. выше) и публичный
  client id веб-клиента (его задаёт администратор организации, `SetWebOIDCClientID`). В IdP зарегистрируйте
  redirect URI `<KUBRON_PUBLIC_URL>/auth/cluster/callback`. Пока одно из двух не задано, работает вход
  `paste-redirect`.
* **Refresh-токен и `offline_access`.** Чтобы вход не повторялся при каждом подключении, IdP должен выдавать
  refresh-токен. Для Dex у клиента нужен scope `offline_access`: добавьте в kubeconfig
  `--oidc-extra-scope=offline_access`. Без него пользователю придётся входить заново, когда истечёт ID-токен.
* **IdP по `http` в серверном режиме не работает.** Адрес IdP должен быть `https`: `http` и loopback допускаются
  только в десктопе (политика C.1). Для тестового IdP поднимите TLS или проверяйте вход в десктопе.
* **Общая учётная запись команды.** Администратор кластера при добавлении или правке кластера команды ставит
  «Общая учётная запись команды». Вход под ней вводит администратор кластера один раз при подключении (проверяет
  провайдер входа), результат хранится зашифрованным и используется всей командой; остальные участники, пока
  входа нет, видят «обратитесь к администратору кластера». Если пароль в провайдере сменился, в окне правки
  кластера нажмите «Забыть общий вход»: следующее подключение попросит войти заново. Пароль в Kubron
  нигде не вводится отдельно от окна входа и не попадает в журнал.
* **Плагины.** Сервер плагины не запускает (веб-режим): пользователи получают токен на своём компьютере и
  вставляют его в окно «Получение токена вручную». Установка клиентских утилит на сервер не нужна.
* **Десктоп.** Плагины из kubeconfig запускаются только после подтверждения пользователя; решения
  «Всегда» хранятся в `exec-trust.json` в каталоге данных приложения. Чтобы сбросить доверие, удалите файл.
* **Сетевая политика.** Все обращения к IdP идут через политику `netpolicy.ScopeClusterAPI` (задача C.1):
  разрешите серверу адреса discovery, authorization, token, device и JWKS вашего IdP.

## Резервное копирование

Ключ и БД копируются **отдельно друг от друга и хранятся в разных местах**: копия БД без ключа бесполезна для
секретов, а копия ключа рядом с копией БД лишает ключ смысла. Порядок копирования, восстановление, ротация ключа и
`rekey` — в `docs/backup.md`. Сервер при старте проверяет, что ключ читает данные в БД, и не запускается с
неверным ключом; восстановление из копии проверяет job `restore-check` в CI.

## Ограничения

- **Потоки при перезапуске.** Веб-сессии хранятся в PostgreSQL и переживают перезапуск и обновление
  пода: повторный вход не нужен. Открытые терминалы, логи и port-forward живут в процессе и
  пропадают вместе с ним: при корректном завершении соединения закрываются кодом `1012`, клиент
  переподключается к живой реплике. Для обновления используется `RollingUpdate` с
  `maxUnavailable: 0`; миграции применяются под advisory-lock, а прежняя версия работает с более
  новой схемой в режиме совместимости.
- **Кэш Helm не постоянный.** Список репозиториев, OCI-реестры с учётными данными и журнал операций
  Helm лежат в PostgreSQL (см. «Helm: состояние и доступ к сети»), поэтому PVC под Helm не нужен.
  В `HOME` пода (`emptyDir`) остаётся только кэш индексов и списков тегов: он воспроизводим и после
  пересоздания пода скачивается заново (кнопка «Обновить» в разделе репозиториев).
- **Пробы.** `startupProbe` и `livenessProbe` — `/healthz`: процесс жив, порт слушается; БД он не
  проверяет, поэтому её недоступность не приводит к перезапуску подов. `readinessProbe` — `/readyz`:
  `200`, если реплика принимает трафик, и `503`, если БД не отвечает (проверка до 2 с, результат
  кэшируется на 1 с) или реплика останавливается (draining после SIGTERM, см. `docs/ha.md`). Ответ —
  JSON без подробностей ошибки (эндпоинт открыт без входа):
  `{"status":"not_ready","draining":false,"checks":{"database":"fail"}}`. Без PostgreSQL
  (файловое хранилище) проверка БД пропускается (`"skipped"`). Поэтому при остановке реплика снимается
  с Service сразу, а `KUBRON_DRAIN_DELAY` лишь оставляет время на распространение изменения.

## Проверка перед выкладкой

```sh
scripts/check-chart.sh                          # helm lint, template, kubeconform, смысловые проверки
hadolint --config .hadolint.yaml Dockerfile
docker build -t kubron:dev .
```

Те же шаги выполняет workflow `.github/workflows/deploy.yml`, который, кроме того, ставит чарт в
kind с PostgreSQL и существующим Secret.

## Версия RPC-контракта

Страница веб-интерфейса собирается вместе с сервером и знает его версию (в образе — `--build-arg VERSION`, она же
`-X main.version`, она же `VITE_KUBRON_BUILD` для Vite). Страница шлёт её в заголовке `X-Kubron-Build` (RPC) и в параметре
`build` (WebSocket `/api/ws` и `/api/terminal/{id}`). Если версия не совпадает с версией сервера, он отвечает `409` с кодом
`contract_mismatch`, а страница один раз в минуту перезагружается сама: так после обновления сервера открытая вкладка не
вызывает методы, которых уже нет или чьи аргументы изменились.

- Версия `dev` (сборка из исходников, `npm run dev:web`, `go run`) отключает проверку; так же ведут себя вызовы без заголовка
  (curl, проверки здоровья).
- При постепенном обновлении (rolling update) вкладка, попавшая на реплику другой версии, получит `409` и перезагрузится.
- Образ собирается одним `VERSION` для Go и фронтенда; собирать их с разными значениями нельзя.
