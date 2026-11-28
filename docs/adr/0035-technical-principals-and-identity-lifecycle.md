# ADR-0035: Технические principals и жизненный цикл Identity

- Статус: Implemented (2026-11-28)
- Дата: 2026-11-27
- Связано: ADR-0007, ADR-0008, ADR-0012, ADR-0018, ADR-0026

## Контекст

Фоновые и автоматические операции не должны исполняться от личной учётной записи инициатора. Kubernetes credentials и полномочия Kubron — разные уровни. Нужны технические principals с независимым жизненным циклом, владельцем и аудитом.

## Решение

1. Существуют три логических вида principal: UserPrincipal, TeamPrincipal и ServicePrincipal.
2. UserPrincipal ссылается на существующего пользователя Kubron и используется для интерактивных действий с активной сессией.
3. TeamPrincipal принадлежит ровно одной Team и ограничен её scope. Он не наследует права Team Admin, создателя или членов команды.
4. ServicePrincipal принадлежит Organization. Само организационное владение и администрирование не дают ему доступа к Team или Kubernetes-кластерам.
5. Creator/initiator хранится только как provenance. Уход создателя или потеря им роли не меняет lifecycle identity.
6. Organization Admin управляет организационными Service Identity; Team Admin — Team Identity своей команды. Эти полномочия не являются Kubernetes grants.
7. Identity lifecycle: active, suspended, revoked. Revoked — терминальное состояние; повторное включение создаётся новой identity с новым ID.
8. Credential assignment, Kubron grants, Cross-Team Delegation и назначение identity расписанию/Operation — независимые, отдельно аудируемые действия.
9. Identity и binding имеют монотонные revisions и CAS transitions. Owner scope неизменяем.
10. CredentialBinding — явное назначение principal к cluster; pending/revoked binding не исполняется и не выдаёт доступа сам по себе.

## Последствия

- Team/Service principals не могут входить в Kubron как пользователь и не получают User Session.
- Создание технической identity не создаёт credentials, grants или Kubernetes permissions.
- Неоднозначность owner, principal kind или lifecycle приводит к отказу.
- Удаление не стирает provenance и audit trail.

Реализация Phase 1 foundation интегрирована в `refactor`; по состоянию на 2026-11-28 владелец проекта подтвердил зелёный CI для завершённых lanes. Этот статус не разрешает Kubernetes write actions и не означает реализации Target/Policy, Plans или Run Engine.
