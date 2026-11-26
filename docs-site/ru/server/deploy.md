# Развёртывание в Docker и Kubernetes

Пошаговое руководство по запуску Web-версии (`kubron-server`) для команды: сначала в Docker, затем в Kubernetes через Helm-чарт. Оно следует [полному руководству по эксплуатации](https://github.com/24DSO-lab/kubron/blob/master/docs/deploy.md) в репозитории, которое остаётся первоисточником по продвинутым темам.

Хотите сначала попробовать на ноутбуке? Используйте [быстрый запуск](/ru/server/quick-start).

[[toc]]

## Перед началом

| Что нужно | Зачем |
|---|---|
| **PostgreSQL 14+** (внешний) | Хранит пользователей, кластеры, журнал аудита, веб-сессии и общее состояние Helm. Миграции сервер применяет сам при старте |
| **Ключ шифрования** | Шифрует kubeconfig и учётные данные Argo CD в базе (`KUBRON_SECRET_KEY`) |
| **Прокси с TLS** | Сервер сам TLS не обслуживает. Без TLS cookie сессии идёт по сети открытым текстом |
| **DNS-имя** | Например, `kubron.example.com` |
| **Kubernetes 1.27+ и Helm 3** | Только для установки через Helm |

В продакшне рассчитывайте на **две и более реплики**. Всё состояние лежит в PostgreSQL, поэтому реплики взаимозаменяемы (см. [высокую доступность](/ru/server/ha)).

## Шаг 1. Подготовьте PostgreSQL

Создайте отдельную роль и базу. Выполните от суперпользователя PostgreSQL:

```sql
CREATE ROLE kubron LOGIN PASSWORD 'придумайте-надёжный-пароль';
CREATE DATABASE kubron OWNER kubron;
```

Роль владеет базой, поэтому может создавать таблицы при миграциях. Расширения не требуются.

Соберите строку подключения. В продакшне используйте проверку сертификата:

```
postgres://kubron:ПАРОЛЬ@db.example.com:5432/kubron?sslmode=verify-full
```

Если вы подключаетесь к PostgreSQL через **PgBouncer в режиме transaction**, задайте ещё и прямой адрес в `KUBRON_DB_LISTEN_URL`: `LISTEN/NOTIFY` через пулер в этом режиме не работает.

::: tip Резервные копии
Настройте резервное копирование PostgreSQL сразу. Ключ и базу нужно копировать **отдельно** и хранить **в разных местах**; см. [руководство по резервному копированию](/ru/server/backup).
:::

## Шаг 2. Сгенерируйте и сохраните ключ шифрования

Ключ — это `id:base64`, где base64 кодирует ровно 32 случайных байта, а `id` — метка (буквы, цифры, `_ . -`, до 32 символов):

```bash
echo "k1:$(openssl rand -base64 32)"
```

Сохраните результат в менеджере паролей или KMS **до первого запуска**. Если ключ потерян, все сохранённые kubeconfig и токены невосстановимы. Образ и чарт ключ не генерируют именно поэтому.

## Шаг 3а. Запуск в Docker

Образ distroless, работает от пользователя `65532`, слушает порт `8080` и настраивается только переменными окружения.

```bash
docker run -d --name kubron --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  --read-only --tmpfs /home/nonroot:uid=65532,gid=65532 --tmpfs /tmp \
  --cap-drop ALL --security-opt no-new-privileges \
  -e KUBRON_ALLOW_REMOTE=1 \
  -e KUBRON_TRUST_PROXY=1 \
  -e KUBRON_ALLOWED_HOSTS=kubron.example.com \
  -e KUBRON_SETUP_TOKEN='случайный-токен-не-короче-16-символов' \
  -e KUBRON_SECRET_KEY='k1:...' \
  -e KUBRON_DATABASE_URL='postgres://kubron:...@db.example.com:5432/kubron?sslmode=verify-full' \
  ghcr.io/24dso-lab/kubron:1.2.0
```

Что делает каждая часть:

- `KUBRON_ALLOW_REMOTE=1` нужен, потому что контейнер слушает `0.0.0.0`.
- Публикуйте порт на `127.0.0.1` или во внутреннюю сеть и ставьте перед ним TLS-прокси. В интернет порт не публикуйте.
- `KUBRON_TRUST_PROXY=1` заставляет сервер верить `X-Forwarded-Proto` от прокси и помечать cookie как `Secure`. Включайте, только если сервер доступен **исключительно** через прокси.
- `KUBRON_ALLOWED_HOSTS` — список имён хостов, которые принимает сервер; запросы с другим `Host` получают `421`.
- Секреты передавайте через окружение или секреты оркестратора, а не флагами: аргументы видны в списке процессов.
- Без `KUBRON_DATABASE_URL` сервер переходит на файловое хранилище во временном каталоге. Это только для знакомства: данные пропадут вместе с `tmpfs`.

Поставьте перед сервером обратный прокси. Минимальный пример на **Caddy**, который сам получит сертификат:

```
kubron.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Для nginx передавайте заголовки `Upgrade` и `Connection` (события и терминалы идут по WebSocket), задайте большой `proxy_read_timeout` (например, `3600s`) и отправляйте `X-Forwarded-Proto`.

## Шаг 3б. Установка через Helm

### Создайте namespace и Secret

Чарт ключ не создаёт. Secret создаёте вы, а чарт на него ссылается:

```bash
kubectl create namespace kubron

kubectl -n kubron create secret generic kubron-secrets \
  --from-literal=KUBRON_SECRET_KEY='k1:...' \
  --from-literal=KUBRON_DATABASE_URL='postgres://kubron:...@db.example.com:5432/kubron?sslmode=verify-full'
```

### Напишите values

Создайте `values.yaml`. В примере ingress-nginx с TLS и две реплики:

```yaml
replicaCount: 2

secrets:
  existingSecret: kubron-secrets

ingress:
  enabled: true
  className: nginx
  annotations:
    # События и терминалы идут по WebSocket; ingress-nginx по умолчанию закрывает простаивающие через 60 с
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-body-size: 5m
    # Необязательно: направлять один браузер на одну и ту же реплику
    nginx.ingress.kubernetes.io/affinity: cookie
    nginx.ingress.kubernetes.io/session-cookie-name: kubron-affinity
    nginx.ingress.kubernetes.io/session-cookie-samesite: Lax
    nginx.ingress.kubernetes.io/session-cookie-secure: "true"
    nginx.ingress.kubernetes.io/session-cookie-max-age: "43200"
  hosts:
    - host: kubron.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: kubron-tls
      hosts:
        - kubron.example.com

kubron:
  sessionTTL: 12h
```

Примечания:

- При включённом ingress с TLS чарт сам ставит `KUBRON_TRUST_PROXY=1` и собирает `KUBRON_ALLOWED_HOSTS` из хостов ingress.
- По умолчанию `networkPolicy` пропускает входящий трафик только из namespace `ingress-nginx`. Если ваш ingress-контроллер в другом месте, задайте `networkPolicy.ingress.from` соответственно, иначе до подов ничего не дойдёт.
- `values.yaml` чарта подробно прокомментирован, а `values.schema.json` проверяет ваши значения. Готовые примеры лежат в `deploy/helm/kubron/ci/`.

### Установите

```bash
helm install kubron oci://ghcr.io/24dso-lab/charts/kubron \
  --version 1.2.0 -n kubron -f values.yaml
```

Версия чарта равна версии релиза (без `v`), и тег образа по умолчанию совпадает с ней. Для сборки из исходников используйте `helm install kubron deploy/helm/kubron ...` и задайте `image.repository` и `image.tag` сами.

В продакшне фиксируйте образ по digest: добавьте `--set image.digest=sha256:...` (он приоритетнее тега).

### Проверьте раскатку

```bash
kubectl -n kubron rollout status deploy/kubron
kubectl -n kubron get pods
kubectl -n kubron logs deploy/kubron | tail
```

Оба пода должны стать `Ready`. Затем откройте `https://kubron.example.com`.

## Шаг 4. Создайте первого администратора

Пока нет ни одного пользователя, любой, кто достучится до сервера, мог бы его занять. Поэтому для этого шага Kubron требует **токен первичной настройки**.

**Helm:** чарт сам создаёт случайный токен в Secret с именем `<релиз>-setup`. Прочитайте его:

```bash
kubectl -n kubron get secret kubron-setup \
  -o jsonpath='{.data.KUBRON_SETUP_TOKEN}' | base64 -d; echo
```

Откройте сайт, введите токен на экране **Welcome to Kubron** и создайте администратора.

**Docker:** используйте значение, которое вы передали как `KUBRON_SETUP_TOKEN`.

**Без токена и без браузера:** создайте администратора прямо в базе. Передавайте пароль через стандартный ввод или `KUBRON_ADMIN_PASSWORD`, но не аргументом:

```bash
kubectl -n kubron exec -i deploy/kubron -- \
  /kubron-server create-admin --username admin < password.txt
```

После этого токен больше не используется. Secret `<релиз>-setup` можно удалить и задать `setup.enabled=false`. **Для GitOps:** задайте `setup.existingSecret` сами; Argo CD и `helm template` рендерят без доступа к кластеру, поэтому сгенерированный токен менялся бы при каждом рендере.

## Шаг 5. Добавьте кластеры и пользователей

1. Войдите администратором.
2. Создайте команды и пользователей и назначьте роли в разделе пользователей. Встроенные роли: `user` (только чтение), `operator` и `admin`.
3. Добавьте кластеры из списка кластеров. Сервер никогда не выполняет команды из kubeconfig: встройте учётные данные, используйте окно входа или вставьте токен. См. [Подключение к Kubernetes](/ru/features/cluster-auth#web) и [правила kubeconfig](/ru/server/quick-start#добавление-первого-кластера).

Чтобы управлять кластером, в котором работает сам Kubron, задайте `inCluster.enabled=true`. Тогда сервер использует собственный ServiceAccount; профиль RBAC выбирается в разделе `rbac` чарта, выдавайте только необходимое.

## Права ServiceAccount в режиме in-cluster

При `inCluster.enabled=true` Kubron управляет собственным кластером через ServiceAccount чарта. Роли и команды Kubron ограничивают только то, что могут запросить пользователи, но прав они не добавляют: **RBAC ServiceAccount задаёт потолок для всех**, включая администратора Kubron. Если профиль не разрешает запись, записать не сможет никто. Выбирайте минимально достаточный профиль (`rbac.profile`):

| Профиль | Что разрешено ServiceAccount | Что работает в Kubron |
|---|---|---|
| `readonly` (по умолчанию) | `get/list/watch` по группам API, которые читает Kubron (нагрузки, сеть, хранилище, объекты RBAC, CRD, метрики), **без `secrets`** | Просмотр (`read`) |
| `operator` | `readonly` плюс `patch/update` для Deployment, StatefulSet, DaemonSet (restart, rollback), `scale` для Deployment и StatefulSet, `create/delete` для Job (повтор), `patch` узлов (cordon) | Просмотр и безопасные операции (`operate`): restart, scale, rollback, повтор Job, cordon |
| `custom` | Только `rbac.customRules` либо существующая ClusterRole (`rbac.existingClusterRole`) | То, что вы выдали |

Правила профилей выведены из вызовов Kubernetes API в коде для прав `read` и `operate`. Шаблоны — `rbac.readonlyRules` и `rbac.operatorRules` в `values.yaml` чарта.

**Что профили не покрывают намеренно.** Терминал Pod (`pods/exec`, `pods/attach`), debug-контейнеры (`pods/ephemeralcontainers`, см. [Debug-контейнеры](/ru/features/debug-containers)), port-forward, оболочка узла (создаёт привилегированный Pod), drain (`pods/eviction`), правка и удаление произвольных объектов, Helm (релизы хранятся в Secret и применяют произвольные манифесты) требуют широких прав, которые нельзя выдавать по умолчанию. Включайте их осознанно через `custom`. Соответствующие права Kubron при этом всё равно можно отключить для всех: `kubron.disableExec`, `kubron.disableNodeShell`, `kubron.disablePortForward`.

- **Значения Secret.** По умолчанию ServiceAccount не читает `secrets`, поэтому значения Secret недоступны никому. `rbac.allowSecretsRead=true` добавляет `get/list/watch` на `secrets` кластера для любого профиля; после этого их увидят все, у кого в Kubron есть право `secrets.read` (роли `operator` и `admin`).
- **Пользовательские ресурсы** (CRD, Argo CD `Application`, объекты Flux и др.) в профили не входят. Добавьте нужные группы в `rbac.extraRules`; готовый фрагмент для Flux приведён [ниже](#rights-for-flux-cd).
- **Существующая роль.** `rbac.profile=custom` и `rbac.existingClusterRole=view` привязывают ServiceAccount к встроенной роли `view` без собственной ClusterRole. Встроенная `view` не включает `secrets`, а `edit` и `admin` включают и дают запись: привязывайте их, только когда это действительно нужно.
- **Смена профиля** применяется сразу при `helm upgrade`. Перезапуск Pod не нужен, права проверяет API-сервер.

### Права для Flux CD {#rights-for-flux-cd}

Профили правил Flux не содержат (как и для Argo CD): объекты Flux читаются и меняются, только если вы добавили правила в `rbac.extraRules`. Правила попадают в ClusterRole чарта; при `rbac.existingClusterRole` они не используются, и нужные права выдаются в самой привязанной роли. Фрагмент ниже разбит по уровням: оставьте только то, что нужно.

```yaml
rbac:
  extraRules:
    # Чтение (право Kubron `read`): списки, детали, дашборд, граф. Нужны все три глагола: список строится из informer.
    - apiGroups: [kustomize.toolkit.fluxcd.io]
      resources: [kustomizations]
      verbs: [get, list, watch]
    - apiGroups: [helm.toolkit.fluxcd.io]
      resources: [helmreleases]
      verbs: [get, list, watch]
    - apiGroups: [source.toolkit.fluxcd.io]
      resources: [gitrepositories, ocirepositories, helmrepositories, helmcharts, buckets]
      verbs: [get, list, watch]
    - apiGroups: [notification.toolkit.fluxcd.io]
      resources: [alerts, providers, receivers]
      verbs: [get, list, watch]
    - apiGroups: [image.toolkit.fluxcd.io]
      resources: [imagerepositories, imagepolicies, imageupdateautomations]
      verbs: [get, list, watch]

    # Reconcile (право Kubron `operate`), suspend и resume (право `write`): merge-patch аннотации и `spec.suspend`.
    - apiGroups: [kustomize.toolkit.fluxcd.io]
      resources: [kustomizations]
      verbs: [patch]
    - apiGroups: [helm.toolkit.fluxcd.io]
      resources: [helmreleases]
      verbs: [patch]
    - apiGroups: [source.toolkit.fluxcd.io]
      resources: [gitrepositories, ocirepositories, helmrepositories, helmcharts, buckets]
      verbs: [patch]
    - apiGroups: [image.toolkit.fluxcd.io]
      resources: [imagerepositories, imageupdateautomations]
      verbs: [patch]

    # Удаление (право Kubron `write`): только Kustomization и HelmRelease.
    - apiGroups: [kustomize.toolkit.fluxcd.io]
      resources: [kustomizations]
      verbs: [delete]
    - apiGroups: [helm.toolkit.fluxcd.io]
      resources: [helmreleases]
      verbs: [delete]
```

- **Чтение без изменений.** Достаточно первого блока: раздел Flux покажет статусы, ресурсы и граф, а кнопки действий завершатся ошибкой Kubernetes RBAC (`forbidden`). Права Kubron (`operate`, `write`) включают возможность внутри Kubron, но не отменяют RBAC кластера: нужны оба уровня.
- **Раздела Flux нет, хотя Flux установлен.** Без права `list` на CRD Flux Kubron считает Flux не установленным и скрывает раздел без ошибки. Проверка повторяется примерно раз в 30 секунд.
- **Prune и uninstall.** Удаление `Kustomization` с `prune: true` и `HelmRelease` выполняют контроллеры Flux под своими учётными записями, поэтому ServiceAccount Kubron для этого лишних прав не получает. Удаление объекта Flux Kubron откатить не может: восстанавливайте его из Git.
- **Секреты.** Правила не затрагивают `secrets`: значения `spec.values`, `valuesFrom` и подстановок `postBuild` Kubron не отдаёт независимо от `rbac.allowSecretsRead`.

Что показывает раздел Flux, описано в разделе [GitOps](/ru/features/gitops#flux-cd).

## Необязательно: вход через браузер и аккаунты Google

Пользователи входят в кластеры через окно входа (см. [Подключение к Kubernetes](/ru/features/cluster-auth)). Две возможности требуют настроек на стороне сервера. Обе необязательны; без них работают способы «вставить адрес» и «ключ сервисного аккаунта».

### Публичный URL для входа через браузер

Для OIDC-сценария `browser-callback` серверу нужен адрес, на который провайдер идентификации вернёт браузер. Задайте `KUBRON_PUBLIC_URL` как публичный origin Kubron:

```bash
KUBRON_PUBLIC_URL=https://kubron.example.com
```

- В продакшне нужен `https`. `http` допускается только для локальной разработки: `localhost`, имя в зоне `.localhost` или loopback IP.
- Это абсолютный URL без пути, учётных данных, query и fragment. Сервер проверяет его при старте и не запускается с небезопасным значением.
- Зарегистрируйте `https://kubron.example.com/auth/cluster/callback` как redirect URI у веб-клиента провайдера.
- Затем администратор организации указывает в разделе пользователей публичный **client ID этого веб-клиента и его issuer**. Нужны оба значения: client ID без issuer не применяется.
- Веб-клиент используется только для кластеров, в kubeconfig которых указан тот же issuer и вход через authorization code. Остальные кластеры и другие виды входа (`password`, `device-code`, `authcode-keyboard`, legacy refresh) используют клиент из своего kubeconfig. Один веб-клиент покрывает один провайдер идентификации.
- Пока не заданы и публичный URL, и клиент, вход через браузер завершается вставкой адреса, на который провайдер вернул браузер (`paste-redirect`). Если клиент сменить или удалить, пользователи, входившие через него, получат запрос на повторный вход.
- Следите, чтобы прокси передавал запросы callback без изменения query-параметров.

В Helm используйте `kubron.extraEnv`:

```yaml
kubron:
  extraEnv:
    - name: KUBRON_PUBLIC_URL
      value: "https://kubron.example.com"
```

### Аккаунты Google для GKE

По умолчанию кластеры GKE входят по ключу сервисного аккаунта. Чтобы пользователи входили своим аккаунтом Google, зарегистрируйте OAuth-клиент в Google Cloud и передайте его серверу:

```bash
KUBRON_GKE_OAUTH_CLIENT_ID=1234567890-abcdef.apps.googleusercontent.com
KUBRON_GKE_OAUTH_CLIENT_SECRET=...   # храните в Secret, не в values
```

```yaml
kubron:
  extraEnv:
    - name: KUBRON_GKE_OAUTH_CLIENT_ID
      value: "1234567890-abcdef.apps.googleusercontent.com"
    - name: KUBRON_GKE_OAUTH_CLIENT_SECRET
      valueFrom:
        secretKeyRef: {name: kubron-gke-oauth, key: client-secret}
```

- При заданном `KUBRON_PUBLIC_URL` создайте клиент типа **Web application** и добавьте `<KUBRON_PUBLIC_URL>/auth/cluster/callback` в authorized redirect URIs. Без него создайте клиент **Desktop app**: пользователь вставит адрес, на который Google вернул браузер.
- Kubron запрашивает scope `openid`, `userinfo.email` и `cloud-platform`. `cloud-platform` для Google чувствительный: для внешних пользователей может потребоваться проверка приложения, а в режиме Testing действуют ограничения на число пользователей и срок refresh-токена. Проверьте актуальные правила экрана согласия OAuth до выкладки.
- Сервер не запустится, если задан секрет клиента без ID клиента. Ранее сохранённые ключи продолжают работать.

### Отчёты об ошибках {#bug-reports}

Кнопка **Сообщить об ошибке** в шапке отправляет описание пользователя и снимок окна браузера на почту. Письмо отправляет сервер через ваш SMTP-аккаунт, либо отчёт уходит в приёмник; кнопка появляется, только если задан и корректен `KUBRON_BUGREPORT_SMTP_HOST` или `KUBRON_BUGREPORT_RELAY_URL`. Серверу без выхода в интернет подойдёт SMTP-аккаунт вашего внутреннего почтового сервера.

```yaml
kubron:
  extraEnv:
    - name: KUBRON_BUGREPORT_SMTP_HOST
      value: smtp.gmail.com
    - name: KUBRON_BUGREPORT_SMTP_USER
      value: 224vadim224@gmail.com
    - name: KUBRON_BUGREPORT_SMTP_PASSWORD
      valueFrom:
        secretKeyRef: {name: kubron-bugreport, key: smtp-password}
```

- Отчёты уходят на `224vadim224@gmail.com`, если `KUBRON_BUGREPORT_SMTP_TO` не задаёт другой адрес. Для Gmail используйте порт 587 и [пароль приложения](https://support.google.com/accounts/answer/185833), а не пароль аккаунта.
- В письме: текст, снимок экрана (если пользователь его оставил), необязательный контактный адрес, версия, язык, открытый раздел и логин отправителя. Имён кластеров и журналов в нём нет. **На снимке может быть всё, что видно на экране**, включая имена и значения, доступные пользователю; скажите пользователям проверять предпросмотр.
- Лимиты: один отчёт от пользователя раз в 30 секунд и 30 отчётов в час на весь сервер. Каждый отправленный отчёт попадает в журнал аудита как `bugreport.submit`, без текста.
- Вместо почты можно задать `KUBRON_BUGREPORT_RELAY_URL` — HTTPS-адрес приёмника, например проектного: `https://kubron.pages.dev/api/bugreport`. По умолчанию у сервера приёмника нет. Если заданы оба способа, сначала пробуется почта. Откройте исходящий HTTPS к приёмнику в сетевой политике.
- При `networkPolicy.enabled` откройте порт SMTP сами, например:

```yaml
networkPolicy:
  egress:
    extra:
      - ports: [{port: 587, protocol: TCP}]
        to: [{ipBlock: {cidr: 0.0.0.0/0}}]
```

### Что разрешить на стороне провайдера идентификации

- **HTTPS issuer.** В Web-версии issuer должен работать по HTTPS с сертификатом, которому доверяет сервер. `insecure-skip-tls-verify` для веб-входа отклоняется; CA передайте встроенными данными kubeconfig.
- **Device grant (RFC 8628).** Включите его у клиента, если пользователи должны входить по коду устройства. Без него Kubron переходит на вход через браузер, если он настроен.
- **Authorization Code с PKCE (S256)** для входа через браузер.
- **Refresh-токены.** Провайдер должен их выдавать, чтобы вход не повторялся при каждом подключении. Для Dex добавьте в kubeconfig `--oidc-extra-scope=offline_access`.
- **Сетевой доступ.** Разрешите серверу обращаться к issuer и к endpoint-адресам из OIDC discovery (authorization, token, device, JWKS, userinfo, revocation). Для нестандартных портов задайте `networkPolicy.egress.idp.ports` или `idp.cidrs`. При политике адресов `strict` добавьте внутренний IdP в `KUBRON_CLUSTER_ADDR_ALLOW_CIDRS`.

## Обновление

Kubron раз в сутки проверяет GitHub на наличие нового релиза. Администраторы видят баннер и окно **Версия и обновления** с точной командой для вашего способа установки (Helm, манифесты Kubernetes, Docker). Сервер сам себя не обновляет. Чтобы отключить такие запросы, например в изолированной сети, задайте `kubron.updateCheck: false`. Если вы сузили `networkPolicy.egress.clusterApi.cidrs`, разрешите `api.github.com` через `networkPolicy.egress.extra` или отключите проверку.

```bash
helm upgrade kubron oci://ghcr.io/24dso-lab/charts/kubron \
  --version <new-version> -n kubron -f values.yaml
```

- Реплики заменяются по одной (rolling update). Миграции базы применяются автоматически при старте. Подробности работы нескольких реплик — в [руководстве по HA](/ru/server/ha).
- Сначала прочитайте [журнал изменений](https://github.com/24DSO-lab/kubron/blob/master/CHANGELOG.md): в нём перечислены изменения поведения, например ролей и разрешений.
- Перед каждым обновлением делайте резервную копию базы.
- Откат: `helm rollback kubron -n kubron`. Откат не отменяет миграцию базы, поэтому, если релиз менял схему и нужно вернуться дальше, восстанавливайте из резервной копии.

## Проверка обновлений {#update-check}

Раз в сутки сервер спрашивает `api.github.com` о последнем релизе. Номер версии в шапке видят все пользователи; администратор (право `users.manage`) видит баннер, если вышла новая версия, и окно **Version and updates** с шагами для вашего способа установки. Сервер сам себя не обновляет. Что показывает окно, см. в разделе [Версия и обновления](/ru/features/updates).

| Установка | Как определяется | Что показывается |
|---|---|---|
| Helm | Чарт задаёт `KUBRON_INSTALL_METHOD=helm`, `KUBRON_HELM_RELEASE`, `KUBRON_HELM_NAMESPACE` | `helm upgrade <релиз> oci://ghcr.io/24dso-lab/charts/kubron --version <версия> --namespace <ns> --reset-then-reuse-values` |
| Kubernetes без Helm | Задан `KUBERNETES_SERVICE_HOST` | Тег нового образа для манифестов, Kustomize или GitOps |
| Docker, Podman, Compose | Признаки контейнера (`/.dockerenv`, cgroup) | `docker pull` и `docker compose pull && docker compose up -d` |
| Бинарный файл | Всё остальное | Ссылка на релиз |

Если автоопределение ошиблось (например, образ запущен вашим оркестратором), задайте `KUBRON_INSTALL_METHOD` равным `helm`, `kubernetes`, `docker` или `binary`.

- **Отключение.** `KUBRON_UPDATE_CHECK=off` (`kubron.updateCheck: false` в чарте) останавливает любые исходящие проверки. Отключайте её в изолированных сетях. Сборка без версии (`dev`) ничего не проверяет. Значение, отличное от `on` и `off`, — ошибка запуска.
- **NetworkPolicy.** Правило исходящего трафика к API-серверам по умолчанию пропускает порт 443 на любые адреса, и до GitHub сервер дойдёт. Если вы сузили `networkPolicy.egress.clusterApi.cidrs`, добавьте правило для `api.github.com` в `networkPolicy.egress.extra` либо отключите проверку; иначе окно покажет ошибку сети.
- **Без токена и без расхода лимита.** Запросы анонимные и условные (`ETag`), поэтому лимит GitHub (60 в час на адрес) не расходуется, а ручная проверка разрешена раз в минуту.

## Удаление

```bash
helm uninstall kubron -n kubron
```

Это убирает рабочую нагрузку, но **не** ваш Secret и не данные PostgreSQL. Если хотите стереть всё, удалите базу и Secret самостоятельно.

## Проверка образа и чарта

Каждый релизный образ и чарт подписаны cosign и снабжены аттестациями provenance и SBOM. Всегда проверяйте **digest**, а не только тег.

```bash
VERSION=1.2.0
IMAGE=ghcr.io/24dso-lab/kubron
CHART=ghcr.io/24dso-lab/charts/kubron

# Происхождение: собрано workflow publish этого репозитория
gh attestation verify "oci://$IMAGE:$VERSION" --repo 24DSO-lab/kubron \
  --signer-workflow 24DSO-lab/kubron/.github/workflows/publish.yml

# Подпись (keyless)
IDENTITY='^https://github\.com/24DSO-lab/kubron/\.github/workflows/publish\.yml@refs/tags/v[0-9].*$'
cosign verify "$IMAGE:$VERSION" \
  --certificate-identity-regexp "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify "$CHART:$VERSION" \
  --certificate-identity-regexp "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Полный набор проверок, включая аттестации SBOM и политику Kyverno, запрещающую неподписанные образы, есть в [руководстве по эксплуатации](https://github.com/24DSO-lab/kubron/blob/master/docs/deploy.md).

## Чек-лист усиления защиты

- [ ] TLS терминируется на доверенном прокси, под доступен только через него.
- [ ] `KUBRON_TRUST_PROXY=1` включён, только если это так.
- [ ] Задан `KUBRON_ALLOWED_HOSTS` (чарт делает это из `ingress.hosts`).
- [ ] `KUBRON_PUBLIC_URL`, если используется, — origin с `https`, совпадающий с хостом ingress.
- [ ] Ключ шифрования лежит в менеджере секретов и копируется отдельно от базы.
- [ ] PostgreSQL использует `sslmode=verify-full`, имеет резервные копии и не доступен публично.
- [ ] `networkPolicy` пропускает только ваш ingress-контроллер; порт метрик доступен только из мониторинга.
- [ ] Две и более реплики с `podDisruptionBudget`.
- [ ] Терминалы, оболочка узла и port-forward отключены, если не нужны (`kubron.disableExec`, `kubron.disableNodeShell`, `kubron.disablePortForward`).
- [ ] Образы зафиксированы по digest и проверены.

## Если что-то не работает

| Симптом | Что проверить |
|---|---|
| Поды в `CrashLoopBackOff` сразу после старта | `kubectl logs`: отсутствие `KUBRON_SECRET_KEY`, недоступная база или ключ, не подходящий к сохранённым данным, намеренно останавливают запуск |
| `ImagePullBackOff` или `InvalidImageName` | Неверный `image.tag` (у чарта из исходников по умолчанию `0.0.0-dev`, такого образа нет), имя образа с заглавными буквами (имена репозиториев должны быть в нижнем регистре, например `ghcr.io/24dso-lab/kubron`) или приватный реестр без `imagePullSecrets` |
| Сайт отвечает `421` | `Host` нет в `KUBRON_ALLOWED_HOSTS`; добавьте его в `kubron.allowedHosts` |
| Не удаётся войти; cookie теряется | TLS не терминируется перед подом либо `trustProxy` выключен за HTTPS-прокси |
| Терминалы и живые обновления рвутся примерно через минуту | Прокси закрывает простаивающие WebSocket. Увеличьте таймауты чтения и отправки (см. аннотации ingress выше) |
| При включённой `networkPolicy` до подов ничего не доходит | Задайте `networkPolicy.ingress.from` на namespace вашего ingress-контроллера |
| «The setup token is missing or invalid» | Заново прочитайте токен из Secret `<релиз>-setup` или используйте `create-admin` |

## Подробнее

- [Справочник по конфигурации](/ru/server/configuration)
- [Высокая доступность](/ru/server/ha)
- [Журналы, аудит и метрики](/ru/server/observability)
- [Резервное копирование, восстановление и ротация ключа](/ru/server/backup)
- [Безопасность HTTP](/ru/server/http-security)
- [Справочник команд](/ru/server/cli)
