# GitOps с Argo CD

Kubron включает интеграцию с Argo CD в рабочее пространство Kubernetes. Ресурсы Argo CD обнаруживаются через Kubernetes API; отдельный UI-компонент Kubron в кластере устанавливать не нужно. Сам Argo CD должен быть установлен, а его CRD — доступны. Flux CD тоже поддерживается: см. раздел [Flux CD](#flux-cd) ниже.

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

Руководства: [English PDF](https://github.com/24DSO-lab/kubron/blob/master/docs/DOCS/EN/Kubron-ArgoCD-User-Guide-EN.pdf) · [Русский PDF](https://github.com/24DSO-lab/kubron/blob/master/docs/DOCS/RU/Kubron-ArgoCD-User-Guide.pdf)


## Flux CD {#flux-cd}

Kubron поддерживает и Flux CD. Как и Argo CD, он обнаруживается через Kubernetes API: ничего дополнительного в кластер для Kubron устанавливать не нужно, а сам Flux должен быть уже запущен. Раздел Flux появляется в кластере, если есть контроллер Kustomize или Helm. Вкладки контроллеров, которых нет (source, notification, image automation), скрываются: Flux можно установить частично.

### Что можно просматривать

- Kustomizations и HelmReleases: вычисленное состояние (Ready, Reconciling, Failed, Stalled или Suspended), interval, источник и зависимости, применённую и последнюю проверяемую ревизию, время последнего reconcile, conditions и сообщение контроллера.
- Ресурсы, которыми управляет Kustomization или HelmRelease (inventory), и цепочку «источник → Kustomization или HelmRelease → ресурсы».
- Чарт HelmRelease, имя релиза и историю релиза.
- Источники: GitRepository, OCIRepository, HelmRepository, HelmChart и Bucket.
- Notification (Alert, Provider, Receiver) и image automation (ImageRepository, ImagePolicy, ImageUpdateAutomation) — только чтение.
- Дашборд: число объектов по состояниям, проблемные объекты, разрез по namespace и источнику, последние reconcile.
- Релизы Helm, которыми управляет `HelmRelease`, помечаются в списке и деталях Helm как **Managed by Flux** (см. [Helm](/ru/features/helm#releases-managed-by-flux)).

Вид обновляется сам при изменении объектов Flux. Значения, в которых могут быть секреты, не показываются никогда: `spec.values` и `valuesFrom` у HelmRelease, подстановки `postBuild` у Kustomization, содержимое `kubeConfig` и `decryption`, учётные данные в URL и пути вебхуков у Receiver.

### Доступные действия

- Reconcile одного или нескольких объектов, при необходимости вместе с источником.
- Suspend и resume.
- Удаление Kustomization или HelmRelease, по одному объекту.

Reconcile, suspend и resume работают для Kustomizations, HelmReleases, источников, ImageRepository и ImageUpdateAutomation. Объекты Notification и политики образов доступны только для чтения. Результат массовых действий сообщается отдельно для каждого объекта.

Удаление просит ввести имя объекта и заранее показывает последствия. Kustomization с `prune: true` удаляет управляемые ресурсы (диалог показывает их число); при `prune: false` ресурсы остаются в кластере без управления. Удаление HelmRelease удаляет (uninstall) Helm-релиз и его ресурсы. Kubron не может отменить удаление: восстановите объект из Git.

### Доступ и разрешения

Reconcile требует права Kubron `operate`; suspend, resume и удаление — `write`. Дополнительно они проверяются Kubernetes RBAC. Профили ServiceAccount in-cluster режима Kubron не содержат правил Flux, поэтому выдайте их через `rbac.extraRules`; готовый фрагмент есть в [Развёртывании в Docker и Kubernetes](/ru/server/deploy#rights-for-flux-cd). Без правил `patch` и `delete` раздел Flux на практике остаётся только для чтения.

### Ограничения

- Нет diff и отката. Откат во Flux — это изменение в Git (например, `git revert`); Kubron его не имитирует.
- Нужен Flux 2.3 или новее (первый релиз со стабильным API `HelmRelease` v2). Если обслуживаются только старые версии API, Kubron подскажет, как обновиться или выполнить миграцию, и не разбирает объекты.
- Вкладка ресурсов у HelmRelease требует Flux 2.8 или новее: с этой версии записывается inventory. На более старых версиях вкладка недоступна.
- Ресурсы, управляемые через `spec.kubeConfig`, находятся в другом кластере: они показываются с пометкой «remote» и локально не разрешаются.
- Ресурсы Flux Operator (`FluxInstance`, `ResourceSet`) не поддерживаются; Kubron не открывает веб-интерфейс Flux и не вызывает CLI `flux`.
- Создание и правка объектов Flux идут через общий YAML-редактор. Если `spec.suspend` задан в Git, родительская Kustomization вернёт значение после ручной приостановки или возобновления.
