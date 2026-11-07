# GitOps с Argo CD

Kubron включает интеграцию с Argo CD в рабочее пространство Kubernetes. Ресурсы Argo CD обнаруживаются через Kubernetes API; отдельный UI-компонент Kubron в кластере устанавливать не нужно. Сам Argo CD должен быть установлен, а его CRD — доступны.

## Что можно просматривать

- Applications, ApplicationSets и Projects.
- Статусы sync и health, развёрнутую revision, destination и время последней синхронизации.
- Граф ресурсов приложения.
- Сравнение live и desired состояния Application.
- Роли и информацию о доступе проекта, предоставляемую Argo CD.

## Доступные действия

- Sync одного или нескольких Applications с параметрами revision, prune, force и dry-run, где они применимы.
- Refresh и hard refresh состояния приложения.
- Остановка выполняющейся операции.
- Откат к предыдущей ревизии.
- Приостановка и возобновление автоматической синхронизации.
- Удаление Application с каскадным удалением или без него.

Результат массовых действий сообщается отдельно для каждого Application: ошибка одного элемента не скрывает результаты остальных.

## Доступ и разрешения

Sync, refresh, прерывание операции и откат требуют права Kubron `operate`. Приостановка и возобновление auto-sync и удаление Application требуют `write`. Все они дополнительно проверяются Kubernetes RBAC; см. [справочник прав](/ru/features/security#справочник-прав). Изменения, сделанные через Kubron, отправляются в ресурсы Argo CD в кластере. Kubron не заменяет собственный reconciliation engine и project policies Argo CD.

В настройках кластера можно указать опубликованный URL Argo CD. Если он задан, Kubron открывает этот адрес напрямую; иначе интерфейс может использовать настроенный port-forward, если он доступен.

Руководства: [English PDF](https://github.com/24DSO-labs/kubron/blob/master/docs/DOCS/EN/Kubron-ArgoCD-User-Guide-EN.pdf) · [Русский PDF](https://github.com/24DSO-labs/kubron/blob/master/docs/DOCS/RU/Kubron-ArgoCD-User-Guide.pdf)