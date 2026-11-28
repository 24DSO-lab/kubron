# Architecture Decision Records

Формат: контекст → решение → альтернативы → последствия. Статусы: предложено, принято, реализовано, заменено.

ADR-0001…ADR-0018 утверждены тимлидом; их статус — `принято`.
ADR-0019…ADR-0022 оформлены ретроспективно 2026-11-05: проект начинался без ADR, а решения уже были реализованы и описаны в `docs/`, CHANGELOG и коде. Они фиксируют то, что есть в репозитории, и прямо отмечают, где причины выбора не записаны.
ADR-0023 принят 2026-11-07 (Vadim).
ADR-0029 предложен 2026-11-09. ADR-0030 предложен 2026-11-10 и заменяет его п. 3.
ADR-0024…ADR-0027 предложены 2026-11-08, приняты и реализованы 2026-11-13 (статус — `реализовано`).
ADR-0028 предложен 2026-11-09.
ADR-0031 предложен 2026-11-14.
ADR-0032 предложен 2026-11-14.
ADR-0033 предложен 2026-11-14.
ADR-0034 предложен 2026-11-14.

## Решения после выпуска v1.3.0

v1.3.0 выпущена 2026-11-26. ADR-0035…0038 фиксируют архитектурные решения Phase 1. На момент подготовки этих ADR runtime implementation ещё не была выполнена. По состоянию на 2026-11-28 реализация Phase 1 (Identity, Credential Resolution, Authorization и Critical Audit lanes) интегрирована в ветку `refactor`; CI для интегрированного состояния зелёный. Это не означает, что Target/Policy, Plans, Run Engine или Kubernetes write actions уже реализованы.

ADR-0039…0052 — пакет архитектурной фиксации целевой модели после v1.3.0. Статус каждого ADR указан в самом документе. Принятые ранее решения записаны ретроспективно; новые технические механизмы, для которых ещё требуется доказательство или реализационный дизайн, имеют статус `предложено`.

| № | Решение | Статус |
|---|---|---|
| [0035](0035-technical-principals-and-identity-lifecycle.md) | Технические principals и жизненный цикл Identity | принято |
| [0036](0036-subject-aware-credential-resolution.md) | Subject-aware credentials и безопасный путь через ConfigFor | принято |
| [0037](0037-technical-authorization-grants-and-delegation.md) | Grants технических principals и Cross-Team Delegation | принято |
| [0038](0038-critical-audit-durability.md) | Durable Critical Audit для защищённых mutations | принято |
| [0039](0039-platform-architecture-and-domain-boundaries.md) | Целевая архитектура Kubron и границы доменов | принято |
| [0040](0040-operation-context-and-resource-identity.md) | Operation Context и Resource Identity | принято |
| [0041](0041-target-groups-and-scope-resolution.md) | Target Groups и безопасное разрешение scope | принято |
| [0042](0042-autonomy-policy-and-profiles.md) | Autonomy Policy, иерархия ограничений и Profiles | принято |
| [0043](0043-typed-action-catalog-and-risk-model.md) | Typed Operation/Action Catalog и модель риска | предложено |
| [0044](0044-immutable-plans-and-approval-binding.md) | Immutable Plans и привязка Approval | принято |
| [0045](0045-atomic-plan-commit-and-revision-consistency.md) | Atomic Plan Commit и согласованность Revision | предложено |
| [0046](0046-durable-run-step-execution-and-concurrency.md) | Durable Run/Step Execution, leases и fencing | принято |
| [0047](0047-verification-recovery-and-cancellation.md) | Verification, Recovery и безопасная отмена операций | принято |
| [0048](0048-cross-cluster-orchestration.md) | Cross-cluster Orchestration и независимые sub-runs | принято |
| [0049](0049-schedule-ownership-and-runtime-semantics.md) | Ownership и runtime semantics расписаний | принято |
| [0050](0050-retention-and-evidence-lifecycle.md) | Retention Run/Step, Evidence и Critical Audit | принято |
| [0051](0051-goal-based-operations-and-ai-boundary.md) | Goal-based Operations и границы AI/Intelligence | предложено |
| [0052](0052-phased-delivery-and-write-action-gates.md) | Поэтапная реализация и gates для Kubernetes write actions | принято |

Новое решение — новый файл `NNNN-slug.md`; принятые ADR не переписываются, а заменяются новыми.

| № | Решение |
|---|---|
| [0001](0001-code-layout.md) | Структура кода: ядро в internal/app, две оболочки |
| [0002](0002-platform-interface.md) | Интерфейс Platform вместо вызовов Wails из ядра |
| [0003](0003-web-transport.md) | Транспорт веба: универсальный RPC + WebSocket |
| [0004](0004-sessions.md) | Сессии: cookie + CSRF + Origin |
| [0005](0005-cluster-manager.md) | Менеджер кластеров: общий пул + сессионный вид |
| [0006](0006-k8s-access-model.md) | Модель доступа к Kubernetes |
| [0007](0007-storage.md) | Хранилище: интерфейс Store, файлы и PostgreSQL |
| [0008](0008-secrets.md) | Шифрование секретов в хранилище |
| [0009](0009-events.md) | Адресные события; серверные ID |
| [0010](0010-frontend-platform.md) | Фронтенд: слой platform/ и сборка по цели |
| [0011](0011-sso-extension-point.md) | SSO: стандартный OIDC за интерфейсом Authenticator |
| [0012](0012-tenancy.md) | Мультитенантность: организация → команда → кластер |
| [0013](0013-licensing.md) | Лицензия: Apache-2.0 |
| [0014](0014-i18n.md) | Локализация интерфейса: vue-i18n, английский как базовый язык |
| [0015](0015-helm-state-service-mode.md) | Состояние Helm в сервисном режиме и защита от SSRF |
| [0016](0016-ha.md) | Высокая доступность: сессии в PostgreSQL, события между репликами |
| [0017](0017-http-port-forward-web.md) | HTTP port-forward в вебе: отдельный origin на поток, ticket, аудит |
| [0018](0018-cluster-auth-providers.md) | Способы входа в кластер: провайдеры учётных данных и вызовы (challenge) |
| [0019](0019-password-hashing.md) | Хеширование паролей: Argon2id и миграция с PBKDF2 |
| [0020](0020-http-security.md) | Защита HTTP-слоя kubron-server |
| [0021](0021-release-supply-chain.md) | Выпуск и цепочка поставки: один тег, подписанные образ и чарт, сканеры в CI |
| [0022](0022-logging-and-metrics.md) | Наблюдаемость: журнал процесса, аудит в stdout, метрики на отдельном порту |
| [0023](0023-flux-support.md) | Поддержка Flux CD рядом с Argo CD |
| [0024](0024-kubron-intelligence.md) | Kubron Intelligence: диагностика и расследование инцидентов Kubernetes |
| [0025](0025-kubron-lifecycle.md) | Kubron Lifecycle: оценка готовности и контроль изменений Kubernetes |
| [0026](0026-shared-platform-foundation.md) | Общая платформа для операционных модулей Kubron |
| [0027](0027-config-drift-baseline.md) | Обнаружение дрейфа конфигурации и baseline |
| [0028](0028-update-check.md) | Проверка обновлений и способ установки |
| [0029](0029-bug-report.md) | «Сообщить об ошибке»: снимок экрана и письмо из десктопа |
| [0030](0030-bug-report-receiver.md) | «Сообщить об ошибке»: приёмник отчётов и очередь вместо `mailto:` |
| [0031](0031-dependency-graph.md) | Граф зависимостей и цепочечная диагностика Kubron Intelligence |
| [0032](0032-metrics-evidence.md) | Метрики как необязательный источник доказательств Kubron Intelligence |
| [0033](0033-correlation-problems.md) | Корреляция расследований по живым данным и группировка в проблемы |
| [0034](0034-feedback-loop.md) | Обратная связь по результатам расследований: детерминированная статистика правил |
