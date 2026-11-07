# Поддержка Flux CD: тикеты для параллельной разработки

Цель: раздел GitOps получает Flux рядом с Argo CD. Решение, объём и обоснование: [ADR-0023](../adr/0023-flux-support.md).
Документ делит работу на задачи так, чтобы программисты и агенты работали параллельно и не правили одни и те же файлы.
Готовые тексты задач для исполнителей: [flux-prompts.md](flux-prompts.md).

Оценки в человеко-днях (ч-д) ориентировочные. Сумма около 82 ч-д, критический путь около 16 ч-д
(0.2 → U.3 → U.4 → Q.2 → Q.4): при 5–6 исполнителях рабочий срок определяется критическим путём и ревью,
а не суммой.

## Принцип: контракт сначала, потом параллель

Конфликты слияния возникают в общих файлах: реестр ресурсов, меню, список методов RPC, файлы переводов, `client.go`.
Поэтому работа идёт в таком порядке:

1. **Задача 0.2 «Контракт и каркас»** один раз правит все общие файлы и создаёт заглушки для всех остальных задач:
   типы DTO, сигнатуры RPC-методов, регистрации, пункты меню, ключи в `nav.json`, `audit.json`, `backend.json`.
2. Дальше **каждый файл принадлежит ровно одной задаче**. Задача только заполняет свои, уже существующие файлы-заглушки,
   поэтому слияние не конфликтует.
3. Заглушки безопасны: чтение возвращает пустой результат, `GetFluxStatus` отвечает «Flux не установлен», действия
   возвращают `errFluxNotImplemented`. Меню Flux скрыто, пока `FluxStatus.installed=false`, поэтому в `refactor`
   можно сливать непрерывно. В `dev` `refactor` переносится только после приёмки (Q.4).

## Сквозные правила

1. **Ветки.** `feat/flux-<номер>-<slug>` от `origin/refactor`, например `feat/flux-b2-kustomization`. Для задач, зависящих
   от 0.2, ветка создаётся после его слияния.
2. **Нет прав на push — патчи.** Результат задачи — `git format-patch origin/refactor..HEAD`; применяется `git am`.
   Имя патча начинается с номера задачи.
3. **Коммиты.** Автор `Vadim <vmilyash@mts.ru>`, строка `Signed-off-by: Vadim <vmilyash@mts.ru>` (`git commit -s`),
   без `Co-Authored-By:` и `Claude-Session:`. Заголовок по образцу репозитория: `feat(flux): …`, `test(flux): …`,
   `docs(flux): …`.
4. **Область правок.** PR меняет только файлы из колонки «Файлы» своей задачи. Проверка перед PR и на ревью:
   `git diff --name-only origin/refactor...HEAD` — каждый путь должен входить в список задачи.
5. **Контракт заморожен.** `flux_types.go`, `flux_errors.go`, `app_flux.go`, `types/flux.ts` и список общих файлов
   (ниже) после 0.2 правит только владелец 0.2 отдельным коротким PR («изменение контракта»), остальные ребейзятся.
   Нужна новая ошибка Go, новое поле DTO или новый пункт меню — задача останавливается и просит контрактный PR, а не
   правит общий файл сама.
6. **Тесты принадлежат своей задаче.** Тест лежит рядом с кодом. Фикстуры объектов Flux — в
   `internal/k8s/testdata/flux/<вид>/*.yaml`, по каталогу на вид (у каждой задачи свои каталоги).
7. **i18n.** Каждая UI-задача создаёт свои файлы `locales/<язык>/<namespace>.json` сразу в трёх языках (`en`, `ru`, `cn`);
   имя файла — первый сегмент ключа. Файлы подхватываются автоматически (`import.meta.glob`), регистрировать их нигде
   не нужно. Общие `nav.json`, `audit.json`, `backend.json` правит только 0.2. Правила — [docs/i18n.md](../i18n.md).
8. **Без новых зависимостей.** `go.mod`, `go.sum`, `package.json` и vendor не меняются (ADR-0023, раздел 2).
9. **Уникальные идентификаторы.** `id` и `view` ресурсов Flux начинаются с `flux-`: реестр резолвит определения по `id`,
   дубликат молча перекрывает чужое определение (см. комментарий в `definitions/application.ts`).
10. **Проверки перед PR** (CONTRIBUTING): `gofmt -l .`, `go vet ./... && go test ./...`; в `frontend/` — `npm test`,
   `npm run i18n:check`, `npx vue-tsc --noEmit`; для новых файлов — `python3 scripts/license-headers.py --fix`.
11. **Сборка.** Go 1.27.3 из `.test/go1.27.3.linux-amd64.tar.gz`, vendor из `.test/vendor.tar.gz`
    (команда сборки vendor записана в `.test/build vendor`). UI-задачам нужны привязки `wailsjs`: они генерируются
    из Go (`wails generate module`) и в репозитории не хранятся, поэтому UI-задача стартует после слияния 0.2.

## Общие файлы (правит только 0.2)

| Область | Файлы |
|---|---|
| RPC и права | `internal/server/methods.go` (`allowedMethods`), `internal/auth/policy_test.go` (`exactPermission`), `internal/app/app_flux.go` |
| Контракт Go | `internal/k8s/flux_types.go`, `flux_errors.go`, `flux_common.go` (+ тест), `flux_kinds.go` (таблица видов: группа, plural, допустимые версии по порядку предпочтения) |
| Меню и регистрации | `frontend/src/resources/navigation.ts`, `resources/types.ts` (`NavGroupId`), `resources/registry.ts`, `components/Sidebar.vue`, `components/resource/ResourceHost.vue` (рисует диалог удаления), `composables/useViewRegistry.ts`, `composables/useClusterConnection.ts`, `App.vue` |
| Контракт TS | `frontend/src/types/flux.ts` |
| Общие переводы | `locales/{en,ru,cn}/nav.json`, `audit.json`, `backend.json`; список литералов в `scripts/i18n-check.mjs` (`Flux`, `Flux CD`, `Kustomization`, `HelmRelease` и т. д.) |

`internal/k8s/client.go` правит только 0.1: он вводит реестр informer-кэшей по GVR в `ClusterConnection`, и Flux (B.7)
подключает свои кэши через него без правок `client.go`. `internal/k8s/flux_list.go` создаёт 0.2 (только API, без
кэша), внутренности заменяет B.7; задачи B.2–B.6 вызывают `fluxList` и сам файл не меняют.

## Контракт (что фиксирует 0.2)

**Go DTO** (`internal/k8s/flux_types.go`, JSON — camelCase): `FluxStatus`, `FluxObjectInfo`, `FluxKustomizationInfo`,
`FluxHelmReleaseInfo`, `FluxSourceInfo`, `FluxProviderInfo`, `FluxAlertInfo`, `FluxReceiverInfo`, `FluxImageRepositoryInfo`,
`FluxImagePolicyInfo`, `FluxImageUpdateAutomationInfo`, `FluxNotifications{Providers, Alerts, Receivers}`,
`FluxImageAutomation{Repositories, Policies, Automations}`, `FluxGraph`, `FluxRef{Kind, Namespace, Name}`,
`FluxActionResult`, `FluxState` (`Suspended`, `Stalled`, `Failed`, `Reconciling`, `Ready`, `Unknown`).

**RPC** (`app_flux.go`, `authz` в каждом методе, права по ADR-0023):

| Метод | Право |
|---|---|
| `GetFluxStatus()`, `GetFluxKustomizations(ns)`, `GetFluxHelmReleases(ns)`, `GetFluxSources(ns)`, `GetFluxNotifications(ns)`, `GetFluxImageAutomation(ns)`, `GetFluxGraph(ref)` | `read` |
| `ReconcileFlux(refs, withSource)` | `operate` |
| `SuspendFlux(refs)`, `ResumeFlux(refs)`, `DeleteFlux(ref)` | `write` |

**Общие функции** (`flux_common.go`): вычисление `FluxState` из conditions и `spec.suspend`, разбор conditions, очистка URL
от userinfo, редактор текстов сообщений. **`fluxList`** — единая точка чтения списка (сигнатура фиксируется в 0.2); GVR вида строится из `flux_kinds.go` и версий, которые отдаёт `fluxServedVersions` (заглушка в `flux_status.go`, настоящую реализацию даёт B.1);
**ошибки** — все Flux-ошибки Go объявлены в `flux_errors.go` сразу, с шаблонами в `backend.json` трёх языков.

**YAML и правка объекта Flux** идут через существующие `cr:group/version/plural`-виды (версию подбирает `customKindFor`
по CRD), поэтому `kinds.go` не меняется и версии Flux в `kindTable` не зашиваются.

**Фронтенд-каркас:** заглушки всех файлов, перечисленных в таблице «Файлы», с зарегистрированными в `registry.ts`
определениями и подгруппами меню `flux`, `flux-sources`, `flux-notifications`, `flux-images` (`requires: 'flux'`);
`useClusterConnection` вызывает `refreshFlux`/`resetFlux`.

## Фаза 0. Основа

| № | Тикет | Результат (критерий приёмки) | Оценка, ч-д | Зависит |
| --- | --- | --- | --- | --- |
| 0.1 | Обобщённый informer-кэш и общий граф | `argoCache` стал экземпляром `crdCache[T]` (generics: GVR, функция разбора, колбэк); построитель графа из `argocd_graph.go` вынесен в функцию над списком ссылок на ресурсы; поведение Argo не изменилось: `argocd_cache_test.go` и golden-тесты графа проходят без правок; тесты debounce переведены на `testing/synctest`; `ClusterConnection` держит кэши через реестр по GVR (мьютекс, `retryAt` на ключ), `Close` останавливает все; после 0.1 `client.go` другим задачам не нужен | 3 | — |
| 0.2 | Контракт и каркас | всё из раздела «Контракт»: DTO, `app_flux.go`, `allowedMethods`, `policy_test`, `flux_errors.go` + `backend.json`, `flux_common.go` с таблицей тестов на `FluxState`, `fluxList` (API без кэша), заглушки Go и UI, пункты меню, ключи `nav`/`audit`; `TestEveryAppMethodIsClassified`, политики прав и `i18n:check` проходят; меню Flux не видно, приложение работает как раньше | 3 | — |

0.1 и 0.2 идут параллельно: общих файлов нет (0.1 правит `argocd_*.go`, `client.go`, новые `crdcache.go`, `graphbuild.go`).

## Фаза B. Бэкенд

Все задачи читают Kubernetes только через dynamic client (`unstructured`), парсеры защитные: пропущенное поле даёт нулевое
значение. Каждая вносит фикстуры в свой каталог `testdata/flux/` и тест на отсутствие секретов в DTO.

| № | Тикет | Результат (критерий приёмки) | Оценка, ч-д | Зависит |
| --- | --- | --- | --- | --- |
| B.1 | Обнаружение и `FluxStatus` | один вызов discovery на подключение, кэш с коротким TTL; `FluxStatus` — присутствие контроллеров (`kustomize`, `helm`, `source`, `notification`, `image`) и обслуживаемые версии по allowlist из ADR-0023, раздел 3; частичная установка (`--components`); только устаревшие версии даёт `unsupportedVersion`; нет CRD, нет прав и недоступный кластер — «не установлен» без ошибки; тесты на fake discovery | 2 | 0.2 |
| B.2 | `Kustomization`: чтение | `GetFluxKustomizations`: DTO по ADR-0023, раздел 4; `State` из conditions; `Remote`, число записей inventory; `postBuild.substitute`, секреты `kubeConfig` и `decryption`, `last-applied-configuration` не попадают в DTO (`TestFluxKustomizationNoSecretLeak`); фикстуры Flux 2.3 и 2.8; область namespace через `parseScope` | 3 | 0.2 |
| B.3 | `HelmRelease`: чтение | `GetFluxHelmReleases`: чарт из `spec.chart.spec` или `spec.chartRef`, `History[]`, `Remote`; `spec.values` и содержимое `valuesFrom` не попадают в DTO (тест с контрольной строкой); фикстуры 2.3 и 2.8 (с `status.inventory` и без) | 3 | 0.2 |
| B.4 | Источники: чтение | `GetFluxSources` для `GitRepository`, `OCIRepository`, `HelmRepository`, `HelmChart`, `Bucket`; версия `OCIRepository`/`Bucket` по allowlist из `FluxStatus`; URL без userinfo, из `secretRef` только имя; артефакт (`revision`, `digest`) | 2 | 0.2 |
| B.5 | Notification: чтение | `GetFluxNotifications` (`Provider`, `Alert`, `Receiver`); у `Provider` адрес только схема и хост; `status.webhookPath` у `Receiver` и путь вебхука нигде не возвращаются (тест с контрольной строкой); версии `v1beta3` и `v1` по allowlist | 2 | 0.2 |
| B.6 | Image automation: чтение | `GetFluxImageAutomation` (`ImageRepository`, `ImagePolicy`, `ImageUpdateAutomation`); версии `v1`/`v1beta2` по allowlist; без вызовов реестра образов | 2 | 0.2 |
| B.7 | Informer-кэш Flux и `flux:changed` | `fluxList` использует `crdCache` и реестр из 0.1 (`client.go` не меняется): informer на `Kustomization` и `HelmRelease` стартует лениво, источники, Notification и Image — при первом обращении; проба права `list`, повтор через 30 с, debounce 1 с, остановка в `ClusterConnection.Close`; событие `flux:changed` адресовано подключению (ADR-0009); `SetTransform` удаляет `managedFields` и `spec.values` у `HelmRelease` (тест: значений нет в кэше); тесты на `synctest` и на гонки (`go test -race`); замер памяти на 2000 объектов записан в описание PR | 4 | 0.1, 0.2 |
| B.8 | Действия: reconcile, suspend, resume | `ReconcileFlux`, `SuspendFlux`, `ResumeFlux` через merge-patch с `FieldManager: "kubron"`; allowlist видов для каждого действия (ADR-0023, раздел 6); не более 200 объектов, таймаут 60 с; результат по каждому объекту, одна ошибка не отменяет остальные; `withSource` патчит источник с тем же временем; resume ставит `requestedAt`; чужой вид или namespace отклоняются; аргументы аудита без секретов; тесты на fake dynamic client | 3 | 0.2 |
| B.9 | Удаление | `DeleteFlux` только для `Kustomization` и `HelmRelease`, один объект, обычный DELETE; данные для подтверждения (`prune`, число записей inventory) UI берёт из уже загруженного DTO, отдельного вызова нет; аргументы `authz`: вид, namespace, имя (`prune` в аудит не попадает, потому что `authz` вызывается до чтения объекта; значение пишется в журнал процесса через `slog`); тесты: чужой вид и чужой namespace отклоняются, отсутствующий объект даёт понятную ошибку | 2 | 0.2 |
| B.10 | Граф и цепочка | `GetFluxGraph`: записи `status.inventory.entries[]` (`id` вида `namespace_name_group_Kind`, `v`) разбираются терпимо, неизвестный формат пропускается (fuzz-тест); общий построитель из 0.1 даёт владение, Service → Pod, Ingress → Service; поверх — рёбра `sourceRef` и `dependsOn`; `Remote=true` — ресурсы не резолвятся локально; для `HelmRelease` без inventory ответ «недоступно» | 4 | 0.1, 0.2 |
| B.11 | `managedBy` и Overview | `managedByOf` (Go, `configmeta.go`) и его копия на фронтенде (`definitions/common.ts`) распознают метки `kustomize.toolkit.fluxcd.io/name\|namespace` и `helm.toolkit.fluxcd.io/name\|namespace`, возвращают `Kustomization/<name>` и `HelmRelease/<name>`; значения для Argo и Helm не изменились; общий набор случаев для Go и TS в одной таблице тестов; `GetRecentChanges` получает источник «Flux» по `Ready`-condition; на фронтенде тип записи `'Flux'` добавлен в `types/insights.ts`, у карточки «Последние изменения» свой цвет | 2 | 0.2 |

## Фаза U. Интерфейс

UI-задачи разрабатываются на фикстурах и типах `types/flux.ts`; реальные данные появляются по мере слияния B.\*, но
блокировать UI они не должны. Тексты только через i18n, свой файл переводов у каждой задачи.

| № | Тикет | Результат (критерий приёмки) | Оценка, ч-д | Зависит |
| --- | --- | --- | --- | --- |
| U.1 | Чистая логика `resources/flux.ts` | ранг проблемности по `FluxState`, сводка для дашборда (по состояниям, namespace, источнику), форматирование ревизий и дайджестов, цель зависимости; тесты vitest; ни Vue, ни Wails в модуле | 2 | 0.2 |
| U.2 | `useFlux` и `useFluxLive` | `fluxStatus`/`fluxInstalled` для `Sidebar` (подгруппа показывается, если есть её контроллер); `refreshFlux`/`resetFlux`; живые обновления по `flux:changed`, подписка только пока Flux установлен (по образцу `useArgo`/`useArgoLive`); тесты | 1 | 0.2 |
| U.3 | Общие компоненты | `FluxStateBadge`, `FluxConditionsSection`, `FluxInventoryTab`, `FluxSourceRefSection`; переводы `flux.json` (состояния, conditions, общие подписи); доступность с клавиатуры; тесты по `data-testid` | 3 | 0.2 |
| U.4 | `Kustomization` | `definitions/flux/kustomization.ts`: колонки, фильтры, свойства, вкладки (условия, ресурсы, источник, YAML через `cr:`); `refreshOn` по `useFluxLive`; переводы `fluxKustomization.json` | 3 | U.1, U.2, U.3 |
| U.5 | `HelmRelease` | `definitions/flux/helmrelease.ts` и `FluxHistoryTab`; для Flux < 2.8 вкладка ресурсов показывает «недоступно»; переводы `fluxHelm.json` | 3 | U.1, U.2, U.3 |
| U.6 | Источники | пять определений `definitions/flux/source-*.ts` (подгруппа `flux-sources`), статус артефакта, ревизия; переводы `fluxSources.json` | 3 | U.1, U.2, U.3 |
| U.7 | Notification | определения `provider`, `alert`, `receiver` (подгруппа `flux-notifications`); без отображения путей вебхуков; переводы `fluxNotify.json` | 3 | U.1, U.2, U.3 |
| U.8 | Image automation | определения `imagerepository`, `imagepolicy`, `imageupdateautomation` (подгруппа `flux-images`); переводы `fluxImages.json` | 3 | U.1, U.2, U.3 |
| U.9 | Дашборд | `FluxDashboardView.vue`: сводка по состояниям (Ready, Reconciling, Failed, Stalled, Suspended), проблемные объекты, разрез по namespace и источнику, последние reconcile; пустое состояние и состояние `unsupportedVersion` с подсказкой про `flux migrate`; переводы `fluxDash.json`; тест на данных фикстуры | 4 | U.1, U.2, U.3 |
| U.10 | Диалоги действий | `resources/fluxActions.ts` и `FluxDeleteDialog.vue`; reconcile (с источником и без), suspend/resume (предупреждение: значение из Git вернётся), delete: последствия из состояния объекта (число ресурсов при `prune: true`, uninstall для `HelmRelease`, повторное создание из Git), подтверждение вводом имени, кнопка неактивна до совпадения; результат по каждому объекту; переводы `fluxActions.json`; e2e-селекторы | 4 | 0.2 |
| U.11 | Граф-цепочка | `FluxGraphTab`, `resources/fluxGraph.ts`: цепочка «источник → объект → ресурсы», метка `remote`; вкладка скрыта для `HelmRelease` без inventory; переводы `fluxGraph.json`; тесты раскладки | 4 | U.3, B.10 (контракт `FluxGraph`) |

## Фаза Q. Проверка и документация

| № | Тикет | Результат (критерий приёмки) | Оценка, ч-д | Зависит |
| --- | --- | --- | --- | --- |
| Q.1 | CI: Flux в kind | в существующий e2e-job добавлены установка Flux из релизного `install.yaml` с зафиксированной версией (и проверкой суммы, ADR-0021), `GitRepository` + `Kustomization` + `HelmRelease` на публичные источники; без нового job; сетевой сбой источника не валит весь job | 2 | — |
| Q.2 | e2e `flux.spec.ts` | список, статус, reconcile, suspend/resume, delete с подтверждением, скрытый раздел без Flux, роль без права `write` не видит действий; селекторы по `data-testid` | 4 | Q.1, B.2, B.3, B.8, B.9, U.4, U.5, U.10 |
| Q.3 | Документация | `docs/deploy.md`: фрагмент RBAC для `extraRules` (чтение, `patch`, `delete`) для всех видов Flux; `docs-site/features/gitops.md` и `docs-site/ru/…`: раздел Flux, права, ограничения (нет diff и rollback); README; `CHANGELOG.md` `[Unreleased]`; перед приёмкой сверяется с фактическим поведением | 3 | — |
| Q.4 | Приёмка | прогон на настоящем kind с Flux 2.3 и 2.8; проверка контракта (`go vet`, `i18n:check --strict`, `npm test`, e2e), утечек секретов (контрольная строка по всем DTO и событиям), нагрузки; исправления найденного отдельными PR; перенос в `dev` по `docs/releasing.md` | 3 | всё выше |

## Фаза X. Смежное

| № | Тикет | Результат (критерий приёмки) | Оценка, ч-д | Зависит |
| --- | --- | --- | --- | --- |
| X.1 | Пометка Flux-релизов в Helm | в списке и деталях релизов Helm видно «управляется Flux» (по `HelmRelease` с тем же именем релиза и namespace хранения); перед upgrade, rollback и uninstall предупреждение; переводы в `helm.json`; тесты. Правит существующие файлы раздела Helm, поэтому не стартует, пока открыты другие PR в этих файлах | 2 | B.3 |

## Волны

| Волна | Что идёт параллельно | Условие старта |
| --- | --- | --- |
| 0 | 0.1, 0.2, Q.1, Q.3 | сразу |
| 1 | B.1–B.6, B.8, B.9, B.11, U.1, U.2, U.3, U.10 | слит 0.2 |
| 2 | B.7, B.10 (слит 0.1); U.4–U.8 (слит U.3); X.1 (слит B.3) | см. «Зависит» |
| 3 | U.9, U.11, Q.2 | слиты U.\*, B.\*, Q.1 |
| 4 | Q.4 | слито всё |

В волне 1 одновременно может идти до 13 задач, но достаточно 4–6 исполнителей: задачи независимы, порядок внутри волны
любой, первыми берутся те, от которых зависит волна 2 (U.3, B.2, B.3).

## Кто исполняет

| Тип | Задачи | Почему |
|---|---|---|
| Человек | 0.1, 0.2, B.7, B.9, Q.1, Q.4 | рефакторинг конкурентного кода, контракт, на который опираются все, informer и память, необратимое удаление, цепочка поставки CI, приёмка |
| Агент и обязательное ревью человеком | B.2, B.3, B.5, B.8, B.10, U.3, U.9, U.10, Q.2 | секреты в DTO, патчи в кластере, граф и UX опасных действий: код понятный по спецификации, но ошибка дорогая |
| Агент | B.1, B.4, B.6, B.11, U.1, U.2, U.4–U.8, U.11, Q.3, X.1 | чёткие входы и выходы, проверка тестами и фикстурами |

Ревью «агент + человек» проверяет два пункта: список файлов в PR совпадает с областью задачи и тест на отсутствие секретов
действительно ловит контрольную строку.

## Паспорт задачи для агента

Готовые промпты по каждой задаче лежат в [flux-prompts.md](flux-prompts.md); ниже — общий шаблон паспорта, по которому они составлены:

```
Задача: <номер> — <название>
Контекст: docs/adr/0023-flux-support.md, docs/tickets/flux-support.md (разделы «Сквозные правила» и «Контракт»)
Ветка: feat/flux-<номер>-<slug> от origin/refactor
Разрешено менять: <файлы задачи из таблицы владения>
Только читать: flux_types.go, flux_errors.go, flux_common.go, app_flux.go, types/flux.ts и все общие файлы
Критерии приёмки: <колонка «Результат» этой задачи>
Проверки: gofmt -l .; go vet ./...; go test ./internal/k8s/...; в frontend/: npm test, npm run i18n:check, npx vue-tsc --noEmit
Коммит: автор Vadim <vmilyash@mts.ru>, git commit -s, без Co-Authored-By и Claude-Session
Результат: патч git format-patch origin/refactor..HEAD
Если нужен файл вне списка, новое поле DTO, новая ошибка Go или пункт меню — остановиться и описать, что именно нужно,
правку общего файла не делать.
```

## Владение файлами

Путь без каталога относится к `internal/k8s/`; пути фронтенда — относительно `frontend/src/`; `locales` — `i18n/locales/{en,ru,cn}/`.

| № | Файлы |
|---|---|
| 0.1 | `argocd_cache.go`, `argocd_graph.go`, `client.go`, новые `crdcache.go`, `graphbuild.go` и их тесты, `argocd_cache_test.go` (только перенос на `synctest`) |
| 0.2 | все общие файлы из таблицы «Общие файлы»; `flux_types.go`, `flux_errors.go`, `flux_common.go`, `flux_kinds.go`, `flux_list.go`; `internal/app/app_flux.go`; заглушки: Go — `flux_status.go`, `flux_kustomization.go`, `flux_helmrelease.go`, `flux_sources.go`, `flux_notification.go`, `flux_image.go`, `flux_actions.go`, `flux_delete.go`, `flux_graph.go`; UI — все файлы из строк U.\* ниже |
| B.1 | `flux_status.go`, `flux_status_test.go` |
| B.2 | `flux_kustomization.go` (+ тест), `testdata/flux/kustomization/` |
| B.3 | `flux_helmrelease.go` (+ тест), `testdata/flux/helmrelease/` |
| B.4 | `flux_sources.go` (+ тест), `testdata/flux/source/` |
| B.5 | `flux_notification.go` (+ тест), `testdata/flux/notification/` |
| B.6 | `flux_image.go` (+ тест), `testdata/flux/image/` |
| B.7 | `flux_list.go` (+ тест), `flux_cache.go` (+ тест) |
| B.8 | `flux_actions.go` (+ тест) |
| B.9 | `flux_delete.go` (+ тест) |
| B.10 | `flux_graph.go`, `flux_inventory.go` (+ тесты), `testdata/flux/inventory/` |
| B.11 | `configmeta.go`, `overview_changes.go` (+ тесты); `resources/definitions/common.ts` (+ тест); `types/insights.ts`, `components/overview/RecentChangesCard.vue` |
| U.1 | `resources/flux.ts`, `resources/flux.test.ts` |
| U.2 | `composables/useFlux.ts`, `composables/useFluxLive.ts` (+ тесты) |
| U.3 | `components/resource/sections/FluxStateBadge.vue`, `FluxConditionsSection.vue`, `FluxInventoryTab.vue`, `FluxSourceRefSection.vue`; `locales/flux.json` |
| U.4 | `resources/definitions/flux/kustomization.ts`; `locales/fluxKustomization.json` |
| U.5 | `resources/definitions/flux/helmrelease.ts`; `components/resource/sections/FluxHistoryTab.vue`; `locales/fluxHelm.json` |
| U.6 | `resources/definitions/flux/source-gitrepository.ts`, `source-ocirepository.ts`, `source-helmrepository.ts`, `source-helmchart.ts`, `source-bucket.ts`, новый `sourceCommon.ts` в том же каталоге; `locales/fluxSources.json` |
| U.7 | `resources/definitions/flux/alert.ts`, `provider.ts`, `receiver.ts`; `locales/fluxNotify.json` |
| U.8 | `resources/definitions/flux/imagerepository.ts`, `imagepolicy.ts`, `imageupdateautomation.ts`; `locales/fluxImages.json` |
| U.9 | `components/views/FluxDashboardView.vue`, новый `composables/useFluxObjects.ts` (+ тест); `locales/fluxDash.json` |
| U.10 | `resources/fluxActions.ts`; `components/panels/FluxDeleteDialog.vue`; `locales/fluxActions.json` |
| U.11 | `components/resource/sections/FluxGraphTab.vue`; `resources/fluxGraph.ts`; `locales/fluxGraph.json` |
| Q.1 | `.github/workflows/ci.yml` (только шаг Flux и переменные его теста), `scripts/` при необходимости |
| Q.2 | `frontend/e2e/flux.spec.ts` |
| Q.3 | `docs/deploy.md`, `docs-site/features/gitops.md`, `docs-site/ru/features/gitops.md` (и русский `deploy.md`, если есть), `README.md`, `CHANGELOG.md` |
| X.1 | файлы раздела Helm в `internal/k8s/helm*.go` и `components/**/Helm*`; `locales/helm.json` (только новые ключи) |
| Q.4 | исправления отдельными PR по результатам приёмки; сам прогон файлов не меняет |

## Где задачи могли бы столкнуться и чем это снято

| Риск | Как снят |
|---|---|
| Несколько задач добавляют методы в `allowedMethods` и `policy_test.go` | все методы и права вносит 0.2 один раз |
| Несколько UI-задач правят `registry.ts`, `navigation.ts`, `Sidebar.vue` | всё регистрируется в 0.2 заглушками; задачи заполняют только свой файл |
| Общие `nav.json`, `audit.json`, `backend.json` | ключи и ошибки заведены в 0.2; остальное — файл переводов на задачу |
| `client.go` нужен и Argo, и Flux | реестр кэшей по GVR вводит 0.1, дальше файл никто не правит |
| B.2–B.6 и B.7 оба касаются чтения списков | B.2–B.6 вызывают `fluxList`, B.7 меняет только его внутренности |
| `managedByOf` дублируется на Go и TS | обе копии и общий набор тестов в одной задаче B.11 |
| Изменение DTO посреди работы | контрактный PR владельца 0.2 и ребейз остальных; своими силами поле не добавляется |
| Привязки `wailsjs` не в репозитории | UI-задачи стартуют после слияния 0.2 и генерируют их локально |
| Половина функций видна пользователям | меню скрыто при `installed=false`, в `dev` переносится только после Q.4 |
