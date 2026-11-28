# ADR-0036: Subject-aware Credential Resolution и ConfigFor

- Статус: Implemented (2026-11-28)
- Дата: 2026-11-27
- Связано: ADR-0008, ADR-0018, ADR-0026; ADR-0035; Phase 1 Contract Freeze

## Контекст

Существующий credential path поддерживает legacy user/team scopes. Его нельзя расширить так, чтобы старые Team-account records стали технической Team Identity. Background execution также не должен создавать фиктивную пользовательскую сессию или обходить credential Broker.

## Решение

1. User, legacy Team-account, TeamPrincipal и ServicePrincipal — разные credential subjects.
2. Существующие ScopeUser и ScopeTeam records, их key semantics и encryption AAD сохраняются. Legacy ScopeTeam никогда не интерпретируется как TeamPrincipal.
3. Для технических principals вводится отдельный типизированный subject, привязанный к PrincipalRef, ClusterID и CredentialBindingID.
4. Subject технической identity не содержит User Session. Session-bound challenge допустим только на этапе интерактивного provisioning/rotation администратором; итоговый credential сохраняется под техническим principal.
5. Все операции проходят через существующие Broker, Provider, CredentialStore, token refresh, envelope encryption и key lifecycle.
6. Единственная точка создания stored-cluster `rest.Config` остаётся `internal/k8s.ConfigFor`. Новый typed ConnPlan path не использует фиктивную AuthSession, не строит config/client напрямую и не обходит Broker.
7. Pool/cache keys включают principal kind, principal ID, cluster и binding/version, исключая reuse между разными principals и bindings.
8. Rotation сохраняет logical identity, новые запросы используют новую проверенную credential version. Revoke запрещает дальнейшую выдачу; fallback к старым credentials запрещён.
9. Секреты не попадают в DTO, Plan, audit, logs, events, errors или pool keys.
10. Новые credential records входят в rekey, keycheck, backup/restore и plaintext checks.

## Последствия

- Требуется проверить совместимость ключей и AAD до изменения формата хранилища.
- Миграция legacy records не должна переинтерпретировать или терять их scope.
- Credential resolution не принимает решение о Kubron grants; оно остаётся отдельной обязательной проверкой.

Реализация Phase 1 foundation интегрирована в `refactor`; по состоянию на 2026-11-28 владелец проекта подтвердил зелёный CI для завершённых lanes. Этот статус не разрешает Kubernetes write actions и не означает реализации Target/Policy, Plans или Run Engine.
