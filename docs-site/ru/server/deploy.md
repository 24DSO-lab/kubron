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

## Проверка образа и чарта

Образы релизов и OCI-чарт подписаны cosign (keyless) и имеют аттестации происхождения сборки и SBOM. Проверьте перед установкой:

```sh
gh attestation verify oci://ghcr.io/24dso-labs/kubron:<версия> --repo 24DSO-labs/kubron
cosign verify ghcr.io/24dso-labs/kubron:<версия> \
  --certificate-identity-regexp '^https://github.com/24DSO-labs/kubron/\.github/workflows/publish\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Те же команды подходят для `oci://ghcr.io/24dso-labs/charts/kubron`. Подробности: [docs/deploy.md](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md).

## Первый запуск

Пока нет первого администратора, сервер принимает первичную настройку только с токеном (`KUBRON_SETUP_TOKEN`, не короче 16 символов). Чарт создаёт токен в Secret `<release>-setup`, если не задан `setup.existingSecret` (для GitOps обязателен: Argo CD и `helm template` не могут прочитать его обратно). Прочитать: `kubectl get secret <release>-setup -o jsonpath='{.data.KUBRON_SETUP_TOKEN}' | base64 -d`.

Без сети администратора можно создать прямо в БД; пароль передавайте через stdin или `KUBRON_ADMIN_PASSWORD`, не аргументом:

```sh
kubectl exec -i deploy/<release> -- /kubron-server create-admin --username admin < password.txt
```

## Сеть и RBAC

- **NetworkPolicy.** Egress к API-серверам открыт, кроме link-local и адресов метаданных облака (`169.254.0.0/16`, `fd00:ec2::254/128`). Сузьте egress к PostgreSQL, API-серверам и IdP до своих сетей через `networkPolicy.egress.*.cidrs`.
- **RBAC in-cluster.** `rbac.profile`: `readonly` (по умолчанию, без Secret), `operator` (restart, scale, rollback, повтор Job, cordon) или `custom`. ServiceAccount общий для всех пользователей, поэтому профиль — потолок прав любого пользователя в этом кластере.

## Домены, Host и TLS

- Чарт задаёт `KUBRON_ALLOWED_HOSTS` из `ingress.hosts` и `*.pf.<portForward.baseDomain>`; остальные адреса добавьте в `kubron.allowedHosts`.
- Задавайте `portForward.baseDomain` на **отдельном регистрируемом домене**, а не поддомене хоста Kubron: иначе JS проксированного приложения сможет подменять cookie Kubron. Нужны wildcard DNS и wildcard-сертификат для `*.pf.<baseDomain>`.
- `KUBRON_TRUST_PROXY` включайте, только если под доступен лишь через ваш прокси. На Ingress используйте sticky-сессии (cookie affinity): терминалы, логи и потоки port-forward живут на открывшей их реплике, а билет port-forward одноразовый в пределах реплики.

## Высокая доступность

При подключённом PostgreSQL поддерживается несколько реплик. Сессии и состояние пользователей общие, изменения передаются через уведомления PostgreSQL, а при остановке реплика корректно завершает WebSocket и terminal connections.

Подробнее: [руководство по HA](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md) и [архитектура сессий](https://github.com/24DSO-labs/kubron/blob/master/docs/ha-sessions.md).

## Важные замечания

- Сервер требует TLS proxy и не завершает TLS самостоятельно.
- Listener метрик отделён от основного порта и должен быть доступен только системе мониторинга.
- Для port-forward в серверном режиме нужны `KUBRON_PF_BASE_DOMAIN`, wildcard DNS и wildcard TLS certificate.
- Настройте резервное копирование PostgreSQL и восстановление ключа шифрования до production-эксплуатации.

Полный список параметров и операционных процедур: [docs/deploy.md](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md).
