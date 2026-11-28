// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package target — доменный пакет Target Foundation (Phase 2, Lane A; ADR-0053, ADR-0054, ADR-0055).
//
// Пакет владеет:
//   - OwnerScope (ровно Organization либо Team) и Actor/Viewer — owner.go;
//   - декларативным selector'ом с серверными пределами — selector.go, limits.go;
//   - агрегатом Target Group (CAS по Revision, tombstone-деактивация) — group.go;
//   - ResolutionReport (диагностика, может быть partial) и TargetSnapshot (только полный результат, у каждой цели
//     есть UID) — snapshot.go;
//   - узкими портами для Lane C и интегратора — ports.go;
//   - use cases (управление группами, preview и публикация snapshot) — service.go, resolve.go.
//
// Чего здесь нет намеренно: Plans, Approvals, Run/Step, leases, scheduler, мутаций Kubernetes, второго каталога
// кластеров и tenant-модели, чтения kubeconfig и сборки rest.Config. Чтение Kubernetes идёт только через порт
// ClusterReader, который Lane C реализует поверх существующих Broker/PlanFor/ConfigFor. Результат resolution и
// принадлежность кластера — не authorization: ownership, grants, delegation, credentials и Kubernetes RBAC
// проверяются заново будущими слоями.
//
// Зависимости: только стандартная библиотека. Пакет не импортирует pgstore, store, transport, Wails/Vue; алфавит
// идентификаторов совпадает с identity.ValidID (общий для org/team/cluster/actor ID). internal/tenant.Scope как
// owner не используется: он требует ClusterID, а владелец группы — ровно Organization или Team (ADR-0054 п.10).
//
// Ошибки — типизированные *Error (Kind + стабильный Reason + имя поля, без значений). Пакет намеренно не создаёт
// ошибок через errors.New/fmt.Errorf со свободным текстом: backend возвращает идентификаторы, подписи разрешаются
// на фронтенде (CONTRIBUTING, «UI text and localization», п.3). Сравнивать ошибки только через errors.Is.
package target
