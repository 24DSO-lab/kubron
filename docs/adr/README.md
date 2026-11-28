# Architecture Decision Records

**Единственный постоянный источник архитектурных решений Kubron.** Решение, его ограничения и текущий статус находятся в соответствующем ADR. Другие постоянные архитектурные спецификации и review-документы не ведутся.

## Жизненный цикл ADR

- **Proposed** — предложено, не согласовано.
- **Accepted** — архитектурно принято, реализация не начата.
- **In Progress** — реализация начата.
- **Implemented** — реализация завершена, проверена и интегрирована.
- **Rejected** — отклонено.
- **Superseded** — заменено другим ADR (указать заменяющий ADR).

Статус изменяется непосредственно в ADR по мере разработки. Само слияние документационного PR не означает принятия решения. Реализация считается завершённой после проверки и интеграции изменений.

Промпты разработчикам — временные рабочие задания. Их храним только пока задача выполняется; после завершения обновляем статус связанных ADR и удаляем промпты.

## Индекс

| ADR | Решение |
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
| [0035](0035-technical-principals-and-identity-lifecycle.md) | Технические principals и жизненный цикл Identity |
| [0036](0036-subject-aware-credential-resolution.md) | Subject-aware credentials и безопасный путь через ConfigFor |
| [0037](0037-technical-authorization-grants-and-delegation.md) | Grants технических principals и Cross-Team Delegation |
| [0038](0038-critical-audit-durability.md) | Durable Critical Audit для защищённых mutations |
| [0039](0039-platform-architecture-and-domain-boundaries.md) | Целевая архитектура Kubron и границы доменов |
| [0040](0040-operation-context-and-resource-identity.md) | Operation Context и Resource Identity |
| [0041](0041-target-groups-and-scope-resolution.md) | Target Groups и безопасное разрешение scope |
| [0042](0042-autonomy-policy-and-profiles.md) | Autonomy Policy, иерархия ограничений и Profiles |
| [0043](0043-typed-action-catalog-and-risk-model.md) | Typed Operation/Action Catalog и модель риска |
| [0044](0044-immutable-plans-and-approval-binding.md) | Immutable Plans и привязка Approval |
| [0045](0045-atomic-plan-commit-and-revision-consistency.md) | Atomic Plan Commit и согласованность Revision |
| [0046](0046-durable-run-step-execution-and-concurrency.md) | Durable Run/Step Execution, leases и fencing |
| [0047](0047-verification-recovery-and-cancellation.md) | Verification, Recovery и безопасная отмена операций |
| [0048](0048-cross-cluster-orchestration.md) | Cross-cluster Orchestration и независимые sub-runs |
| [0049](0049-schedule-ownership-and-runtime-semantics.md) | Ownership и runtime semantics расписаний |
| [0050](0050-retention-and-evidence-lifecycle.md) | Retention Run/Step, Evidence и Critical Audit |
| [0051](0051-goal-based-operations-and-ai-boundary.md) | Goal-based Operations и границы AI/Intelligence |
| [0052](0052-phased-delivery-and-write-action-gates.md) | Поэтапная реализация и gates для Kubernetes write actions |
| [0053](0053-trusted-target-catalog-and-ownership-resolution.md) | Доверенный каталог целей и разрешение ownership |
| [0054](0054-target-group-aggregate-and-revision-lifecycle.md) | Target Group aggregate и жизненный цикл Revision |
| [0055](0055-consistent-target-resolution-and-immutable-snapshots.md) | Согласованное разрешение целей и immutable Target Snapshots |
| [0056](0056-deterministic-autonomy-policy-evaluation.md) | Детерминированная оценка Autonomy Policy |
| [0057](0057-autonomy-profile-versioning-and-binding.md) | Версионирование Autonomy Profiles и правила binding |
| [0058](0058-phase-2-scope-and-exit-gates.md) | Границы Phase 2 и критерии завершения |
| [0059](0059-machine-error-codes-for-client-decisions.md) | Машинные коды ошибок для решений клиента |
