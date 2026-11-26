// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// RPC проблем (ADR-0033 §7) и статистики правил (ADR-0034 §5, §8). Все операции работают только с сохранёнными
// данными, без подключения к кластеру (incidentStoredOp). Расследования они не удаляют и не меняют, кроме ссылки
// на проблему (это обеспечивает internal/incident).

// maxProblemAffected — предел списка затронутых нагрузок в ответе (как у кандидата корреляции).
const maxProblemAffected = correlation.MaxAffected

// workloadKey — ключ нагрузки для слияния повторов у разных участников проблемы.
type workloadKey struct{ kind, namespace, name string }

// affectedAcc собирает затронутые нагрузки проблемы по расследованиям-участникам.
type affectedAcc struct {
	seen  map[workloadKey]bool
	items []correlation.WorkloadRef
	total int
	trunc bool
}

// add добавляет нагрузки одного участника: без повторов, с учётом заявленного им общего числа и усечения.
func (acc *affectedAcc) add(ws []correlation.WorkloadRef, total int, truncated bool) {
	if acc.seen == nil {
		acc.seen = map[workloadKey]bool{}
	}
	for _, w := range ws {
		k := workloadKey{w.Kind, w.Namespace, w.Name}
		if acc.seen[k] {
			continue
		}
		acc.seen[k] = true
		acc.items = append(acc.items, w)
	}
	acc.total = max(acc.total, total)
	acc.trunc = acc.trunc || truncated
}

// result возвращает нагрузки в детерминированном порядке (namespace, вид, имя), не более maxProblemAffected,
// общее число (не меньше числа различных нагрузок) и признак усечения.
func (acc *affectedAcc) result() (items []correlation.WorkloadRef, total int, truncated bool) {
	slices.SortFunc(acc.items, func(a, b correlation.WorkloadRef) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
	total = max(acc.total, len(acc.items))
	truncated = acc.trunc || len(acc.items) > maxProblemAffected
	items = acc.items
	if len(items) > maxProblemAffected {
		items = items[:maxProblemAffected]
	}
	if items == nil {
		items = []correlation.WorkloadRef{}
	}
	return items, total, truncated
}

// incidentAffected возвращает нагрузки, которые расследование in вносит в проблему p. Для проблемы, созданной
// корреляцией, это нагрузки кандидата с тем же ключом из снимка расследования; для ручной проблемы снимка с
// таким ключом нет, и нагрузкой считается цель расследования.
func incidentAffected(p *incident.Problem, in *incident.Incident) (ws []correlation.WorkloadRef, total int, truncated bool) {
	if p.Origin == incident.ProblemManual {
		return []correlation.WorkloadRef{{Kind: in.Target.Kind, Namespace: in.Target.Namespace, Name: in.Target.Name}}, 1, false
	}
	if in.Correlation == nil {
		return nil, 0, false
	}
	for _, c := range in.Correlation.Candidates {
		if c.Key() == p.Key {
			return c.Affected, c.Total, c.Truncated
		}
	}
	return nil, 0, false
}

func problemSummary(p *incident.Problem, affectedTotal int) ProblemSummaryDTO {
	return ProblemSummaryDTO{ID: p.ID, Key: p.Key, Kind: p.Kind, Source: p.Source, Status: p.Status, Origin: p.Origin,
		MemberCount: len(p.Members), AffectedTotal: affectedTotal, UpdatedAt: p.UpdatedAt}
}

// listAffectedTotal — оценка числа затронутых нагрузок для строки списка: по самому новому участнику. Список не
// читает все расследования каждой проблемы; точное число — в подробностях (problemDetail).
func listAffectedTotal(ctx context.Context, svc *incident.Service, scope tenant.Scope, p *incident.Problem) int {
	if len(p.Members) == 0 {
		return 0
	}
	last := p.Members[len(p.Members)-1]
	d, err := svc.Get(ctx, scope, last.IncidentID, false)
	if err != nil {
		return 0
	}
	ws, total, _ := incidentAffected(p, d.Incident)
	return max(total, len(ws))
}

// problemDetail собирает подробности проблемы: участники со сводками и затронутые нагрузки. Удалённые расследования
// пропускаются; расследование, которое не удалось прочитать по другой причине, остаётся в составе без сводки.
func problemDetail(ctx context.Context, svc *incident.Service, scope tenant.Scope, p *incident.Problem) ProblemDetailDTO {
	var acc affectedAcc
	members := make([]ProblemMemberDTO, 0, len(p.Members))
	for _, m := range p.Members {
		dto := ProblemMemberDTO{IncidentID: m.IncidentID, AddedAt: m.AddedAt, AddedBy: m.AddedBy}
		d, err := svc.Get(ctx, scope, m.IncidentID, false)
		switch {
		case err == nil:
			s := d.Incident.Summarize()
			dto.Summary = &s
			ws, total, trunc := incidentAffected(p, d.Incident)
			acc.add(ws, total, trunc)
		case errors.Is(err, incident.ErrNotFound):
			continue // расследование удалено
		}
		members = append(members, dto)
	}
	affected, total, truncated := acc.result()
	detached := slices.Clone(p.Detached)
	if detached == nil {
		detached = []string{}
	}
	out := ProblemDetailDTO{ProblemSummaryDTO: problemSummary(p, total), Members: members, Affected: affected,
		AffectedTruncated: truncated, Detached: detached}
	out.MemberCount = len(members)
	return out
}

func problemIDOrEmpty(p *incident.Problem) string {
	if p == nil {
		return ""
	}
	return p.ID
}

// linkedProblemID возвращает проблему, в которую сейчас сгруппировано расследование, или "".
func linkedProblemID(ctx context.Context, svc *incident.Service, scope tenant.Scope, incidentID string) string {
	d, err := svc.Get(ctx, scope, incidentID, false)
	if err != nil {
		return ""
	}
	return d.Incident.ProblemID
}

// IncidentProblemList возвращает страницу проблем кластера, новые первыми. Число затронутых нагрузок в строке —
// оценка по самому новому участнику (см. ProblemSummaryDTO).
func (a *App) IncidentProblemList(clusterID string, q IncidentProblemListQuery) (list ProblemListDTO, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return ProblemListDTO{}, err
	}
	ctx, op, svc, err := a.incidentStoredOp(clusterID, incOpProblemList, auth.PermRead)
	if err != nil {
		return ProblemListDTO{}, err
	}
	defer func() { op.End(err) }()
	page, err := svc.ListProblems(ctx, op.Scope, incident.ProblemQuery{Status: q.Status, Limit: q.Limit, Cursor: q.Cursor})
	if err != nil {
		return ProblemListDTO{}, err
	}
	items := make([]ProblemSummaryDTO, 0, len(page.Items))
	for i := range page.Items {
		p := &page.Items[i]
		items = append(items, problemSummary(p, listAffectedTotal(ctx, svc, op.Scope, p)))
	}
	return ProblemListDTO{Items: items, NextCursor: page.NextCursor}, nil
}

// IncidentProblemGet возвращает проблему с составом и затронутыми workload'ами.
func (a *App) IncidentProblemGet(clusterID, problemID string) (d ProblemDetailDTO, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return ProblemDetailDTO{}, err
	}
	ctx, op, svc, err := a.incidentStoredOp(clusterID, incOpProblemGet, auth.PermRead)
	if err != nil {
		return ProblemDetailDTO{}, err
	}
	defer func() { op.End(err) }()
	p, err := svc.GetProblem(ctx, op.Scope, problemID)
	if err != nil {
		return ProblemDetailDTO{}, err
	}
	return problemDetail(ctx, svc, op.Scope, p), nil
}

// IncidentProblemAttach добавляет расследование в проблему вручную; если оно было в другой проблеме, выходит из неё.
func (a *App) IncidentProblemAttach(clusterID, problemID, incidentID string) (d ProblemDetailDTO, err error) {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return ProblemDetailDTO{}, err
	}
	ctx, op, svc, err := a.incidentStoredOp(clusterID, incOpProblemAttach, auth.PermOperate)
	if err != nil {
		return ProblemDetailDTO{}, err
	}
	defer func() { op.End(err) }()
	prev := linkedProblemID(ctx, svc, op.Scope, incidentID)
	p, err := svc.AttachToProblem(ctx, storedCaller(op), incidentID, problemID)
	op.Audit(incidentAction(incOpProblemAttach), problemID, err, slog.String("incident", incidentID))
	if err != nil {
		return ProblemDetailDTO{}, err
	}
	op.Notify(EventProblemChanged, p.ID)
	if prev != "" && prev != p.ID {
		op.Notify(EventProblemChanged, prev)
	}
	return problemDetail(ctx, svc, op.Scope, p), nil
}

// IncidentProblemDetach выводит расследование из проблемы; автоприсоединение его туда больше не вернёт.
func (a *App) IncidentProblemDetach(clusterID, incidentID string) (err error) {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return err
	}
	ctx, op, svc, err := a.incidentStoredOp(clusterID, incOpProblemDetach, auth.PermOperate)
	if err != nil {
		return err
	}
	defer func() { op.End(err) }()
	p, err := svc.DetachFromProblem(ctx, storedCaller(op), incidentID)
	op.Audit(incidentAction(incOpProblemDetach), incidentID, err)
	if err != nil {
		return err
	}
	op.Notify(EventProblemChanged, p.ID)
	return nil
}

// IncidentProblemMerge объединяет проблему fromID в toID; исходная закрывается.
func (a *App) IncidentProblemMerge(clusterID, fromID, toID string) (d ProblemDetailDTO, err error) {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return ProblemDetailDTO{}, err
	}
	ctx, op, svc, err := a.incidentStoredOp(clusterID, incOpProblemMerge, auth.PermOperate)
	if err != nil {
		return ProblemDetailDTO{}, err
	}
	defer func() { op.End(err) }()
	to, err := svc.MergeProblems(ctx, storedCaller(op), fromID, toID)
	op.Audit(incidentAction(incOpProblemMerge), fromID, err, slog.String("into", toID))
	if err != nil {
		return ProblemDetailDTO{}, err
	}
	op.Notify(EventProblemChanged, to.ID)
	op.Notify(EventProblemChanged, fromID)
	return problemDetail(ctx, svc, op.Scope, to), nil
}

// IncidentProblemCreate создаёт проблему вручную из выбранных расследований (в том числе чтобы выделить часть
// участников другой проблемы).
func (a *App) IncidentProblemCreate(clusterID string, incidentIDs []string) (d ProblemDetailDTO, err error) {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return ProblemDetailDTO{}, err
	}
	ctx, op, svc, err := a.incidentStoredOp(clusterID, incOpProblemCreate, auth.PermOperate)
	if err != nil {
		return ProblemDetailDTO{}, err
	}
	defer func() { op.End(err) }()
	p, err := svc.CreateProblem(ctx, storedCaller(op), incidentIDs)
	op.Audit(incidentAction(incOpProblemCreate), problemIDOrEmpty(p), err, slog.Int("incidents", len(incidentIDs)))
	if err != nil {
		return ProblemDetailDTO{}, err
	}
	op.Notify(EventProblemChanged, p.ID)
	return problemDetail(ctx, svc, op.Scope, p), nil
}

// IncidentRuleStats возвращает статистику правил по закрытым расследованиям кластера (до 200 последних).
func (a *App) IncidentRuleStats(clusterID string) (st RuleStatsDTO, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return RuleStatsDTO{}, err
	}
	ctx, op, svc, err := a.incidentStoredOp(clusterID, incOpRuleStats, auth.PermRead)
	if err != nil {
		return RuleStatsDTO{}, err
	}
	defer func() { op.End(err) }()
	st, err = svc.RuleStats(ctx, op.Scope)
	return st, err
}

// IncidentRuleStatsExport выгружает анонимную статистику правил файлом для ручной отправки мантейнерам. Право то же,
// что у настроек хранения расследований (users.manage). В файле только коды правил и счётчики; в аудит — только размер.
func (a *App) IncidentRuleStatsExport(clusterID string) (file ExportFileDTO, err error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return ExportFileDTO{}, err
	}
	ctx, op, svc, err := a.incidentStoredOp(clusterID, incOpRuleStatsExport, auth.PermRead)
	if err != nil {
		return ExportFileDTO{}, err
	}
	defer func() { op.End(err) }()
	data, err := svc.ExportRuleStats(ctx, op.Scope)
	op.Audit(incidentAction(incOpRuleStatsExport), "", err, slog.Int("bytes", len(data)))
	if err != nil {
		return ExportFileDTO{}, err
	}
	return ExportFileDTO{
		Filename:   "kubron-rule-stats-" + time.Now().UTC().Format("20060102") + ".json",
		MIME:       "application/json",
		DataBase64: base64.StdEncoding.EncodeToString(data),
	}, nil
}
