# Reason-коды Kubron grant evaluator

`GrantDecision.ReasonCode` — **стабильный публичный контракт** Lane C (Phase 1 Contract Freeze §6.3).
Значения не меняются и не переиспользуются; новые коды добавляются только через Contract Amendment.
Коды безопасны для показа и аудита: они не содержат значений полей, имён чужих целей, владельцев и окружений.

`Granted == true` бывает только при `ReasonCode == "granted"` и `err == nil`. Любой другой исход — отказ.
Детерминированный отказ возвращается как решение с `err == nil`; недоступность зависимости — решение
`dependency_unavailable` **вместе с** ошибкой `errors.Is(err, ErrUnavailable)`.

`GrantDecision` не является итоговым разрешением, bearer-токеном или доказательством доступа: его нельзя
сохранять и передавать дальше, каждая защищённая операция оценивает заново. Положительный кеш не допускается.

## Принятие

| Код | Значение |
|---|---|
| `granted` | Grants, обязательная delegation (для ServicePrincipal) и границы тенантности разрешают кортеж. В решении — ID и revisions grants и ID delegation. |

## Запрос и principal

| Код | Значение |
|---|---|
| `request_invalid` | Некорректный principal, OperationID/ActionID или TargetRef (в т.ч. имя объекта без UID). |
| `principal_kind_unsupported` | Technical grants применимы только к TeamPrincipal и ServicePrincipal. |
| `principal_unknown` | Identity не найдена. |
| `principal_not_active` | Identity приостановлена, отозвана либо её критичный аудит не подтверждён. |

## Цель и действие

| Код | Значение |
|---|---|
| `target_not_accessible` | Цель неизвестна, вне Organization principal'а, вне его Team (для TeamPrincipal) либо данные владения некорректны. **Все эти случаи неотличимы** — метаданные чужих целей не раскрываются. |
| `action_unknown` | Пара OperationID+ActionID отсутствует в доверенном каталоге. Grant на неизвестное действие ничего не разрешает. |

## Delegation (ServicePrincipal)

| Код | Значение |
|---|---|
| `delegation_required` | Нет ни одной delegation от Team-владельца цели для этого сервиса, покрывающей действие и цель. |
| `delegation_invalid` | Подходящая delegation повреждена (не проходит проверку формы). |
| `delegation_revoked` | Подходящая delegation отозвана. |
| `delegation_suspended` | Подходящая delegation приостановлена. |
| `delegation_expired` | Срок подходящей delegation истёк (`now >= ExpiresAt`). |
| `delegation_constraint_unsupported` | Constraints delegation не поддерживаются/не вычислимы. |
| `delegation_constraint_not_satisfied` | Constraints delegation (AND) не выполнены. |
| `delegation_audit_unconfirmed` | Нет подтверждённого `succeeded` outcome критичного аудита delegation. |

## Grants

| Код | Значение |
|---|---|
| `grant_required` | Нет grant, покрывающего точную пару действия и цель. |
| `grant_invalid` | Подходящий grant повреждён (в т.ч. TeamPrincipal-grant с чужой Team). |
| `grant_revoked` | Подходящий grant отозван. |
| `grant_expired` | Срок подходящего grant истёк (`now >= ExpiresAt`). |
| `grant_constraint_unsupported` | Constraints не поддерживаются/не вычислимы. |
| `grant_constraint_not_satisfied` | Constraints (AND): окружение, DenyDestructive или NotBefore не выполнены. |
| `grant_audit_unconfirmed` | Нет подтверждённого `succeeded` outcome критичного аудита grant (отсутствует, failed или unknown). |

## Состояние и зависимости

| Код | Значение |
|---|---|
| `stale_revision` | Принципал или использованная запись изменились во время оценки (повторная проверка перед выдачей решения). |
| `dependency_unavailable` | Недоступна зависимость (хранилище, каталог, identity, аудит, часы, контекст). Всегда сопровождается `ErrUnavailable`. |

## Выбор причины при нескольких кандидатах

Если отказали несколько подходящих записей, возвращается причина **ближайшая к допуску**, независимо от
порядка записей: `*_audit_unconfirmed` > `*_constraint_not_satisfied` > `*_constraint_unsupported` >
`*_expired` > `*_suspended` > `*_revoked` > `*_invalid`; при отсутствии подходящих записей — `delegation_required` /
`grant_required`. Для ServicePrincipal delegation проверяется раньше grants.

## Единственное намеренное различие внутри Organization

`delegation_required` отличается от `target_not_accessible`: он возвращается только для цели **своей
Organization** сервиса и сообщает лишь, что нужно согласие Team. Владелец Team, окружение и имя цели не
раскрываются. Чужая Organization и несуществующая цель неотличимы.
