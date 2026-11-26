# Cross-Team Delegation, Target Group Governance and Resolution — принятое архитектурное решение

- Статус: принято владельцем продукта и дополнено архитектурными решениями, 2026-11-26
- Область: ServicePrincipal, межкомандный доступ, Target Group ownership/sharing, selector resolution, Plan safety
- Связано: ADR-0012, ADR-0018, `2026-11-26-technical-identity-authorization-grants-decision.md`, `2026-11-26-scope-target-groups-decision.md`, `execution-identity-durable-operations-design.md`

## 1. Цель и неизменяемые границы

Organization-level Service Identity может обслуживать операции нескольких Teams, но Organization ownership самой identity не предоставляет ей доступ к кластерам этих Teams.

ADR-0012 остаётся в силе: каждый cluster принадлежит ровно одной Team; универсальный ClusterGrant не вводится. Межкомандный доступ оформляется отдельным явным **Cross-Team Delegation**, выданным Team-владельцем конкретному ServicePrincipal.

Target Group остаётся описанием целей, а не механизмом авторизации. Владение группой, право использовать её описание, право видеть объекты и право выполнять действия над объектами — разные полномочия.

## 2. Cross-Team Delegation

### 2.1. Кто и кому делегирует

- Organization Admin создаёт и администрирует Organization-level Service Identity/ServicePrincipal, credentials и её жизненный цикл в рамках Organization.
- Team Admin может выдать Cross-Team Delegation только от имени своей Team и только на ресурсы кластеров, принадлежащих этой Team.
- Получателем делегирования может быть только явно указанный ServicePrincipal той же Organization.
- TeamPrincipal не получает cross-team delegation: он остаётся ограничен своей Team.
- Organization Admin не может единолично выдать ServicePrincipal доступ к ресурсам Team, минуя Team-владельца. Организационная административная роль не является implicit consent.
- Делегирование не может быть выдано пользователю, Target Group или parent Run; оно адресовано конкретному ServicePrincipal.

### 2.2. Содержание делегирования

Каждая запись делегирования содержит:
- Organization ID и Team ID grantor-владельца ресурсов;
- ServicePrincipal ID получателя;
- разрешённые Operation IDs и Action IDs;
- Target Scope, ограниченный ресурсами этой Team;
- constraints (например, environment, namespace, GVK, временное окно, concurrency, budget, destructive-action restrictions);
- состояние, revision, created/updated/revoked metadata;
- срок действия (обязателен конечный expiry; максимальный срок задаётся организационной политикой);
- reason/change reference для аудита.

Делегирование не содержит Kubernetes secrets и не заменяет credential assignment или Kubron grants.

### 2.3. Семантика полномочий

Действие над cross-team target разрешено только при одновременном выполнении всех условий:
1. ServicePrincipal имеет подходящий собственный Kubron grant.
2. Есть активное Cross-Team Delegation от Team-владельца, покрывающее Operation, Action и target.
3. Target всё ещё принадлежит указанной Team и Organization.
4. Назначенная Execution Identity и её credential binding действительны.
5. Kubernetes RBAC разрешает действие.
6. Effective Autonomy Policy, Plan, Approval и action preconditions разрешают действие.

Эти ограничения пересекаются; ни один слой не расширяет другой. Delegation не выдаёт ServicePrincipal глобальные права и не делает его членом Team.

### 2.4. Изменение, отзыв и конфликт

- Team Admin может сузить, приостановить или отозвать делегирование своей Team независимо от владельца Service Identity.
- Расширение полномочий — новая revision и новая авторизация; старые Plan/Approval не расширяются.
- Изменение делегирования применяется к новым Runs и проверяется перед каждым следующим side effect уже работающего Run.
- Отзыв запрещает новые Runs и новые side effects. Уже отправленный Kubernetes request может завершиться; outcome reconciles через fresh reads.
- При переносе кластера, изменении Team ownership или удалении Team делегирование для затронутых targets автоматически становится невалидным. Повторное предоставление требует нового явного решения владельца.
- Expiry, revocation, loss of ownership, отсутствие/неоднозначность данных или невозможность проверки означают deny.
- Никакого fallback на права Organization Admin, creator, инициатора, соседней Team или другого sub-run.

### 2.5. Административное разделение

Создание Service Identity, назначение credentials, выдача Kubron grants, выдача Team delegation и назначение identity Operation/Schedule остаются разными аудируемыми действиями. Ни одно не подразумевает другое.

Team Admin управляет делегированиями, выданными его Team. Organization Admin управляет Service Identity и может видеть организационный аудит; просмотр не означает возможность изменять делегирование от имени Team.

## 3. Target Group ownership и sharing

### 3.1. Владение

- Organization Group принадлежит Organization; управляется Organization Admin в пределах app-RBAC.
- Team Group принадлежит одной Team; управляется Team Admin этой Team.
- Custom Group принадлежит ровно одному owner scope: Organization либо Team. Владелец и scope неизменяемы после создания; для смены владельца создаётся новая группа и выполняется явный перенос ссылок/доступов.
- Creator хранится только как provenance/audit metadata. Уход creator не влияет на существование и управление группой.
- Группа имеет монотонно возрастающую revision. Каждое изменение selectors, explicit members, labels/metadata, source references или sharing ACL создаёт новую revision.
- Удаление группы — tombstone/soft-delete до истечения retention и отсутствия ссылок из активных Plan/Run; активные immutable snapshots продолжают хранить собственные resolved targets.

### 3.2. Использование и видимость

Доступ к группе разделяется на:
- manage — изменять группу и её sharing;
- use — ссылаться на группу при построении Plan;
- view definition — читать описание selectors/members, если это разрешено политикой;
- view resolved targets — видеть конкретные объекты, только если пользователь/principal имеет право наблюдать их по каждому target.

Sharing ACL группы разрешает только соответствующую операцию с описанием группы. Он не предоставляет доступ к Kubernetes объектам, не является Kubron grant и не создаёт Cross-Team Delegation.

Для пользовательского интерфейса и API нельзя раскрывать сведения о недоступных объектах сверх минимального результата «target недоступен/не авторизован», если политика не разрешает видеть его metadata. Это предотвращает утечку имён, labels и состава чужих кластеров через общие группы.

### 3.3. Использование Organization Group

Organization Group может содержать targets разных Teams. При создании Plan каждая цель независимо проходит tenant, ownership, grants, delegation, Kubernetes RBAC и policy checks. Успешная авторизация одной Team не распространяется на другую.

## 4. Selector contract

До появления отдельного selector ADR действуют следующие нормативные правила:

- Поддерживаются только декларативные Kubernetes label selectors и явно перечисленные object references; произвольные выражения, скрипты, шаблоны с исполнением кода, внешние запросы и пользовательские функции запрещены.
- Field selectors разрешаются только для документированного allowlist полей и типов ресурсов; неизвестные поля отклоняются при валидации, а не интерпретируются как пустой результат.
- Selector парсится, валидируется и канонизируется серверным общим доменным кодом. Web и Desktop не имеют независимой реализации семантики.
- Неоднозначный, невалидный или неподдерживаемый selector блокирует создание новой revision/Plan.
- Selector всегда ограничен явным верхним scope группы и authorization scope инициатора/principal. Selector не может выбирать за пределами него.
- Resolution имеет ограничение по времени, количеству объектов, глубине/числу запросов и объёму ответа. Превышение лимита приводит к явной ошибке/blocked Plan, не к молчаливому усечению.
- Порядок targets детерминирован после canonical sort по Cluster ID, GVK, namespace, name, UID.
- Для воспроизводимости сохраняются canonical selector, group revision, resolution timestamp и provenance каждого target.

## 5. Consistent resolution и конкурентные изменения

Создание Plan выполняет разрешение группы относительно одной зафиксированной revision:

1. Прочитать Group ID, owner, revision и selector/member definition.
2. Зафиксировать revision как input конкретного resolution.
3. Разрешить targets и выполнить per-target authorization.
4. Повторно проверить, что текущая Group revision совпадает с revision из шага 1.
5. Если revision изменилась во время resolution, отбросить результат и повторить ограниченное число раз; после исчерпания попыток завершить конфликтом и предложить повторить Plan.
6. Сохранить immutable Target Snapshot и Plan атомарно в рамках специализированного domain backend contract.

Не допускается snapshot, который незаметно смешивает members/selectors из разных revisions. Для Web/PostgreSQL используются транзакции/conditional writes; для Desktop — сериализованный доступ и атомарная запись согласно существующим Store boundaries. Это не требует универсальной распределённой транзакции между Kubernetes API и Kubron Store.

## 6. Partial target resolution policy

Безопасность и предсказуемость важнее удобства. По умолчанию действует **all-or-nothing planning** для операций, способных менять состояние:

- Если хотя бы одна заявленная цель не разрешена, недоступна, не авторизована, не может быть однозначно идентифицирована или не проходит preconditions — весь mutating Plan блокируется.
- Система показывает инициатору безопасную сводку причин и counts; подробности о чужих targets раскрываются только при наличии права их видеть.
- Нельзя молча пропускать цели, подменять их похожими объектами, применять действие к «успешной части» или автоматически сужать approval set.
- Пользователь может явно изменить Target Group/scope либо выбрать видимое разрешённое подмножество и создать новый Plan; это новая операция планирования и новое approval.

Явный partial execution допускается только если конкретная Operation объявляет такую семантику в своём versioned contract:
- операция обязана быть безопасно независимой по target (нет скрытой атомарности/зависимостей между объектами);
- Plan явно разделяет accepted и rejected targets с причинами и фиксирует точный accepted snapshot;
- UI/API явно показывает частичный охват до approval;
- Approval привязан к accepted snapshot и partial-result semantics;
- runtime сообщает per-target outcome и общий статус `partially_succeeded`, не маскируя пропуски;
- повторная авторизация и preconditions остаются обязательными для каждого side effect.

Для Observe/read-only операций partial results разрешены по умолчанию, но недоступные цели должны быть обозначены как unknown/unavailable, а не как healthy/empty. Нельзя делать вывод «проблем нет» по неполному охвату.

## 7. Plan, approval и изменения после разрешения целей

- Target Snapshot содержит конкретные Cluster ID, GVK, namespace/name, UID, observed ResourceVersion, group revision, selector provenance и action-specific preconditions.
- Plan digest включает identity binding, Operation/Action, exact target snapshot, group revision, policy revision и параметры, влияющие на безопасность/результат.
- Approval относится только к этому digest и expiry.
- Изменение Group revision после snapshot не мутирует старый Plan. Оно не обязательно аннулирует уже разрешённый snapshot само по себе, но любые изменения актуального доступа, ownership, identity, policy, UID или action preconditions блокируют side effect. Для использования новой группы требуется новый Plan.
- При UID mismatch, неизвестном результате предыдущего side effect или нарушенных preconditions требуется fresh read/reconciliation; blind retry запрещён.
- Для каждого cluster sub-run создаётся отдельный snapshot, Plan, identity binding и authorization context.

## 8. Audit и lifecycle

Аудируются:
- создание/изменение/приостановка/отзыв/expiry Cross-Team Delegation;
- создание/изменение/sharing/deletion Target Group;
- попытки использовать недоступные targets (без утечки запрещённых metadata);
- resolution result: counts, revisions, digest, partial/full outcome;
- изменение ownership и автоматическая инвалидизация делегирования;
- решение authorization и его причины в пределах безопасного audit schema.

Audit не содержит Kubernetes secrets, токенов или расшифрованных credential data. Retention аудита независим от удаления Group, Run или Evidence.

## 9. Desktop/Web и отказоустойчивость

- Доменная семантика delegation, sharing, selectors, revisions и partial resolution едина для Web и Desktop.
- UI не является источником истины; backend повторно проверяет все полномочия.
- Web resolution и Plan creation должны быть устойчивы к нескольким server replicas через специализированные atomic domain contracts.
- Desktop использует тот же contract в пределах локального процесса; закрытие процесса не превращает незавершённое разрешение целей в подтверждённый Plan.
- Потеря связи, timeout или неизвестный результат записи не должны создавать второй Plan/Run без idempotency/reconciliation.

## 10. Инварианты

1. Cluster ownership остаётся у одной Team; ClusterGrant не вводится.
2. Только Team-владелец выдаёт/отзывает Cross-Team Delegation для собственных ресурсов.
3. Organization Admin управляет Service Identity, но не подменяет согласие Team.
4. Delegation адресовано конкретному ServicePrincipal и ограничено Operation, Action, Target Scope и сроком.
5. Target Group ownership, sharing, target visibility, grants и delegation — независимые понятия.
6. Ни группа, ни её sharing ACL не дают доступ к объектам.
7. Selector декларативен, ограничен scope, валидируется сервером и не исполняет код.
8. Resolution привязано к одной Group revision; конкурентное изменение приводит к retry/conflict, а не смешанному snapshot.
9. Mutating Plan по умолчанию all-or-nothing; partial execution требует явного versioned Operation contract и явного approval.
10. Partial Observe никогда не интерпретируется как полный охват.
11. Snapshot/Plan/Approval неизменяемы; актуальная authorization и preconditions проверяются перед каждым side effect.
12. Web и Desktop соблюдают одинаковые доменные правила.

## 11. Граница решения

Это решение закрывает продуктовую семантику cross-team delegation, Target Group ownership/sharing, selector safety, consistent resolution и partial target behavior. Оно не утверждает конкретную SQL/file schema, API DTO, UI, selector parser library или runtime implementation.

До начала реализации должны быть подготовлены отдельные контракты и review для:
- доменных интерфейсов Group/Delegation/Resolution и атомарных Store operations;
- точного selector allowlist и лимитов;
- Operation contract для безопасного partial execution;
- миграции и совместимости существующих identity/credential records;
- audit event schema и authorization decision provenance.

Ни одно из этих решений не разрешает обход ADR-0012, создание общего ClusterGrant или начало runtime write-action implementation без отдельного implementation plan/review.
