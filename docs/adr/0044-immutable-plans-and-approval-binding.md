# ADR-0044: Immutable Plans и привязка Approval

- Статус: принято (ретроспективная фиксация согласованной модели)
- Дата: 2026-11-28
- Связано: ADR-0035…0038, ADR-0040…0043; Execution Identity & Durable Operations Design; Executable Platform Architecture Specification

## Контекст

Approval имеет смысл только для конкретного набора действий, целей, полномочий и ограничений. Если после подтверждения меняется target set, policy, identity или Action definition, старое подтверждение не должно переноситься на новую операцию.

## Решение

1. Plan — неизменяемый snapshot конкретного исполнения Operation. Plan содержит:
   - Operation ID/Version и упорядоченный набор Action IDs/Versions;
   - canonical input digest;
   - immutable Target Snapshot и target preconditions;
   - отдельную Execution Identity binding для каждого cluster-scoped sub-run;
   - Actor/Initiator и Trigger как provenance;
   - authorization context и наблюдавшиеся revisions principal, identity, grants, delegation и ownership;
   - Action Catalog Version;
   - Autonomy Policy/Profile revisions и effective decision;
   - approvals required, expiry, risk/budget constraints;
   - ожидаемые postconditions, verification и recovery metadata.
2. Plan не содержит secrets, plaintext credentials, kubeconfig или refresh tokens. Credential binding хранится как opaque reference и version.
3. Plan имеет canonical serialization и версионированный digest algorithm. Digest вычисляется по всем security-relevant полям; порядок элементов нормализуется только там, где семантика Operation явно объявляет порядок незначимым.
4. Plan после commit не редактируется. Любое существенное изменение входов, targets, action version, identity binding, policy, authorization facts или preconditions требует нового Plan.
5. Approval привязывается к точному Plan digest, Target Snapshot, Execution Identity binding, policy revision, risk context и expiry. Approval нельзя перенести на другой Plan или новый набор целей.
6. Approval хранит approver, время, решение, scope и provenance. Для high-risk actions политика может требовать разделения proposer/approver, quorum или дополнительный approval; UI не может ослабить это требование.
7. Approval истекает по времени и может быть отозван. Перед исполнением backend проверяет, что Plan не изменён, approval действителен, actor полномочен, а текущие authorization/policy/target preconditions не запрещают действие.
8. Изменение Plan после approval аннулирует связанные approvals. Новый Plan проходит полный approval workflow заново.
9. Plan Commit и Approval — разные операции: успешный commit фиксирует immutable Plan, но не означает автоматического разрешения на side effect.

## Альтернативы

- Approval только на Operation ID или имя.
- Подтверждение набора целей без фиксации target snapshot.
- Разрешить редактировать Plan после approval.
- Считать Approval бессрочным capability token.

Отклонены из-за риска исполнения неподтверждённого действия или изменения scope после подтверждения.

## Последствия

- Plan digest и schema version должны быть стабильными и тестируемыми.
- Approval storage обязан обеспечивать CAS/atomic state transitions и tenant-scoped reads.
- Перед каждым side effect выполняется повторная проверка approval, identity, authorization, policy и preconditions.
- Plan/Approval audit сохраняет references и revisions, но не секреты.
