# ADR-0042: Autonomy Policy, иерархия ограничений и Profiles

- Статус: принято (ретроспективная фиксация ранее согласованной модели)
- Дата: 2026-11-28
- Связано: ADR-0012, ADR-0035, ADR-0037, ADR-0039…0041; Kubron Platform Architecture Roadmap; Executable Platform Architecture Specification

## Контекст

Kubron должен поддерживать постепенный переход от наблюдения к контролируемому и затем ограниченному автономному исполнению. Единый глобальный флаг Autonomous не учитывает tenant boundaries, риск действий и локальные ограничения. Нужна модель, в которой политики можно применять как к иерархии Kubernetes, так и к произвольным группам объектов.

## Решение

1. Autonomy имеет три режима:
   - **Observe:** сбор данных, анализ и планирование без side effects;
   - **Assisted:** side effects допустимы только в рамках явного workflow и необходимых approvals;
   - **Autonomous:** заранее разрешённые действия могут исполняться без подтверждения каждого запуска при соблюдении всех ограничений.
2. Режим по умолчанию — Observe. Отсутствие политики, неоднозначное сопоставление, недоступность policy store или ошибка evaluation означают Observe и запрет mutating actions.
3. Базовая иерархия политик: Organization → Cluster → Namespace → Workload → Object → Action. Политики верхнего уровня задают верхнюю границу полномочий и ограничений.
4. Нижележащая политика может только сузить родительский набор разрешённых действий и условий. Она не может расширить разрешения, снизить risk classification, отменить обязательный approval или ослабить guardrail родителя.
5. Помимо иерархии допускаются произвольные пользовательские группы targets, включая cross-cluster группы. Их policy binding является дополнительным ограничением и никогда не расширяет иерархическую effective policy.
6. Effective Policy вычисляется как пересечение всех применимых ограничений. При конфликте, неопределённости или недоступности любого обязательного источника выбирается более ограничительный результат; отсутствие достоверного решения означает Observe/deny.
7. Вводятся переиспользуемые Autonomy Profiles. Profile задаёт mode, allowlist/denylist typed actions, risk limits, approval requirements, budgets, time/frequency constraints, verification requirements, retry/recovery bounds и audit requirements.
8. Profile не выдаёт Kubron grants, Kubernetes permissions, credentials или delegation. Он только ограничивает допустимый способ использования уже авторизованных действий.
9. Policy evaluation выполняется отдельно для каждого target/action и учитывает tenant ownership, principal grants, delegation, Kubernetes RBAC, credential validity, target preconditions и operation/action risk.
10. Режимы и механизмы approval — независимые измерения. Например, Assisted не означает обязательное подтверждение каждого read-only шага, а Autonomous не отменяет обязательный per-action approval, заданный более высокой политикой.
11. Все решения должны быть объяснимыми: effective mode, matched policy/profile revisions, reason codes и ограничения фиксируются в контексте Plan/Run и audit.

## Альтернативы

- Один глобальный Autonomous toggle.
- Наследование политик по принципу «последняя дочерняя политика побеждает».
- Автоматическое разрешение Autonomous при отсутствии policy.
- Считать Profile источником прав вместо grants и tenant authorization.

Эти варианты отклонены, поскольку позволяют дочерним областям расширять полномочия и делают поведение неоднозначным.

## Последствия

- Нужен отдельный autonomy domain с версионируемыми Policy/Profile и детерминированным evaluator.
- Policy revisions входят в immutable Plan и approval binding.
- Любое изменение policy требует повторной проверки перед новым side effect; изменение, несовместимое с Plan, блокирует продолжение.
- Runtime write actions не разрешаются одним фактом реализации policy engine: нужны typed actions, Plans, approvals, critical audit, verification и recovery.
