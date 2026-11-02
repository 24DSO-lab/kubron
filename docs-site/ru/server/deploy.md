# Развёртывание в Docker и Kubernetes

Для tagged-релизов Kubron публикует контейнерный образ и Helm chart:

- Образ: `ghcr.io/24dso-labs/kubron`.
- OCI chart: `oci://ghcr.io/24dso-labs/charts/kubron`.
- Исходники chart: [deploy/helm/kubron](https://github.com/24DSO-labs/kubron/tree/master/deploy/helm/kubron).

## Предварительные требования

- PostgreSQL 14+.
- Ingress или reverse proxy с TLS.
- Постоянное хранилище и резервное копирование БД.
- Ключ шифрования из 32 байт в формате `id:base64`.

Создайте ключ:

```sh
echo "k1:$(openssl rand -base64 32)"
```

Сохраните его в менеджере секретов до развёртывания. Потеря ключа означает потерю доступа к зашифрованным kubeconfig и GitOps tokens.

## Установка через Helm

```sh
kubectl create namespace kubron
kubectl -n kubron create secret generic kubron-secrets \
  --from-literal=KUBRON_SECRET_KEY='k1:…' \
  --from-literal=KUBRON_DATABASE_URL='postgres://kubron:…@db.example.com:5432/kubron?sslmode=verify-full'

helm install kubron oci://ghcr.io/24dso-labs/charts/kubron \
  --namespace kubron \
  --set secrets.existingSecret=kubron-secrets \
  --set image.tag=<release-version>
```

Перед установкой изучите `values.yaml` и `values.schema.json`. Настройте TLS ingress, trusted proxy, network policy, requests/limits и резервное копирование PostgreSQL.

## Первый администратор

На пустой базе войти некому, поэтому Kubron должен создать первого администратора. Настройка по сети защищена
токеном; выберите один из двух способов:

- **Токен настройки.** Добавьте `KUBRON_SETUP_TOKEN` (не короче 16 символов) в secret и задайте
  `--set secrets.keys.setupToken=KUBRON_SETUP_TOKEN`. Экран настройки запросит токен; он сравнивается за постоянное
  время, неверные попытки ограничиваются по адресу.
- **Командная строка.** Без токена настройка по сети отключена. Выполните
  `kubron-server create-admin --username <имя>` рядом с сервером (команда работает с БД напрямую); пароль берётся из
  `KUBRON_ADMIN_PASSWORD` или stdin, аргументом он не передаётся.

Когда администратор создан, токен не нужен; сервер для одного пользователя на loopback его не использует.

## Самостоятельная сборка образа

```sh
docker build -t kubron:dev .
```

## Высокая доступность

При подключённом PostgreSQL поддерживается несколько реплик. Сессии и состояние пользователей общие, изменения передаются через уведомления PostgreSQL, а при остановке реплика корректно завершает WebSocket и terminal connections.

Подробнее: [руководство по HA](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md) и [архитектура сессий](https://github.com/24DSO-labs/kubron/blob/master/docs/ha-sessions.md).

## Важные замечания

- Сервер требует TLS proxy и не завершает TLS самостоятельно.
- Listener метрик отделён от основного порта и должен быть доступен только системе мониторинга.
- Для port-forward в серверном режиме нужны `KUBRON_PF_BASE_DOMAIN`, wildcard DNS и wildcard TLS certificate.
- Настройте резервное копирование PostgreSQL и восстановление ключа шифрования до production-эксплуатации.

Полный список параметров и операционных процедур: [docs/deploy.md](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md).
