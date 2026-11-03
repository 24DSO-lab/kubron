# Конфигурация сервера

Kubron Server настраивается переменными окружения и параметрами Helm chart. Ниже перечислены важные настройки; список не является полным.

| Параметр | Назначение |
|---|---|
| `KUBRON_LISTEN` | HTTP-адрес прослушивания |
| `KUBRON_ALLOW_REMOTE=1` | Требуется при прослушивании не-loopback адреса |
| `KUBRON_DATABASE_URL` | Строка подключения PostgreSQL 14+ |
| `KUBRON_SECRET_KEY` | Ключ шифрования kubeconfig и GitOps credentials в хранилище |
| `KUBRON_DB_LISTEN_URL` | Прямое подключение PostgreSQL для LISTEN/NOTIFY; PgBouncer в transaction mode не поддерживает LISTEN |
| `KUBRON_REPLICA_ID` | Необязательный уникальный идентификатор реплики |
| `KUBRON_DRAIN_DELAY` | Пауза при graceful shutdown для удаления реплики из балансировщика |
| `KUBRON_METRICS_LISTEN` | Необязательный отдельный listener Prometheus; по умолчанию выключен |
| `KUBRON_ALLOWED_HOSTS` | Список допустимых значений `Host` через запятую (имя или IP, порт необязателен, IPv6 в скобках); остальные получают `421`. Пусто — проверка выключена; `*.pf.<домен>` разрешается автоматически при включённом port-forward |
| `KUBRON_PF_BASE_DOMAIN` | Wildcard-домен, необходимый для port-forward в серверном режиме |
| `KUBRON_DISABLE_EXEC` | Отключить pod exec и node shell |
| `KUBRON_DISABLE_NODE_SHELL` | Отключить node shell |
| `KUBRON_DISABLE_PORT_FORWARD` | Отключить port-forward |

## Секреты и ротация ключа

Используйте secret manager или Secret оркестратора для хранения пароля БД и ключа шифрования. Не помещайте production-секреты в Helm values или аргументы командной строки.

Формат ключа шифрования: `id:base64`, где base64 кодирует ровно 32 случайных байта. Для ротации задайте новый ключ в `KUBRON_SECRET_KEY`, старый передайте через `KUBRON_SECRET_KEY_PREVIOUS`, затем выполните `kubron-server rekey`. Удаляйте старые ключи только после завершения и проверки перешифрования.

## Сеть и TLS

Сервер не завершает TLS. Используйте доверенный reverse proxy или ingress. Доверяйте proxy-заголовкам только если прямой доступ к pod ограничен. Не публикуйте listener метрик через внешний ingress.

Подробности — в [руководстве по развёртыванию](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md) и [описании метрик](https://github.com/24DSO-labs/kubron/blob/master/docs/metrics.md).
