# Phase 2 — Lane C: Persistence & Integration

## Назначение

Реализовать persistence, platform adapters и интеграцию Target/Autonomy Foundation для Desktop/File и Web/PostgreSQL. Это временное задание; постоянные решения находятся в ADR.

## Обязательные ADR

- ADR-0035–0038 — Identity, credentials, authorization/delegation, Critical Audit;
- ADR-0053–0058 — Phase 2 Target & Policy contracts;
- ADR-0041/0042 — Target Groups и Autonomy Policy;
- ADR-0052 — phased delivery and write-action gates.

Все ADR находятся в `docs/adr/`. Не вводить новые архитектурные решения в обход ADR.

## File ownership

Разрешено изменять:
- `internal/store/**`;
- `internal/pgstore/**`;
- необходимые PostgreSQL migrations;
- app composition/platform adapter files, необходимые для wiring;
- integration/contract tests и соответствующие test fixtures.

Не изменять:
- `internal/target/**` (Lane A owns);
- `internal/autonomy/**` (Lane B owns);
- ADR и другие постоянные документы.

Не расширять allowlist без согласования. Перед изменением любого файла вне списка остановиться и запросить согласование.

## Начать немедленно, не блокируя A/B

Пока Lane A и B формируют package interfaces:
1. Изучи существующие File/Desktop и PostgreSQL persistence patterns, transaction/CAS подходы, migrations и composition roots.
2. Подготовь implementation map: какие существующие компоненты можно переиспользовать, какие adapter points нужны, какие файлы потребуется изменить.
3. Подготовь contract-test harness и parity test plan на основе ADR-0054–0057.
4. Проанализируй trusted ownership seams: `pgstore.Store.OrgID()`, trusted cluster metadata и Directory; локальный File/Desktop режим.
5. Проверь существующие Critical Audit и Kubernetes credential seams.
6. Не изобретай package interface самостоятельно. Как только A/B передадут exported interfaces, зафиксируй их адаптеры и продолжай реализацию. Если интерфейс противоречит ADR, остановись и сообщи.

## Реализовать после получения интерфейсов A/B

1. File/Desktop persistence adapters для Target и Autonomy.
2. PostgreSQL/Web persistence adapters и migrations.
3. CAS/ExpectedRevision и атомарные aggregate operations.
4. Atomic snapshot publication: проверка Group Revision и публикация Snapshot в одной consistency boundary:
   - File: одна сериализованная операция под lock + durable atomic replacement;
   - PostgreSQL: одна transaction с условной проверкой revision.
5. Trusted ownership resolver:
   - PostgreSQL: Organization берётся из `pgstore.Store.OrgID()`;
   - Team — только из trusted cluster metadata и Directory, с проверкой принадлежности Team текущей Organization;
   - File/Desktop: локальный Organization scope `local`;
   - Team ownership в локальном режиме не поддерживать;
   - неизвестный или неподтверждённый owner => fail closed.
6. ClusterReader через существующие subject-aware Broker / `PlanFor` / `ConfigFor` seams. Не создавать Kubernetes clients из Target domain и не читать kubeconfig напрямую.
7. Authorization и Critical Audit integration для защищённых Target/Policy/Profile/Binding mutations. Intent должен быть durable до mutation; failure записи intent запрещает mutation.
8. Tenant-scoped storage и одинаковые доменные инварианты для File и PostgreSQL.
9. Composition/wiring без дублирования cluster catalog и без обхода существующих сервисных границ.
10. Integration tests и общие contract suites для File и PostgreSQL.

## Безопасность и ограничения

- Client-supplied OrgID/TeamID не являются trusted ownership.
- Ownership resolver не заменяет authorization. Cross-Team доступ требует существующей delegation и authorization по ADR-0037.
- Не обходить Broker, Provider, `PlanFor`, `ConfigFor` и существующий cluster catalog.
- Не сохранять secrets/plaintext credentials в Target, Policy, Snapshot, audit, logs или errors.
- Не добавлять Kubernetes write path.
- Не реализовывать Plans, Approvals, Run/Step, leases, scheduler, daemon или AI execution.
- Не удалять Critical Audit по retention.

## Обязательные тесты

- один и тот же contract suite для File и PostgreSQL;
- CAS, stale revision, concurrent update;
- atomic snapshot publish under revision race;
- tenant isolation / spoofed owner;
- Organization/Team ownership resolution;
- local Desktop `local` scope и запрет Team owner в local mode;
- authorization/delegation enforcement;
- Critical Audit intent-before-mutation и failure path;
- Broker/ConfigFor usage;
- restart/durability и corrupted storage handling;
- отсутствие Kubernetes write path.

## Координация

- A и B владеют только своими domain packages; C владеет adapters, migrations, composition и integration tests.
- A/B должны передать C package interface как можно раньше, отдельным сообщением или первым коммитом.
- C не меняет A/B интерфейсы самостоятельно. Несовместимость с ADR — остановка и эскалация.
- Сначала завершить совместимость domain interfaces и adapters, затем интеграционный тестовый прогон.

## Definition of Done

- File/Desktop и Web/PostgreSQL реализуют одинаковые доменные гарантии.
- Contract/integration tests зелёные.
- Critical Audit и authorization подключены к защищённым mutations.
- Нет secrets leakage и Kubernetes write path.
- Нет изменений вне allowlist.
- PR описывает migrations, atomicity model, tests и все ограничения.

Не переводить ADR в `Implemented`: это делается после интеграции всей Phase 2, review и зелёного CI.
