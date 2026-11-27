# Kubron Phase 1 — Identity & Authorization Engineering Package

- Статус: **engineering package / implementation handoff**
- Дата: 2026-11-27
- Base: актуальный refactor, SHA f102f31ce79b9fdffb8762c5b7ba1655574343f4
- Нормативные документы:
  - Executable Platform Architecture Specification
  - Phase 0 Executable Contracts
  - Technical Identity Authorization Grants decision
  - Execution Identity Rotation and Revocation decision
  - Cross-Team Delegation and Target Group Governance decision
  - Execution Identity & Durable Operations Design

## 1. Цель и границы

Phase 1 реализует безопасный фундамент технической идентичности и авторизации Kubron:

1. Самостоятельные UserPrincipal, TeamPrincipal, ServicePrincipal с независимым lifecycle.
2. Credentials технических principals через существующие credential Broker/provider/encryption path и единственную точку построения Kubernetes config — ConfigFor.
3. Явные Kubron authorization grants и Cross-Team Delegation с fail-closed evaluation.
4. Durable critical audit для security-sensitive mutations и как обязательный prerequisite для будущих Kubernetes side effects.
5. Одинаковая доменная семантика Desktop/Web и контрактные тесты File/PostgreSQL.

**Phase 1 не реализует** Target Groups, Autonomy policy engine, Plan/Approval/Run/Step executor, scheduler, Kubernetes write actions, autonomous actions, отдельный daemon, универсальный workflow engine или новый secrets subsystem.

Никакая новая mutating Kubernetes action не должна появиться в Phase 1. Завершение Phase 1 само по себе не разрешает write runtime: для него нужны следующие фазы, operation-specific contracts, Plans/Approvals, critical audit intent и verification/recovery.

## 2. Проверенная база и реальные ограничения кода

Проверено на refactor. Сравнение refactor с merge commit PR #265 показало identical; SHA f102f31ce79b9fdffb8762c5b7ba1655574343f4.

| Текущий код | Фактическое назначение / ограничение | Требование Phase 1 |
|---|---|---|
| internal/credential/contract.go | Scope сейчас только user/team; Subject содержит ClusterID, Scope, OwnerID, SessionID | Расширить subject типизированно; не смешивать identity ID с UserID/TeamID и session context |
| internal/credential/store.go | Key = ClusterID + Scope + OwnerID; versioned Put/DeleteIfVersion | Определить collision-free key для ServicePrincipal и миграцию существующих записей |
| internal/k8s/configfor.go | PlanFor(AuthSession, clusterID) строит ConnPlan; ConnPlan хранит AuthSession; ConfigFor — единственная точка создания stored-cluster rest.Config | Ввести отдельный typed principal-aware planning path без фиктивной Session и обхода ConfigFor |
| internal/k8s/configfor.go / client.go | Personal credentials имеют отдельный pool key; Team credentials сейчас используют cluster key | Изоляция pool key по principal kind + ID + cluster + binding/version |
| internal/auth/access.go | auth.Access авторизует User по членству Team; auth.Directory — каталог команд | Не расширять User role enum для Team/Service; не выдавать техническим principals права через пользователя/Team Admin |
| internal/tenant/scope.go | Scope = OrgID, TeamID, ClusterID; все три обязательны | Ownership только из trusted catalog; client input не является источником tenant ownership |
| internal/audit/audit.go | Logger.Record best-effort, глотает ошибки | Не использовать как critical gate; добавить отдельный error-returning durable contract |
| pkg/ext/store.go | AuditBackend.Append возвращает error, но публичный Logger не даёт её вызывающему | Critical path использует специализированный контракт, не меняя legacy semantics |
| internal/pgstore/audit.go | PostgreSQL audit append через отдельный pool.Exec; legacy audit_events schema | Critical mutation + audit intent имеют явно определённую atomicity boundary |
| internal/store/store.go, store/pg.go | Store — composition interface | Новые доменные backends остаются узкими; не создавать generic CRUD |
| internal/app | Composition root, Wails bindings/RPC, user/session boundary | Технические principals вызываются через use cases; app layer не вычисляет authorization rules |

Важные уточнения:
- credential.Subject.SessionID — challenge/session context, а не owner ID.
- ConfigFor принимает ConnPlan, а не произвольную user session напрямую.
- Нельзя создать фиктивную AuthSession, чтобы обойти это ограничение.
- Не строить rest.Config/client напрямую и не вызывать Provider мимо Broker.
- Существующие user/team credential records должны остаться читаемыми.

## 3. Зафиксированные доменные инварианты

### 3.1 Principal и identity

- UserPrincipal, TeamPrincipal, ServicePrincipal — разные типы principals.
- Principal ID — стабильный opaque ID, уникальный в пределах Organization и непереиспользуемый после удаления.
- Creator/initiator — только provenance; не владелец и не источник полномочий.
- TeamPrincipal принадлежит ровно одной Team и ограничен ею и её кластерами согласно ADR-0012.
- ServicePrincipal принадлежит Organization, но не получает доступ ко всем Teams/Clusters автоматически.
- Organization Admin управляет организационными Service Identity, но администрирование не даёт Kubernetes access.
- Team Admin управляет Team Identity своей Team.
- Team/Service principal не может аутентифицироваться как пользователь Kubron и не получает интерактивную User Session.
- Создание identity, credential assignment, Kubron grants, delegation, schedule/Operation assignment — независимые и отдельно аудируемые действия.
- Отключение/удаление identity — явные lifecycle transitions; hard-delete не стирает provenance/audit и не обходит credential revocation.

### 3.2 Execution Identity

- Interactive operation может использовать User identity только при действующей сессии и актуальной app-RBAC.
- Automatic/background Run может использовать только явно назначенную Team или Service identity.
- Никогда не наследовать credentials инициатора, текущего пользователя Desktop, Team Admin или соседнего sub-run.
- Multi-cluster parent Run не имеет общей автоматически наследуемой Kubernetes identity: каждый cluster sub-run имеет собственную назначенную Execution Identity.
- Identity binding включает principal ID, principal kind, credential reference/version и назначенный cluster/scope; secrets в binding не попадают.
- Изменение identity binding требует нового Plan и applicable Approval; Plan в Phase 1 ещё не реализуется.

### 3.3 Grants и delegation

Effective authorization — пересечение всех обязательных ограничений:
1. Tenant ownership / membership.
2. Собственные Kubron grants principal.
3. Для ServicePrincipal — активный Cross-Team Delegation от Team-владельца каждого Team-owned target; Organization Admin не может заменить согласие Team. TeamPrincipal остаётся ограничен своей Team.
4. Credential assignment и его актуальная версия.
5. Kubernetes RBAC фактического credential.
6. Autonomy Policy / operation constraints на последующих фазах.

Grant semantics:
- Разрешающие grants складываются по union.
- Constraints внутри одного grant применяются совместно (AND).
- Внешние границы сужают результат (intersection).
- Нет подходящего grant, невозможно вычислить условие, неизвестный principal/target, stale revision или ambiguity → deny.
- Отдельный пользовательский Deny grant и last-grant-wins не вводятся.
- Новые права не расширяют уже созданный immutable Plan; revoke/changes учитываются при следующей проверке.

Cross-Team Delegation:
- Выдаётся Team Admin только от имени своей Team и только для ресурсов кластеров, принадлежащих этой Team.
- Получатель — конкретный ServicePrincipal той же Organization.
- Organization Admin не может обойти Team owner и выдать delegation за Team.
- Delegation не содержит secrets, не заменяет credential assignment или Kubron grants.
- Отзыв Team delegation блокирует новые Runs и следующий side effect; уже отправленный Kubernetes request может завершиться.
- Изменение ownership/перенос кластера invalidates затронутую delegation; требуется новое явное решение.
- Никакого fallback на инициатора, creator, Organization Admin, другую Team или соседний sub-run.

### 3.4 Credential lifecycle

- Использовать существующие Broker, Provider, CredentialStore, envelope encryption, key lifecycle, token refresh и revoke mechanisms.
- Service credential — отдельный logical subject kind; не отдельная secret-management система.
- Credential resolution принимает явный typed principal context и cluster context; не принимает AuthSession как substitute.
- Перед выдачей/использованием credentials проверяются principal active state, assignment, scope, tenant ownership, grants/delegation и credential version.
- Pool/cache keys включают principal kind + principal ID + cluster ID + credential binding revision при необходимости; никакого cross-principal reuse.
- Rotation сохраняет logical identity, новые запросы используют новую валидированную credential version.
- Revoke блокирует новые выдачи и будущие side effects; уже отправленный запрос может завершиться.
- Нет fallback к отозванному credential, user credential или другой identity.
- Secrets не попадают в logs, audit, events, errors, Plan, DTO или pool keys.

### 3.5 Critical audit

- Существующий audit.Logger.Record остаётся best-effort для legacy use cases.
- Новая критическая запись имеет error-returning durable API.
- Если critical audit intent не удалось durably persist, security-sensitive mutation/будущий protected side effect не начинается.
- Для PostgreSQL операция изменения security state и соответствующая audit запись должны быть атомарны в одной DB transaction, где это один backend/aggregate boundary.
- Для Desktop durable journal должен подтвердить запись до действия; обеспечить sync semantics (fsync или эквивалентный platform durable flush) и crash-recovery tests. Если это невозможно, операция fail-closed.
- Audit outcome фиксирует success/failure/unknown. Ошибка записи outcome после side effect означает unknown и reconciliation, не success.
- Audit append-only; отдельная обязательная retention policy. Run/Evidence cleanup не удаляет обязательный audit.
- В audit только opaque IDs, actor/principal refs, scope, action, revisions, result/reason, correlation IDs и timestamps. Ни одного секрета или полного credential payload.

## 4. Package contracts и dependency direction

Сохранять текущий Go core и internal/app composition root. Не создавать internal/core.

| Пакет | Владение |
|---|---|
| internal/identity | Principal refs/kinds, identity metadata/lifecycle, credential assignments, owner/admin use cases и узкий Backend contract |
| internal/credential | Subject-aware credential resolution, subject key validation, Broker integration, provider/store tests |
| internal/authorization (или минимальное расширение internal/identity) | Grants, delegation, effective authorization evaluator и Backend contracts |
| internal/audit | Critical audit event/API и File durable implementation; без переписывания legacy Logger |
| internal/pgstore | PostgreSQL adapters/migrations/transactions, реализующие доменные Backend contracts |
| internal/store | Composition после того, как доменные интерфейсы заморожены |
| internal/app | Composition, transport DTO mapping, authenticated actor extraction, вызов use cases |
| internal/k8s | Principal-aware ConnPlan/connection integration; единственный путь к rest.Config сохраняется |

Правила:
- Domain packages не импортируют internal/app, internal/platform/*, Wails, HTTP handlers, Vue или internal/pgstore.
- internal/pgstore импортирует domain backend contracts и реализует их; обратной зависимости нет.
- Credential package не принимает authorization decisions; он выдаёт credentials только через typed subject resolution от доверенного use case.
- Authorization package не расшифровывает credentials и не строит Kubernetes clients.
- Audit package не вызывает auth/identity services под их locks.
- App — composition root; доменные правила не переносить в handlers.

### 4.1 Минимальные shared value types

Точные Go signatures — предмет контракта первого integration commit. Семантика обязательна:

- PrincipalRef: Kind, ID, OrgID, optional TeamID — typed, opaque, immutable reference.
- ActorRef: Kind, ID — кто инициировал действие (user/system/scheduler).
- IdentityRef: PrincipalRef, CredentialBindingID, CredentialVersion — без secret.
- TenantScope: OrgID, optional TeamID, optional ClusterID — canonical trusted scope.
- AuthorizationContext: Actor, Principal, Scope, Operation/Action, Target, PolicyRevision.
- CredentialSubject: Kind, PrincipalID, ClusterID, CredentialBindingID — отделён от web SessionID.
- AuditIntent: EventID, Actor, Principal, Scope, Action, ResourceRef, CorrelationID, OccurredAt.
- Grant: ID, PrincipalRef, OperationIDs, ActionIDs, TargetScope, Constraints, Revision, State, Expiry.
- Delegation: ID, ServicePrincipalID, OwnerTeamID, OperationIDs, ActionIDs, TargetScope, Constraints, Revision, State, Expiry.

Никакие client-supplied OrgID/TeamID/ownership values не становятся trusted без серверного разрешения из каталога.

## 5. Work split — четыре независимых направления

Разработчики получают этот документ как общий контракт. Каждый работает в отдельной ветке от одного frozen contract commit. Нельзя менять общие DTO, интерфейсы, migration registry или composition root без согласования с integration owner.

### Lane A — Identity Domain

**Владелец: Developer A. Результат: технические principals и lifecycle.**

Scope:
- internal/identity: principal kinds/IDs, Team/Service identity model, status/lifecycle, owner scope, provenance.
- Identity creation/read/update/disable/revoke use cases.
- Admin authorization через injected interface; не через произвольные user roles.
- Узкие Backend interfaces и File implementation/contract tests.
- Credential assignment metadata (opaque binding reference only); не секреты и не Broker internals.
- Team ownership invariants; Service Identity Organization ownership.
- Audit intent/outcome calls через frozen CriticalAudit interface.
- Unit/property/contract tests, включая no creator privilege inheritance и creator departure.

Не трогать:
- internal/credential/**, internal/k8s/**.
- Grants/delegation evaluation — Lane C.
- Critical audit backend implementation — Lane D.
- internal/store/**, internal/pgstore/**, migration registry, internal/app/**, RPC/Wails DTOs.
- Не создавать отдельный secret store.

Acceptance:
- TeamPrincipal и ServicePrincipal не создаются с некорректным owner scope.
- Creator не становится owner или authorization principal.
- Team/Service identity не наследует User role/grants.
- Identity disable/revoke transitions идемпотентны и audit-required.
- Неполные tenant scopes fail closed.
- File contract tests и domain tests проходят.

### Lane B — Credential Resolution

**Владелец: Developer B. Результат: typed technical-principal credentials через текущий credential path.**

Scope:
- Расширить credential.Scope/Subject/Key/Meta типизированным Service principal kind (и при необходимости Team principal reference без коллизий).
- Определить backward-compatible storage key/migration semantics для существующих user/team records.
- Subject-aware Broker request path; no fake AuthSession. Separate admin-assisted provisioning flow may complete IdP challenges for a technical identity, but the challenge is bound to the authenticated admin actor/session while the resulting credential is stored only under the technical principal. Runtime Acquire/Token remains non-interactive and never inherits the admin's user credentials.
- Principal-aware ConnPlan construction/credential resolution; единственная точка rest.Config остаётся ConfigFor.
- Connection pool isolation по principal kind + ID + cluster + binding/version.
- Rotation/revocation/version conflict semantics.
- Critical audit intent/outcome around credential assignment, replacement and revoke; pending binding stays unusable until durable success is confirmed.
- Contract tests for credential store, broker, token refresh, concurrent rotation, wrong-subject denial, no fallback.
- Проверить call graph: Manager, ForSession, ConnectionFor, health/ops clients and all ConfigFor consumers.

Не трогать:
- Identity domain model/store/use cases — Lane A.
- Grants/delegation evaluator — Lane C.
- Audit backends — Lane D.
- internal/store/**, internal/pgstore/**, migrations, internal/app/**, RPC/Wails bindings.
- Не создавать rest.Config/client напрямую и не вызывать Provider мимо Broker.
- Не превращать Team/Service principal в user session.

Dependency contract:
- Использовать только frozen PrincipalRef/CredentialSubject/IdentityRef contracts из integration commit.
- Если credential.Subject не может выразить контракт без breaking change, предложить adapter/versioned schema; не подменять principal ID OwnerID без explicit kind.

Acceptance:
- Невозможен cross-principal token/connection reuse.
- User credential path остаётся behavior-compatible.
- Existing user/team credential records читаются без потери данных.
- Service credential storage проходит envelope encryption, rekey, keycheck, backup/restore and plaintext leak checks.
- No fake session; no ConfigFor bypass. ProvisioningContext is separate from the technical Subject and cannot become the runtime principal.
- Rotation/revocation tests prove no stale fallback.

### Lane C — Authorization Grants & Cross-Team Delegation

**Владелец: Developer C. Результат: детерминированная Kubron authorization для технических principals.**

Scope:
- Grant and Delegation domain models, validation, revisions, lifecycle.
- Grant/Delegation Backend interfaces and File implementation/contract tests.
- Evaluator: union of applicable allow grants; AND constraints within each grant; intersection with tenant, identity scope, credential assignment, Kubernetes access and later autonomy policy.
- Cross-Team Delegation issuance only by owner Team Admin; recipient only same-org ServicePrincipal.
- Target authorization uses canonical ownership lookup; Target Group membership/sharing never grants access.
- Revocation/expiry/ownership transfer invalidation semantics.
- Explainable deny reason codes without leaking foreign target metadata.
- Audit intent/outcome through frozen CriticalAudit interface.
- Security tests for no grant, ambiguity, cross-team denial, ServicePrincipal no implicit access, org admin cannot bypass Team owner, revoked grant blocks next check.

Не трогать:
- Identity lifecycle/store — Lane A.
- Credential Subject/Broker/ConfigFor — Lane B.
- Critical audit backend — Lane D.
- internal/store/**, internal/pgstore/**, migrations, internal/app/**, RPC/Wails bindings.
- Не вводить отдельные Deny grants, precedence или last-grant-wins.
- Не трактовать delegation как credential assignment.

Dependency contract:
- Consume frozen PrincipalRef, TenantScope, IdentityRef and credential assignment contracts.
- Evaluator получает typed facts через injected interfaces; не читает SQL/secrets и не строит Kubernetes clients самостоятельно.

Acceptance:
- Table-driven tests prove union/AND/intersection semantics.
- No matching grant / unresolved ownership / expired or revoked delegation / unavailable evaluation → deny.
- TeamPrincipal never crosses Team boundary.
- ServicePrincipal gets no implicit cluster/team access.
- Org Admin cannot issue delegation on behalf of Team.
- Changes are visible at next authorization check; no cached stale allow.

### Lane D — Critical Audit Durability

**Владелец: Developer D. Результат: error-returning, durable, append-only critical audit path.**

Scope:
- Add explicit critical audit contract in internal/audit (or a small subpackage) with intent/outcome and typed event IDs.
- Preserve legacy Logger.Record best-effort behavior; do not silently change every existing audit call.
- Desktop File backend: append-only journal, serialized writes, durable flush semantics, crash/truncated-tail recovery, integrity/sequence checks as appropriate.
- Define PostgreSQL contract and transaction hooks for atomic mutation + audit append; actual pgstore/schema integration owned by integration owner.
- Error taxonomy: unavailable, serialization, durability failure, conflict, outcome unknown.
- Redaction/size limits; typed IDs only; no secrets.
- Test disk-full/permission denied/short write/flush failure, DB failure/rollback, crash between intent and outcome, duplicate EventID, concurrent writers, corrupt tail.
- Document audit retention separation from Run/Evidence retention.

Не трогать:
- Identity/grants domain rules — lanes A/C.
- Credential and k8s code — Lane B.
- internal/store/**, internal/pgstore/**, migrations, internal/app/**.
- Do not use stdout mirror as durable source.
- Do not claim exactly-once side effects or cross-system transaction.

Acceptance:
- API returns persistence errors to caller.
- Failed intent persistence prevents caller from proceeding.
- File backend durability behavior is explicit and tested.
- Duplicate event ID is idempotent or returns typed conflict; never duplicates silently.
- Legacy audit behavior and UI remain compatible.
- Critical audit never includes credential secret/token/kubeconfig.

## 6. Integration ownership and collision rules

**Integration owner (lead) owns exclusively:**
- internal/store/store.go and internal/store/*.go composition.
- internal/pgstore/**, SQL migrations, migration manifest/check scripts.
- internal/app/**, RPC allowlists, Wails bindings/generated files, HTTP routes.
- Cross-domain DTOs, frontend types/adapters, docs index and final package integration.
- Final PostgreSQL adapters and transaction composition for A/C/D contracts.

Lane A/C/D may submit schema proposals and backend interface contracts, but must not edit shared migration sequence or pgstore composition. Lane B submits credential storage migration requirements and tests; actual pgstore migration is integration-owned. This avoids concurrent migration ordering and shared composition edits.

**Frozen contract commit required before parallel implementation:**
1. PrincipalRef / CredentialSubject / IdentityRef / TenantScope definitions.
2. Domain-owned backend interfaces and error semantics.
3. CriticalAudit intent/outcome interface.
4. Package ownership and exact file allowlist.
5. Schema ownership map and migration sequencing.
6. Integration test command list.

After this commit, each developer can implement against frozen contracts in an isolated branch. A change to a frozen contract requires a coordination PR before dependent implementation continues.

No developer should edit the same file as another lane. If overlap appears, stop and send a proposal; do not resolve by force-pushing or silently expanding scope.

## 7. Recommended integration order

1. **Contract freeze** — integration owner; no runtime behavior change.
2. **Lane D Critical Audit core** — merge first or alongside domain-only work; no protected side effect may use it until durability acceptance passes.
3. **Lane A Identity Domain** — principal lifecycle and File contract.
4. **Lane C Authorization** — consumes A contracts; may develop in parallel against frozen types, integrates after A.
5. **Lane B Credential Resolution** — consumes frozen principal/identity contracts; may code in parallel, integrates after A contract is stable.
6. **Integration owner** — pgstore migrations/adapters, Store composition, app/RPC wiring, cross-platform tests.
7. **Security review and Phase 1 closure** — all acceptance criteria and failure injection evidence.

The order of merging does not weaken runtime gates: no mutating Kubernetes action is enabled in Phase 1. A lane can merge domain code before another lane if it compiles against frozen interfaces, but no public API exposes an incomplete capability.

## 8. Required ADRs before corresponding runtime code

Next available ADR numbers must be confirmed against repository state immediately before creation. Do not overwrite or renumber existing ADRs.

| Proposed ADR | Decision to record | Required before |
|---|---|---|
| ADR-0035 — Technical Principals and Identity Lifecycle | Principal kinds, ownership, lifecycle, creator provenance, admin authority, non-inheritance | Identity runtime and storage |
| ADR-0036 — Subject-Aware Credential Resolution | Typed credential subject, key compatibility, ConnPlan, pool isolation, ConfigFor integration, rotation/revocation | Credential runtime changes |
| ADR-0037 — Technical Authorization Grants and Delegation | Grant union/AND/intersection, scope, delegation lifecycle, deny semantics, revalidation | Authorization runtime |
| ADR-0038 — Critical Audit Durability | Critical intent/outcome, File durability, PG atomicity, idempotency, retention, fail-closed behavior | Security-sensitive mutations and any future protected side effect |

Previously accepted decision documents are normative input; ADRs must not reopen settled product/security choices. They formalize implementation contracts and explicitly mark unresolved engineering details. If ADR numbering has advanced, choose the next unused numbers.

## 9. Test strategy and minimum acceptance

### Shared domain tests
- Principal kind/ID validation and owner-scope immutability.
- Creator departure/disable does not transfer or remove ownership.
- Team/Service principals do not inherit User permissions.
- Grant union, per-grant AND constraints, outer intersection.
- No grant/ambiguity/unknown tenant → deny.
- Delegation issuer/recipient/same-org/owner-team restrictions.
- Revocation and expiry apply at next check.
- Credential assignment and principal binding remain distinct.
- Rotation uses current version; revoked version never fallback.
- No cross-principal pool reuse.

### Persistence tests
- Same domain contract against File and PostgreSQL.
- Atomic create/update/revoke plus required audit intent.
- Version/CAS conflicts.
- Restart/recovery after partial writes.
- Envelope encryption, rekey, keycheck, backup/restore.
- Tenant isolation for every query and mutation.
- No hard-delete of mandatory audit/provenance.

### Critical audit tests
- Intent failure blocks mutation.
- Outcome failure after external effect produces unknown/reconciliation state.
- Disk full, permission failure, flush/fsync failure, truncated tail.
- PostgreSQL transaction rollback includes audit.
- Duplicate event IDs do not create duplicate logical records.
- Redaction of tokens, passwords, kubeconfig, refresh tokens and provider payloads.

### Cross-platform tests
- Same domain use cases and result semantics in Desktop and Web.
- File/PostgreSQL contract tests.
- Web request/session loss cannot turn an operation into a User credential background task.
- Desktop app shutdown does not create a daemon or continue background execution.
- UI cannot authoritatively assign grants, delegation or execution identity without backend checks.
- API allowlist and generated Wails binding parity where endpoints are added.

### Phase 1 exit checklist
- [ ] Three principal kinds exist as typed domain entities.
- [ ] Team/Service lifecycle and owner/admin boundaries are enforced.
- [ ] Credentials resolve through Broker/provider/encryption path and ConfigFor only.
- [ ] No fake AuthSession or user credential inheritance.
- [ ] Pool isolation by technical principal is tested.
- [ ] Grants and Cross-Team Delegation match accepted semantics.
- [ ] Revocation/rotation affects the next authorization/credential resolution.
- [ ] Critical audit is durable and fail-closed for protected mutations.
- [ ] File and PostgreSQL backends pass the same contracts.
- [ ] Desktop and Web share domain semantics.
- [ ] No new Kubernetes mutating action or Autonomous execution was introduced.
- [ ] ADRs, tests, migration and rollback notes are reviewed.
- [ ] CI results are obtained for the exact merged commit; do not infer green from local tests.

## 10. Explicit stop conditions

Stop and request architecture review if a proposed change:
- introduces a fake user session for a technical principal;
- creates a second credential/secrets subsystem;
- makes Organization Admin implicitly able to operate all Teams' clusters;
- lets TeamPrincipal cross Team boundary;
- treats Target Group sharing or membership as access grant;
- allows creator/initiator to supply background Kubernetes credentials implicitly;
- reuses connection pools across different principals;
- builds rest.Config outside ConfigFor;
- makes audit best-effort on a protected mutation;
- stores plaintext secrets in Plan, Run, audit, events, errors or logs;
- changes existing user/team credential records without backward compatibility;
- edits shared migration registry or Store composition from a lane branch;
- exposes any mutating Kubernetes action before Plan/Approval/critical audit/verification/recovery contracts are implemented.

## 11. Developer handoff

Each developer receives:
1. This entire package as the shared source of truth.
2. Only their lane prompt from Section 5.
3. Frozen contract commit SHA and exact branch/file allowlist.
4. Required test commands from current CI workflows.
5. Explicit instruction: do not merge, force-push shared branches, change frozen contracts or expand scope without integration owner approval.

Expected PR format:
- What changed / what did not change.
- Files owned and touched.
- ADRs/contracts implemented.
- Threats addressed and fail-closed behavior.
- Tests run (exact commands and results).
- Migration compatibility / rollback.
- Cross-platform behavior.
- Known limitations and deferred work.

A PR is not accepted solely because it compiles. Security and persistence contract tests must prove the stated invariants.
