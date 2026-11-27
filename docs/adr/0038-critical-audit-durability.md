# ADR-0038: Durable Critical Audit

- Статус: принято (архитектурная модель; runtime-реализация не выполнена)
- Дата: 2026-11-27
- Связано: ADR-0007, ADR-0008, ADR-0022, ADR-0026, ADR-0035…0037; Phase 1 Contract Freeze

## Контекст

Текущий `audit.Logger.Record` сохраняет legacy best-effort семантику и не возвращает ошибку append вызывающему коду. Поэтому его нельзя использовать как gate для изменения security-sensitive state или будущих Kubernetes side effects.

## Решение

1. Legacy Logger.Record остаётся best-effort; его поведение не меняется в рамках добавления critical audit.
2. В `internal/audit` вводится отдельный error-returning CriticalRecorder с immutable intent и outcome.
3. Защищённое изменение выполняется только после durable AppendIntent. Ошибка записи intent запрещает mutation.
4. Intent связывается с одной mutation через EventID, MutationID, canonical request digest, expected revision и opaque resource references. Повтор EventID с другим payload — conflict.
5. После попытки mutation записывается outcome: succeeded, failed или unknown. Ошибка AppendOutcome означает unknown, а не success; повторять mutation вслепую запрещено.
6. Resource/identity/binding/grant/delegation хранит LastAuditEventID. Технический principal или binding не используется, пока соответствующий outcome не подтверждён как succeeded.
7. PostgreSQL intent/outcome сохраняются durable с уникальностью EventID; integration owner реализует PG adapter и миграции.
8. Desktop использует отдельный append-only critical journal, complete-write checks и durable flush (Sync/fsync). Повреждённый или неполный tail не трактуется как успех.
9. Audit не содержит secrets, plaintext credentials или provider payloads. Retention audit отделён от Run/Evidence cleanup.
10. Если side effect мог произойти, но outcome не сохранён, состояние остаётся unknown и требует reconciliation.

## Последствия

- Critical audit — обязательный prerequisite для любых будущих protected Kubernetes side effects.
- Нельзя заменять intent/outcome best-effort stdout mirror или legacy Logger.
- Phase 1 реализует только foundation и security-sensitive identity/grant/credential mutations; новые Kubernetes write actions не добавляются.
- Точные API и protocol contracts определены в Phase 1 Contract Freeze.
