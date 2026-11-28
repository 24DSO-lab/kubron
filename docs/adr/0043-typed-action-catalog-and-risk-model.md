# ADR-0043: Typed Operation/Action Catalog и модель риска

- Статус: предложено
- Дата: 2026-11-28
- Связано: ADR-0024…0027, ADR-0039…0042; Kubron Platform Architecture Roadmap; Executable Platform Architecture Specification

## Контекст

Operations Platform должна выполнять только понятные, проверяемые действия. Универсальная команда, произвольный kubectl или код, сгенерированный AI, не дают платформе надёжно определить scope, риск, preconditions, ожидаемый результат и способ восстановления. Для безопасного планирования требуется каталог версионируемых типизированных действий.

## Решение

1. Operation — версионируемый пользовательский/системный сценарий, который объединяет одно или несколько typed Actions. Action — минимальная единица, для которой определены входы, target requirements, authorization, risk, preconditions, side effect и verification.
2. Каждая Operation и Action имеет стабильный opaque ID и неизменяемую Version. Изменение семантики, входов, side effects, risk или verification создаёт новую версию. In-flight Plan продолжает ссылаться на точную версию.
3. Action definition содержит как минимум:
   - типизированную схему входов и валидацию;
   - допустимые Resource kinds и target constraints;
   - необходимые Kubron permissions/grants и cluster capabilities;
   - risk class и impact/blast-radius metadata;
   - preconditions и expected postconditions;
   - approval requirements;
   - idempotency/retry classification;
   - verification и recovery/compensation contract;
   - timeout, budget и audit requirements.
4. Risk classification принадлежит каталогу, а не инициатору или UI. Клиент не может понизить risk class или ослабить guardrails.
5. Execution adapter получает только валидированный typed input, immutable target references и проверенный execution context. Adapter не выбирает произвольный cluster, principal или credentials.
6. Первые реализуемые действия должны быть read-only либо не иметь внешних side effects. Mutating actions допускаются только после прохождения gates ADR-0044…0047 и отдельной проверки конкретного action contract.
7. Произвольный shell, произвольный kubectl, универсальный command runner, произвольные scripts и прямое исполнение AI-generated commands не являются Action Catalog entries и не допускаются как обход typed action model.
8. AI/Intelligence может предложить Operation или параметры typed Action и объяснить evidence, но не может самостоятельно присвоить права, изменить risk classification, принять approval или обойти policy/authorization.
9. Каталог предоставляет versioned read interface. Action catalog revision и конкретные Operation/Action versions фиксируются в Plan; изменение каталога не переписывает старые планы.

## Альтернативы

- Универсальный command execution API.
- Action schema, полностью определяемая frontend.
- Динамическая интерпретация произвольного кода/AI output.
- Изменение существующей Action definition без новой версии.

Варианты отклонены из-за невозможности обеспечить стабильные preconditions, воспроизводимый audit и безопасную проверку риска.

## Последствия

- Требуется отдельный Operation/Action domain и реестр typed definitions.
- Для каждой Action обязательны contract tests и документированные side-effect/retry/verification semantics.
- Версии каталога участвуют в Plan digest и Plan Commit.
- Добавление нового mutating Action требует отдельного security review и не следует автоматически из принятия этого ADR.
