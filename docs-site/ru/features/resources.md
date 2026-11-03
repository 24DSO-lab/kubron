# Ресурсы Kubernetes и YAML-редактор

Kubron предоставляет каталог стандартных объектов Kubernetes и автоматически обнаруживаемых custom resources. В одном сценарии объединены списки, подробности объектов и контролируемое редактирование.

## Встроенные типы ресурсов

Представления охватывают основные группы Kubernetes:

- **Workloads:** Pods, Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs и CronJobs.
- **Сеть:** Services, Endpoints, EndpointSlices, Ingresses, IngressClasses и NetworkPolicies.
- **Конфигурация:** ConfigMaps, Secrets и Events.
- **Хранилище:** PersistentVolumes, PersistentVolumeClaims и StorageClasses.
- **Управление доступом:** ServiceAccounts, Roles, RoleBindings, ClusterRoles и ClusterRoleBindings.
- **Планирование и политики:** HPAs, PodDisruptionBudgets, ResourceQuotas, LimitRanges, PriorityClasses, RuntimeClasses и Leases.
- **Admission:** MutatingWebhookConfigurations и ValidatingWebhookConfigurations.

Фактический список зависит от ресурсов Kubernetes API и разрешений подключённой учётной записи.

## Custom resources

Kubron обнаруживает CustomResourceDefinitions активного кластера. Для поддерживаемых CRD читаются предпочтительная обслуживаемая версия, scope, short names и `additionalPrinterColumns`. Это позволяет показывать custom resources в таблицах по аналогии с `kubectl get`.

## Подробности объекта

Панели деталей объединяют conditions, events, owner references, containers, volumes, affinity, tolerations, labels и annotations — в зависимости от типа ресурса.

## YAML-редактор

Редактор Monaco позволяет просматривать и изменять YAML ресурса. Перед применением изменений можно проверить сформированный diff. Также можно создать новый объект из manifest. Удаление ресурсов и применение YAML требуют соответствующего разрешения на запись.

## Массовые действия

Некоторые представления позволяют выбрать несколько объектов и выполнить для них одно действие. Результат сообщается отдельно для каждого объекта: ошибка одного не скрывает результат остальных операций.

## Отдельная защита Secrets

Возможность видеть объект Secret не означает автоматический доступ к его значениям. Чтение данных Secret требует отдельного разрешения Kubron `secrets.read` и соответствующего разрешения Kubernetes. У встроенной роли `user` такого права нет.

[Роли и разрешения](/ru/features/security) · [Повседневные операции](/ru/features/operations)