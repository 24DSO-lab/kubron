# ADR-0057: Версионирование Autonomy Profiles и правила binding

- Статус: предложено
- Дата: 2026-11-28
- Область: Phase 2 — Target & Policy Foundation
- Связано: ADR-0041, ADR-0042, ADR-0044, ADR-0054, ADR-0056

## Контекст

Autonomy Profile — переиспользуемый набор ограничений, а не identity или grant. Один Profile может применяться к нескольким scope, а несколько ограничений могут одновременно действовать на один target. Изменение Profile не должно незаметно менять ранее показанное или впоследствии одобренное решение.

## Предлагаемое решение

1. Profile является отдельным версионируемым доменным объектом с owner scope Organization либо Team, opaque ID, monotonic Revision, declarative constraints и audit provenance.
2. Profile не содержит credentials, grants, delegation, произвольных команд или executable scripts. Он ссылается только на versioned typed actions и declarative policy constraints.
3. Binding — отдельная сущность/отношение между Profile и scope. Binding не меняет owner Profile и не выдаёт доступ к target.
4. Binding может применяться к поддерживаемому scope hierarchy и/или Target Group. Его effective contribution только сужает итоговую policy согласно ADR-0042 и ADR-0056.
5. Update Profile или Binding использует ExpectedRevision/CAS. Изменение любого поля, влияющего на effective policy, увеличивает соответствующую revision.
6. Evaluation сохраняет конкретные Profile ID + Revision и Binding IDs + Revisions, чтобы объяснение решения было воспроизводимым. Изменение Profile/Binding не переписывает прошлые evaluation records.
7. Удаление Profile, на который ссылаются bindings или сохранённые evaluations, выполняется через deactivation/tombstone; физическое удаление регулируется retention и ссылочной целостностью.
8. Не вводить implicit default Profile с разрешающим поведением. При отсутствии Profile действует Observe из ADR-0042.
9. Profile owner scope и binding scope проверяются независимо. Organization-owned Profile не даёт доступ к Team-owned target; Team-owned Profile не применяется за пределами Team без явно принятого правила sharing, которое всё равно не создаёт grants.
10. Phase 2 должна предоставлять понятный preview effective profile/policy для выбранного target scope, включая причины ограничений и неразрешённые bindings.

## Альтернативы

- Хранить Profiles как неверсируемые JSON-настройки.
- Встраивать Profile прямо в Target Group.
- Считать Profile источником прав.
- Автоматически назначать permissive default Profile.
- Перезаписывать прошлые evaluation results после изменения Profile.

Отклонены из-за потери воспроизводимости, смешения конфигурации с доступом и неявного расширения полномочий.

## Последствия и открытые вопросы

- Contract Freeze должен выбрать canonical schema/serialization и определить revision ownership для binding.
- Нужно определить допустимые операции sharing для Profile без изменения owner scope и без выдачи доступа.
- Нужны tests на profile revision race, binding revoke, scope mismatch, stale evaluation, deletion with references и monotonic restriction.
