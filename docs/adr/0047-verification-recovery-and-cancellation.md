# ADR-0047: Verification, Recovery и безопасная отмена операций

- Статус: принято (ретроспективная фиксация согласованной модели)
- Дата: 2026-11-28
- Связано: ADR-0038, ADR-0043…0046; Execution Identity & Durable Operations Design; Executable Platform Architecture Specification

## Контекст

Ответ Kubernetes API не всегда доказывает, что желаемое состояние достигнуто. Запрос может примениться, а ответ потеряться; процесс может завершиться между side effect и сохранением результата. Слепой retry способен повторить опасное изменение. Отмена также не может гарантировать отмену уже отправленного запроса.

## Решение

1. Для каждой mutating Action обязательны action-specific preconditions, expected postconditions, verification method, timeout, retry classification и recovery/compensation policy до включения Action в разрешённый каталог.
2. Execution pipeline для каждого Step:
   1. проверить текущий Run/Plan/Approval и fencing;
   2. повторно проверить identity, credential binding, authorization, delegation, tenant ownership, policy и target preconditions;
   3. записать durable Step intent и Critical Audit intent;
   4. только после подтверждения durable intent отправить Kubernetes request;
   5. получить наблюдение и выполнить свежую verification;
   6. durable сохранить outcome и audit outcome.
3. Ошибка записи Critical Audit intent блокирует side effect. Если side effect мог быть отправлен, но outcome/audit не удалось durable сохранить, результат становится unknown и требует reconciliation.
4. HTTP/API success сам по себе не означает успешный Step. Успех фиксируется только после подтверждения postconditions в соответствии с Action contract.
5. Unknown означает, что side effect мог произойти, но достоверный результат не установлен. Unknown нельзя автоматически переводить в failed или retry. Сначала выполняется свежая проверка фактического состояния и action-specific reconciliation.
6. Retry разрешён только если Action contract доказывает безопасность повторения в данном состоянии, idempotency/preconditions подтверждены и предыдущий attempt reconciled. Никаких blind retries.
7. Compensation — отдельное typed Action с собственными authorization, policy, preconditions, audit и verification. Компенсация не предполагается автоматически и не гарантируется для необратимых действий.
8. Cancellation кооперативна. Она запрещает запуск новых Steps после принятия запроса отмены, но не утверждает, что уже отправленный Kubernetes request отменён.
9. Run становится cancelled только когда все неопределённости разрешены и не осталось незавершённых side effects. Если часть результата уже достигнута или outcome неизвестен, Run остаётся partially_succeeded/unknown/interrupted в соответствии с reconciliation.
10. Для multi-step и multi-cluster операций отсутствие общей distributed transaction означает, что частичный успех должен быть видимым и auditable.

## Альтернативы

- Считать успешный HTTP status доказательством postcondition.
- Повторять запрос при timeout/connection reset.
- Отменять Run как будто отмена гарантирует rollback.
- Выполнять универсальную автоматическую компенсацию.

Варианты отклонены из-за риска повторных или неучтённых изменений Kubernetes.

## Последствия

- Каждая mutating Action требует отдельного verification/recovery contract.
- Failure-injection tests должны покрывать crash до/после отправки запроса, потерю ответа, ошибку audit outcome и рестарт worker.
- UI обязан показывать unknown/partial состояния явно и не маскировать их как failed или succeeded.
- Recovery и compensation требуют актуальных полномочий и не обходят policy.
