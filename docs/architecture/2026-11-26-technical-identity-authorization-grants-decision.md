# Kubron authorization principals and grants for technical identities

- Статус: принятое архитектурное решение от 2026-11-26
- Область: Team Identity, Service Identity, фоновые и автономные операции
- Связано: ADR-0006, ADR-0012, ADR-0018, `execution-identity-durable-operations-design.md`

## Контекст

Существующая модель различает app-RBAC пользователя Kubron и Kubernetes RBAC, а ADR-0018 определяет credential scopes `user` и `team`. Эти credential scopes не являются полноценной моделью авторизации фонового исполнителя. Kubernetes credentials сами по себе не предоставляют полномочий внутри Kubron.

Фоновая операция должна исполняться от явно назначенной Team или Service Execution Identity, не наследуя текущие права инициатора. Поэтому Kubron должен авторизовать техническую identity как самостоятельный субъект.

## Решение

### 1. Отдельные authorization principals

Ввести логически отдельные типы субъектов авторизации:

- **UserPrincipal** — интерактивный пользователь; использует существующие user session и app-RBAC.
- **TeamPrincipal** — технический субъект команды, связанный с Team Identity.
- **ServicePrincipal** — технический субъект организации, связанный с Service Identity и ограниченный назначением.

TeamPrincipal и ServicePrincipal не являются пользовательскими аккаунтами, не получают интерактивную сессию и не наследуют права пользователя, создавшего identity, администратора или команды целиком.

Credential identity и authorization principal — связанные, но независимые измерения. Credential record определяет, какие Kubernetes credentials разрешено разрешить через credential subsystem; principal и grants определяют, что этот субъект вправе делать в Kubron. Система должна проверять корректную привязку между ними, но не выводить grants из наличия credentials.

### 2. Независимость административных действий

Следующие действия независимы и требуют собственных authorization checks и audit events:

1. Создание Team/Service Identity.
2. Назначение, обновление или ротация credentials.
3. Выдача, изменение и отзыв Kubron authorization grants.
4. Назначение identity расписанию, Operation или другому execution scope.

Успешное создание identity или назначение credentials не выдаёт ей автоматически никаких дополнительных Kubron grants. Аналогично, выдача grants не создаёт и не назначает Kubernetes credentials.

### 3. Grants вместо широких пользовательских ролей

Для технических principals не вводить широкие пользовательские роли `Admin`, `Editor`, `Viewer`. Полномочия задаются явными grants с ограничениями как минимум по следующим измерениям:

- **Operations** — какие зарегистрированные Operation разрешено запускать.
- **Actions** — какие типизированные действия внутри Operation разрешены.
- **Targets** — какие Organization/Team/Cluster и, где применимо, namespace/workload/object scope разрешены.
- **Constraints** — дополнительные ограничения, например запрет destructive actions, ограничение окружений, concurrency, budgets или временного окна.

Модель должна быть least-privilege и deny-by-default. Отсутствие grant, неоднозначное сопоставление или невозможность проверить любой обязательный элемент означает отказ.

### 4. Границы Team и Service principals

- TeamPrincipal ограничен своей Team и кластерами, принадлежащими этой Team согласно ADR-0012. Он не получает доступ к другим Teams или кластерам через Target Group, Organization Admin или parent Run.
- ServicePrincipal управляется на уровне Organization, но само организационное владение не даёт ему доступ ко всем командам и кластерам. Его доступ определяется явными grants и проверками tenant/target ownership.
- Organization Admin и Team Admin могут управлять соответствующими identity/grants только в пределах своей административной области и действующего app-RBAC.
- Ни одна техническая identity не получает административных полномочий в Kubron только потому, что её credentials имеют широкие Kubernetes permissions.

### 5. Effective authorization — пересечение ограничений

Разрешение на действие вычисляется по всем применимым независимым уровням:

1. Organization/Team tenancy и ownership target согласно ADR-0012.
2. Явные Kubron grants самого TeamPrincipal/ServicePrincipal.
3. Разрешённые Operation и конкретные Action.
4. Target scope и ограничения действия.
5. Действующие Kubernetes permissions назначенных credentials.
6. Effective Autonomy Policy, включая mode, guardrails и approval requirements.
7. Проверки immutable Plan/Approval binding, текущего состояния identity и credential binding.

Действие разрешено только если каждый обязательный уровень разрешает его. Ни один уровень не может расширить полномочия, запрещённые другим уровнем. Нельзя считать наличие Kubernetes credentials, membership, Target Group или approval самостоятельным достаточным основанием доступа.

Для интерактивного UserPrincipal продолжают действовать существующие app-RBAC, пользовательская сессия и Kubernetes authorization пользователя; это решение не заменяет интерактивную модель ADR-0018.

### 6. Повторная проверка и жизненный цикл

- Авторизация проверяется при планировании/создании Plan и повторно перед каждым side effect.
- Изменение или отзыв grants блокирует последующие запрещённые side effects, даже если Plan/Approval был создан ранее.
- Расширение grants не расширяет immutable Plan и не меняет старое approval. Для расширения действий или targets требуется новый Plan и применимое approval.
- Grants одного cluster-scoped sub-run не распространяются на соседние sub-runs.
- Все изменения principals/grants и решения авторизации аудируются. Секреты и токены не попадают в audit.

## Последствия и ограничения

- Нужна отдельная модель хранения и разрешения Kubron authorization principals/grants, связанная с существующей credential subsystem, но не смешанная с ней.
- Нельзя переиспользовать пользовательские роли как скрытый механизм авторизации Team/Service identities.
- Конкретная схема grants, формат policy expressions, inheritance/deny precedence, UI управления и миграция требуют отдельного проектирования до runtime implementation.
- Решение должно быть реализовано единообразно в Web и Desktop через общие domain contracts и backend enforcement.
- Это решение не добавляет ClusterGrant и не меняет владение кластерами из ADR-0012.

## Инварианты

1. Техническая identity — самостоятельный Kubron authorization principal.
2. Credentials не создают grants; grants не создают credentials.
3. Инициатор и администратор, создавший identity, не становятся execution principal.
4. Kubernetes RBAC и Kubron authorization независимы; оба обязательны.
5. Нет явного разрешения — отказ.
6. Проверка актуальных полномочий выполняется перед каждым side effect.
7. Никакого расширения старого Plan/Approval после изменения grants.
8. Все проверки выполняются backend, UI не является источником истины.

Это решение закрывает архитектурный вопрос о том, как Team/Service Execution Identity авторизуются в Kubron. Оно не является разрешением начинать runtime implementation.
