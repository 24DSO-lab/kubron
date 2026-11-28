# ADR-0046: Durable Run/Step Execution, leases и fencing

- Статус: принято (ретроспективная фиксация согласованной модели)
- Дата: 2026-11-28
- Связано: ADR-0007, ADR-0016, ADR-0026, ADR-0038, ADR-0039, ADR-0043…0045; Execution Identity & Durable Operations Design

## Контекст

Operation должна переживать закрытие UI, потерю WebSocket, рестарт Desktop и потерю server replica. В HA нельзя полагаться на память одного worker или на локальную блокировку. При этом Kubernetes API не поддерживает транзакцию с внутренним Run Store, поэтому нельзя обещать exactly-once side effects.

## Решение

1. Разделяются:
   - Operation — версия сценария/определения;
   - Plan — immutable snapshot конкретного исполнения;
   - Run — конкретная попытка исполнения committed Plan;
   - Step — типизированный action invocation внутри Run.
2. Run и Step используют доменные state machines. Состояние меняется только через разрешённые переходы с expected state/version; произвольная установка state вызывающим кодом запрещена.
3. Run state включает как минимум: draft, planned, awaiting_approval, approved, queued, running, verifying, succeeded, partially_succeeded, failed, blocked, cancel_requested, cancelled, interrupted, unknown, expired.
4. Step state включает как минимум: pending, claimed, precondition_check, executing, verification_pending, succeeded, failed, blocked, retry_wait, cancel_requested, interrupted, unknown, compensating, compensated, compensation_failed.
5. Durable backend владеет семантическими методами CreateRun, ClaimStep, TransitionStep, RequestCancel и ReconcileInterrupted. Универсальный CRUD/Save(any) интерфейс не вводится.
6. Claim использует lease с owner, expiry и монотонным fencing token. Любой transition и начало следующего Step проверяет актуальность fencing token. Истёкший или вытесненный worker не может менять durable state или начинать новый Step.
7. В PostgreSQL lease/expiry и occurrence/claim arbitration используют authoritative DB time и conditional updates/transactions. В Desktop single-process File Store использует сериализованный durable state; он не притворяется распределённым lease backend.
8. Idempotency keys и уникальные occurrence/Run constraints предотвращают дублирование внутренних записей и повторную обработку одного логического запроса. Они не гарантируют exactly-once Kubernetes effects.
9. Гарантия внешних side effects формулируется как durable at-least-once attempts с action-specific idempotency, precondition checks, fresh reads и reconciliation. Неопределённый результат переводится в unknown, а не в success или автоматический retry.
10. События/WebSocket — уведомления. После reconnect клиент загружает состояние из durable backend.
11. Web runner принадлежит серверной платформе, а не HTTP request, browser tab или WebSocket. Он работает через HA-safe claims и durable state.
12. Desktop runner работает внутри процесса Kubron Desktop, отделён от UI/window lifecycle и не требует отдельного daemon. Пока процесс жив, сворачивание окна или блокировка ОС сами по себе не останавливают Run. Завершение процесса останавливает локальное исполнение; restart начинает с reconciliation.
13. Run/Step records и claims всегда tenant-scoped; repository queries проверяют scope до выдачи данных.

## Альтернативы

- In-memory queue или mutex как источник истины.
- Exactly-once guarantee для Kubernetes side effects.
- Runner, привязанный к HTTP request/WebSocket.
- Постоянный Desktop daemon/service.
- Универсальный CRUD Store или глобальный transaction manager.

Варианты отклонены из-за потери состояния при рестарте, split-brain в HA и невозможности обеспечить заявленные гарантии.

## Последствия

- Нужны contract tests File/PostgreSQL для transitions, claims, leases, fencing, idempotency и restart recovery.
- Нельзя начать новый side effect после потери lease/fencing.
- Desktop и Web имеют разный runtime ownership, но одинаковую семантику Run/Step states.
- Нельзя заявлять exactly-once; неизвестные результаты требуют reconciliation.
