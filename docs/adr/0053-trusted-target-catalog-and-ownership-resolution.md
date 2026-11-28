# ADR-0053: Доверенный каталог целей и разрешение ownership

- Статус: принято
- Дата: 2026-11-28
- Область: Phase 2 — Target & Policy Foundation
- Связано: ADR-0012, ADR-0035, ADR-0037, ADR-0040, ADR-0041

## Контекст

Target Group может объединять объекты разных кластеров и Teams. При этом ни selector, ни переданный клиентом ClusterID/TeamID, ни sharing не являются доказательством владения или доступа. Phase 1 добавила технические principals, grants и delegation, но не реализовала Target domain. Для Phase 2 нужен стабильный источник сведений о кластере и его владельце, не создающий параллельный tenant catalog.

В текущем коде internal/tenant.Scope требует OrgID, TeamID и ClusterID, а internal/store.Store предоставляет Clusters() через существующий k8s.ClusterStore. Это важные существующие seams, но они сами по себе ещё не определяют контракт trusted ownership resolution для всех сценариев Target.

## Решение

1. Target domain не становится владельцем каталога кластеров и не дублирует cluster ownership. Источник истины о кластере и его tenant ownership остаётся в существующем доверенном cluster catalog / интеграционном слое.
2. Target domain получает ownership только через backend-интерфейс доверенного resolver-а. Он принимает идентификатор кластера и возвращает канонический owner scope либо безопасную ошибку. Точная Go-сигнатура определяется integration owner после проверки k8s.ClusterStore и существующих app seams; этот ADR не предписывает неподтверждённую сигнатуру.
3. Resolver не принимает OrgID/TeamID от клиента как авторитетные значения. Клиент может передать ClusterID и ссылку на target, но ownership вычисляется сервером.
4. Отсутствие кластера, неразрешённый owner, конфликт каталога, неполный scope или недоступность resolver-а приводят к отказу. Нельзя трактовать пустой TeamID как wildcard или автоматически повышать Team scope до Organization scope.
5. Target Group имеет собственный OwnerScope (Organization либо Team), который не выводится из создателя и не меняется при sharing. Правила ADR-0041 остаются нормативными.
6. Для каждого target проверяется совместимость owner scope группы, owner scope кластера, tenant membership, principal grants и, когда применимо, Cross-Team Delegation. Успешная проверка ownership не означает разрешение операции.
7. Target domain не строит rest.Config, не получает credentials напрямую и не вызывает Kubernetes API в обход существующего credential Broker / ConfigFor boundary.

### Нормативный контракт ownership resolver

- Resolver получает только ClusterID и доверенный server-side context. Он возвращает канонический Organization owner и, если существует, Team owner. OrgID/TeamID из клиентского запроса не принимаются.
- В PostgreSQL Organization определяется через `pgstore.Store.OrgID()`; Team — через доверенные метаданные кластера `ClusterStore.Get` и существующий Directory. Team, не принадлежащая текущей Organization или отсутствующая в Directory, считается неразрешённой.
- В File/Desktop и в адаптерах без tenant Directory действует локальная single-user область: канонический Organization OwnerScope с зарезервированным ID `local`. Team OwnerScope в этом режиме не поддерживается. Непустой TeamID без доверенного Directory не трактуется как доказательство Team ownership и приводит к отказу.
- Локальный `local` scope существует только внутри соответствующего локального хранилища и не переносится автоматически в серверную Organization. Desktop не получает фиктивную Team directory.
- Отсутствие кластера, Organization, Team или однозначного ownership всегда fail closed. Organization ownership не означает доступ к Team-owned target.
- Ownership resolver не строит Kubernetes client и не читает credentials. Для чтения Kubernetes используются только существующие `PlanFor`/`ConfigFor` и credential Broker seams; Target domain не обращается к kubeconfig напрямую.

Ownership observation в Phase 2 не является долгоживущей authorization grant. При изменении TeamID/OrgID resolution прекращается; будущие Plan/side-effect слои обязаны заново разрешить ownership и authorization.

## Альтернативы

- Доверять owner fields, присланным frontend.
- Создать второй независимый каталог кластеров внутри Target.
- Выводить Team ownership из инициатора или текущей пользовательской сессии.
- Считать Organization ownership автоматическим доступом ко всем Team-owned targets.

Отклонены: они создают рассинхронизацию каталога и обходят tenant/delegation boundaries.

## Последствия и ограничения

- Integration owner должен определить адаптер к существующему k8s.ClusterStore и закрепить точную сигнатуру resolver-а в Phase 2 Contract Freeze.
- Нужны contract tests: unknown cluster, missing owner, stale owner, cross-org target, Team-owned target, resolver unavailable, spoofed client owner.
- Требуется проверить, как ownership changes отражаются на уже сохранённых Target Groups и ранее созданных snapshots. Предлагаемый безопасный default: новые resolution блокируются до согласованного ownership; существующий snapshot не переписывается задним числом, но перед будущим side effect будет заново авторизован.
- Этот ADR не меняет существующую модель tenant и не выдаёт permissions.
