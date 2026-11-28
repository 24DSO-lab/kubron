// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package authorization — Kubron-grants технических principals и Team-issued Cross-Team Delegation
// (Phase 1, Lane C, ADR-0037, Phase 1 Contract Freeze §6 и Contract Amendment 01).
//
// Пакет отвечает ТОЛЬКО на вопрос «разрешают ли Kubron grants + обязательная delegation + границы
// тенантности этому технического principal'у кортеж (OperationID, ActionID, Target)». Он не авторизует
// вызовы Kubernetes API: GrantDecision — не итоговое разрешение, не bearer-токен и не подменяет
// Kubernetes RBAC, валидность credential, Autonomy Policy, Approval, Plan preconditions и guardrails
// конкретного действия. Пакет не загружает credentials, не строит Kubernetes-клиентов и не пишет в кластер.
//
// Состав:
//   - types.go — доменные типы, ошибки, каноникализация и валидация;
//   - backend.go — узкие интерфейсы Backend и инъектируемые порты (владение, факты действий, часы);
//   - evaluator.go — Evaluator (алгоритм §6.3), стабильные reason-коды описаны в REASONS.md;
//   - service.go — use cases мутаций grants/delegation с обязательным критичным аудитом (§7.2);
//   - file.go — File backend (Desktop, один процесс);
//   - authorizationtest — переиспользуемый контрактный набор для любой реализации Backend.
//
// Зависимости: identity (канонические Principal/Actor/IdentityState), tenant (tenant.Scope), audit (типы
// и интерфейс CriticalRecorder). authorization НЕ импортирует credential, k8s, pgstore и app.
//
// authorization.OwnershipResolver намеренно отличен от identity.ClusterOwnerResolver: адаптеры могут читать
// один и тот же доверенный каталог, но доменные контракты не взаимозаменяемы и второй источник истины о
// владении не создаётся.
package authorization
