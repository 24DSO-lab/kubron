# Phase 2 — Lane B: Autonomy Domain

## Назначение

Реализовать независимый доменный пакет Autonomy Foundation по принятым ADR. Это временное задание; постоянные архитектурные решения находятся в ADR.

## Обязательные ADR

- ADR-0056 — Deterministic Autonomy Policy Evaluation
- ADR-0057 — Autonomy Profile Versioning and Binding
- ADR-0042 — Autonomy Policy and Profiles
- ADR-0035/0037/0038 — identity, authorization/delegation и Critical Audit boundaries
- ADR-0058 — Phase 2 scope and exit gates.

Все ADR находятся в `docs/adr/`. Не вводить альтернативную модель. При противоречии ADR и кода остановиться и сообщить о нём.

## File ownership

Разрешено изменять:
- `internal/autonomy/**`
- unit/property tests для `internal/autonomy`.

Не изменять:
- `internal/target/**`;
- `internal/store/**`, `internal/pgstore/**`, migrations;
- app composition, transport/UI;
- ADR и другие постоянные документы.

Не расширять allowlist без согласования.

## Реализовать

1. Явный versioned Policy aggregate с opaque ID, OwnerScope, provenance, lifecycle, monotonic Revision и ExpectedRevision/CAS.
2. Каноническую declarative constraint schema, общую для Policy и Profile:
   - mode ceiling: Observe / Assisted / Autonomous;
   - typed Action allow/deny constraints;
   - maximum risk ceiling;
   - approval requirements;
   - execution budgets/frequency limits;
   - allowed time windows;
   - verification/recovery constraints;
   - mandatory audit constraints.
3. Versioned Autonomy Profile с тем же constraint schema, собственным OwnerScope и revision.
4. Отдельный versioned Binding aggregate с собственными ID и Revision, ссылкой на Profile и scope. Любое изменение effective semantics увеличивает Binding Revision.
5. Чистый deterministic evaluator над immutable input. Никаких UI, Kubernetes clients, storage calls или side effects внутри evaluator.
6. Effective Policy — intersection всех применимых ограничений. Порядок входных policies/bindings не влияет на результат:
   - наиболее строгий mode;
   - deny union;
   - allow intersection;
   - наиболее строгие risk/budget limits;
   - approval/verification/audit requirements объединяются;
   - time windows пересекаются;
   - конфликт/пустое пересечение/unknown обязательного input => Observe/deny.
7. Immutable Evaluation Decision с explain result, стабильными reason codes, неизвестными inputs и revision vector (Policy/Profile/Binding/Target Group).
8. Проверки совместимости OwnerScope и binding scope по ADR-0057. В Phase 2 cross-owner Profile sharing запрещён. Organization Profile применяется только внутри той же Organization; Team Profile — только внутри той же Team.
9. Deactivation/tombstone с ExpectedRevision. Не реализовывать physical delete при наличии references. Не создавать default Profile.
10. Typed Action ID + Version. Если точного action reference нет, разрешён только scope-level preview; любая mutating decision запрещена. Runtime Action Catalog не входит в эту задачу.

## Fail-closed правила

- Нет Policy/Profile там, где он обязателен, источник недоступен, binding неоднозначен, revision stale или scope facts неизвестны => Observe/deny.
- Policy/Profile не является grant, delegation, credential или Kubernetes RBAC.
- Положительное policy decision не заменяет authorization, credential validity, approval или action preconditions.
- Revision vector не объявляется атомарной междоменной авторизацией. Future Plan Commit обязан повторно проверить версии согласно ADR-0045.

## Важное разделение ответственности

- Ты владеешь только `internal/autonomy/**`.
- Lane C реализует persistence/adapters за интерфейсами, которые ты объявишь.
- В первом рабочем коммите (или сразу после него) передай C список exported interfaces, сигнатуры и CAS semantics. Не жди завершения evaluator.
- Не меняй Target domain и C-owned файлы.

## Запрещено в Phase 2

- Action Catalog runtime;
- Plans, Approvals, Run/Step, leases, scheduler;
- Kubernetes write actions;
- autonomous execution, scripts, arbitrary commands;
- выдача grants/delegation/credentials.

## Обязательные тесты

- permutation invariance;
- monotonic restriction;
- fail-closed для missing/unavailable/ambiguous/stale inputs;
- deterministic output и stable reason codes;
- allow intersection / deny union;
- conflicting constraints / empty time-window intersection;
- profile revision и binding revision race;
- scope mismatch и cross-owner rejection;
- deactivation/tombstone;
- exact Action ID+Version requirement для mutating decision;
- cross-tenant isolation.

## Definition of Done

- Пакет компилируется, evaluator чистый и deterministic.
- Unit/property tests зелёные.
- Интерфейсы для Lane C опубликованы.
- Нет изменений вне allowlist.
- CI по затронутому коду зелёный.
- PR содержит список проверенных ADR и оставшихся ограничений.

Не переводить ADR в `Implemented`: это делается после интеграции всей Phase 2, review и зелёного CI.
