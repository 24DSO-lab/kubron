# ADR-0023: Поддержка Flux CD рядом с Argo CD

- Статус: принято
- Дата: 2026-11-06 (предложено), 2026-11-07 (принято: Vadim)
- Связано: ADR-0005, 0006, 0009, 0010, 0014, 0021, 0022; код Argo CD — `internal/k8s/argocd_*.go`,
  `internal/app/app_gitops.go`, `frontend/src/resources/argocd.ts`, `docs-site/features/gitops.md`

## Контекст

Kubron поддерживает Argo CD: обнаружение по CRD, список и детали `Application`, дашборд, граф ресурсов, diff через
Argo CD API, действия (sync, refresh, terminate, rollback, suspend/resume автосинка, delete), открытие UI. Всё читается
через Kubernetes API (dynamic client, unstructured), список строится из informer-кэша, об изменениях фронтенд узнаёт
по событию `argocd:changed`. Для токена Argo CD API есть отдельное хранилище, для diff и UI — port-forward.

Flux — второй основной GitOps-инструмент (CNCF); текущая ветка — Flux 2.8 (февраль 2026). Сейчас его ресурсы видны только
через общий просмотр CRD (`cr:group/version/plural`): без статусов, графа и действий. Цель — полнота GitOps-раздела.

Flux устроен иначе, и прямое копирование Argo-кода не подходит:

| | Argo CD | Flux |
|---|---|---|
| Единица | `Application`: источник, назначение и статус в одном объекте | Цепочка: источник (`GitRepository`, `OCIRepository`, `HelmRepository`, `Bucket`) → `Kustomization` / `HelmRelease` |
| Серверная часть | `argocd-server` (diff, токен, UI) | Нет: только контроллеры и CRD |
| Статус | `sync.status` + `health.status` | `conditions` (`Ready`, `Reconciling`, `Stalled`) и `spec.suspend` |
| Ресурсы объекта | `status.resources` | `status.inventory` (у `HelmRelease` — с Flux 2.8) |
| История | `status.history` | Только `HelmRelease.status.history` (ревизии Helm) |
| Действия | sync, rollback, terminate | reconcile (аннотация), suspend/resume (`spec.suspend`); откат — это `git revert` |
| Версии API | `v1alpha1` | Стабильные: `Kustomization` v1, `HelmRelease` v2 (с Flux 2.3), source v1; beta-версии удалены из CRD в Flux 2.8 |

Часть инфраструктуры переиспользуется целиком: dynamic client, `parseScope`/`scopedListTimeout`, informer с debounce,
адресные события (ADR-0009), `authz` и аудит, слой `resources/` и i18n на фронтенде.

## Решение

### 1. Объём

**Входит:**

- обнаружение Flux;
- `Kustomization` и `HelmRelease`: список, детали, conditions, inventory, история `HelmRelease`;
- источники (`GitRepository`, `OCIRepository`, `HelmRepository`, `HelmChart`, `Bucket`) на чтение;
- Notification (`Alert`, `Provider`, `Receiver`) на чтение;
- Image automation (`ImageRepository`, `ImagePolicy`, `ImageUpdateAutomation`) на чтение;
- дашборд;
- действия: reconcile, suspend, resume, delete;
- событие `flux:changed`;
- метка «управляется» (`managedBy`) и запись в «Последние изменения» на Overview.

**Не входит** (каждое — отдельный ADR или тикет): diff, rollback, force/reset `HelmRelease`, режим «удалить, оставив
ресурсы», создание и правка (остаётся общий YAML-редактор), CRD Flux Operator (`FluxInstance`, `ResourceSet`), вызов
`flux` CLI, открытие веб-интерфейса Flux Operator.

Хранилище, схема БД, секреты и port-forward не затрагиваются: у Flux нет API-сервера и токена, поэтому ADR-0007, 0008 и
0017 остаются без изменений, миграций нет.

### 2. Без Go-модулей Flux

Объекты читаются как `unstructured` через dynamic client, как у Argo. Свои DTO — контракт для UI; парсеры защитные:
отсутствующее или неожиданное поле даёт нулевое значение, не панику (CRD живёт в чужих версиях). Модули `fluxcd/*`
тянут controller-runtime, kustomize и SOPS, фиксируют версии `k8s.io/*` и дают перекос версий с CRD в кластере. Для
парсеров в `testdata/flux/` лежат фикстуры реальных объектов Flux 2.3 и 2.8.

### 3. Обнаружение и версии API

Один вызов discovery на подключение (результат кэшируется на короткий TTL) отдаёт `FluxStatus`: какие контроллеры
присутствуют (`kustomize`, `helm`, `source`, `notification`, `image`) и какая версия API обслуживается. Flux можно поставить
частично (`--components`), поэтому раздел показывается, если есть `kustomize` или `helm`, а недоступные вкладки скрываются.

Поддерживаются `kustomize.toolkit.fluxcd.io/v1`, `helm.toolkit.fluxcd.io/v2`, `source.toolkit.fluxcd.io/v1`;
минимальная версия Flux — 2.3. Остальные виды разрешаются по discovery из фиксированного allowlist версий: `OCIRepository`, `Bucket`, `ImageRepository`,
`ImagePolicy`, `ImageUpdateAutomation` и `Receiver` — `[v1, v1beta2]`; `Alert` и `Provider` — `[v1beta3]` (стабильной версии у
них пока нет). Читаемые поля в этих версиях совпадают; версия вне allowlist считается неподдерживаемой. Если CRD есть, но обслуживаются только устаревшие версии, `FluxStatus.State =
unsupportedVersion`: UI показывает подсказку «обновите Flux до 2.3+ / выполните `flux migrate`», объекты не разбираются.

### 4. Модель данных

Общая часть `FluxObjectInfo` и два вида: `FluxKustomizationInfo`, `FluxHelmReleaseInfo`.

- Общее: `Kind`, `Name`, `Namespace`, `Age`, `Interval`, `Suspended`, `SourceRef`, `DependsOn`, `Conditions`, `Revision`
  (`status.lastAppliedRevision`), `LastAttemptedRevision`, `LastReconcileAt`, `Message`.
- `State` вычисляется в Go и приходит готовым: `Suspended` > `Stalled` > `Failed` (`Ready=False`, не `Reconciling`) >
  `Reconciling` > `Ready` > `Unknown`. Фронтенд не разбирает conditions для статуса.
- `Kustomization`: `Path`, `Prune`, `Wait`, `TargetNamespace`, `ServiceAccount`, `Remote` (задан `spec.kubeConfig`),
  число записей inventory.
- `HelmRelease`: чарт (`spec.chart.spec` или `spec.chartRef`), имя релиза, `TargetNamespace`, `StorageNamespace`,
  `History[]`, `Remote`.
- Источники: `URL`, ссылка (`ref`: ветка, тег, semver, digest), `Revision` и `Digest` артефакта, имя `secretRef`.
- `Provider`: `Type`, `Channel`, адрес только как схема и хост. `Alert`: `ProviderRef`, `EventSeverity`, `EventSources[]`.
  `Receiver`: `Type`, `Resources[]`.
- Image automation: `ImageRepository` (образ, число тегов, последний скан), `ImagePolicy` (правило, `LatestImage`),
  `ImageUpdateAutomation` (источник, ветка, последний запуск и коммит).

**Наружу не отдаются:** `spec.values`, содержимое `valuesFrom`, значения `spec.postBuild.substitute`, секреты
`kubeConfig` и `decryption` (допустимо только имя), userinfo в URL, аннотация
`kubectl.kubernetes.io/last-applied-configuration`, путь и query в `spec.address` у `Provider` (в адресах вебхуков
бывают токены), `status.webhookPath` у `Receiver` (путь вебхука сам служит секретом: зная его, можно запустить reconcile). Тексты сообщений контроллеров проходят общий редактор секретов
(ADR-0022). Тест `TestFluxNoSecretLeak` — по образцу `TestArgoSettingsNoSecretLeak`: новое поле DTO добавляется только через него.

### 5. Живые обновления

Сначала подготовительный рефакторинг без изменения поведения: `argoCache` становится экземпляром обобщённого
`crdCache[T]` (generics: GVR, функция разбора, события). Существующие `argocd_cache_test.go` проходят без правок.

Для Flux — informer на `Kustomization` и `HelmRelease`; источники, Notification и Image automation стартуют лениво при первом открытии соответствующего раздела. Как у Argo:
проба права `list` перед стартом, повтор через 30 с, debounce 1 с, остановка в `ClusterConnection.Close`, отдельное
событие `flux:changed` (не `cluster:changed`), адресованное подключению (ADR-0009). Informer стартует при первом
обращении, не при подключении. В `SetTransform` кроме `managedFields` удаляется `spec.values` у `HelmRelease`: значения
не должны лежать в памяти процесса. Правка YAML читает объект напрямую, не из кэша.

### 6. Действия

| RPC-метод | Право | Что делает |
|---|---|---|
| `GetFluxStatus`, `GetFluxKustomizations`, `GetFluxHelmReleases`, `GetFluxSources`, `GetFluxNotifications`, `GetFluxImageAutomation`, `GetFluxGraph` | `read` | чтение |
| `ReconcileFlux(refs, withSource)` | `operate` | merge-patch аннотации `reconcile.fluxcd.io/requestedAt` |
| `SuspendFlux(refs)` | `write` | merge-patch `spec.suspend: true` |
| `ResumeFlux(refs)` | `write` | merge-patch `spec.suspend: false` и `requestedAt` (как `flux resume`) |
| `DeleteFlux(ref)` | `write` | удаление одного `Kustomization` или `HelmRelease` |

- `FluxRef{Kind, Namespace, Name}`; `Kind` проверяется по allowlist отдельно для каждого действия. Reconcile, suspend и
  resume — для `Kustomization`, `HelmRelease`, источников, `ImageRepository` и `ImageUpdateAutomation`; остальные виды
  только на чтение. Таблица allowlist лежит в коде, тест сверяет её с этим ADR. Произвольный GVR от клиента не принимается. Не более 200 объектов за вызов, таймаут 60 с.
- Результат — по каждому объекту отдельно (`FluxActionResult`), одна ошибка не отменяет остальные, как у Argo.
- Патч, а не get-update: нет конфликтов `resourceVersion` с контроллерами, нужен только глагол `patch` (меньше прав
  в RBAC), операция идемпотентна. `FieldManager: "kubron"`, чтобы в managed fields было видно источник изменения.
- `withSource` патчит объект источника с тем же временем, что и `flux reconcile --with-source`. Точный набор для
  `HelmRelease` сверить с `cmd/flux/reconcile_helmrelease.go` в flux2 при реализации.
- Аргументы в `authz` — компактный список целей (как `argoAuditApps`); секретов в аргументах нет.
- Каждый метод начинается с `a.authz` и вносится в `allowedMethods` и `exactPermission` в `policy_test.go`;
  без этого `TestEveryAppMethodIsClassified` и политики не пройдут.
- **Delete.** Только `Kustomization` и `HelmRelease`, один объект за вызов (как удаление Application в Argo). Это
  самое опасное действие раздела, поэтому диалог подтверждения обязателен и показывает последствия из текущего
  состояния объекта: у `Kustomization` с `prune: true` вместе с ним удаляются управляемые ресурсы (показывается число
  записей inventory, а при `prune: false` — что ресурсы останутся без управления); удаление `HelmRelease` удаляет
  Helm-релиз и его ресурсы (uninstall); если объект описан в Git, родительская `Kustomization` создаст его снова.
  Подтверждение — ввод имени объекта. Режима «удалить, оставив ресурсы» нет: это правка `spec.prune` и
  отдельное решение. В аргументы `authz` и аудит попадают вид, namespace, имя и значение `prune`.
- Если `spec.suspend` явно задан в Git, родительская `Kustomization` вернёт значение: это штатный GitOps, UI
  предупреждает об этом в подтверждении.

### 7. Граф и цепочка

Построитель графа из `argocd_graph.go` (владение, Service → Pod, Ingress → Service) выносится в общую функцию над
списком ссылок на ресурсы; `GetArgoApplicationGraph` переходит на неё с неизменным результатом (golden-тесты). Для Flux
вход — `status.inventory.entries[]` (`id` вида `namespace_name_group_Kind` и версия `v`; разбор терпимый, неизвестный
формат пропускается). Поверх графа — рёбра Flux: `sourceRef` и `dependsOn`, то есть цепочка «источник → Kustomization/HelmRelease → ресурсы».
Если `Remote=true`, ресурсы inventory находятся в другом кластере: они не резолвятся локально, UI показывает метку
«remote». Для `HelmRelease` на Flux < 2.8 вкладка ресурсов недоступна (нет `status.inventory`).

### 8. Доступ

- Чтение — `PermRead`; доступ ограничивается и RBAC кластера (ADR-0006). Список ограничивается набором namespace
  через `parseScope`, как у Argo: объекты Flux в мультитенантной установке живут в namespace команд.
- В профили чарта (`rbac.profile`) правила Flux **не** включаются: как у Argo, CRD добавляются через `extraRules`.
  В `docs/deploy.md` — готовый фрагмент: `get/list/watch` на `kustomizations`, `helmreleases`, `gitrepositories`,
  `ocirepositories`, `helmrepositories`, `helmcharts`, `buckets`, `alerts`, `providers`, `receivers`, `imagerepositories`,
  `imagepolicies`, `imageupdateautomations`; для `operate` и suspend/resume ещё `patch`, для delete — `delete`.
- `GetRecentChanges` (Overview) получает источник «Flux» по `Ready`-condition; недоступный источник просто не даёт записей.
- `managedByOf` распознаёт метки `kustomize.toolkit.fluxcd.io/name|namespace` и `helm.toolkit.fluxcd.io/name|namespace`
  и возвращает `Kustomization/<name>` и `HelmRelease/<name>`. Существующие значения для Argo и Helm не меняются.

### 9. Фронтенд

- Навигация: `requires` у группы расширяется до `'argocd' | 'flux'`; группа `flux` (дашборд, `Kustomizations`,
  `HelmReleases`) и свёрнутые подгруппы `flux-sources`, `flux-notifications`, `flux-images`, по образцу `argocd-settings`;
  подгруппа показывается, только если в `FluxStatus` есть соответствующий контроллер; `'flux'` в `NAV_ORDER` после `argocd`.
  Идентификаторы ресурсов с префиксом `flux-`, чтобы не пересекаться с `application` Argo.
- Ресурсы — через `defineResource` в `resources/definitions/flux/`; `useFlux` (состояние установки, общее с `Sidebar`),
  `useFluxLive` (тик по `flux:changed`); чистая логика (ранг проблемности, сводка) — в `resources/flux.ts` с тестами vitest.
- Вкладки деталей: условия, ресурсы (inventory), цепочка/граф, история (`HelmRelease`), источник. Компоненты Argo
  переиспользуются только там, где их props общие; общее выносится при реальном дублировании, а не заранее.
- `FluxDashboardView.vue`: сводка по состояниям (Ready, Reconciling, Failed, Stalled, Suspended), проблемные объекты,
  разрез по namespace и по источнику, последние reconcile.
- Раздел Helm помечает релизы, управляемые Flux (метка `helm.toolkit.fluxcd.io/name`), и предупреждает перед
  upgrade, rollback и uninstall: ручное действие расходится с Git.
- i18n: `flux.json` в `en`, `ru`, `cn`, плюс ключи в `nav.json`, `audit.json`; новые ошибки Go — шаблоны в `backend.json`
  всех языков (`npm run i18n:check`). Новых npm-зависимостей нет.

### 10. Тесты и CI

- Go: fake dynamic client и фикстуры; тесты debounce на `testing/synctest`; `TestFluxNoSecretLeak`; политики прав.
- Frontend: vitest для чистой логики; Playwright `frontend/e2e/flux.spec.ts`.
- e2e: в существующий job с kind добавляется установка Flux из релизного `install.yaml` с зафиксированной версией
  (по образцу Argo CD, ADR-0021), `GitRepository` + `Kustomization` на публичный репозиторий; проверяются список,
  reconcile, suspend/resume и delete (с подтверждением). Отдельный job не создаётся: старт kind дорогой.

## План реализации

Каждый шаг — отдельный PR в `refactor`, размер условный (S/M/L).

| № | Шаг | Размер |
|---|---|---|
| 0 | Рефакторинг без изменения поведения: `crdCache[T]`, общий построитель графа | M |
| 1 | Бэкенд (чтение): обнаружение, `Kustomization`/`HelmRelease`, informer, `flux:changed`, `managedBy`, RPC, allowlist, policy-тесты | L |
| 2 | Бэкенд (действия): reconcile, suspend, resume, delete, аудит | M |
| 3 | Фронтенд: навигация, ресурсы, дашборд, диалоги действий (в том числе подтверждение delete), i18n, пометка в Helm | L |
| 4 | Источники, Notification и Image automation (чтение), граф-цепочка, Overview | L |
| 5 | e2e, `docs/deploy.md`, `docs-site` (en/ru), README, CHANGELOG `[Unreleased]` | M |

Перед каждым PR: `gofmt -l .`, `go vet ./... && go test ./...`, в `frontend/` — `npm test && npm run i18n:check`;
заголовки лицензии (`scripts/license-headers.py --fix`).

## Рассмотренные альтернативы

- **Typed-клиенты `fluxcd/*`.** Типобезопасно, но тяжёлые транзитивные зависимости, перекос версий с CRD кластера,
  ручная работа при каждом minor. Отклонено в пользу `unstructured` с фикстурами.
- **Единая абстракция `GitOpsApplication` для Argo и Flux.** Модели разные (один объект против цепочки), а RPC-контракт
  Argo уже используется фронтендом и тестами политик. Отклонено сейчас; общий дашборд возможен позже на уровне
  фронтенда, когда накопится реальное дублирование.
- **Вызов `flux` CLI с сервера.** Бинарь в образ (размер, цепочка поставки по ADR-0021), exec в сервисном режиме,
  kubeconfig из БД на диск (запрещено ADR-0015, п. 5). Отклонено.
- **Открытие веб-интерфейса Flux Operator (аналог `OpenArgoUI`).** Это отдельный продукт, он есть не у всех. Позже —
  ссылка через настройку кластера по образцу `ArgoURL`.
- **Оставить общий просмотр CRD.** Это текущее состояние: нет статусов, графа и действий. Не закрывает цель.
- **Опрос API вместо informer.** Отличается от принятого подхода Argo и дороже при множестве объектов.

## Последствия

- Нет изменений хранилища, секретов, транспорта и миграций; поверхность атаки растёт только на три RPC-метода с `authz`.
- Дублирование парсеров для двух систем смягчается `crdCache[T]` и общим графом; плата — поддержка совместимости
  полей с каждым minor Flux. Фикстуры 2.3 и 2.8 и один pinned e2e-прогон это ловят, но не гарантируют.
- Пользователи Flux < 2.3 видят «версия не поддерживается»; на Flux < 2.8 у `HelmRelease` нет вкладки ресурсов.
- Расширение до diff, rollback, force/reset и режима «удалить, оставив ресурсы» потребует новых ADR; diff в Flux
  означает сборку kustomize и доступ к артефакту source-controller, это отдельная сложная задача.
- `Alert` и `Provider` пока в beta (`v1beta3`): смена их версии в Flux потребует правки allowlist и фикстур.
- Delete может снести рабочие ресурсы через prune или uninstall. Защита — право `write`, подтверждение вводом имени и
  показ последствий; откатить удаление Kubron не может, восстановление идёт из Git.
- Откат и «исправить» в Flux — действия в Git; Kubron это не имитирует, и это сознательно.
- Informer'ы на больших кластерах съедят память пропорционально числу объектов; ленивый старт и удаление `spec.values`
  ограничивают расход, но замеры нужны на шаге 1.
- Новый пункт меню и десятки ключей i18n в трёх языках увеличивают поверхность переводов.

## Принятые решения

Приняты 2026-11-07 (Vadim), вопросы владельцу из первой редакции закрыты:

1. Права: reconcile — `operate`, suspend/resume и delete — `write`, как у Argo.
2. Минимальная версия Flux — 2.3 (первая с `HelmRelease` v2).
3. RBAC чарта: правила Flux в профили не включаются, только `extraRules` и фрагмент в `docs/deploy.md`. Вариант выбран
   как минимальный по правам и не требующий изменений чарта; переключатель `rbac.flux.enabled` можно добавить позже
   отдельным PR без нового ADR.
4. Notification и Image automation входят в первый релиз, на чтение.
5. Delete входит в первый релиз, с явным предупреждением про prune и uninstall в подтверждении.
