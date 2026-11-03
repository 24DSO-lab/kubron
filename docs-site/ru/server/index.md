# Web-версия

Web-версия запускает Kubron как самостоятельно разворачиваемое приложение и предоставляет привычный интерфейс Kubron по HTTP. Открывайте его в браузере для совместной работы нескольких пользователей. Инфраструктуру сервера обслуживаете вы или ваша команда; Kubron не предоставляет готовый облачный SaaS-сервис.

## Возможности Web-версии

- PostgreSQL для пользователей, кластеров, web-сессий, журнала аудита и общего состояния Helm.
- Шифрование kubeconfig и GitOps credentials в хранилище с помощью `KUBRON_SECRET_KEY`.
- Локальные учётные записи, роли и детальные разрешения.
- Несколько реплик сервера с общими сессиями и состоянием пользователей через PostgreSQL.
- Уведомления между репликами, graceful draining и readiness checks.
- Необязательные метрики Prometheus на отдельном порту.
- Опубликованные контейнерный образ и Helm chart.

## Требования для production

- PostgreSQL 14 или новее.
- Стабильный ключ шифрования из 32 байт, сохранённый в менеджере секретов.
- Reverse proxy или ingress, завершающий TLS.
- Постоянное хранилище и проверенные резервные копии PostgreSQL.
- Сетевые ограничения для приложения и endpoint метрик.

Сервер самостоятельно не завершает TLS. Не публикуйте его HTTP listener напрямую в недоверенную сеть. Для port-forward в серверном режиме также нужны wildcard DNS и TLS.

## Развёртывание Web-версии

- Образ: `ghcr.io/24dso-labs/kubron`.
- OCI chart: `oci://ghcr.io/24dso-labs/charts/kubron`.
- [Быстрый запуск](/ru/server/quick-start).
- [Docker и Kubernetes](/ru/server/deploy).
- [Конфигурация](/ru/server/configuration).
- [Полное руководство по развёртыванию](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md).
- [Эксплуатация HA](https://github.com/24DSO-labs/kubron/blob/master/docs/ha.md).
- [Метрики Prometheus](https://github.com/24DSO-labs/kubron/blob/master/docs/metrics.md).

::: warning Безопасность
Создайте и сохраните резервную копию `KUBRON_SECRET_KEY` до первого production-запуска. Потерянный ключ невозможно восстановить, а зашифрованные credentials станут недоступны. TLS должен завершаться на доверенном proxy; ограничьте прямой доступ к серверу и порту метрик.
:::
