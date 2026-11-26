# Scope and Target Groups — принятое архитектурное решение

- Статус: принято владельцем продукта, 2026-11-26
- Область: Kubron authorization grants, Target Groups, Plan resolution и durable operations
- Связано: ADR-0012, ADR-0018, `execution-identity-durable-operations-design.md`, `2026-11-26-technical-identity-authorization-grants-decision.md`

## Контекст

Техническим Team/Service principals нужны безопасные и выразительные ограничения на объекты Kubernetes. При этом набор целей может включать ресурсы нескольких кластеров и команд. Описание набора объектов не должно превращаться в механизм выдачи доступа, а одобрение операции не должно незаметно распространяться на объекты, появившиеся или изменившиеся после планирования.

ADR-0012 сохраняет модель Organization → Team → Cluster: кластер принадлежит одной команде, а произвольный ClusterGrant не вводится. Это решение не меняет указанную модель владения.

## 1. Разделение Scope, Target Group и Target Snapshot

Это три самостоятельных понятия:

- **Scope** — максимальная область полномочий principal/grant: где и над какими типами объектов он в принципе может выполнять разрешённые действия.
- **Target Group** — версионируемое описание набора потенциальных целей, сформированное selectors и/или явными ссылками на объекты.
- **Target Snapshot** — неизменяемый набор конкретных объектов, разрешённых при создании Plan для конкретного Run или cluster-scoped sub-run.

Target Group отвечает на вопрос «какие объекты выбрать», но не на вопрос «имеет ли субъект право действовать с ними». Snapshot фиксирует конкретные цели Plan, но также не является разрешением на действие.

## 2. Scope

Scope может ограничивать доступ по сочетанию следующих измерений:

- Organization;
- Team;
- Cluster;
- Namespace;
- Kubernetes Group/Version/Kind (GVK);
- labels/selectors;
- точный объект, идентифицируемый кластером, GVK, namespace/name и UID.

Примеры гранулярности:
- Observe для всех кластеров конкретной Team;
- Restart только Deployment в namespace или окружении development;
- Scale только перечисленные StatefulSet;
- запрет destructive actions через ограничения grant или обязательную Autonomy Policy.

Scope выражается как ограничение полномочий. Более низкий уровень или более узкий selector может только сузить родительский scope, но не расширить его. Эффективный scope остаётся пересечением grants, максимального scope identity, tenancy/ownership, Kubernetes RBAC и Autonomy Policy.

Отсутствие scope, невозможность его разрешить или неоднозначность трактуются fail-closed.

## 3. Target Groups

Target Group — самостоятельная версионируемая сущность с владельцем. Поддерживаются следующие области владения:

- **Organization Group** — может описывать наборы объектов из нескольких Teams и clusters одной Organization.
- **Team Group** — ограничена кластерами и объектами Team-владельца согласно ADR-0012.
- **Custom Group** — пользовательская комбинация явных объектов и selectors; может быть межкластерной и межкомандной по составу, если её владелец и политика допускают такое описание.

Custom Group — способ выразить набор целей, а не новый security principal и не grant. Наличие объекта в группе не предоставляет её владельцу или пользователю группы права читать или изменять этот объект.

### 3.1. Разрешение межкомандных групп

Группа может описывать объекты нескольких команд. Однако фактическое разрешение использовать каждый объект проверяется отдельно по tenant boundary, ownership, grants и Kubernetes RBAC.

Особенно важно: это решение не вводит ClusterGrant и не отменяет ADR-0012. Механизм явного делегирования доступа техническому Organization-level ServicePrincipal к кластерам разных Teams остаётся отдельным архитектурным вопросом. До его решения одна лишь Organization Group, роль Organization Admin или членство объекта в Custom Group не разрешают cross-team execution.

Невозможно подтвердить полномочия для отдельной цели — цель исключается из разрешённого набора либо весь Plan блокируется согласно выбранной Operation safety policy; молча расширять scope запрещено.

## 4. Target Snapshot и идентичность Kubernetes-объекта

При создании Plan selectors разрешаются в конкретный immutable Target Snapshot. Для каждой цели сохраняются как минимум:

- стабильный Kubron Cluster ID;
- Kubernetes Group/Version/Kind (GVK);
- Namespace и Name (для cluster-scoped resources namespace отсутствует);
- Kubernetes UID;
- наблюдавшийся ResourceVersion;
- идентификатор и revision Target Group, если использовалась группа;
- selectors и критерии, по которым объект попал в snapshot;
- action-specific preconditions, необходимые для безопасного выполнения.

**UID — обязательная идентичность конкретного экземпляра существующего объекта.** Одного сочетания GVK/namespace/name недостаточно: после удаления и повторного создания объект с тем же именем имеет другой UID и не удовлетворяет старый Plan.

ResourceVersion — наблюдавшийся маркер состояния, а не постоянная идентичность. Его изменение само по себе не доказывает, что цель стала другим объектом. Перед side effect исполнитель выполняет свежую проверку UID и action-specific preconditions. Если preconditions больше не выполняются, действие блокируется и требуется новый Plan/approval; нельзя автоматически применять старое одобрение к изменившемуся состоянию.

## 5. Изменения Target Group и объектов после Plan

- Добавление объекта в группу не добавляет его в уже созданный Target Snapshot.
- Удаление объекта из группы, изменение selector или иное изменение определения группы создаёт новую Group revision.
- Изменение Group revision после создания Plan требует повторного разрешения целей, нового Plan и нового применимого approval; существующий Plan не переинтерпретируется по новой версии группы.
- Удаление объекта и создание нового объекта с тем же именем приводит к UID mismatch и блокирует действие старого Plan.
- Изменение labels или других атрибутов, участвующих в selector, требует повторной проверки соответствия. Если объект больше не удовлетворяет selector, действие блокируется.
- Изменение ResourceVersion требует проверки action-specific preconditions. При нарушении preconditions старый Plan не исполняется.
- Потеря membership, tenant access, grant, Kubernetes permission или изменение Autonomy Policy блокирует действие при следующей проверке, независимо от ранее выданного approval.

Изменение состава группы никогда не переписывает уже сохранённый snapshot и не расширяет ранее одобренный набор целей.

## 6. Планирование и исполнение

Рекомендуемый доменный поток:

1. Принять Operation, Action, principal, Execution Identity, Target Group и требуемый scope.
2. Разрешить selectors в кандидатный список целей.
3. Для каждой цели проверить tenant/ownership, grants, operation/action scope, target scope и доступность Kubernetes authorization.
4. Создать immutable Target Snapshot с UID, наблюдавшимся ResourceVersion, group revision и preconditions.
5. Создать Plan и связать его digest с Target Snapshot, policy revision и Execution Identity binding.
6. Получить Approval, если его требует Autonomy Policy; approval относится только к этому Plan.
7. Перед каждым side effect заново проверить актуальные grants, tenant/ownership, identity/credentials, Kubernetes permissions, Autonomy Policy, approval, lease/fencing, UID и action-specific preconditions.
8. Выполнить действие и проверить postconditions свежим чтением.

Для multi-cluster Run каждый cluster-scoped sub-run имеет собственные Target Snapshot, Execution Identity, authorization checks и Plan/approval binding. Parent Run не создаёт общую межкластерную транзакцию и не передаёт полномочия от одного sub-run другому.

## 7. Fail-closed и частичный результат

- Нельзя подтвердить scope или авторизацию конкретной цели — действие над ней запрещено.
- Система не должна молча заменять недоступную цель другой целью с тем же именем или selector.
- Для операций с несколькими целями поведение при частично недоступном наборе должно быть явно задано контрактом Operation: блокировать весь Plan либо явно показывать и отдельно подтверждать разрешённое подмножество. Неявное сокращение набора запрещено.
- Если результат side effect неизвестен, требуется reconciliation через свежие reads; blind retry запрещён.

## 8. Инварианты

1. Scope ограничивает полномочия; Target Group описывает цели; Target Snapshot фиксирует цели конкретного Plan.
2. Target Group никогда не выдаёт доступ.
3. Organization/Team/Cluster ownership и tenancy из ADR-0012 остаются обязательными.
4. Cross-team membership группы не означает cross-team authorization.
5. Каждый существующий объект в snapshot идентифицируется UID; имя не является достаточной идентичностью.
6. ResourceVersion используется для проверки состояния/preconditions, но не вместо UID.
7. Plan и Approval привязаны к неизменяемому snapshot и revision; изменение определения группы требует нового Plan/approval.
8. Каждый side effect требует актуальной повторной проверки authorization и preconditions.
9. Web и Desktop используют одинаковую доменную семантику и backend enforcement.

## Открытые вопросы до runtime implementation

Это решение фиксирует продуктовую семантику, но не утверждает конкретную схему хранения или язык selectors. Отдельно требуется спроектировать:

- формат и валидацию Scope/Target Group selectors, включая допустимые Kubernetes label/field selectors;
- владельца, делегирование использования и жизненный цикл Organization/Team/Custom Groups;
- явный механизм cross-team delegation для ServicePrincipal, совместимый с ADR-0012 и без неявного ClusterGrant;
- атомарность и revision semantics при конкурентном изменении Target Group во время создания Plan;
- Operation-specific policy для partial target resolution;
- порядок и стоимость повторного чтения/проверки больших target sets;
- retention и аудит Target Snapshot.

Это принятое архитектурное решение не является разрешением начинать runtime implementation.
