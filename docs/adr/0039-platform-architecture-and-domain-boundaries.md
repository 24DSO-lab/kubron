# ADR-0039: Целевая архитектура Kubron и границы доменов

- Статус: принято (ретроспективная фиксация согласованной целевой архитектуры)
- Дата: 2026-11-28
- Область: развитие после выпуска v1.3.0
- Связано: ADR-0001, ADR-0002, ADR-0007, ADR-0010, ADR-0012, ADR-0016, ADR-0024…0026, ADR-0035…0038; Kubron Platform Architecture Roadmap; Executable Platform Architecture Specification

## Контекст

После v1.3.0 Kubron развивается от набора инструментов управления и диагностики Kubernetes к единой платформе управления операциями и понимания инфраструктуры. Развитие включает Control Plane, Operations Platform, Intelligence Platform и слой Autonomy. При этом репозиторий уже содержит Go application core, Desktop на Wails, Web на HTTP/RPC и общие frontend/domain seams.

Необходимо закрепить целевую архитектуру, чтобы новые возможности не создавали параллельные платформы, дублирующие модели доступа или отдельные execution paths.

## Решение

1. Продукт развивается как единая платформа **Kubron — Kubernetes Operations & Intelligence Platform** со следующими логическими слоями:
   - **Foundation:** tenancy, identity, credentials, authorization, storage, audit, events и platform capabilities.
   - **Control Plane:** каталог кластеров, владельцы, scope, target groups и координация.
   - **Operations Platform:** типизированные операции, планы, approvals, исполнение, verification и recovery.
   - **Intelligence Platform:** сбор и корреляция evidence, диагностика, объяснения, рекомендации и обратная связь.
   - **Autonomy Layer:** policy evaluation и управление допустимым уровнем автоматизации поверх Operations.
2. Это логические доменные границы, а не требование развернуть отдельные микросервисы. Основной deployment и Go application core остаются едиными, пока отдельное архитектурное решение не докажет необходимость декомпозиции.
3. Desktop и Web — два полноценных клиента общей платформы:
   - Desktop использует Wails и локальный Go runtime.
   - Web использует HTTP/RPC и серверный Go runtime.
   - Domain services, security semantics, state machines и application use cases общие.
   - Различия локальной среды выражаются через существующие Platform interfaces/capabilities и adapters.
4. Backend является единственным источником истины для identity, authorization, policy, approvals, Plans, Runs и их состояний. UI, WebSocket и локальное состояние frontend не принимают решений о полномочиях и не являются источником истины.
5. Существующие seams расширяются, а не обходятся: `internal/app`, `pkg/ext.Platform`, `Capabilities`, `frontend/src/platform/`, `internal/platform/wails`, `internal/platform/web`, существующий credential Broker/Provider и `ConfigFor`.
6. Новые домены владеют собственными типами и узкими Backend interfaces. Предпочтительные границы включают identity, credential, authorization, target, autonomy, operation, scheduler, audit и evidence. Domain packages не импортируют transport, Wails, Vue или конкретный PostgreSQL adapter.
7. Сохраняются общие семантики Desktop/Web при разных runtime owners и persistence adapters. Локальные capabilities Desktop не дают дополнительных Kubron permissions.
8. Не вводятся как обязательные архитектурные предпосылки: микросервисная декомпозиция, отдельный control-plane deployment, обязательный агент/CRD/operator в каждом кластере, обязательный message broker, отдельный Desktop daemon или универсальный workflow engine.
9. Интеллектуальные функции могут предоставлять evidence, hypotheses и recommendations, но не становятся источником прав, tenant ownership, policy decisions или единственным доказательством причины.

## Альтернативы

- Разделить Control Plane, Operations и Intelligence на независимые микросервисы немедленно.
- Создать параллельный execution framework вне существующего application core.
- Реализовать разные security и state semantics для Desktop и Web.
- Сделать UI или WebSocket источником состояния операций.

Эти варианты отклонены из-за избыточной распределённой сложности, дублирования логики и риска расхождения гарантий безопасности.

## Последствия

- Новые возможности проектируются как домены единой платформы с явными владельцами типов и зависимостей.
- Каждая функция обязана описывать общие domain contracts, Desktop/Web adapters, persistence и platform-specific limitations.
- Cross-platform contract tests обязательны для критичных сценариев.
- Runtime и persistence могут различаться, но security decisions и terminal states должны иметь одинаковый смысл.
- Этот ADR фиксирует целевую архитектуру и не означает, что все перечисленные слои уже реализованы.
