# Execution Identity rotation, permission changes and revocation

- Статус: принятое архитектурное решение от 2026-11-26
- Область: Team Identity и Service Identity, расписания, фоновые и автономные операции
- Связано: `execution-identity-durable-operations-design.md`, ADR-0008, ADR-0018

## Решение

Изменение credentials, полномочий или жизненного цикла Execution Identity не должно незаметно менять identity binding уже созданного Run или расширять его разрешения. Перед каждым новым side effect Kubron повторно проверяет актуальное состояние identity, credential binding, grants, target access и effective policy.

### 1. Ротация credentials

- Ротация сохраняет логическую Execution Identity и её назначение.
- Новые Runs используют новую активную версию credentials только после успешной проверки/активации этой версии существующим credential subsystem.
- Перед следующим side effect уже выполняющийся Run повторно разрешает актуальную версию credentials через общий subject-aware credential resolution path.
- Если новая версия не проходит проверку или не может быть безопасно выдана, Run блокируется до вмешательства; автоматический откат к отозванным/неактуальным credentials запрещён.
- Plan сохраняет непрозрачный version/revision reference credential binding, достаточный для аудита и проверки согласованности. Секреты, токены и расшифрованные credentials в Plan/Run/Step/audit не попадают.
- Если side effect уже отправлен в Kubernetes API в момент ротации/отзыва, его исход может оставаться неизвестным. Kubron выполняет fresh read/reconciliation и не утверждает, что запрос гарантированно остановлен.

### 2. Изменение полномочий или scope

- Изменение Kubernetes RBAC, Kubron grants, tenant access, разрешённого cluster scope или иных effective permissions применяется к следующим side effects.
- Перед каждым side effect выполняется повторная авторизация с актуальными полномочиями; ранее выданное approval не отменяет эту проверку.
- Если действие больше не разрешено, соответствующий Run/Step переходит в `blocked` либо иной определённый state для безопасного ожидания/эскалации. Запрещённое действие не выполняется.
- Расширение полномочий само по себе не расширяет уже утверждённый Plan: для новых действий или изменившегося scope нужен новый Plan и approval согласно policy.

### 3. Отзыв или деактивация identity

- Отзыв/деактивация Team или Service Identity запрещает создание новых исполняемых Runs с этой identity.
- Незавершённые Runs блокируются от начала новых side effects и переходят к безопасной остановке/reconciliation. Уже отправленный внешний запрос может завершиться; такой результат фиксируется как `unknown` до подтверждения свежим чтением.
- Существующие Plan/Approval не позволяют продолжить исполнение отозванной identity.
- Отзыв не приводит к автоматической подмене другой identity.

### 4. Замена identity

- Назначение другой Team/Service Identity — явное административное действие, а не автоматический fallback.
- Замена создаёт новый Plan с новым identity binding и повторной проверкой всех target, tenant, policy, credential и permission constraints.
- Требуется новое approval там, где его требует effective autonomy policy; старое approval не переносится на новый Plan.
- Для уже выполняющегося Run замена identity не меняет его binding на месте. Run безопасно блокируется/завершается согласно outcome текущих side effects; продолжение с новой identity оформляется новым Run/Plan.

### 5. Multi-cluster Runs

- Правила применяются независимо к каждому cluster-scoped sub-run.
- Ротация, изменение scope или отзыв identity одного sub-run не изменяет identity binding и исполнение других sub-runs.
- Parent Run агрегирует статусы и результаты, но не подменяет identity и не предоставляет полномочий дочерним Runs.

## Инварианты реализации

1. Единственный источник credentials — существующая общая credential subsystem и subject-aware resolver; запрещены прямое чтение секретов из Store и обход credential providers.
2. Перед каждым side effect проверяются актуальные identity status, credential version/status, Kubron authorization grants, Kubernetes permissions, tenant/target access, policy и approval binding.
3. Credential version reference — метаданные для binding/audit, не секрет и не гарантия неизменности внешних полномочий.
4. Ни ротация, ни расширение полномочий не переписывают immutable Plan и не расширяют старое approval.
5. Неизвестный результат внешнего side effect требует reconciliation, а не слепого retry или автоматической смены identity.
6. Все операции управления identity и результаты блокировки/остановки аудируются без записи секретов.

Это решение уточняет разделы 2.4–2.5 и lifecycle/execution pipeline основного design proposal. Оно не является разрешением начинать runtime implementation.
