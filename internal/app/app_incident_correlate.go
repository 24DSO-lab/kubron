// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/incident"
)

// incOpCorrelationRecompute — операция пересчёта корреляции; имя действия аудита — IncidentCorrelationRecompute.
const incOpCorrelationRecompute = "correlation_recompute"

// IncidentCorrelationRecompute пересчитывает снимок корреляции открытого расследования по живым данным кластера
// (ADR-0033 §5). Снимок Events, отчёт, фрагменты и проблема расследования не меняются. Закрытое расследование не
// пересчитывается (incident.ErrClosed, как у IncidentRefresh). Право — operate: пересчёт читает кластер по
// запросу пользователя, но пишет только производный снимок; в аудит попадают идентификатор расследования и число
// найденных кандидатов, без содержимого снимка.
func (a *App) IncidentCorrelationRecompute(clusterID, id string) (d *incident.Detail, err error) {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return nil, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, incOpCorrelationRecompute, auth.PermOperate)
	if err != nil {
		return nil, err
	}
	defer func() { op.End(err) }()
	// Метрики корреляции не нужны: вызывающий — без источника сигналов.
	caller := incident.Caller{Scope: op.Scope, Actor: op.Initiator, Reader: diagReader{op.Reader}}
	in, err := svc.RecomputeCorrelation(ctx, caller, id)
	candidates := 0
	if in != nil && in.Correlation != nil {
		candidates = len(in.Correlation.Candidates)
	}
	op.Audit(incidentAction(incOpCorrelationRecompute), id, err, slog.Int("candidates", candidates))
	if err != nil {
		return nil, err
	}
	op.Notify(EventIncidentUpdated, id)
	return svc.Get(ctx, op.Scope, id, a.canReadLogs(clusterID))
}
