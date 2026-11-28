# ADR-0050: Retention Run/Step, Evidence и Critical Audit

- Статус: принято (ретроспективная фиксация ранее согласованной модели)
- Дата: 2026-11-28
- Связано: ADR-0007, ADR-0008, ADR-0012, ADR-0022, ADR-0038, ADR-0044…0049; Execution Identity & Durable Operations Design

## Контекст

Operation history, evidence и audit имеют разные цели, чувствительность, доступ и сроки хранения. Удаление истории выполнения не должно стирать обязательный security audit или evidence, необходимое для расследования и reconciliation. Retention также не может удалять активные операции и approvals.

## Решение

1. Organization задаёт настраиваемую retention policy для Run/Step history и связанных execution records. В этой модели retention ownership находится на уровне Organization; отдельная Team-level настройка не вводится без нового архитектурного решения.
2. Retention учитывает классы данных, срок, объём, завершённость Run, активные approvals и необходимость reconciliation. Незавершённые Run, unknown outcomes, активные approvals и записи, нужные для recovery, не удаляются обычной retention cleanup.
3. Evidence — отдельный доменный поток/объект со своим owner, access checks и retention policy. Run хранит references и provenance, а не произвольные копии больших payloads.
4. Critical Audit — отдельный обязательный поток с собственными правилами retention. Удаление/архивация Run, Step или Evidence никогда не удаляет обязательные audit records.
5. Retention Run/Step, Evidence и Audit не объединяется в одну настройку или одну операцию очистки. У каждого домена свои authorization, audit и cleanup semantics.
6. Audit retention не может быть ослаблен через обычную пользовательскую очистку истории. Legal hold, export, точные сроки и требования конкретной юрисдикции требуют отдельной реализации/политики и не должны трактоваться как разрешение удалять audit по умолчанию.
7. Evidence и Run payloads минимизируются. Секреты, plaintext credentials, токены и неограниченные Kubernetes object payloads не сохраняются в Plan/Run/Step/Audit.
8. Очистка выполняется tenant-scoped, идемпотентно и не должна нарушать ссылки, необходимые для active reconciliation. Все административные изменения retention и фактические удаления подлежат аудиту.

## Альтернативы

- Один общий TTL для Run, Evidence и Audit.
- Удаление audit вместе с Run history.
- Удаление unknown/active Run по возрасту.
- Хранение полного Kubernetes object payload для удобства UI.

Отклонены из-за потери расследуемости, нарушения recovery и избыточного хранения чувствительных данных.

## Последствия

- Нужны отдельные retention contracts и cleanup jobs для Run/Step, Evidence и Audit.
- Организационная retention policy должна быть частью администрирования и аудита.
- Точные сроки, legal hold, export и deletion approvals должны быть уточнены до production retention rollout.
