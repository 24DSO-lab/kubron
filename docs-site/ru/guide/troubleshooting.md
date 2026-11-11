# Диагностика и частые вопросы

Найдите свой симптом, прочитайте вероятную причину и перейдите по ссылке, если есть более подробное объяснение. Ниже приведены сообщения, которые показывает Kubron, поэтому текст, который вы видите, можно искать по этой странице.

[[toc]]

## Запуск Kubron

| Симптом | Что делать |
|---|---|
| Windows пишет **Windows защитила ваш компьютер** | Сборки пока не подписаны. Выберите **Подробнее, Выполнить в любом случае**. См. [Desktop](/ru/guide/desktop#если-что-то-не-работает) |
| macOS пишет, что приложение повреждено или от неизвестного разработчика | Снимите флаг карантина или откройте через правую кнопку **Открыть**. См. [Desktop](/ru/guide/desktop#если-что-то-не-работает) |
| Linux: нет `libwebkit2gtk-4.1` или `libgtk-3` | Установите GTK 3 и WebKitGTK 4.1. См. [Desktop](/ru/guide/desktop#если-что-то-не-работает) |
| Окно пустое в Linux | Попробуйте `WEBKIT_DISABLE_DMABUF_RENDERER=1 kubron`. См. [Desktop](/ru/guide/desktop#если-что-то-не-работает) |
| **Sign-in unavailable**: файл пользователей не удалось прочитать | Kubron не смог прочитать `~/.kubron/users.json` и остаётся закрытым, чтобы защитить ваши данные. Восстановите файл из резервной копии |

## Вход в Kubron

| Сообщение или симптом | Причина и решение |
|---|---|
| **login temporarily blocked after failed attempts** | Слишком много неверных паролей. На логин даётся 5 бесплатных попыток, на адрес — 30 за 15 минут, затем блокировка до 5 минут, растущая с каждой неудачей. Подождите и повторите. Администратор видит отказы в журнале аудита. См. [Безопасность HTTP](/ru/server/http-security#ограничение-попыток) |
| **initial setup required: create an administrator** | Пользователей ещё нет. Создайте первого администратора на экране приветствия |
| **initial setup is already complete** | Администратор уже есть. Войдите |
| **the setup token is missing or invalid** (Web) | Введите значение `KUBRON_SETUP_TOKEN` или создайте администратора командой `kubron-server create-admin`. См. [Web: локальный быстрый старт](/ru/server/quick-start#b-многопользовательская-проба-в-docker-compose) |
| **initial setup over the network is disabled** (Web) | `KUBRON_SETUP_TOKEN` не задан. Задайте его или выполните `kubron-server create-admin` на сервере. См. [Справочник команд](/ru/server/cli#create-admin) |
| **the setup token is too short** (Web) | В `KUBRON_SETUP_TOKEN` должно быть не меньше 16 символов |
| Через некоторое время вы вышли из системы | Автовыход при бездействии (по умолчанию 15 минут; администратор может изменить срок или отключить его). Веб-сессии также завершаются через `KUBRON_SESSION_TTL` (по умолчанию 12 часов) |
| **Вам выдан временный пароль** | Администратор сбросил ваш пароль. Задайте свой, чтобы продолжить |
| **invalid or missing CSRF token** (Web) | Перезагрузите страницу. Если за прокси ошибка повторяется, прокси должен передавать исходный заголовок `Host`: Kubron принимает запрос, только когда хост в `Origin` браузера совпадает с `Host` |

## Подключение к кластеру

| Сообщение или симптом | Причина и решение |
|---|---|
| Проверка подключения завершается по таймауту | API-сервер недоступен оттуда, где работает Kubron: VPN, файрвол или прокси. В Web это значит «недоступен **с сервера**» |
| `x509: certificate signed by unknown authority` | В kubeconfig нет CA кластера либо корпоративный прокси подменяет TLS |
| `Unauthorized` | Токен или сертификат истёк либо отозван. Войдите заново или замените учётные данные |
| `Forbidden` | У пользователя не хватает прав Kubernetes RBAC. Права Kubron никогда не добавляют прав сверх RBAC |
| **no permission to read namespaces: some data may be unavailable** | Вы не можете получить список namespace по всему кластеру. Разделы работают в выбранных вами namespace: выберите их в селекторе либо задайте namespace по умолчанию в настройках кластера. Namespace, которые Kubron прочитать не может, помечаются **No access**. См. [Работа с ресурсами](/ru/features/working-with-resources#choosing-namespaces) |
| **the kubeconfig refers to files on the server: embed certificates and tokens in it (the -data fields)** (Web) | Сервер не может читать файлы с вашего компьютера. Встройте `certificate-authority-data`, `client-certificate-data` и `client-key-data` либо используйте окно входа |
| Добавление кластера не проходит из-за адреса (Web) | Политика адресов закрывает loopback, link-local и адреса метаданных облака (а при `strict` и частные сети). См. [Конфигурацию](/ru/server/configuration) |

## Окно входа в кластер

| Сообщение | Причина и решение |
|---|---|
| **the plugin command was not found; install it and try again** | `exec`-плагин (`aws`, `kubelogin`, ...) не находится в `PATH`, который видит приложение. Запустите Kubron из терминала либо укажите в kubeconfig полный путь |
| **the plugin command is a relative path** | Используйте абсолютный путь или имя, найденное в `PATH` |
| **the plugin was not confirmed** | Вы отклонили подтверждение. Desktop спрашивает перед запуском любого плагина, который Kubron не обрабатывает сам |
| **the plugin did not finish in time** / **exited with an error** | Выполните показанную команду в терминале, чтобы увидеть причину сбоя |
| **plugins that require an interactive terminal are not supported yet** | Запустите плагин сами и вставьте токен. См. [Подключение к Kubernetes](/ru/features/cluster-auth) |
| **the pasted ExecCredential has no token** / **is not a valid ExecCredential** | В Web вставьте JSON, который печатают плагины в стиле `kubectl`, либо сам токен одной строкой |
| **client certificates returned by a plugin are not supported** | Вместо этого встройте клиентский сертификат в kubeconfig |
| **the identity provider is unreachable** | Адрес провайдера недоступен с сервера либо его блокирует политика исходящего трафика. См. [Развёртывание в Docker и Kubernetes](/ru/server/deploy#необязательно-вход-через-браузер-и-аккаунты-google) |
| **skipping TLS verification is not allowed here** (Web) | Веб-вход отклоняет `insecure-skip-tls-verify`. Передайте CA провайдера встроенными данными |
| **the sign-in response came from an unexpected issuer** / **does not match the request** | Браузер вернулся от другого провайдера или после старой попытки. Начните вход заново и завершите его сразу |
| **the identity provider does not offer the device sign-in** | Используйте вход через браузер или включите device grant у клиента |
| **Azure requires an authentication method unsupported by password sign-in** | Для учётной записи нужна MFA. Используйте код устройства вместо логина и пароля |
| **the clock of this server differs too much from the AWS clock** (EKS) | Исправьте синхронизацию времени (NTP) на сервере Kubron |
| **AWS does not allow these credentials to assume the role** (EKS) | Проверьте политику доверия роли и `role-arn` в kubeconfig |
| **Google sign-in was denied or cancelled** (GKE) | Начните заново и подтвердите запрос на странице Google |
| **the shared account of this cluster is not signed in: ask a cluster administrator** | Общая учётная запись команды есть, но никто в неё не входил. Администратор команды выполняет вход один раз |

## Работа с ресурсами

| Сообщение или симптом | Причина и решение |
|---|---|
| **insufficient permissions for this action** | У вашей роли Kubron нет нужного права. См. [справочник прав](/ru/features/security#справочник-прав) |
| **eviction is blocked by a PodDisruptionBudget** | Бюджет сейчас не допускает добровольного вытеснения. Увеличьте число реплик, почините неработающие Pod или подождите. Блокирующие бюджеты видны на дашборде |
| Нет значений Secret или сертификатов | У роли `user` нет `secrets.read`. В режиме in-cluster ServiceAccount тоже нуждается в `rbac.allowSecretsRead=true`. См. [Права ServiceAccount в режиме in-cluster](/ru/server/deploy#права-serviceaccount-в-режиме-in-cluster) |
| На дашборде нет «горячих» Pod и томов по CPU, памяти и месту | Для них нужен необязательный стек метрик. См. [Дашборд кластера](/ru/features/dashboard#metrics-and-hotspots) |
| **chart ... has no versions (semver tags) or access is denied** | В OCI-реестре у этого чарта нет semver-тегов либо нужны учётные данные. Добавьте реестр с учётными данными в настройках Helm |
| **port-forward is disabled** / **too many port-forwards (maximum N)** | Администратор отключил его (`KUBRON_DISABLE_PORT_FORWARD`) либо достигнут лимит на пользователя. См. [Конфигурацию](/ru/server/configuration) |
| **timed out establishing port-forward** | Pod или Service недоступен либо у него нет готового endpoint |
| Port-forward не работает в Web | Web требует `KUBRON_PF_BASE_DOMAIN`, wildcard DNS-запись и сертификат для `*.pf.<домен>`. Без этого port-forward в сервисном режиме выключен |
| Терминалы или живые обновления рвутся примерно через минуту (Web) | Прокси закрывает простаивающие WebSocket. Увеличьте таймауты чтения и отправки. См. [Развёртывание в Docker и Kubernetes](/ru/server/deploy#если-что-то-не-работает) |

## Flux CD

| Сообщение или симптом | Причина и решение |
|---|---|
| **This Flux version is not supported** | Обслуживаются только старые версии API. Обновите Flux до 2.3 или новее и выполните `flux migrate`, чтобы перевести сохранённые объекты на стабильные версии API. См. [GitOps](/ru/features/gitops#flux-cd) |
| Раздела Flux нет, хотя Flux установлен | Kubron считает Flux не установленным, если CRD нельзя прочитать (нет права `list`) или нет ни контроллера Kustomize, ни Helm, и скрывает раздел без ошибки. Проверка повторяется примерно раз в 30 секунд. См. [Права для Flux CD](/ru/server/deploy#rights-for-flux-cd) |
| Не хватает вкладок Flux (источники, уведомления, автоматизация образов) | Flux установлен частично: вкладка показывается, только если есть её контроллер |
| Reconcile, suspend, resume или удаление завершается `forbidden` | В Kubernetes RBAC нет `patch` или `delete` для этих видов Flux. Права Kubron (`operate`, `write`) RBAC не отменяют. Добавьте правила из раздела [Права для Flux CD](/ru/server/deploy#rights-for-flux-cd) |
| У объекта нет кнопок действий | Объекты уведомлений (`Alert`, `Provider`, `Receiver`) и `ImagePolicy` доступны только для чтения, а вашей роли нужен `operate` для reconcile или `write` для suspend, resume и удаления |
| Вкладка ресурсов у HelmRelease недоступна | Flux записывает inventory только с версии 2.8. В старых версиях показывать нечего |
| Ресурс помечен **remote** | Объект использует `spec.kubeConfig`, поэтому его ресурсы находятся в другом кластере и локально не разрешаются |
| Объект сам снова становится приостановленным или активным | `spec.suspend` задан в Git, и родительская Kustomization возвращает значение. Меняйте его в Git |
| Удалённый объект вернулся | Он описан в Git, и родительская Kustomization создала его снова. Удалите его из Git |

## Debug-контейнеры, обновления и отчёты о проблемах

| Сообщение или симптом | Причина и решение |
|---|---|
| **debug containers are disabled by the administrator** | Задан `KUBRON_DISABLE_DEBUG` или `KUBRON_DISABLE_EXEC`. См. [Debug-контейнеры](/ru/features/debug-containers) |
| Учётной записи кластера нельзя добавлять ephemeral-контейнеры | Добавьте `patch` на `pods/ephemeralcontainers` через `rbac.extraRules`. См. [Debug-контейнеры](/ru/features/debug-containers#what-you-need) |
| **the cluster rejected the debug container (image, profile or Pod Security policy)** | Попробуйте профиль **Restricted** или другой образ |
| **Could not reach GitHub. Check the network connection.** | Проверка обновлений не достаёт до `api.github.com`. Исправьте прокси, файрвол или `NetworkPolicy` либо отключите проверку. См. [Версия и обновления](/ru/features/updates#network-access) |
| Баннера обновления нет, хотя новая версия вышла | Его видят только администраторы, его можно скрыть до следующей версии, сборка для разработки не проверяет, а проверка может быть отключена (`KUBRON_UPDATE_CHECK=off`) |
| **The mail server is not set up yet** или **The mail server for reports is not set up** | Нет доступного канала доставки. В Web администратор задаёт `KUBRON_BUGREPORT_SMTP_*` или `KUBRON_BUGREPORT_RELAY_URL`. См. [Сообщить о проблеме](/ru/features/bug-reports) |
| **No connection: the report is saved on this computer and will be sent automatically.** | Desktop поставил отчёт в очередь и повторяет отправку; **Save as archive** позволяет передать его другим способом |
| Нет кнопки Report a problem (Web) | На сервере не настроены ни почтовый сервер, ни приёмник |

## Запуск Web-сервера

| Сообщение или симптом | Причина и решение |
|---|---|
| `... is not loopback: the server does not terminate TLS ...` и выход | Задайте `KUBRON_ALLOW_REMOTE=1` и поставьте перед сервером TLS-прокси. См. [Web: локальный быстрый старт](/ru/server/quick-start#если-что-то-не-работает) |
| **KUBRON_SECRET_KEY is required when a database is used** | Задавайте ключ всегда, когда задан `KUBRON_DATABASE_URL` |
| Сервер не запускается и называет неизвестный или неподходящий ключ | Ключ не подходит к сохранённым данным. См. [Резервное копирование, восстановление и ротация ключа](/ru/server/backup#восстановление) |
| Сайт отвечает `421` | `Host` нет в `KUBRON_ALLOWED_HOSTS`. См. [Безопасность HTTP](/ru/server/http-security#cookie-сессии-и-заголовок-host) |
| Не получается остаться в системе за HTTPS | TLS должен завершаться на доверенном прокси, и должен быть задан `KUBRON_TRUST_PROXY=1`, чтобы защищённая cookie принималась |
| Pod в `ImagePullBackOff` или `InvalidImageName` | Используйте имя образа в нижнем регистре (`ghcr.io/24dso-lab/kubron`) и опубликованный тег. См. [Развёртывание в Docker и Kubernetes](/ru/server/deploy#если-что-то-не-работает) |
| Реплика не становится готовой после обновления | Посмотрите её журнал: отсутствие или неверный ключ, недоступная база или неудачная миграция намеренно останавливают запуск. См. [Высокую доступность](/ru/server/ha) |
| Журнал аудита или сессии пусты после восстановления | Веб-сессии намеренно не восстанавливаются, пользователи входят заново. Проверьте срок хранения аудита (`KUBRON_AUDIT_RETENTION`) |

## Частые вопросы

**Нужен ли `kubectl`?** Нет. Kubron сам обращается к API Kubernetes. Плагины в стиле `kubectl` (например, `aws` или `kubelogin`) нужны, только когда kubeconfig использует `exec`-плагин, который Kubron не обрабатывает сам.

**Отправляет ли Kubron данные куда-либо?** Телеметрии нет. Kubron подключается к вашим кластерам (в Desktop — с вашего компьютера, в Web — с вашего сервера) и к провайдерам идентификации, через которые вы входите. Раз в сутки он также спрашивает GitHub, не вышла ли новая версия; это можно отключить ([Версия и обновления](/ru/features/updates)). Отчёт о проблеме покидает ваш компьютер, только когда вы нажмёте **Send** в **Report a problem** ([подробнее](/ru/features/bug-reports)).

**Где мои данные?** Desktop и однопользовательский сервер: `~/.kubron` (`%USERPROFILE%\.kubron` в Windows). Web с PostgreSQL: в базе, а секреты зашифрованы `KUBRON_SECRET_KEY`. См. [Резервное копирование, восстановление и ротация ключа](/ru/server/backup).

**Меняет ли Kubron мой kubeconfig?** Нет. Он хранит копию содержимого в собственной папке. Исходный `~/.kube/config` не изменяется.

**Можно ли использовать несколько кластеров и контекстов?** Да. Добавьте каждый контекст как отдельный кластер с понятным именем и тегом окружения. См. [Первый кластер](/ru/guide/first-cluster).

**Работает ли с ограниченным доступом?** Да. Вы можете работать в выбранных namespace без cluster-wide `list`. См. [Работа с ресурсами](/ru/features/working-with-resources#choosing-namespaces).

**Сколько это стоит и под какой лицензией?** Kubron — открытое ПО под Apache-2.0, включая Web-версию.

**Как убедиться, что загрузка подлинная?** См. [Требования, загрузка и проверка](/ru/guide/installation#проверка-загрузки).

**Как сообщить об ошибке или попросить помощи?** Откройте issue на [GitHub](https://github.com/24DSO-lab/kubron/issues/new/choose): укажите ОС, версию Kubron и что вы пробовали. Об уязвимостях сообщайте по [политике безопасности](https://github.com/24DSO-lab/kubron/blob/master/SECURITY.md).
