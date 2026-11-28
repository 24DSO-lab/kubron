# ADR-0045: Atomic Plan Commit и согласованность Revision

- Статус: предложено (требует реализации и concurrency proof)
- Дата: 2026-11-28
- Связано: ADR-0035, ADR-0037, ADR-0038, ADR-0040…0044; ADR-0035…0038

## Контекст

Authorization Evaluate и последующий Plan Commit разделены во времени. Между ними могут измениться Delegation, Grants, Principal/Identity state, ownership, Target Group, Action Catalog или Autonomy Policy. Простое повторное чтение каждого источника перед записью Plan не устраняет TOCTOU: факт может измениться сразу после чтения и до commit.

Lane C фиксирует наблюдавшуюся DelegationRevision в GrantDecision, но сам по себе этот механизм не закрывает race до Plan Commit. Аналогичные версии нужны для других изменяемых security-relevant facts.

## Решение

1. Вводится единая семантическая операция `CommitPlan`. Её успешная запись является linearization point, в котором immutable Plan и весь его revision vector принимаются как согласованное решение.
2. Revision vector включает все применимые изменяемые факты, как минимум:
   - Principal/Identity и CredentialBinding revisions;
   - Grant и Delegation revisions;
   - trusted Ownership revision;
   - Target Group revision и Target Snapshot binding;
   - Action Catalog revision и конкретные Operation/Action versions;
   - Autonomy Policy/Profile revisions;
   - остальные action-specific authorization/precondition versions.
3. Revision должен исходить от владельца соответствующего домена. Его нельзя вычислять на клиенте, копировать из устаревшего cache или подменять временем чтения.
4. Каждый домен, владеющий mutable security-relevant fact, обязан предоставить проверяемый механизм version validation, который может участвовать в commit protocol. Обычная последовательность «прочитать текущую версию → затем независимо сохранить Plan» не считается атомарной.
5. Backend обязан обеспечить один из двух доказуемых вариантов:
   - все участвующие revision guards и immutable Plan фиксируются/проверяются в одной атомарной consistency boundary (например, одной транзакции PostgreSQL или сериализованной durable операции File Store); либо
   - используется эквивалентный domain-owned fencing/reservation protocol, при котором конкурентная мутация не может пройти незамеченной относительно commit point.
6. Мутация исходного доменного факта обязана атомарно обновлять его revision guard. Если источник не может участвовать в выбранном consistency protocol, Plan Commit для зависящего от него защищённого действия завершается отказом. Нельзя объявлять глобальную атомарность, если фактически есть только последовательные независимые проверки.
7. CommitPlan выполняет tenant-scoped validation и CAS по ожидаемому revision vector. Любое несовпадение, отсутствие версии, ambiguity или недоступность источника приводит к отказу без сохранения исполнимого Plan.
8. Для Desktop допустима сериализованная single-process реализация с durable journal/atomic replacement; она не обещает multi-process coordination. Для Web/PostgreSQL требуется транзакционная/условная фиксация, пригодная для HA и конкурентных реплик.
9. CommitPlan не создаёт распределённую транзакцию с Kubernetes API и не обещает, что внешние факты останутся неизменными после commit. Перед каждым side effect Run Engine повторно проверяет текущие authorization, policy, identity, delegation и target preconditions согласно ADR-0046/0047.
10. Ошибка проверки не должна приводить к частично сохранённому исполнимому Plan. Сохранённый Plan либо полностью committed с revision vector, либо отсутствует/имеет неисполняемый rejected outcome.

## Альтернативы

- Доверять результату Evaluate без проверки на Commit.
- Повторно читать все данные последовательно и считать это атомарностью.
- Использовать один TTL cache или timestamp вместо domain revisions.
- Разрешить commit при недоступности одного источника.
- Заявить распределённую транзакцию между PostgreSQL/File Store и Kubernetes API.

Все варианты отклонены как неспособные доказать отсутствие TOCTOU или создающие ложную гарантию атомарности.

## Последствия

- Нужен отдельный implementation design с конкретным протоколом revision guards для File и PostgreSQL.
- До доказательства linearizability Plan Commit нельзя считать готовым для mutating operations.
- Contract tests должны моделировать конкурентные изменения каждого revision source между Evaluate и Commit.
- Отзыв/изменение после Commit всё равно блокирует последующие side effects через revalidation; immutable Plan не является бессрочным разрешением.
