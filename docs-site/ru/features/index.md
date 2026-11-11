# Возможности Kubron


Kubron — Kubernetes Operations Workspace в двух версиях: desktop-приложение и самостоятельно разворачиваемая Web-версия. Основные рабочие сценарии общие. При подключении PostgreSQL Web дополнительно предоставляет совместный доступ, изоляцию команд и централизованное администрирование.

## Кратко о возможностях

| Возможность | Desktop | Web |
|---|---|---|
| Управление кластерами и ресурсами | Да | Да |
| Локальные пользователи и роли | Да | Да |
| Организация, команды и кластеры команд | — | Service mode с PostgreSQL |
| Helm, Argo CD и Flux CD | Да | Да |
| Дополнительный стек метрик Prometheus | Да | Да |
| Несколько реплик сервера (HA) | — | Service mode с PostgreSQL |
| Port-forward | Локальный порт | Локальный режим одного пользователя или настроенный proxy mode |


## Кластеры и дашборд

- Подключение нескольких кластеров и контекстов; для каждого можно задать имя, иконку, цвет и тег окружения.
- Фильтрация по одному, нескольким или всем namespace; поддерживается ограниченный Kubernetes RBAC.
- Актуализация списков ресурсов через shared informers без агрессивного polling.
- Обзор здоровья кластера, узлов, workloads, capacity, хранилища, rollout и недавних изменений.
- Выявление проблемных Pod, перегруженных ресурсов, pending/lost PVC и TLS-сертификатов с истекающим сроком.
- Дополнительные графики использования ресурсов при установленном metrics stack.

## Ресурсы Kubernetes

- Просмотр workloads, сетевых, конфигурационных, storage-, RBAC- и policy-ресурсов.
- Автоматическое обнаружение CRD и отображение их `additionalPrinterColumns`.
- Подробности conditions, events, owners, containers, volumes и параметров планирования.
- Просмотр и редактирование YAML в Monaco с предварительным diff перед применением.
- Создание ресурсов из manifest, удаление объектов и поддерживаемые массовые действия.

## Операции

- Просмотр логов Pod и интерактивные терминалы при наличии разрешений.
- Масштабирование, перезапуск и rollback поддерживаемых workloads; операции с Job и CronJob.
- Cordon и drain узлов; node shell защищён отдельным привилегированным разрешением.
- Port-forward к Pod и Service с учётом версии и настроек сервера.
- Дополнительная установка Prometheus, kube-state-metrics и node-exporter в кластер.

## Helm и GitOps

- Каталог Helm-репозиториев и OCI registry, установка и обновление с dry-run и diff manifest.
- Просмотр values, manifest, notes, истории и diff ревизий; rollback и удаление релиза.
- Работа с Argo CD Applications, ApplicationSets и Projects: sync, refresh, rollback, diff и граф ресурсов.
- Работа с Kustomization и HelmRelease Flux CD, а также с источниками, уведомлениями и автоматизацией образов: состояние, ревизия, зависимости и управляемые ресурсы, reconcile, suspend, resume и удаление.

## Безопасность и совместная работа

- Встроенные роли `admin`, `operator` и `user` с проверкой детальных разрешений на backend.
- Локальные учётные записи, хеширование паролей, блокировка входа и автоматический выход.
- Журнал аудита привилегированных и административных действий.
- Web team mode разделяет пользователей и кластеры по организациям и командам; kubeconfig и Helm-секреты в PostgreSQL хранятся в зашифрованном виде.
- Нет телеметрии. Desktop хранит kubeconfig локально; аккаунт в облачном сервисе Kubron не требуется.

## Подробнее

- [Несколько кластеров](/ru/features/multi-cluster)
- [Подключение к Kubernetes](/ru/features/cluster-auth)
- [Дашборд кластера](/ru/features/dashboard)
- [Ресурсы и YAML](/ru/features/resources)
- [Работа с ресурсами](/ru/features/working-with-resources)
- [Повседневные операции](/ru/features/operations)
- [Debug-контейнеры](/ru/features/debug-containers)
- [Стек метрик и графики](/ru/features/metrics)
- [Helm](/ru/features/helm)
- [GitOps с Argo CD и Flux CD](/ru/features/gitops)
- [Команды и совместный доступ](/ru/features/teams)
- [Безопасность и управление доступом](/ru/features/security)
- [Пользователи и журнал аудита](/ru/features/administration)
- [Настройки и настройки кластера](/ru/features/settings)
- [Версия и обновления](/ru/features/updates)
- [Сообщить о проблеме](/ru/features/bug-reports)