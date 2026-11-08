# Поддержка Flux CD: промпты для задач

Дополнение к [flux-support.md](flux-support.md): готовые тексты задач для программистов и агентов. Задача выдаётся как
**«Общая часть» + промпт своей задачи**. Номера, зависимости, владение файлами и критерии приёмки — в `flux-support.md`;
в промптах они повторены в той мере, в какой исполнителю нужно действовать без вопросов.

Промпты для задач, помеченных «человек» (0.1, 0.2, B.7, B.9, Q.1, Q.4), написаны в том же виде: их можно отдать
и агенту, но тогда ревью обязательно и плотное.

## Общая часть (в начало каждого промпта)

```text
Ты выполняешь одну задачу в проекте Kubron (Wails + Vue 3 + Go; https://github.com/24DSO-lab/kubron, ветка refactor).
Проект добавляет поддержку Flux CD рядом с Argo CD. Перед началом прочитай docs/adr/0023-flux-support.md и
docs/tickets/flux-support.md (разделы «Сквозные правила», «Контракт», «Владение файлами» и строку своей задачи).
Если указание ниже расходится с ADR-0023, прав ADR: опиши расхождение в отчёте и не придумывай своё решение.

ПОДГОТОВКА
  git clone https://github.com/24DSO-lab/kubron && cd kubron
  git checkout -b feat/flux-<номер>-<slug> origin/refactor
  mkdir -p ~/goroot && tar -C ~/goroot -xzf .test/go1.27.3.linux-amd64.tar.gz
  export PATH=~/goroot/go/bin:$PATH GOFLAGS="-mod=vendor -tags=webkit2_41"
  tar -xzf .test/vendor.tar.gz && echo vendor/ >> .git/info/exclude    # vendor не коммитить
  Для UI-задач: cd frontend && npm ci. Привязки wailsjs генерируются из Go (wails generate module, версия wails
  как в go.mod) и в репозитории не хранятся; без них vue-tsc не пройдёт.

ПРАВИЛА
1. Меняй только файлы своей задачи (список ниже). Перед сдачей: git diff --name-only origin/refactor...HEAD —
   каждый путь должен входить в список. Новый файл допустим только внутри каталога/префикса, которым ты владеешь.
2. Контракт заморожен: flux_types.go, flux_errors.go, flux_common.go, flux_kinds.go, app_flux.go, types/flux.ts и все
   общие файлы только читай. Нужна новая ошибка, поле DTO, пункт меню или ключ nav/audit/backend — остановись и
   опиши, что именно нужно; сам общий файл не правь.
3. Образец — соседний код Argo CD (указан в задаче). Повторяй его стиль и структуру. Комментарии на русском, как в
   репозитории; тексты ошибок Go английские (шаблоны в backend.json заведены в 0.2).
4. Во всех новых .go, .ts, .vue — заголовок лицензии: // Copyright 2026 The Kubron Authors и // SPDX-License-Identifier:
   Apache-2.0 (в .vue — HTML-комментарии). В .md и .json заголовка нет.
5. Интерфейс только через i18n: ключи в locales/{en,ru,cn}/<файл>.json с одинаковыми плейсхолдерами; t() не вызывается
   на верхнем уровне модуля; кириллицы вне locales/ нет (кроме комментариев); имена объектов Kubernetes, поля YAML и
   статусы API не переводятся.
6. Секреты: ничего чувствительного в DTO, событиях, логах, ошибках (ADR-0023, раздел 4). Для всего, что читает
   данные кластера, есть тест с контрольными строками вида S3CRET-..., проверяющий json.Marshal ответа.
7. Новых зависимостей нет: go.mod, go.sum, package.json, vendor не менять.
8. Современный код: Go 1.27 (generics, slices и maps, errors.Join, context, t.Context(), testing/synctest, log/slog);
   Vue 3 с <script setup lang="ts"> и composables. Без устаревших приёмов, без глобального состояния сверх образца.
9. Порядок: сначала тесты по критериям приёмки, затем код. Парсеры защитные: отсутствующее или неожиданное поле даёт
   нулевое значение, а не панику.
10. Не расширяй задачу: если замечаешь проблему вне своих файлов, запиши в отчёт.

ПРОВЕРКИ ПЕРЕД СДАЧЕЙ (все должны быть зелёными)
  gofmt -l .                      # пусто
  go vet ./... && go test ./internal/...
  go test -race ./internal/k8s/...       # если трогал k8s-код
  cd frontend && npm test && npm run i18n:check && npx vue-tsc --noEmit
  python3 scripts/license-headers.py --fix   # затем проверь, что изменились только твои файлы

КОММИТ И РЕЗУЛЬТАТ
  git add <только файлы задачи>
  git -c user.name=Vadim -c user.email=vmilyash@mts.ru commit -s --author="Vadim <vmilyash@mts.ru>" -m "feat(flux): <суть>"
  Без Co-Authored-By и Claude-Session. git push не выполняй: прав нет.
  git format-patch origin/refactor..HEAD -o ../patches     # имя патча начинается с номера задачи

ОТЧЁТ (в конце ответа)
  1) список изменённых файлов; 2) по каждому критерию приёмки: сделано или нет и чем доказано; 3) команды проверок и
  их результат; 4) что не сделано и вопросы; 5) расхождения с ADR или контрактом.
```

## Фаза 0

### 0.1 Обобщённый informer-кэш и общий граф

```text
ЗАДАЧА 0.1 — Обобщённый informer-кэш и общий построитель графа. Ветка: feat/flux-0-1-crdcache.
Цель: подготовить базу для Flux без изменения поведения Argo CD.

ЧИТАЙ: internal/k8s/argocd_cache.go и argocd_cache_test.go; client.go (поля argo*, Close); argocd_graph.go и
argocd_graph_test.go; ADR-0023, разделы 5 и 7.
МОЖНО МЕНЯТЬ: argocd_cache.go, argocd_cache_test.go (только перенос тестов debounce на testing/synctest),
argocd_graph.go, client.go; создать crdcache.go, graphbuild.go и тесты к ним.

СДЕЛАТЬ
1. crdcache.go: crdCache[T any] — dynamic shared informer по GVR, SetTransform (managedFields удаляются всегда;
   плюс точка расширения для дополнительной очистки объекта), debounce, onChange, list(scope) ([]T, bool) с фильтром
   namespace до разбора, stop(). Кэш Argo — экземпляр с T = ArgoApplicationInfo; константа EventArgoChanged и
   сортировка остаются в Argo-коде.
2. Реестр кэшей в ClusterConnection: ключ — GVR; на ключ свой retryAt; один mutex; метод получения лениво запускает
   informer после пробы права list (как сейчас: List с Limit 1, при отказе повтор не раньше чем через 30 с);
   Close останавливает все кэши. После этой задачи client.go больше никто не правит: Flux (задача B.7) подключается
   через реестр.
3. graphbuild.go: вынести из argocd_graph.go построение графа над списком ссылок на ресурсы (владение по
   ownerReferences, Service -> Pod, Ingress -> Service) в функцию с нейтральными типами пакета k8s.
   GetArgoApplicationGraph строит ссылки из status.resources и вызывает общую функцию; JSON Argo-графа не меняется.
4. Тесты debounce в argocd_cache_test.go перевести на testing/synctest, без time.Sleep.

КРИТЕРИИ ПРИЁМКИ
- argocd_*_test.go проходят без изменения ожиданий, golden-тесты графа тоже;
- go test -race ./internal/k8s/... зелёный;
- в описании PR перечислено, какие внутренние сигнатуры изменились;
- поведение Argo не изменилось (если есть kind, проверь список, граф и событие argocd:changed вручную).
НЕ ДЕЛАТЬ: ничего Flux-специфичного кроме точек расширения; не менять DTO и события Argo.
```

### 0.2 Контракт и каркас

```text
ЗАДАЧА 0.2 — Контракт и каркас. Ветка: feat/flux-0-2-contract.
Цель: один раз завести все общие файлы и заглушки; после слияния остальные задачи работают без пересечений.
Это единственная задача, которая правит общие файлы.

ЧИТАЙ: ADR-0023 целиком; docs/tickets/flux-support.md (разделы «Общие файлы», «Контракт», «Владение файлами»).
Образцы: internal/app/app_gitops.go, internal/k8s/argocd_types.go, internal/server/methods.go,
internal/auth/policy_test.go (exactPermission), internal/server/contract_test.go (TestEveryAppMethodIsClassified);
frontend: resources/navigation.ts, resources/types.ts, resources/registry.ts, components/Sidebar.vue,
components/resource/ResourceHost.vue, composables/useArgo.ts, useArgoLive.ts, useClusterConnection.ts,
useViewRegistry.ts, App.vue, scripts/i18n-check.mjs.
МОЖНО МЕНЯТЬ: всё из таблицы «Общие файлы» и создать все заглушки из таблицы «Владение файлами» (строка 0.2).

GO
1. flux_types.go: DTO и FluxRef, FluxActionResult, FluxState (Suspended, Stalled, Failed, Reconciling, Ready, Unknown),
   FluxStatus (installed, state ok или unsupportedVersion, контроллеры kustomize/helm/source/notification/image,
   обслуживаемые версии по видам), FluxNotifications{Providers, Alerts, Receivers},
   FluxImageAutomation{Repositories, Policies, Automations}, константа EventFluxChanged = "flux:changed".
   FluxGraph: Available, Remote, Nodes []FluxGraphNode{ID, Kind, Group, Version, Namespace, Name, Role
   (source, flux, resource), State}, Edges []FluxGraphEdge{From, To, Type (owns, routes, sourceRef, dependsOn, inventory)}.
   Поля DTO по ADR-0023, раздел 4; JSON camelCase; в комментарии к каждому типу — что наружу не отдаётся.
   Образец оформления: argocd_types.go.
2. flux_kinds.go: таблица видов (Kind, группа, plural, namespaced, допустимые версии по порядку предпочтения
   из ADR раздел 3, какие действия разрешены: reconcile, suspend, delete).
3. flux_errors.go: все ошибки Flux, которые понадобятся задачам (вид не поддерживается для действия, пустой список,
   больше 200 объектов, версия API не поддерживается, объект удаляется, объект приостановлен, не реализовано).
   Для каждой — шаблон в backend.json en, ru, cn.
4. flux_common.go (+ тест): FluxStateOf(conditions, suspended) по приоритету Suspended > Stalled > Failed
   (Ready=False и не Reconciling) > Reconciling > Ready > Unknown; разбор conditions в []ConditionInfo; очистка URL от
   userinfo (stripURLUserinfo); redactFluxMessage (общий редактор секретов, ADR-0022). Табличные тесты на все ветки.
5. flux_list.go: fluxList[T any](m *Manager, kind fluxKind, namespaces []string, parse func(*unstructured.Unstructured) T,
   nsOf func(T) string) ([]T, error) — только через API (scopedListTimeout, parseScope), без кэша.
   GVR строится из flux_kinds.go и версий из fluxServedVersions.
6. Заглушки Manager-методов, каждая в своём файле: flux_status.go (GetFluxStatus даёт installed=false;
   fluxServedVersions даёт первую допустимую версию), flux_kustomization.go, flux_helmrelease.go, flux_sources.go,
   flux_notification.go, flux_image.go (чтение возвращает пустой результат), flux_actions.go, flux_delete.go
   (возвращают errFluxNotImplemented), flux_graph.go (Available=false).
7. internal/app/app_flux.go: все методы из таблицы «Контракт»; каждый начинается с a.authz(право, ...) и делегирует
   в a.k8sMgr. Аргументы аудита — компактный список целей (образец argoAuditApps); DeleteFlux: вид, namespace, имя
   (prune в аргументы не попадает: authz вызывается первым; значение prune пишет в журнал процесса B.9).
8. methods.go: все методы в allowedMethods. policy_test.go: GetFlux* = PermRead, ReconcileFlux = PermOperate,
   SuspendFlux, ResumeFlux, DeleteFlux = PermWrite. TestEveryAppMethodIsClassified и политики зелёные.

UI
9. types/flux.ts — зеркало Go DTO, имена полей = JSON.
10. navigation.ts: группы flux, flux-sources, flux-notifications, flux-images (requires: 'flux'; подгруппы свёрнуты,
    parent: 'flux'); тип requires расширен до 'argocd' | 'flux'; resources/types.ts: NavGroupId; в NAV_ORDER 'flux'
    после 'argocd'; Sidebar.vue: набор available включает 'flux' при fluxInstalled, а подгруппы — по контроллерам
    из fluxStatus.
11. Заглушки (с лицензионным заголовком, с экспортами нужной формы, без логики): resources/flux.ts,
    resources/fluxActions.ts (экспорты: pendingFluxDelete, closeFluxDelete, requestFluxDelete(obj),
    fluxActionTick, reconcileFlux(items, { withSource }), setFluxSuspended(items, suspended)), resources/fluxGraph.ts,
    composables/useFlux.ts (fluxInstalled=false, fluxStatus, refreshFlux, resetFlux), composables/useFluxLive.ts
    (fluxLiveTick, startFluxLive, stopFluxLive), definitions/flux/*.ts (по одному defineResource на вид, id и view
    с префиксом flux-, зарегистрированы в registry.ts в порядке меню), components/resource/sections/Flux*.vue
    (StateBadge, ConditionsSection, InventoryTab, SourceRefSection, HistoryTab, GraphTab),
    components/views/FluxDashboardView.vue (в useViewRegistry.ts как 'flux-dashboard', ленивая загрузка),
    components/panels/FluxDeleteDialog.vue.
12. ResourceHost.vue: рисует FluxDeleteDialog, когда задан pendingFluxDelete. useClusterConnection.ts: refreshFlux и
    resetFlux рядом с вызовами Argo. App.vue: пропсы для 'flux-dashboard' по образцу 'argo-dashboard'.
13. locales/{en,ru,cn}/nav.json: заголовки групп и пунктов; audit.json: audit.actions для всех RPC-методов (образец —
    записи Argo); scripts/i18n-check.mjs: допустимые литералы Flux, Flux CD, Kustomization, HelmRelease,
    GitRepository, OCIRepository, HelmRepository, HelmChart, Bucket, Alert, Provider, Receiver, ImageRepository,
    ImagePolicy, ImageUpdateAutomation.

КРИТЕРИИ ПРИЁМКИ
- приложение работает как раньше, меню Flux не видно;
- go vet, go test, TestEveryAppMethodIsClassified, policy_test, i18n:check, npm test, vue-tsc зелёные;
- каждый файл из таблицы «Владение файлами» существует;
- в описании PR: список заглушек и кто какой файл заполняет.
```

## Фаза B. Бэкенд

### B.1 Обнаружение и FluxStatus

```text
ЗАДАЧА B.1 — Обнаружение и FluxStatus. Ветка: feat/flux-b1-status.
ЧИТАЙ: ADR-0023, раздел 3; internal/k8s/argocd.go (argoInstalled); flux_types.go, flux_kinds.go.
МОЖНО МЕНЯТЬ: flux_status.go, flux_status_test.go.

СДЕЛАТЬ: заменить заглушки GetFluxStatus и fluxServedVersions.
1. Один вызов discovery на подключение (выбери ServerGroupsAndResources или ServerPreferredResources так, чтобы
   частичная ошибка группы — ErrGroupDiscoveryFailed — считалась частичным успехом). Кэш на подключение с коротким TTL
   (около 30 с), потокобезопасный.
2. FluxStatus: installed — есть контроллер kustomize или helm; контроллеры kustomize, helm, source, notification,
   image; по каждому виду — обслуживаемая версия из allowlist в порядке предпочтения. state=unsupportedVersion, если CRD
   есть, но версии из allowlist нет (например, только v1beta2). Нет CRD, нет прав или кластер недоступен — installed=false
   без ошибки.
3. fluxServedVersions(conn) отдаёт карту вид -> версия из того же кэша.

ТЕСТЫ (k8s.io/client-go/discovery/fake): полная установка 2.8; частичная (--components); только устаревшие версии;
нет CRD; ошибка discovery; Alert только v1beta3; image-виды в v1beta2.
КРИТЕРИИ: ADR-0023, раздел 3, выполнен; кроме discovery обращений к API нет.
```

### B.2 Kustomization: чтение

```text
ЗАДАЧА B.2 — Kustomization: чтение. Ветка: feat/flux-b2-kustomization. Ревью человеком обязательно (секреты).
ЧИТАЙ: ADR-0023, разделы 2 и 4; образцы: argocd_apps.go (parseArgoApplication, хелперы strAt, boolAt, sliceAt,
mapAt, orDefault, stripURLUserinfo, argoConditions), argocd_settings_test.go (TestArgoSettingsNoSecretLeak и список
маркеров); flux_types.go, flux_common.go, flux_list.go.
МОЖНО МЕНЯТЬ: flux_kustomization.go, flux_kustomization_test.go, testdata/flux/kustomization/.

СДЕЛАТЬ: GetFluxKustomizations(namespaces) -> []FluxKustomizationInfo через fluxList.
1. parseFluxKustomization(*unstructured.Unstructured): метаданные, interval, suspend, sourceRef (namespace по умолчанию —
   namespace объекта), dependsOn, path, prune, wait, targetNamespace, serviceAccountName, Remote (задан spec.kubeConfig),
   Revision (status.lastAppliedRevision), LastAttemptedRevision, LastReconcileAt (выбери между
   status.lastHandledReconcileAt и временем Ready-condition, обоснуй в комментарии), число записей
   status.inventory.entries, conditions и State через FluxStateOf; Message через redactFluxMessage.
2. НЕ отдавать: значения spec.postBuild.substitute и содержимое substituteFrom, секреты kubeConfig и decryption
   (допустимо имя), аннотацию kubectl.kubernetes.io/last-applied-configuration (исключить из отдаваемых annotations),
   userinfo в URL.
3. Фикстуры настоящих объектов Flux 2.3 и 2.8 в testdata/flux/kustomization/ (ready, reconciling, failed, stalled,
   suspended, remote, с dependsOn); тесты читают YAML.

ТЕСТЫ: таблица по фикстурам; TestFluxKustomizationNoSecretLeak по образцу Argo (маркеры S3CRET-... во всех запрещённых
полях, ни одного в json.Marshal ответа); мусор в status не приводит к панике.
КРИТЕРИИ: State верен на всех фикстурах; тест утечек действительно падает, если временно вернуть запрещённое поле
(проверь и напиши в отчёте).
```

### B.3 HelmRelease: чтение

```text
ЗАДАЧА B.3 — HelmRelease: чтение. Ветка: feat/flux-b3-helmrelease. Ревью человеком обязательно (секреты).
ЧИТАЙ: ADR-0023, разделы 2 и 4; те же образцы, что в B.2; документацию API HelmRelease v2 (fluxcd.io).
МОЖНО МЕНЯТЬ: flux_helmrelease.go, flux_helmrelease_test.go, testdata/flux/helmrelease/.

СДЕЛАТЬ: GetFluxHelmReleases(namespaces) -> []FluxHelmReleaseInfo через fluxList.
1. parseFluxHelmRelease: общая часть как у Kustomization; чарт — из spec.chart.spec (chart, version, sourceRef) или
   spec.chartRef; имя релиза: из status.history[0].name, если есть, иначе по правилу releaseName из документации API
   (с targetNamespace имя по умолчанию <targetNamespace>-<name>; сверь с документацией и поясни в комментарии);
   TargetNamespace, StorageNamespace; Remote (spec.kubeConfig); lastReleaseRevision.
2. History[] из status.history: версия релиза, chartName, chartVersion, appVersion, status, firstDeployed,
   lastDeployed (новые первыми). Digest-поля не нужны.
3. status.inventory (есть с Flux 2.8) — только число записей и флаг наличия; без него флаг false.
4. НЕ отдавать: spec.values, содержимое valuesFrom (имена ссылок допустимы, сами Secret и ConfigMap не читаются),
   секреты kubeConfig, last-applied-configuration, userinfo в URL.
5. Фикстуры Flux 2.3 и 2.8 (с inventory и без; chartRef; с kubeConfig; приостановленный).

ТЕСТЫ: таблица по фикстурам; TestFluxHelmReleaseNoSecretLeak (маркеры в spec.values, valuesFrom, kubeConfig, URL);
мусор в status не приводит к панике.
КРИТЕРИИ: имя релиза и StorageNamespace пригодны для сопоставления с Helm-релизом (на них опирается задача X.1).
```

### B.4 Источники: чтение

```text
ЗАДАЧА B.4 — Источники: чтение. Ветка: feat/flux-b4-sources.
ЧИТАЙ: ADR-0023, разделы 3 и 4; flux_kinds.go; образец списка — argocd_apps.go.
МОЖНО МЕНЯТЬ: flux_sources.go, flux_sources_test.go, testdata/flux/source/.

СДЕЛАТЬ: GetFluxSources(namespaces) -> []FluxSourceInfo для GitRepository, OCIRepository, HelmRepository, HelmChart, Bucket.
1. Один DTO с полем Kind: URL (без userinfo; у Bucket — endpoint и bucketName), ссылка на ревизию (spec.ref:
   ветка, тег, semver, commit, digest; у HelmChart — chart и version), тип HelmRepository (default или oci),
   имя secretRef (только имя), suspend, interval, status.artifact (revision, digest, lastUpdateTime), conditions,
   State через FluxStateOf.
2. Версия каждого вида — по allowlist и fluxServedVersions; вид без CRD или без прав пропускается без ошибки.
   Ошибка одного вида не отменяет остальные; ошибка возвращается, только если не удалось прочитать ни один вид.
3. НЕ отдавать: userinfo, содержимое Secret, spec.ignore и прочие шаблоны, которые не нужны UI.

ТЕСТЫ: по фикстуре на вид; частичный отказ (один вид недоступен); TestFluxSourcesNoSecretLeak (userinfo в URL, secretRef).
```

### B.5 Notification: чтение

```text
ЗАДАЧА B.5 — Notification: чтение. Ветка: feat/flux-b5-notification. Ревью человеком обязательно.
ЧИТАЙ: ADR-0023, разделы 3 и 4; flux_kinds.go.
МОЖНО МЕНЯТЬ: flux_notification.go, flux_notification_test.go, testdata/flux/notification/.

СДЕЛАТЬ: GetFluxNotifications(namespaces) -> *FluxNotifications.
1. Provider: type, channel, username, адрес ТОЛЬКО как схема и хост (путь, query и userinfo отбрасываются: в адресах
   вебхуков бывают токены), имя secretRef, suspend, State.
2. Alert: providerRef, eventSeverity, eventSources (kind, name, namespace, matchLabels), suspend, State.
3. Receiver: type, resources (kind, name, namespace), events, имя secretRef, suspend, State.
   status.webhookPath и любые производные от него значения НЕ отдавать: путь сам служит секретом.
4. Версии: Alert и Provider — v1beta3, Receiver — v1 или v1beta2 по allowlist. Вид без CRD пропускается, как в B.4.

ТЕСТЫ: фикстуры на вид; TestFluxNotificationNoSecretLeak: токен в пути адреса Provider, userinfo, webhookPath
Receiver с маркером; ни один маркер не попадает в json.Marshal ответа.
```

### B.6 Image automation: чтение

```text
ЗАДАЧА B.6 — Image automation: чтение. Ветка: feat/flux-b6-image.
ЧИТАЙ: ADR-0023, разделы 3 и 4; flux_kinds.go.
МОЖНО МЕНЯТЬ: flux_image.go, flux_image_test.go, testdata/flux/image/.

СДЕЛАТЬ: GetFluxImageAutomation(namespaces) -> *FluxImageAutomation.
1. ImageRepository: image (без userinfo), interval, suspend, status.lastScanResult (tagCount, scanTime), State.
2. ImagePolicy: тип правила (semver, alphabetical, numerical) и его параметр, imageRepositoryRef, LatestImage из
   status.latestRef (name:tag, digest при наличии), State.
3. ImageUpdateAutomation: sourceRef, ветка checkout и push, путь и стратегия обновления, status.lastAutomationRunTime,
   lastPushCommit, lastPushTime, State. Адреса электронной почты автора и шаблоны сообщений не отдавать.
4. Версии v1 или v1beta2 по allowlist. Обращений к реестрам образов нет: только объекты кластера.

ТЕСТЫ: фикстуры на вид (v1 и v1beta2); частичный отказ; TestFluxImageNoSecretLeak (userinfo в image).
```

### B.7 Informer-кэш Flux и flux:changed

```text
ЗАДАЧА B.7 — Informer-кэш Flux и событие flux:changed. Ветка: feat/flux-b7-cache. Исполнитель: человек.
ЧИТАЙ: ADR-0023, раздел 5; crdcache.go и реестр из 0.1; argocd_cache.go; ADR-0009 (адресные события);
flux_list.go (заглушка из 0.2).
МОЖНО МЕНЯТЬ: flux_list.go, flux_list_test.go, flux_cache.go, flux_cache_test.go. client.go НЕ менять.

СДЕЛАТЬ
1. fluxList сначала смотрит в кэш подключения (реестр из 0.1): если informer синхронизирован — список из памяти,
   иначе чтение через API (как GetArgoApplications). Сигнатуру fluxList и поведение для вызывающих B.2-B.6 не менять.
2. Informer на Kustomization и HelmRelease стартует лениво при первом чтении; источники, Notification и Image — при
   первом обращении к соответствующему виду. Проба права list, повтор через 30 с, остановка в Close — через реестр.
3. Событие EventFluxChanged ("flux:changed"), адресованное подключению (m.emitCluster), debounce 1 с.
4. SetTransform: managedFields всегда; у HelmRelease дополнительно удаляется spec.values — значения не должны лежать в
   памяти процесса.
5. Замер памяти на 2000 объектов (benchmark или тест с runtime.MemStats): результат в описание PR.

ТЕСТЫ: testing/synctest для debounce; go test -race; у HelmRelease со spec.values в кэше значений нет; отказ пробы права
даёт чтение через API; остановка на Close; чужое подключение не получает событие.
КРИТЕРИИ: ADR-0023, раздел 5; B.2-B.6 работают без правок.
```

### B.8 Действия: reconcile, suspend, resume

```text
ЗАДАЧА B.8 — Действия reconcile, suspend, resume. Ветка: feat/flux-b8-actions. Ревью человеком обязательно.
ЧИТАЙ: ADR-0023, раздел 6; образец: argocd_actions.go (forEachArgoApp, normalizeArgoRefs, argoMaxBulk,
argoActionTimeout) и argocd_actions_test.go (newArgoDyn на dynamicfake); flux_kinds.go (какие действия разрешены
для вида), flux_errors.go. Сверь поведение flux CLI по исходникам cmd/flux/reconcile_kustomization.go,
reconcile_helmrelease.go, suspend.go, resume.go в github.com/fluxcd/flux2 (raw.githubusercontent.com).
МОЖНО МЕНЯТЬ: flux_actions.go, flux_actions_test.go.

СДЕЛАТЬ: ReconcileFlux(refs, withSource), SuspendFlux(refs), ResumeFlux(refs) -> []FluxActionResult.
1. normalizeFluxRefs: непусто, не больше 200, обрезка пробелов, дедупликация; вид должен входить в allowlist
   ДЕЙСТВИЯ (reconcile: Kustomization, HelmRelease, источники, ImageRepository, ImageUpdateAutomation; suspend и
   resume — те же виды; остальные только чтение). Произвольный GVR от клиента не принимается.
2. Только merge-patch (types.MergePatchType), FieldManager "kubron", без get-update:
   reconcile — metadata.annotations["reconcile.fluxcd.io/requestedAt"] = время RFC3339Nano;
   suspend — spec.suspend=true; resume — spec.suspend=false и тот же requestedAt (как flux resume).
3. withSource: сначала патчится источник из spec.sourceRef (namespace по умолчанию — объекта; для HelmRelease набор
   объектов сверь с flux CLI и опиши в PR) с тем же временем, затем сам объект. Сбой источника отражается в результате
   этого объекта, остальные не отменяются.
4. Таймаут 60 с (m.baseCtx()), результат по каждому объекту, одна ошибка не отменяет остальные; NotFound — понятная
   ошибка; reconcile приостановленного объекта — поведение как у CLI (см. flux_errors.go; нужной ошибки нет — остановись и
   запроси).
5. Секретов в аргументах и результатах нет.

ТЕСТЫ (fake dynamic client): точное тело патча и тип патча каждого действия; FieldManager "kubron" (через reactor);
пакет из нескольких объектов с одним NotFound; вид вне allowlist; больше 200; дубликаты; resume ставит requestedAt;
withSource порядок и сбой источника.
```

### B.9 Удаление

```text
ЗАДАЧА B.9 — Удаление. Ветка: feat/flux-b9-delete. Исполнитель: человек.
ЧИТАЙ: ADR-0023, раздел 6 (абзац Delete); образец: DeleteArgoApplication в argocd_actions.go; flux_errors.go.
МОЖНО МЕНЯТЬ: flux_delete.go, flux_delete_test.go.

СДЕЛАТЬ: DeleteFlux(ref FluxRef) error.
1. Только Kustomization и HelmRelease, один объект; namespace и имя обрезаются и непусты; вид вне allowlist отклоняется.
2. Обычный DELETE через dynamic client, без собственных propagation-опций. NotFound — понятная ошибка; объект уже
   удаляется (deletionTimestamp) — ошибка из flux_errors.go.
3. Перед удалением прочитай объект и запиши в журнал процесса (slog.Info): вид, namespace, имя, значение prune.
   Этот журнал заменяет prune в аргументах audit: authz по правилу policy_test вызывается первым, до чтения.
   Секретов в записи нет.

ТЕСТЫ: чужой вид и пустые поля отклоняются; ровно один вызов delete; опций каскада нет; NotFound и «удаляется»;
запись slog содержит prune.
КРИТЕРИИ: никакого bulk-удаления и режимов «оставить ресурсы» (вне объёма ADR-0023).
```

### B.10 Граф и цепочка

```text
ЗАДАЧА B.10 — Граф и цепочка. Ветка: feat/flux-b10-graph. Ревью человеком обязательно.
ЧИТАЙ: ADR-0023, раздел 7; argocd_graph.go и argocd_graph_test.go; graphbuild.go (из 0.1); FluxGraph в flux_types.go.
МОЖНО МЕНЯТЬ: flux_graph.go, flux_inventory.go, их тесты, testdata/flux/inventory/.

СДЕЛАТЬ: GetFluxGraph(ref FluxRef) -> *FluxGraph.
1. flux_inventory.go: терпимый разбор status.inventory.entries[] (id вида namespace_name_group_Kind, поле v — версия).
   Двойное подчёркивание в имени означает двоеточие; кластерные объекты имеют пустой namespace. Неизвестный формат
   записи пропускается, а не ломает граф. Fuzz-тест: ни паники, ни зависания.
2. Узлы-ресурсы получаются тем же способом, что у Argo (повтори подход argocd_graph.go); связи владения, Service -> Pod,
   Ingress -> Service строит общая функция из 0.1.
3. Поверх графа — узлы и рёбра Flux: sourceRef (Role source) и dependsOn между Flux-объектами, ребро inventory от
   объекта к ресурсам; Role flux и resource, State узла по FluxStateOf где применимо.
4. Remote=true (spec.kubeConfig): ресурсы в другом кластере не резолвятся локально, граф отдаёт Remote=true и узлы
   только Flux-уровня. Для HelmRelease без status.inventory — Available=false.
5. Лимит размера графа (как у Argo); превышение — усечение с признаком.

ТЕСТЫ: фикстуры Kustomization и HelmRelease 2.8; Remote; без inventory; цикл в dependsOn не зацикливает обход;
fuzz парсера id.
```

### B.11 managedBy и Overview

```text
ЗАДАЧА B.11 — managedBy и последние изменения. Ветка: feat/flux-b11-managedby.
ЧИТАЙ: ADR-0023, раздел 8; internal/k8s/configmeta.go (managedByOf); frontend/src/resources/definitions/common.ts
(managedByOf — дубль на TS); internal/k8s/overview_changes.go; frontend/src/types/insights.ts,
components/overview/RecentChangesCard.vue.
МОЖНО МЕНЯТЬ: configmeta.go, overview_changes.go (+ тесты); resources/definitions/common.ts (+ тест);
types/insights.ts; components/overview/RecentChangesCard.vue.

СДЕЛАТЬ
1. managedByOf на Go и на TS: метки kustomize.toolkit.fluxcd.io/name (+ /namespace) дают "Kustomization/<name>",
   helm.toolkit.fluxcd.io/name (+ /namespace) дают "HelmRelease/<name>". Приоритет: Argo (как сейчас), затем Flux,
   затем Helm. Результаты для Argo и Helm не меняются.
2. Одинаковый набор случаев в тестах Go и TS (скопируй таблицу, в комментарии каждого теста укажи парный файл).
3. GetRecentChanges: источник «Flux» (changeKindFlux = "Flux") по Kustomization и HelmRelease через m.GetFluxKustomizations(nil)
   и m.GetFluxHelmReleases(nil); момент — время перехода Ready-condition в True; недоступный источник не даёт записей и
   не ломает остальные (как у Argo).
4. types/insights.ts: тип записи 'Flux'; RecentChangesCard.vue: свой класс и цвет для 'Flux'.
КРИТЕРИИ: существующие тесты Argo и Helm проходят без правок; i18n:check зелёный (подпись «Flux» — литерал-бренд).
```

## Фаза U. Интерфейс

Общее для U.4–U.8: образец — `resources/definitions/argocd/application.ts` (`defineResource`: id, kind, view, nav, columns,
`autoRefresh`, `refreshOn`, вкладки, действия). `id` и `view` начинаются с `flux-`. Данные — `callApp('GetFlux…', namespaces)`.
Видимость действий — по правам (`Perm` из `types/auth`): reconcile — Operate, suspend/resume/delete — Write.
`t()` не вызывается на верхнем уровне модуля.

### U.1 Чистая логика

```text
ЗАДАЧА U.1 — Чистая логика resources/flux.ts. Ветка: feat/flux-u1-logic.
ЧИТАЙ: resources/argocd.ts и argocd.test.ts (образец чистых функций и тестов); types/flux.ts.
МОЖНО МЕНЯТЬ: resources/flux.ts, resources/flux.test.ts.

СДЕЛАТЬ (без Vue и Wails внутри модуля)
1. fluxStateRank(state) и problemRank(obj): Failed и Stalled выше Reconciling и Suspended; Ready = 0.
2. summarize(items): счётчики по состояниям, список проблемных (по убыванию ранга), разрезы по namespace и по
   источнику (sourceRef), последние reconcile (по lastReconcileAt, новые первыми).
3. Форматирование: shortRevision (Flux-ревизии вида branch@sha1:<40 hex> и sha256:<64 hex>: хэш — до 7 символов,
   ветка и тег целиком), sourceRefLabel(ref), dependsOnLabel(ref), chartLabel(name, version).
4. Тесты vitest на все функции, включая пустой ввод и неизвестные состояния.
КРИТЕРИИ: npm test, vue-tsc зелёные; текстов для пользователя в модуле нет (только структуры и коды).
```

### U.2 useFlux и useFluxLive

```text
ЗАДАЧА U.2 — Композаблы useFlux и useFluxLive. Ветка: feat/flux-u2-composables.
ЧИТАЙ: composables/useArgo.ts, useArgoLive.ts, useClusterConnection.ts; platform/events.ts.
МОЖНО МЕНЯТЬ: composables/useFlux.ts, useFluxLive.ts и их тесты.

СДЕЛАТЬ
1. useFlux(): fluxInstalled, fluxStatus (ref на FluxStatus), refreshFlux(), resetFlux(), хелпер hasController(name).
   refreshFlux вызывает callApp('GetFluxStatus'); любая ошибка (нет прав, кластер недоступен) = «не установлен».
   При installed=true стартует startFluxLive(), иначе stopFluxLive().
2. useFluxLive: fluxLiveTick (ref number), startFluxLive/stopFluxLive идемпотентны; событие "flux:changed".
3. Тесты с моками callApp и onEvent: установлен, не установлен, ошибка, повторный start не плодит подписки, смена
   кластера (reset).
КРИТЕРИИ: Sidebar показывает/скрывает группу Flux и подгруппы по fluxStatus (за это отвечает код из 0.2).
```

### U.3 Общие компоненты

```text
ЗАДАЧА U.3 — Общие компоненты Flux. Ветка: feat/flux-u3-shared.
ЧИТАЙ: components/resource/sections/ArgoConditionsSection.vue, ArgoResourcesTab.vue, ArgoSourcesTab.vue;
resources/status.ts (badgeClass, statusColor); types/flux.ts; GetFluxGraph и FluxGraph в Go-контракте.
МОЖНО МЕНЯТЬ: components/resource/sections/FluxStateBadge.vue, FluxConditionsSection.vue, FluxInventoryTab.vue,
FluxSourceRefSection.vue; locales/{en,ru,cn}/flux.json.

СДЕЛАТЬ
1. FluxStateBadge (prop state: FluxState): цвет и подпись по состоянию, доступность (не только цвет).
2. FluxConditionsSection (prop conditions): тип, статус, причина, сообщение, время; пустое состояние.
3. FluxInventoryTab: список ресурсов объекта из GetFluxGraph (узлы Role=resource), группировка на рабочие нагрузки
   и прочие, фильтр по виду, имени и namespace, ссылка на открытие ресурса, как в ArgoResourcesTab. Состояния:
   Available=false -> «недоступно для этой версии Flux», Remote=true -> «ресурсы в другом кластере».
4. FluxSourceRefSection: sourceRef и dependsOn с переходом к объекту, если вид открывается в приложении.
5. flux.json: состояния, conditions, общие подписи и пустые состояния (en, ru, cn).
6. Все интерактивные элементы доступны с клавиатуры; у ключевых узлов data-testid.
ТЕСТЫ: vitest/Vue Test Utils на рендер состояний и пустых данных (как у существующих секций).
```

### U.4 Kustomization

```text
ЗАДАЧА U.4 — Ресурс Kustomization. Ветка: feat/flux-u4-kustomization.
ЧИТАЙ: общее для U.4-U.8 (выше); definitions/argocd/application.ts; definitions/common.ts (flatNameCol, flatNsCol,
ageCol); resources/fluxActions.ts (заглушка из 0.2: reconcileFlux, setFluxSuspended, requestFluxDelete).
МОЖНО МЕНЯТЬ: resources/definitions/flux/kustomization.ts; locales/{en,ru,cn}/fluxKustomization.json.

СДЕЛАТЬ
1. defineResource: id 'flux-kustomization', kind 'Kustomization', view 'flux-kustomizations', nav { group: 'flux' },
   namespaced, autoRefresh и refreshOn: () => fluxLiveTick.value; загрузка callApp('GetFluxKustomizations', namespaces).
2. Колонки: имя, namespace, состояние (FluxStateBadge), сообщение, ревизия (короткая), источник, путь, interval,
   приостановлен, возраст. Фильтр по состоянию.
3. Действия строки и массовые: Reconcile, Reconcile with source (Operate), Suspend/Resume (Write; подтверждение —
   через fluxActions), Delete (Write; requestFluxDelete). Кнопки скрыты без нужного права.
4. Вкладки деталей: условия (FluxConditionsSection), ресурсы (FluxInventoryTab), источник и зависимости
   (FluxSourceRefSection), граф (FluxGraphTab из U.11; пока заглушка), YAML (через cr:-вид).
5. Переводы колонок и свойств — fluxKustomization.json (en, ru, cn).
ТЕСТЫ: определение регистрируется, id и view уникальны (registry.test.ts), колонки отдают значения на фикстуре DTO.
```

### U.5 HelmRelease

```text
ЗАДАЧА U.5 — Ресурс HelmRelease и история. Ветка: feat/flux-u5-helmrelease.
ЧИТАЙ: как для U.4; ArgoHistoryTab.vue (образец истории).
МОЖНО МЕНЯТЬ: resources/definitions/flux/helmrelease.ts; components/resource/sections/FluxHistoryTab.vue;
locales/{en,ru,cn}/fluxHelm.json.

СДЕЛАТЬ
1. defineResource: id 'flux-helmrelease', kind 'HelmRelease', view 'flux-helmreleases'; данные
   GetFluxHelmReleases; колонки: имя, namespace, состояние, чарт (имя@версия), релиз, целевой namespace, ревизия, возраст.
2. Действия — как в U.4 (общие функции из fluxActions.ts).
3. Вкладки: условия, история (FluxHistoryTab: версия релиза, версия чарта, appVersion, статус, время; новые первыми),
   ресурсы (при отсутствии inventory — «недоступно для Flux до 2.8»), источник, граф, YAML.
4. Переводы — fluxHelm.json.
ТЕСТЫ: история на фикстуре (порядок, пустая), регистрация и уникальность id.
```

### U.6 Источники

```text
ЗАДАЧА U.6 — Ресурсы-источники. Ветка: feat/flux-u6-sources.
ЧИТАЙ: как для U.4; ArgoSourcesTab.vue.
МОЖНО МЕНЯТЬ: resources/definitions/flux/source-gitrepository.ts, source-ocirepository.ts, source-helmrepository.ts,
source-helmchart.ts, source-bucket.ts, а также новый sourceCommon.ts в этом же каталоге; locales/{en,ru,cn}/fluxSources.json.

СДЕЛАТЬ
1. Пять определений (id flux-gitrepository, flux-ocirepository, flux-helmrepository, flux-helmchart, flux-bucket;
   nav group 'flux-sources'); данные GetFluxSources, фильтр по Kind; общие колонки и действия вынести в sourceCommon.ts.
2. Колонки: имя, namespace, состояние, URL (без секретов), ссылка (ветка, тег, semver), ревизия и digest артефакта
   (коротко), возраст. Действия: Reconcile и Suspend/Resume (права как в U.4); Delete для источников НЕ добавляется.
3. Вкладки: условия, артефакт, YAML.
ТЕСТЫ: регистрация, уникальность id и view, колонки на фикстурах каждого вида.
```

### U.7 Notification

```text
ЗАДАЧА U.7 — Ресурсы Notification. Ветка: feat/flux-u7-notification.
ЧИТАЙ: как для U.4.
МОЖНО МЕНЯТЬ: resources/definitions/flux/alert.ts, provider.ts, receiver.ts; locales/{en,ru,cn}/fluxNotify.json.

СДЕЛАТЬ: определения flux-alert, flux-provider, flux-receiver (nav group 'flux-notifications'), данные из
GetFluxNotifications. Provider: тип, канал, адрес (схема и хост). Alert: провайдер, severity, источники событий.
Receiver: тип, ресурсы, события. Путь вебхука нигде не показывается и не ожидается в DTO. Действий нет, кроме
Suspend/Resume там, где вид входит в allowlist B.8 (если не входит — только чтение). Вкладки: условия, YAML.
ТЕСТЫ: регистрация, уникальность id, пустые списки.
```

### U.8 Image automation

```text
ЗАДАЧА U.8 — Ресурсы Image automation. Ветка: feat/flux-u8-image.
ЧИТАЙ: как для U.4.
МОЖНО МЕНЯТЬ: resources/definitions/flux/imagerepository.ts, imagepolicy.ts, imageupdateautomation.ts;
locales/{en,ru,cn}/fluxImages.json.

СДЕЛАТЬ: определения flux-imagerepository, flux-imagepolicy, flux-imageupdateautomation (nav group 'flux-images'),
данные из GetFluxImageAutomation. ImageRepository: образ, число тегов, последний скан. ImagePolicy: правило и последний
образ. ImageUpdateAutomation: источник, ветка, последний запуск и коммит. Действия: Reconcile и Suspend/Resume для
ImageRepository и ImageUpdateAutomation (Operate и Write); у ImagePolicy действий нет. Вкладки: условия, YAML.
ТЕСТЫ: регистрация, уникальность id, колонки на фикстурах.
```

### U.9 Дашборд

```text
ЗАДАЧА U.9 — Дашборд Flux. Ветка: feat/flux-u9-dashboard.
ЧИТАЙ: components/views/ArgoDashboardView.vue, composables/useArgoApplications.ts; resources/flux.ts (U.1), useFlux и
useFluxLive (U.2); resources/fluxActions.ts (fluxActionTick).
МОЖНО МЕНЯТЬ: components/views/FluxDashboardView.vue; новый composables/useFluxObjects.ts (+ тест);
locales/{en,ru,cn}/fluxDash.json.

СДЕЛАТЬ
1. useFluxObjects: загрузка Kustomization и HelmRelease (callApp), перечитывание по fluxLiveTick и fluxActionTick,
   таймер как страховка; ошибки не ломают дашборд.
2. FluxDashboardView: сводка по состояниям (Ready, Reconciling, Failed, Stalled, Suspended), список проблемных
   объектов, разрезы по namespace и по источнику, последние reconcile; переход к объекту через onNavigate.
3. Состояния: загрузка, пусто, ошибка, unsupportedVersion (подсказка про обновление до Flux 2.3 и flux migrate),
   «приостановлено объектов: N» как отдельная плитка.
4. Переводы — fluxDash.json.
ТЕСТЫ: на данных фикстуры (сводка и состояния), без реального бэкенда.
```

### U.10 Диалоги действий

```text
ЗАДАЧА U.10 — Диалоги и функции действий. Ветка: feat/flux-u10-actions. Ревью человеком обязательно (опасное действие).
ЧИТАЙ: resources/argoActions.ts (report, changed, pendingArgoDelete, askConfirm), components/panels/DeleteAppDialog.vue,
SyncDialog.vue; ADR-0023, раздел 6 (абзац Delete и предупреждение про suspend).
МОЖНО МЕНЯТЬ: resources/fluxActions.ts, components/panels/FluxDeleteDialog.vue, locales/{en,ru,cn}/fluxActions.json.
Сигнатуры экспортов из заглушки 0.2 не менять: на них опираются U.4-U.8.

СДЕЛАТЬ
1. reconcileFlux(items, { withSource }), setFluxSuspended(items, suspended): вызовы ReconcileFlux, SuspendFlux, ResumeFlux;
   результат по каждому объекту (тосты по образцу report: успех, ошибки по объектам, «ещё N»); fluxActionTick и перечитывание
   сразу и через 3 с. Suspend/Resume — с подтверждением; в тексте suspend предупреждение: если spec.suspend задан в Git,
   родительская Kustomization вернёт значение.
2. requestFluxDelete(obj), pendingFluxDelete, closeFluxDelete; FluxDeleteDialog: последствия из DTO объекта —
   Kustomization с prune=true: «будут удалены управляемые ресурсы» и их число (inventory); prune=false: «ресурсы останутся
   без управления»; HelmRelease: «Helm-релиз будет удалён (uninstall) вместе с ресурсами»; если объект описан в Git,
   родительская Kustomization создаст его снова.
3. Подтверждение вводом имени объекта; кнопка неактивна до точного совпадения и во время запроса; фокус на поле ввода,
   Esc и клик вне диалога закрывают, Enter не отправляет без совпадения.
4. data-testid для всех элементов диалога и кнопок (на них опирается e2e Q.2).
ТЕСТЫ: логика текстов последствий (чистая функция), блокировка кнопки до совпадения имени, разбор результатов
(успех, частичный отказ).
```

### U.11 Граф-цепочка

```text
ЗАДАЧА U.11 — Вкладка «Граф» (цепочка). Ветка: feat/flux-u11-graph.
ЧИТАЙ: components/resource/sections/ArgoGraphTab.vue, resources/argoGraph.ts и argoGraph.test.ts; FluxGraph в Go-контракте.
МОЖНО МЕНЯТЬ: components/resource/sections/FluxGraphTab.vue, resources/fluxGraph.ts (+ тест); locales/{en,ru,cn}/fluxGraph.json.

СДЕЛАТЬ
1. fluxGraph.ts: раскладка по слоям «источник -> Flux-объект -> ресурсы», рёбра owns, routes, sourceRef, dependsOn,
   inventory; чистые функции, тесты как у argoGraph.test.ts.
2. FluxGraphTab: вызывает GetFluxGraph, состояние узлов цветом и подписью (не только цветом); Remote=true — метка
   «ресурсы в другом кластере»; Available=false — вкладка скрыта; перечитывание по fluxLiveTick для того же объекта,
   сброс при смене объекта (как в ArgoGraphTab).
ТЕСТЫ: раскладка на фикстуре (с dependsOn, remote, без inventory).
```

## Фаза Q и X

### Q.1 CI: Flux в kind

```text
ЗАДАЧА Q.1 — CI: Flux в kind. Ветка: feat/flux-q1-ci. Исполнитель: человек (цепочка поставки, ADR-0021).
ЧИТАЙ: .github/workflows/ci.yml — шаг «Argo CD» (около строки 619) и env e2e; ADR-0021.
МОЖНО МЕНЯТЬ: .github/workflows/ci.yml (только шаг Flux и его переменные), при необходимости scripts/.

СДЕЛАТЬ
1. В существующий e2e-job (без нового job) добавить шаг установки Flux из релизного install.yaml с закреплённой версией
   (актуальный патч 2.8.x: проверь на странице релизов fluxcd/flux2) и закреплённой суммой sha256 файла, сверка
   sha256sum -c; версия и сумма в переменных workflow.
2. Применить тестовые объекты: GitRepository и Kustomization на публичный репозиторий (например stefanprodan/podinfo,
   путь ./kustomize), HelmRepository и HelmRelease на публичный чарт. Ожидание Ready — с таймаутом и без падения job
   при сетевом сбое источника (e2e проверяет списки и действия, а не загрузку чарта).
3. Передать в e2e переменную E2E_FLUX=1 и пространство имён объектов.
КРИТЕРИИ: все действия закреплены по версии и сумме; job не становится заметно дольше без необходимости (оцени на первом
прогоне и запиши); существующие e2e не затронуты.
```

### Q.2 e2e flux.spec.ts

```text
ЗАДАЧА Q.2 — e2e-тест Flux. Ветка: feat/flux-q2-e2e.
ЧИТАЙ: frontend/e2e/pf-argocd.spec.ts и соседние спеки, playwright-конфиги; data-testid в компонентах U.3, U.4, U.5, U.10.
МОЖНО МЕНЯТЬ: frontend/e2e/flux.spec.ts.

СДЕЛАТЬ (пропускается без E2E_FLUX=1): раздел Flux виден в кластере с Flux; списки Kustomization и HelmRelease
содержат тестовые объекты; Reconcile меняет lastHandledReconcileAt; Suspend затем Resume меняют признак; Delete:
кнопка неактивна, пока имя не введено, после удаления объект исчезает из списка; роль без права write не видит
Suspend/Resume/Delete; в кластере без Flux раздела нет. Селекторы по роли и data-testid, без текстов интерфейса.
Если нужного data-testid нет — остановись и запроси его у владельца компонента.
```

### Q.3 Документация

```text
ЗАДАЧА Q.3 — Документация. Ветка: feat/flux-q3-docs.
ЧИТАЙ: ADR-0023; docs-site/features/gitops.md и docs-site/ru/…; docs/deploy.md (раздел про RBAC и extraRules);
deploy/helm/kubron/values.yaml (rbac.extraRules); CHANGELOG.md.
МОЖНО МЕНЯТЬ: docs/deploy.md, docs-site/features/gitops.md, docs-site/ru/features/gitops.md (и русский deploy.md,
если он есть), README.md, CHANGELOG.md.

СДЕЛАТЬ
1. docs/deploy.md: готовый фрагмент rbac.extraRules для Flux — get/list/watch на kustomizations, helmreleases,
   gitrepositories, ocirepositories, helmrepositories, helmcharts, buckets, alerts, providers, receivers,
   imagerepositories, imagepolicies, imageupdateautomations; отдельно patch (reconcile, suspend, resume) и delete.
   Пояснить, что профили rbac.profile правил Flux не содержат (как у Argo).
2. docs-site (en и ru): раздел Flux в gitops.md — что видно, какие действия есть, права, ограничения (нет diff и
   rollback; версия Flux не ниже 2.3; inventory у HelmRelease с 2.8; нет Flux Operator CRD).
3. README: Flux в списке возможностей. CHANGELOG: запись в [Unreleased] (формат — как у соседних).
4. Перед сдачей сверь текст с фактическим поведением по ADR-0023, расхождения укажи в отчёте.
```

### X.1 Пометка Flux-релизов в Helm

```text
ЗАДАЧА X.1 — Пометка Flux-релизов в разделе Helm. Ветка: feat/flux-x1-helm-marker. Зависит от B.3.
ЧИТАЙ: ADR-0023, раздел 9 (абзац про Helm); ADR-0015; внутренние Helm-файлы: grep по internal/k8s/helm*.go и
frontend/src/components/**/Helm*, locales/en/helm.json. Сначала перечисли в отчёте файлы, которые собираешься править;
править можно только файлы раздела Helm, которые отвечают за список и детали релизов и за подтверждение действий.

СДЕЛАТЬ
1. Бэкенд: в данных релиза признак «управляется Flux» (имя и namespace HelmRelease): сопоставление по имени релиза и
   namespace хранения с HelmRelease из кэша Flux (fluxList). Если Flux не установлен — никаких лишних запросов.
2. UI: метка «Flux» в списке и деталях релиза; перед upgrade, rollback и uninstall релиза под управлением Flux —
   предупреждение, что ручное изменение разойдётся с Git, с возможностью продолжить.
3. Переводы — только новые ключи в helm.json (en, ru, cn). Тесты на сопоставление и на отсутствие запросов без Flux.
ВНИМАНИЕ: файлы Helm могут править другие PR; перед стартом проверь открытые PR и сообщи о пересечении.
```

### Q.4 Приёмка

```text
ЗАДАЧА Q.4 — Приёмка. Исполнитель: человек. Ветка для исправлений: fix/flux-<суть>, по PR на исправление.
Выполни по чек-листу и приложи результаты к отчёту.
1. Среды: kind с Flux 2.3 и kind с Flux 2.8; кластер без Flux; кластер с частичной установкой (--components=source-controller,
   kustomize-controller).
2. Функции: все списки, детали, вкладки, граф и цепочка, reconcile (с источником и без), suspend/resume, delete с
   подтверждением; живые обновления (событие flux:changed, правка объекта через kubectl видна без перезагрузки);
   Overview «Последние изменения»; метка в Helm; managedBy у ресурсов Flux.
3. Безопасность: прогон контрольной строки по всем DTO и событиям (HelmRelease со spec.values, postBuild.substitute,
   userinfo в URL, адрес Provider с токеном, webhookPath Receiver) — ни один маркер не должен попасть ни в ответы, ни в
   журнал процесса, ни в аудит; роли readonly и operator не видят и не могут лишнего; чужой namespace и вид вне allowlist
   отклоняются.
4. Нагрузка: 2000 объектов — время ответа списков и расход памяти informer.
5. Проверки: go vet, go test -race, npm test, npm run i18n:check --strict, e2e, лицензии зависимостей (новых нет).
6. Итог: список найденных проблем с приоритетом; решение о переносе refactor в dev по docs/releasing.md.
```
