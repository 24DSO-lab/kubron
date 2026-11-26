# Execution Identity & Durable Operations — design proposal

- Статус: проектирование, не утверждённый ADR
- Базовая ветка: `refactor`
- Связано: ADR-0005, 0007, 0009, 0012, 0016, 0018, 0022, 0026; целевая архитектура Kubron A+B+C
- Назначение: определить безопасную модель идентичности исполнителя и долговременного жизненного цикла операций до начала runtime-реализации.

## 1. Краткое решение

Kubron должен поддерживать интерактивные и автоматические операции через единый доменный контракт, но не смешивать идентичность инициатора с идентичностью, от имени которой выполняется действие в Kubernetes.

**Автоматическое выполнение не наследует личную Kubernetes-идентичность пользователя.** Оно использует явно назначенную credential identity типа `team` или `service`. Личные credentials предназначены для интерактивных операций. Исключение возможно только после отдельного архитектурного решения, которое определяет безопасное хранение, отзыв, срок действия и повторную авторизацию.

Базовые сущности:

- `Operation` — определение задачи/намерения, которое можно запустить.
- `Run` — конкретный запуск Operation.
- `Step` — отдельный исполняемый шаг Run.
- `Plan` — неизменяемый план конкретного Run.
- `Approval` — подтверждение конкретной версии Plan.
- `ExecutionIdentity` — субъект, чьи credentials используются для Kubernetes API.
- `TriggerIdentity` — субъект/механизм, инициировавший запуск.
- `EvidenceReference` — ссылка на данные, использованные при планировании и проверке.
- `Lease` — временное владение исполнением Run или его Step.

Это логические доменные сущности. Не следует заранее сводить их в одну универсальную таблицу или один универсальный DTO.

## 2. Термины и разделение идентичностей

### 2.1. Четыре независимых измерения

| Измерение | Вопрос | Пример |
|---|---|---|
| Actor / Initiator | Кто запросил или инициировал запуск? | user ID, scheduler, system trigger |
| Execution identity | Чьи Kubernetes credentials используются? | team credential, service identity |
| Authorization context | Какие действия разрешены сейчас? | app-RBAC, tenant membership, effective autonomy policy |
| Runtime owner | Какой процесс сейчас владеет исполнением? | Desktop process ID или server replica ID + fencing token |

Ни одно из этих измерений не выводится автоматически из другого.

Инициатор сохраняется для аудита и объяснения происхождения запуска. Он не становится автоматически владельцем credentials и не получает права на выполнение через сам факт создания Run.

### 2.2. Типы ExecutionIdentity


**User identity**
- Связана с конкретным пользователем и его персональными Kubernetes credentials.
- Допускается для интерактивной операции, пока пользовательская сессия и права действительны.
- Не используется расписаниями, системными триггерами и автономными запусками.
- Не копируется и не преобразуется в team/service identity.

**Team identity**
- Явно настроенная техническая учётная запись команды.
- Владелец и область действия определяются каталогом Kubron.
- Подходит для расписаний команды и автоматических операций в пределах разрешённых кластеров.
- Её права в Kubernetes могут быть шире или уже прав отдельного пользователя; поэтому app-RBAC и policy enforcement Kubron обязательны независимо от Kubernetes RBAC.

**Service identity**
- Идентичность конкретного системного компонента/сценария с отдельным назначением и ограниченным scope.
- Не должна быть универсальной учётной записью с административными правами.
- Создание, изменение, ротация и отзыв управляются явными административными процедурами и аудитом.
- Service identity не означает обход app-RBAC, tenant isolation или autonomy policy.

**Модель каталога и управления (принятое решение)**
- `Team identity` и `Service identity` — логически разные типы с разными назначением, scope и правилами использования.
- Они используют общую существующую подсистему хранения секретов, credential resolution, ротации и отзыва. Две независимые системы управления secrets/credentials не создаются.
- Organization Admin управляет организационными Service Identity.
- Team Admin управляет Team Identity своей команды.
- Эти административные роли сами по себе не обходят app-RBAC, tenant boundaries и audit; каждая операция управления identity должна отдельно авторизоваться и журналироваться.

### 2.3. Матрица допустимости

| Тип запуска | Actor | Execution identity |
|---|---|---|
| Интерактивный запуск из UI | Пользователь | User identity или явно выбранная Team identity, если продуктовый сценарий это допускает |
| Пользовательский Run, продолжающийся после закрытия UI | Пользователь | Только явно назначенная Team/Service identity |
| Расписание команды | Scheduler + владелец расписания | Team/Service identity, закреплённая в конфигурации расписания |
| Системный trigger | System component + причина trigger | Service identity, ограниченная назначением trigger |
| Автономный запуск по policy | Policy engine / trigger + исходное событие | Явно разрешённая Team/Service identity |
| Запуск без определённой identity | Любой | Запрещён для write actions; Observe/read-only допускается только по обычным правилам доступа |

В интерактивном сценарии использование user credentials требует активной сессии и повторной проверки полномочий перед каждым side effect. Если операция должна пережить сессию, до постановки в очередь она обязана иметь подходящую Team/Service identity; иначе такой переход отклоняется с понятной причиной.

### 2.4. Credential binding

Run фиксирует ссылку на credential identity, а не секрет и не копию kubeconfig.

Для multi-cluster Run действует обязательное правило: **каждый cluster-scoped sub-run имеет собственную явно назначенную Execution Identity**. Identity назначается и фиксируется до создания Plan соответствующего sub-run. Она не наследуется от parent Run и не подменяется автоматически. В пределах одного sub-run identity не меняется; смена identity требует нового Plan и нового approval. Каждый sub-run независимо проходит tenant, cluster access, policy и credential checks.

Credential binding содержит как минимум:
- тип identity и её стабильный ID;
- tenant scope и разрешённые cluster scope;
- ссылку на версию/ревизию credentials или иной механизм обнаружения ротации;
- момент выбора identity;
- политику поведения при отзыве/ротации;
- provenance: кто и каким правилом назначил identity.

Секреты остаются в существующей подсистеме credentials/secret storage (ADR-0008, ADR-0018). Они не копируются в Plan, Run payload, события, логи или audit. Все новые credential records для Service Identity обязаны использовать envelope encryption и key lifecycle ADR-0008; должны входить в rekey/keycheck, backup/restore и проверки отсутствия plaintext. Общая подсистема означает общие механизмы защиты и выдачи, а не смешение владельцев или credential scope.

Перед каждым side effect Kubron повторно разрешает identity и получает актуальную конфигурацию подключения через существующий credential path.

ADR-0018 устанавливает единственную точку получения Kubernetes config — `ConfigFor(session, cluster)` — и запрещает обходить её. Фоновая модель должна сохранить этот инвариант: требуется расширить credential-resolution boundary явным типизированным subject/context для User, Team и Service identity (либо эквивалентным узким интерфейсом), чтобы resolver мог безопасно работать без интерактивной user session. Нельзя создавать фиктивную пользовательскую Session, напрямую читать расшифрованные credentials из Store или строить `rest.Config` в обход credential providers, token refresh, subject isolation и security checks ADR-0018. Конкретное имя/сигнатура нового метода фиксируется отдельным ADR после анализа текущего `ConfigFor` и его вызовов.

### 2.5. Отзыв и изменение прав

- Удаление пользователя или завершение его сессии немедленно запрещает новые интерактивные действия от его имени.
- Отзыв User identity не может быть компенсирован автоматической подменой Team identity.
- Отзыв Team/Service identity блокирует все ещё не начатые side effects, использующие эту identity.
- При ротации credentials Run не продолжает действие со старой конфигурацией автоматически: он повторно разрешает актуальную версию и проверяет, не изменились ли effective permissions.
- Если identity удалена, недоступна или её scope изменился, Run переходит в состояние ожидания вмешательства/отмены; секреты не восстанавливаются из старого Run.
- Уже отправленный запрос к Kubernetes API может завершиться после отзыва. Kubron фиксирует неопределённый исход и делает fresh read/reconciliation, а не утверждает, что side effect гарантированно остановлен.

## 3. Модель Operation / Run / Step / Plan

### 3.1. Operation

Operation — описание запускаемой возможности, а не история выполнения.

Содержит:
- стабильный ID и версию определения;
- домен-владелец и тип операции;
- допустимые trigger types;
- типизированную схему входных параметров;
- требования к scope и execution identity;
- классификацию read/write и риск;
- поддержку отмены, retry и recovery;
- ссылки на handler/action definitions.

Operation definition версионируется. Изменение семантики или схемы входных параметров создаёт новую версию. Исторический Run сохраняет версию, с которой был создан.

### 3.2. Run

Run — долговременная запись одного запуска.

Минимальные атрибуты:
- `run_id`, `operation_id`, `operation_version`;
- organization/team scope и конкретные cluster/target references;
- инициатор/trigger identity;
- execution identity binding;
- autonomy mode и policy revision на момент планирования;
- plan digest и версия action catalog;
- состояние, timestamps, deadline;
- idempotency key;
- correlation ID;
- итоговый результат и ссылка на audit trail.

Run не содержит credentials, секретов и полных диагностических payloads. Чувствительные evidence сохраняются по правилам домена с отдельными правами и retention.

### 3.3. Step

Каждый Step — типизированное действие с:
- стабильным ID в рамках Plan;
- action type и версией;
- target identity;
- входными параметрами, очищенными от секретов;
- preconditions и postconditions;
- risk classification и blast-radius contribution;
- retry policy;
- cancellation behavior;
- recovery/compensation definition либо явным указанием, что компенсация отсутствует.

Статус Step не может быть выведен только из статуса внешнего HTTP-запроса. Он должен отражать подтверждённый результат Kubernetes API и последующей проверки.

### 3.4. Plan

Plan неизменяем после публикации для approval/execution. Он включает:
- цель и scope;
- точный snapshot targets;
- упорядоченные шаги и зависимости;
- нормализованные параметры действий;
- EvidenceReferences с source, observed_at и freshness limits;
- preconditions/postconditions;
- risk, blast radius и лимиты;
- action catalog revision;
- policy revision и effective decision;
- execution identity binding;
- approval requirements;
- deadline и expiry.

Изменение любого существенного компонента создаёт новый Plan revision/digest. Старое approval не переносится.

### 3.5. Approval

Approval связывается с:
- plan digest;
- target snapshot;
- policy revision;
- execution identity binding;
- сроком действия;
- субъектом, принявшим решение;
- набором одобренных действий/ограничений.

Перед каждым side effect approval проверяется повторно. Approval не является бессрочным разрешением и не заменяет повторную authz/policy evaluation.

Если план изменился, targets изменились, execution identity изменилась, policy стала строже или approval истёк — требуется новое решение. Политика может разрешить автоматическое продолжение при менее строгих изменениях только после отдельного определения семантики; безопасное значение по умолчанию — остановка и пересмотр.

## 4. State machine

### 4.1. Run lifecycle

Предлагаемые состояния:

- `draft` — подготовка, не исполняется.
- `planned` — immutable Plan создан.
- `awaiting_approval` — требуется подтверждение.
- `approved` — approval действителен, можно claim-ить исполнение.
- `queued` — ожидает исполнителя/ресурсный лимит.
- `running` — есть действующий lease.
- `verifying` — шаги завершены, проверяются postconditions.
- `succeeded` — все обязательные postconditions подтверждены.
- `partially_succeeded` — часть независимых целей/шагов выполнена, часть нет.
- `failed` — ошибка с известным неуспешным исходом.
- `blocked` — policy, identity, precondition или guardrail запрещает продолжение.
- `cancel_requested` — запрошена отмена; результат side effect ещё может быть неизвестен.
- `cancelled` — отмена подтверждена, незавершённых неизвестных side effects нет.
- `interrupted` — исполнитель прекратил работу; состояние требует reconciliation.
- `unknown` — достоверно определить исход одного или нескольких side effects пока невозможно.
- `expired` — Plan/approval/deadline истёк до завершения.

Не каждое состояние обязано быть доступно пользователю как отдельный ярлык. Внутренние статусы должны сохранять точность, а UI может группировать их без потери семантики.

### 4.2. Step lifecycle

Step должен различать как минимум:
- `pending`;
- `claimed`;
- `precondition_check`;
- `executing`;
- `verification_pending`;
- `succeeded`;
- `failed`;
- `blocked`;
- `retry_wait`;
- `cancel_requested`;
- `interrupted`;
- `unknown`;
- `compensating`;
- `compensated`;
- `compensation_failed`.

Переходы фиксируются атомарно в durable storage. Нельзя считать Step завершённым только потому, что worker перестал его выполнять.

### 4.3. Cancellation

Отмена — кооперативная, а не обещание прервать уже отправленный запрос.

- До side effect исполнитель может безопасно остановить Step.
- Во время сетевого запроса отмена контекста может прекратить ожидание, но не доказывает, что API server не применил изменение.
- После неопределённого исхода Step получает `unknown`/ `interrupted` и требует fresh read.
- Run становится `cancelled` только после разрешения неопределённостей; иначе сохраняет `unknown` или `partially_succeeded`.

## 5. Execution pipeline и повторные проверки

Общий цикл:

`Observe → Understand → Decide → Approve (if required) → Claim → Revalidate → Execute → Verify → Recover/Escalate → Audit`

Перед каждым side effect worker обязан повторно проверить:
1. tenant и принадлежность кластера;
2. app-RBAC текущего пользователя для интерактивного действия. Для фонового/автоматического исполнения полномочия инициатора не являются источником execution authorization: проверяются отдельные Kubron permissions/grants назначенной Execution Identity, актуальная tenant policy и доступ к каждому target. Это требует явной модели authorization principal для Team/Service identity; Kubernetes credentials сами по себе не предоставляют права в Kubron;
3. актуальность execution identity и credential binding;
4. Kubernetes RBAC/фактическую возможность действия;
5. текущий autonomy mode и policy revision;
6. действительность approval и его привязку к digest;
7. target UID и preconditions;
8. deadline, rate/action budget и blast-radius limits;
9. владение актуальным lease/fencing token;
10. отсутствие уже подтверждённого результата или эквивалентного side effect.

Проверки выполняются backend-ом. UI не является доверенной границей безопасности.

### 5.1. Результат и verification

Успех означает подтверждённые свежим чтением postconditions, а не только успешный ответ Kubernetes API.

Verification различает:
- подтверждено;
- не подтверждено;
- неизвестно из-за доступа/недоступности источника;
- частично подтверждено;
- deadline истёк до проверки.

Для объектов используется стабильная идентичность: cluster ID + GVK + namespace/name + UID. `resourceVersion` — маркер наблюдавшегося состояния, а не постоянная идентичность. Удаление и пересоздание объекта с тем же именем не должно удовлетворять старый target reference.

## 6. Durable storage и backend parity

### 6.1. Принцип

Следовать ADR-0007 и ADR-0026:
- узкие доменные backend interfaces;
- File Store для Desktop;
- PostgreSQL для Web/server;
- одинаковая семантика контрактов в обоих режимах;
- отдельные реализации и контрактные тесты;
- без универсального CRUD Store и без собственной независимой БД модуля.

Operation, Run, Step, Plan, Approval и lease state — разные логические агрегаты/границы атомарности. Конкретное физическое разбиение таблиц/документов должно следовать инвариантам ниже, а не стремлению к одной универсальной таблице.

### 6.2. Атомарность

Необходимы атомарные доменные операции:
- создать Run вместе с его immutable Plan и исходным state;
- атомарно claim-ить Run/Step и выдать lease;
- записать переход состояния вместе с fencing token;
- сохранить результат side effect до перехода к следующему шагу;
- принять/отклонить approval только для ожидаемого digest/revision;
- запросить cancellation без потери текущего execution state;
- завершить Run только после согласованной проверки всех обязательных Step.

Для PostgreSQL это транзакции с подходящей блокировкой/условным UPDATE. Для File Store — сериализованный доступ в одном процессе и атомарная запись документа/журнала с чёткой точкой commit. Нельзя утверждать, что файловая реализация поддерживает межпроцессную конкуренцию: Desktop runner работает в одном процессе приложения.

### 6.3. История и retention

- История Run/Step должна быть доступна после перезапуска и из любой Web replica.
- Для Run/Step history применяется настраиваемая retention policy на уровне Organization. Конкретные сроки и классы данных задаются политикой, а не зашиваются в доменные обработчики.
- Audit остаётся отдельным потоком существующей audit subsystem/Store согласно ADR-0007 и ADR-0022 (stdout mirror не является источником истины) и подчиняется отдельным обязательным правилам хранения/retention. Очистка Run history или Evidence не должна удалять обязательный audit trail.
- Evidence хранится по правилам домена-владельца; Run содержит ссылки и метаданные происхождения. Retention Evidence согласуется с организационной политикой и требованиями домена.
- Retention не удаляет незавершённые Run, активные approvals или записи, необходимые для reconciliation.
- Удаление/архивация завершённых Run не должно удалять обязательный audit trail.
- В Desktop поддерживаемый объём истории может быть меньше, но state machine и смысл статусов совпадают с Web; организационная retention policy должна применяться с учётом возможностей локального хранения.

## 7. Desktop и Web execution lifecycle

### 7.1. Desktop

- Runner живёт внутри процесса Kubron Desktop, но отделён от UI/window lifecycle.
- Сворачивание окна, неактивность приложения и блокировка пользовательской сессии ОС не останавливают Run.
- Отдельный daemon/service не вводится.
- Завершение процесса, logout ОС, shutdown/reboot или crash прекращают локальное исполнение.
- Sleep/hibernation физически приостанавливает процесс; Kubron не обещает исполнение во время сна.
- При следующем запуске runner загружает durable state, находит незавершённые Run и сначала выполняет reconciliation.
- Step с неизвестным результатом не повторяется слепо. Повтор разрешён только после проверки фактического состояния и action-specific retry rules.
- В File Store нет межпроцессного claim/lease: единственный владелец исполнения — текущий процесс Desktop.

### 7.2. Web/server

- Runner принадлежит серверной платформе, а не HTTP request, WebSocket или браузерной сессии.
- Закрытие вкладки, потеря сети и истечение UI connection не останавливают Run.
- Durable state находится в PostgreSQL и доступен любой реплике.
- В каждый момент времени side effects может исполнять только владелец действующего lease.
- После потери реплики другой worker может claim-ить просроченный Run только после проверки предыдущего Step и fencing state.
- События для UI — уведомления/подсказки; UI всегда может восстановить состояние чтением durable API.

### 7.3. Единая доменная семантика

Web и Desktop используют общие модели Operation/Run/Step/Plan/Approval и одинаковые переходы состояний. Различаться могут:
- транспорт и platform adapters;
- способ запуска процесса;
- доступные credential providers;
- наличие server HA;
- лимиты и поддерживаемая глубина истории.

Различия должны быть явно описаны как platform capability, а не приводить к разной семантике безопасности или результата операции.

## 8. HA, leases и fencing

### 8.1. Lease

Lease должен включать:
- run/step ID;
- owner replica/process ID;
- expiry;
- монотонный fencing token;
- heartbeat/renewal metadata.

Claim/renew/release — атомарные операции durable backend. В PostgreSQL lease expiry рассчитывается по времени БД, чтобы избежать расхождения часов реплик. В Desktop lease локален одному процессу и не притворяется распределённой блокировкой.

### 8.2. Fencing

Каждая успешная выдача нового lease увеличивает fencing token. Любая запись состояния worker-а условно принимается только если token совпадает с текущим.

Важно: Kubernetes API не проверяет fencing token Kubron. Поэтому fencing защищает durable state и предотвращает дальнейшие шаги устаревшего worker-а, но сам по себе не может отменить уже отправленный Kubernetes запрос. Для внешнего side effect нужны:
- короткий участок между последней проверкой lease и отправкой запроса;
- action-specific idempotency/reconciliation;
- повторная проверка target/preconditions;
- фиксация результата как unknown, если запрос мог примениться, но ответ потерян.

Нельзя описывать это как строгую гарантию fencing внешнего API.

### 8.3. Exactly-once

Exactly-once для side effects во внешнем Kubernetes API не обещается. Целевая модель — durable at-least-once attempts + action-specific idempotency + fresh reads + reconciliation. Для неидемпотентных действий retry по умолчанию запрещён, пока результат предыдущей попытки не установлен.

## 9. Target Groups, tenant boundaries и immutable target snapshot

- Target Group — логическая выборка объектов/кластеров, не grant и не механизм выдачи доступа.
- Межкластерная группа может ссылаться на кластеры разных команд только в рамках явно разрешённой организационной модели; сама группа не расширяет права пользователя.
- ADR-0012 сегодня закрепляет один кластер за одной командой и не вводит ClusterGrant. Это ограничение остаётся в силе, пока отдельный ADR не изменит его.
- При разрешении группы каждый target независимо проходит tenant, app-RBAC, cluster membership и Kubernetes access checks.
- Plan сохраняет immutable target snapshot: cluster ID, GVK, namespace/name, UID и нужные precondition markers.
- Изменение состава группы после Plan не меняет уже созданный Plan. Для нового состава нужен новый Plan и approval.
- Потеря доступа к одному target блокирует соответствующий sub-run; она не даёт права пропустить проверку или использовать credentials другого tenant.
- Cross-cluster execution — parent Run с независимыми cluster-scoped sub-runs. Каждый sub-run получает собственную явно назначенную Execution Identity до создания своего Plan; identity одного кластера не переносится на другой. Distributed transaction не имитируется; partial success остаётся видимым.

## 10. Security invariants

1. Автоматический Run использует только явно назначенную Team/Service identity.
2. Отсутствие или неоднозначность identity блокирует write action.
3. User identity не подменяется автоматически Team/Service identity.
4. Trigger identity, initiator и execution identity хранятся раздельно.
5. Секреты никогда не входят в Plan, Run, Step, events или audit payload.
6. Credentials разрешаются через существующий credential subsystem; обход `ConfigFor`/approved credential path запрещён.
7. App-RBAC, tenant isolation, Kubernetes RBAC и autonomy policy — независимые проверки.
8. Любое отсутствие/ошибка policy, authz, Store, lease или verification работает fail-closed для side effects.
9. Approval привязан к точному Plan digest, target snapshot, policy revision, identity binding и expiry.
10. UI не может выдать себе права и не является источником истины для approval/state.
11. Никакого произвольного shell, kubectl или AI-generated command execution.
12. Старый worker с истёкшим fencing token не может менять durable state или начинать следующий Step.
13. Неизвестный результат side effect не повторяется слепо.
14. Audit фиксирует trigger, initiator, execution identity reference, policy decision, approval, plan digest, шаги и результат без секретов.
15. Platform capabilities, cluster capabilities и effective permissions не смешиваются.

## 11. Предлагаемые package boundaries

Это ответственность, а не приказ немедленно создать все пакеты:

| Область | Ответственность |
|---|---|
| Operation Catalog | Версионированные Operation/Action definitions |
| Execution Identity Resolver | Разрешение identity и безопасная выдача credentials через существующий путь |
| Planning | Plan, target snapshot, evidence references, digest |
| Policy | Autonomy mode, policy evaluation, constraints |
| Approval | Approval lifecycle и digest binding |
| Execution | Run/Step state machine, claim, lease, fencing, retries |
| Verification | Fresh reads, postconditions, reconciliation |
| Recovery | Compensation, escalation, unknown outcomes |
| Persistence adapters | Узкие доменные backend interfaces для File Store и PostgreSQL |
| Platform adapters | Desktop runner и server runner; общий доменный контракт |

Доменные модули не должны импортировать `internal/app` или `internal/server` ради доступа к инфраструктуре. Composition root связывает сервисы через интерфейсы. Конкретный import graph фиксируется после анализа текущих пакетов и до реализации.

## 12. Порядок реализации после утверждения дизайна

### Phase 1 — Contracts and read-only Run

- Operation/Run/Step identifiers и типизированные DTO.
- Execution identity references без выдачи новых прав.
- Durable read-only Run history.
- Backend contract tests на File Store и PostgreSQL.
- Web/Desktop отображают одну и ту же семантику.
- Никаких Kubernetes write actions.

### Phase 2 — Assisted read/diagnostic workflows

- Immutable Plan и evidence references.
- Approval lifecycle для read/diagnostic workflows, где нужен контроль.
- Отмена, deadline, audit и восстановление UI после reconnect.
- Нагрузочные и конкурентные тесты.

### Phase 3 — Controlled write actions

- Ограниченный typed action catalog.
- Явная Team/Service identity binding.
- Повторная authz/policy/precondition validation.
- Lease/fencing и action-specific idempotency.
- Verification и безопасная обработка unknown outcomes.
- Только Assisted mode на первом выпуске.

### Phase 4 — Policy-based Autonomy

- Observe/Assisted/Autonomous enforcement.
- Profiles, schedules и system triggers.
- Per-action approval, budgets, time windows.
- Ограниченный набор проверенных actions.
- Постепенное расширение после security/recovery review.

### Phase 5 — Cross-cluster and goal-based workflows

- Parent/child Runs и target groups.
- Волны, concurrency budgets, partial success.
- Goal decomposition только в typed actions и deterministic policy checks.

## 13. Обязательные acceptance criteria до первого write action

- Нельзя создать автоматический Run без явной Team/Service identity.
- Нельзя исполнить Run после отзыва identity или потери tenant/cluster access.
- Нельзя продолжить Run по изменённому Plan со старым Approval.
- Нельзя начать Step с устаревшим lease/fencing token.
- Crash/restart Desktop не приводит к слепому повтору неизвестного side effect.
- Потеря server replica не приводит к двум действующим владельцам одного Step.
- Два конкурентных claim-а не могут получить один и тот же актуальный fencing token.
- File Store и PostgreSQL проходят одинаковые доменные contract tests.
- Web и Desktop дают одинаковые state transitions и security decisions.
- Закрытие браузера не прекращает server Run; закрытие окна Desktop не прекращает Run, пока процесс приложения жив.
- Sleep/shutdown/process termination не описываются как поддерживаемое время исполнения.
- Audit содержит достаточные ссылки для расследования без хранения секретов.
- Для каждого action документированы idempotency, verification, retry и recovery semantics.

## 14. Закрытые архитектурные решения

Все вопросы, перечисленные в предыдущей редакции этого раздела, рассмотрены и закрыты следующими решениями.

### 14.1. Execution Identity для cross-cluster Run

- Parent Run не обязан иметь одну общую Kubernetes Execution Identity.
- Каждый cluster-scoped sub-run использует отдельную явно назначенную Execution Identity.
- Identity назначается до создания Plan конкретного sub-run и включается в его immutable Plan/approval binding.
- Identity не наследуется автоматически от инициатора или соседнего sub-run.
- В пределах одного sub-run identity неизменна. Её изменение требует нового Plan и повторного approval.
- Каждый sub-run независимо проходит authorization, tenant, cluster access, policy и credential validation.

### 14.2. Управление Team/Service Identity

- Organization Admin управляет организационными Service Identity.
- Team Admin управляет Team Identity своей команды.
- Team и Service — логически разные типы identity, но используют общую подсистему хранения и выдачи credentials.
- Это расширяет текущую модель ADR-0018 (`user` / `team`) новым логическим типом Service; это не повод вводить отдельный secrets subsystem или обходить credential providers.
- Все операции создания, изменения, ротации, отзыва и назначения identity подлежат app-RBAC checks и audit.
- Это решение не создаёт вторую независимую систему secret/credential management.

### 14.3. Права инициатора после создания автоматического задания

- Автоматическое выполнение не зависит от сохранения текущих прав инициатора.
- Инициатор сохраняется как provenance/audit metadata, но не является execution principal для фонового Run.
- При запуске и перед каждым side effect проверяются актуальные tenant policy, права и состояние назначенной Team/Service Execution Identity, доступ к target и effective autonomy policy.
- Потеря инициатором роли или членства сама по себе не отзывает техническую identity и не останавливает задание.
- Отзыв/изменение самой Execution Identity, tenant access или policy блокирует дальнейшие side effects.
- Интерактивные действия от User identity продолжают требовать действующую пользовательскую сессию и актуальную авторизацию.

### 14.4. Где исполняются расписания

- Desktop schedules локальны и исполняются только пока запущен процесс Kubron Desktop.
- Сворачивание окна, неактивность приложения и блокировка ОС не останавливают задания.
- Отдельный Desktop daemon/service не вводится. Завершение процесса, logout ОС, shutdown/reboot или crash прекращают локальное исполнение; sleep/hibernation физически приостанавливает его.
- Web schedules исполняются серверной платформой независимо от браузера, UI connection и WebSocket.
- Синхронизация расписаний между Desktop и Web не входит в это решение и требует отдельного проектирования.

### 14.5. Retention

- Run/Step history и связанные с ними данные управляются настраиваемой retention policy на уровне Organization.
- Политика должна определять сроки/классы хранения и учитывать незавершённые Runs, активные approvals и данные, необходимые для reconciliation.
- Audit — отдельный поток с отдельными обязательными правилами хранения. Очистка Run/Step history или Evidence не может удалять обязательные audit records.
- Точные сроки, legal hold, экспорт и поведение при удалении Organization требуют отдельной детализации при проектировании retention implementation; они не блокируют принятие этой архитектурной модели.

### 14.6. Тип Service Identity

- Service Identity — отдельный логический тип, а не неявный alias Team Identity.
- Team и Service Identity используют общие credential storage/resolution/rotation/revocation mechanisms.
- Различия типов выражаются в назначении, владельце, scope, допустимых trigger types и правилах управления, а не в дублировании secret subsystem.


## 15. Проверка согласованности с действующими ADR

Этот раздел фиксирует обязательные границы интеграции. Он не заменяет и не переписывает принятые ADR.

### 15.1. ADR-0007 и ADR-0026 — Store и доменная атомарность

- Run/Step/Plan/Approval/Lease должны получить собственные узкие domain Backend interfaces и методы доступа через существующий Store composition. Универсальный CRUD Store или общий API транзакций для всех доменов не вводится.
- Атомарные переходы (claim, fencing, approval binding, state transition) реализуются как специализированные атомарные операции соответствующего backend. PostgreSQL использует транзакции/conditional updates; Desktop File Store — сериализованный доступ и атомарную запись в одном процессе.
- Оба backend проходят один набор domain contract/concurrency tests. Поддержка HA и межрепличного claim относится к PostgreSQL/server; Desktop не получает межпроцессную конкуренцию.
- Новые таблицы/записи с чувствительными credential/evidence payloads должны учитывать правила шифрования и rekey ADR-0008.

### 15.2. ADR-0008 и ADR-0018 — Credentials и ConfigFor

- Существующие credential providers, subject isolation, token refresh, challenge flow и secret encryption остаются единственным путём работы с Kubernetes credentials.
- Текущий `ConfigFor(session, cluster)` — security boundary, а не просто helper. Background execution требует явного subject-aware расширения credential resolver, а не прямого доступа к Store и не фиктивной пользовательской Session.
- Service Identity добавляет логический тип к существующим User/Team credential scopes. Формат хранения/каталог может быть расширен, но envelope encryption, keyring/KMS, rekey, rotation, revocation и redaction остаются общими.
- Статические team credentials и интерактивные user credentials сохраняют текущую семантику ADR-0018. Автоматический Run использует только заранее назначенную Team/Service identity; User identity автоматически не подставляется.
- Успешная выдача Kubernetes config не заменяет Kubron app authorization. И наоборот, Kubron permission не доказывает Kubernetes RBAC.

### 15.3. ADR-0012 — Tenant isolation и cross-team targets

- Organization Admin, Service Identity или Target Group не получают доступ к кластеру автоматически.
- Каждый cluster sub-run разрешается в контексте команды-владельца кластера и собственной Execution Identity, имеющей допустимый scope для этого кластера.
- Существующее правило «один кластер — одна команда», отсутствие ClusterGrant и отдельная операция переноса кластера сохраняются.
- Межкомандный parent Run допустим только как оркестрация независимых sub-runs, каждый из которых отдельно проходит tenant membership/policy, identity grants, app authorization и Kubernetes RBAC. Группа целей не расширяет права.

### 15.4. ADR-0016 и ADR-0026 — HA и фоновые задачи

- Существующий `internal/jobs` и PostgreSQL advisory locks можно использовать для периодических служебных циклов (например, проверки расписаний, retention cleanup), но не как хранилище Run/Step и не как durable user-operation queue.
- Durable Run ownership, claim, lease и fencing — отдельный доменный механизм с состоянием в PostgreSQL. `LISTEN/NOTIFY` и Event Hub остаются уведомлениями, не источником истины.
- Desktop runner остаётся внутри процесса приложения; локальный File Store не притворяется распределённым lease backend.
- Web schedule trigger должен быть HA-safe: повторный опрос/запуск scheduler tick не может создать дублирующий Run для одного occurrence. Для этого нужны durable schedule occurrence identity/idempotency и атомарное создание Run.
- Правила missed schedule occurrences (catch-up, skip, coalesce) должны быть явно определены до реализации; нельзя автоматически воспроизводить пропущенные write actions без policy/approval revalidation.

### 15.5. ADR-0022 — Audit, logs и metrics

- Audit остаётся отдельной долговременной audit subsystem и Store domain; stdout mirror — только дополнительная доставка, не retention backend.
- Run/Step history, Evidence и Audit имеют разные retention policies и права чтения. Удаление истории выполнения не удаляет audit.
- Audit фиксирует actor/trigger, execution identity reference, cluster/team scope, policy decision, approval, plan digest, action/step outcome и correlation IDs без секретов и полных диагностических payloads.
- Метрики не получают user IDs, cluster names, namespace, resource names, credential IDs или иные высококардинальные/чувствительные значения в labels. Для диагностики используются агрегированные фиксированные категории.

### 15.6. Итог проверки

Архитектурная модель совместима с указанными ADR при соблюдении границ выше. Это расширение существующих контрактов, а не замена Store, credential providers, tenancy, audit или HA foundation. Перед runtime implementation остаются обязательными отдельные решения по subject-aware credential resolver, authorization principal для Team/Service identity, durable schedule semantics и domain-specific atomic Store contracts.


## 16. Что этот документ не утверждает

- Это design proposal, не утверждённый ADR и не разрешение начать write-action implementation.
- Он не вводит отдельный daemon для Desktop.
- Он не меняет текущую модель tenant ownership ADR-0012.
- Он не вводит новый message broker, cluster agent или обязательные CRD.
- Он не обещает exactly-once для Kubernetes side effects.
- Он не заменяет существующие ADR по credentials, Store, HA, audit и platform adapters.
- Он не разрешает автономные действия без отдельной policy, identity, approval и recovery модели.
