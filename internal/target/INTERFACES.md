# Интерфейсы `internal/target` для Lane C

Временная сводка для интегратора (Lane C); постоянная спецификация — ADR-0053…0055. Точные сигнатуры и
контракты ошибок — в `ports.go` (godoc), здесь только суть.

## Что реализует Lane C

| Интерфейс | Что нужно | Опора в существующем коде |
|---|---|---|
| `Backend` | File (Desktop) и PostgreSQL; обе проходят `targettest.Run` | store composition root, `pgstore` |
| `OwnershipResolver` | `ResolveClusterOwner(ctx, clusterID) (ClusterOwner, error)` | `k8s.ClusterStore.Get`, Directory, `pgstore.Store.OrgID()`; Desktop — Organization `local` |
| `ClusterReader` | read-only `GetObject` / `ListObjects` от имени requester'а | только `PlanFor`/`ConfigFor`/Broker; kubeconfig и `rest.Config` из домена запрещены |
| `Access` | `AuthorizeGroupAdmin`, `ViewerFor` поверх app-RBAC | existing authorization/tenant seams |

## Семантика атомарности `Backend`

- Переходы состояния выполняет ТОЛЬКО домен: `Group.Created`, `Group.ApplyUpdate`, `Group.ApplyDeactivate`,
  `Group.CheckSnapshotPublishable`. Backend вызывает их внутри своей атомарной операции:
  File — под одной блокировкой + durable atomic replacement; PostgreSQL — в транзакции с условным UPDATE по revision.
- `PublishSnapshot`: проверка revision группы и запись snapshot — в ОДНОЙ consistency boundary
  (повторного чтения revision до/после записи недостаточно). Отказ не оставляет частичной записи.
- `Get/List/GetSnapshot` фильтруют по `Viewer`; невидимое неотличимо от отсутствующего (`ErrNotFound`).
- Возвращать копии (`Clone`); snapshot неизменяем.
- Ошибки: `ErrNotFound`, `ErrStaleRevision`, `ErrAlreadyExists`, `ErrDeactivated`, `ErrGroupRevisionChanged`,
  `ErrUnavailable` (исход нельзя доказать — fail closed).

## Что осталось за рамками Lane A

- Критичный аудит мутаций (ADR-0038/0058 п.4) подключает интегратор: в домене порта аудита нет.
- Cross-Team включение (Organization-группа с Team-owned кластером) fail closed до отдельной delegation.
- Идемпотентность мутаций по `mutationID` не вводилась (в ADR-0054 не требуется).
