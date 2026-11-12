// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/jobs"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
	"github.com/24DSO-lab/kubron/internal/store"
)

// EventIncidentUpdated — снимок Events расследования обновлён (адресно сессии, ADR-0009). Данные — ID инцидента.
const EventIncidentUpdated = "incident:updated"

const moduleIncident = "incident"

var errIncidentsUnavailable = errors.New("incident storage is not available")

// SetupIncidents подключает сервис расследований: хранилище берётся из общего Store (PostgreSQL) или из каталога
// данных (десктоп), значения по умолчанию — из KUBRON_INCIDENT_*. Возвращает задачу retention для internal/jobs
// (в server — с Locker, в десктопе — без). Это функция пакета, а не метод App: в Wails и RPC она не попадает.
func SetupIncidents(root *App, getenv func(string) string) (*jobs.Job, error) {
	cfg, err := incident.ConfigFromEnv(getenv)
	if err != nil {
		return nil, err
	}
	st := root.store
	if st == nil {
		st = store.NewFile(k8s.GetKubronDir())
	}
	svc := incident.NewService(st.Incidents(), incidentRedactor(), incident.WithDefaults(cfg.Defaults))
	root.incidents = svc
	return &jobs.Job{Name: "incident retention", Every: cfg.PurgeInterval, Logf: func(format string, args ...any) {
		slog.Info(fmt.Sprintf(format, args...), "component", "incident")
	}, Run: svc.Purge}, nil
}

// IncidentOpenRequest — открытие расследования для workload (Pod, Deployment или ReplicaSet).
type IncidentOpenRequest struct {
	Kind        string `json:"kind"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	DisableLogs bool   `json:"disableLogs"`
}

// IncidentListQuery — страница истории.
type IncidentListQuery struct {
	Limit  int             `json:"limit"`
	Cursor string          `json:"cursor"`
	Status incident.Status `json:"status"`
}

// IncidentOutcomeRequest — отметка результата расследования.
type IncidentOutcomeRequest struct {
	Result                incident.Result `json:"result"`
	ConfirmedHypothesisID string          `json:"confirmedHypothesisId"`
	Note                  string          `json:"note"`
}

// IncidentFragmentRequest — явное сохранение фрагмента лога как доказательства.
type IncidentFragmentRequest struct {
	Pod        string `json:"pod"`
	Container  string `json:"container"`
	Previous   bool   `json:"previous"`
	AnchorLine int    `json:"anchorLine"`
}

// IncidentSettingsInfo — настройки хранения и пределы из кода (для экрана настроек).
type IncidentSettingsInfo struct {
	Settings incident.Settings `json:"settings"`
	Limits   IncidentLimits    `json:"limits"`
	// CanManage — пользователь вправе менять настройки (администратор).
	CanManage bool `json:"canManage"`
}

// IncidentLimits — фиксированные пределы (ADR-0024 §5): интерфейс их показывает, но не меняет.
type IncidentLimits struct {
	FragmentLines            int `json:"fragmentLines"`
	FragmentBytes            int `json:"fragmentBytes"`
	LineBytes                int `json:"lineBytes"`
	FragmentsPerIncident     int `json:"fragmentsPerIncident"`
	IncidentFragmentBytes    int `json:"incidentFragmentBytes"`
	MaxFragmentRetentionDays int `json:"maxFragmentRetentionDays"`
	MaxIncidentRetentionDays int `json:"maxIncidentRetentionDays"`
}

func (a *App) incidentOp(clusterID, operation string, perm auth.Permission) (context.Context, *opsplatform.Op, *incident.Service, error) {
	if a.incidents == nil {
		return nil, nil, nil, errIncidentsUnavailable
	}
	ctx, op, err := a.beginOp(opsplatform.OpRequest{Module: moduleIncident, Operation: operation, ClusterID: clusterID, Permission: perm})
	if err != nil {
		return nil, nil, nil, err
	}
	return ctx, op, a.incidents, nil
}

func incidentCaller(op *opsplatform.Op) incident.Caller {
	return incident.Caller{Scope: op.Scope, Actor: op.Initiator, Reader: diagReader{op.Reader}}
}

// canReadLogs — есть ли у пользователя право на содержимое фрагментов. Проверка без записи отказа в аудит: она
// только определяет, скрывать ли содержимое в ответе (само чтение проверяется отдельно, при каждом обращении).
func (a *App) canReadLogs(clusterID string) bool {
	if a.auth == nil {
		return false
	}
	_, err := a.auth.CheckCluster(auth.PermDiagnosticsLogs, clusterID)
	return err == nil
}

// IncidentOpen открывает расследование для workload: контекст, findings, гипотезы, снимок Events и фрагменты логов.
func (a *App) IncidentOpen(clusterID string, req IncidentOpenRequest) (d *incident.Detail, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return nil, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "open", auth.PermRead)
	if err != nil {
		return nil, err
	}
	defer func() { op.End(err) }()
	in, err := svc.Open(ctx, incidentCaller(op), incident.OpenRequest{
		Target:      diagnose.ResourceRef{Kind: req.Kind, Namespace: req.Namespace, Name: req.Name},
		DisableLogs: req.DisableLogs,
	})
	op.Audit("IncidentOpen", idOrEmpty(in), err, slog.String("kind", req.Kind), slog.String("namespace", req.Namespace), slog.String("name", req.Name))
	if err != nil {
		return nil, err
	}
	return svc.Get(ctx, op.Scope, in.ID, a.canReadLogs(clusterID))
}

func idOrEmpty(in *incident.Incident) string {
	if in == nil {
		return ""
	}
	return in.ID
}

// IncidentRefresh обновляет снимок Events открытого расследования.
func (a *App) IncidentRefresh(clusterID, id string) (d *incident.Detail, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return nil, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "refresh", auth.PermRead)
	if err != nil {
		return nil, err
	}
	defer func() { op.End(err) }()
	if _, err = svc.Refresh(ctx, incidentCaller(op), id); err != nil {
		return nil, err
	}
	op.Notify(EventIncidentUpdated, id)
	return svc.Get(ctx, op.Scope, id, a.canReadLogs(clusterID))
}

// IncidentGet возвращает инцидент и метаданные фрагментов; содержимое без права diagnostics.logs скрыто.
func (a *App) IncidentGet(clusterID, id string) (d *incident.Detail, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return nil, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "get", auth.PermRead)
	if err != nil {
		return nil, err
	}
	defer func() { op.End(err) }()
	return svc.Get(ctx, op.Scope, id, a.canReadLogs(clusterID))
}

// IncidentList возвращает страницу истории кластера.
func (a *App) IncidentList(clusterID string, q IncidentListQuery) (p incident.Page, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return incident.Page{}, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "list", auth.PermRead)
	if err != nil {
		return incident.Page{}, err
	}
	defer func() { op.End(err) }()
	return svc.List(ctx, op.Scope, incident.ListQuery{Limit: q.Limit, Cursor: q.Cursor, Status: q.Status})
}

// IncidentMarkOutcome отмечает результат расследования и закрывает его.
func (a *App) IncidentMarkOutcome(clusterID, id string, req IncidentOutcomeRequest) (d *incident.Detail, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return nil, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "outcome", auth.PermRead)
	if err != nil {
		return nil, err
	}
	defer func() { op.End(err) }()
	_, err = svc.MarkOutcome(ctx, incidentCaller(op), id, incident.Outcome{Result: req.Result,
		ConfirmedHypothesisID: req.ConfirmedHypothesisID, Note: req.Note})
	op.Audit("IncidentMarkOutcome", id, err, slog.String("result", string(req.Result)))
	if err != nil {
		return nil, err
	}
	return svc.Get(ctx, op.Scope, id, a.canReadLogs(clusterID))
}

// IncidentSaveFragment сохраняет фрагмент лога как доказательство по явному действию (право read — допущение NOTES).
func (a *App) IncidentSaveFragment(clusterID, id string, req IncidentFragmentRequest) (m incident.FragmentMeta, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return incident.FragmentMeta{}, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "fragment_save", auth.PermRead)
	if err != nil {
		return incident.FragmentMeta{}, err
	}
	defer func() { op.End(err) }()
	m, err = svc.SaveFragment(ctx, incidentCaller(op), id, incident.SaveFragmentRequest{Pod: req.Pod,
		Container: req.Container, Previous: req.Previous, AnchorLine: req.AnchorLine})
	op.Audit("IncidentSaveFragment", id, err, slog.String("container", req.Container), slog.Bool("previous", req.Previous))
	return m, err
}

// IncidentDeleteFragment удаляет сохранённый фрагмент (право diagnostics.logs — допущение NOTES).
func (a *App) IncidentDeleteFragment(clusterID, id, fragmentID string) (err error) {
	if err := a.authzCluster(auth.PermDiagnosticsLogs, clusterID); err != nil {
		return err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "fragment_delete", auth.PermDiagnosticsLogs)
	if err != nil {
		return err
	}
	defer func() { op.End(err) }()
	err = svc.DeleteFragment(ctx, op.Scope, id, fragmentID)
	op.Audit("IncidentDeleteFragment", id, err, slog.Int("fragments", 1))
	return err
}

// IncidentSetLogCapture включает или отключает сохранение фрагментов для расследования; deleteExisting удаляет
// сохранённые. Отключение — право read; удаление — как удаление фрагмента (diagnostics.logs).
func (a *App) IncidentSetLogCapture(clusterID, id string, enabled, deleteExisting bool) (d *incident.Detail, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return nil, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "log_capture", auth.PermRead)
	if err != nil {
		return nil, err
	}
	defer func() { op.End(err) }()
	canRead := a.canReadLogs(clusterID)
	if deleteExisting && !canRead {
		err = op.Require(auth.PermDiagnosticsLogs)
		return nil, err
	}
	if _, err = svc.SetSaveLogs(ctx, op.Scope, id, enabled, deleteExisting); err != nil {
		return nil, err
	}
	op.Audit("IncidentSetLogCapture", id, nil, slog.Bool("enabled", enabled), slog.Bool("deleted", deleteExisting))
	return svc.Get(ctx, op.Scope, id, canRead)
}

// IncidentReadFragment возвращает содержимое фрагмента. Право diagnostics.logs проверяется на сервере при каждом
// чтении; чтение записывается в аудит без содержимого.
func (a *App) IncidentReadFragment(clusterID, id, fragmentID string) (f *incident.Fragment, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return nil, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "fragment_read", auth.PermRead)
	if err != nil {
		return nil, err
	}
	defer func() { op.End(err) }()
	f, err = svc.ReadFragment(ctx, op.Scope, id, fragmentID, func() error { return op.Require(auth.PermDiagnosticsLogs) })
	op.Audit("IncidentReadFragment", id, err, slog.Int("fragments", 1))
	if err != nil {
		return nil, err
	}
	return f, nil
}

// IncidentExport выгружает инцидент в JSON. Фрагменты логов — только по явному выбору и при diagnostics.logs.
func (a *App) IncidentExport(clusterID, id string, includeFragments bool) (out string, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return "", err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "export", auth.PermRead)
	if err != nil {
		return "", err
	}
	defer func() { op.End(err) }()
	ex, err := svc.ExportIncident(ctx, op.Scope, id, includeFragments, func() error { return op.Require(auth.PermDiagnosticsLogs) })
	n := 0
	if ex != nil {
		n = len(ex.Fragments)
	}
	op.Audit("IncidentExport", id, err, slog.Int("fragments", n), slog.Bool("include_fragments", includeFragments))
	if err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(ex, "", "  ")
	return string(raw), err
}

// IncidentDelete удаляет инцидент вместе с фрагментами (право diagnostics.logs — допущение NOTES).
func (a *App) IncidentDelete(clusterID, id string) (err error) {
	if err := a.authzCluster(auth.PermDiagnosticsLogs, clusterID); err != nil {
		return err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "delete", auth.PermDiagnosticsLogs)
	if err != nil {
		return err
	}
	defer func() { op.End(err) }()
	err = svc.Delete(ctx, op.Scope, id)
	op.Audit("IncidentDelete", id, err)
	return err
}

// IncidentGetSettings возвращает настройки хранения организации (приложения) и пределы из кода.
func (a *App) IncidentGetSettings(clusterID string) (info IncidentSettingsInfo, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return IncidentSettingsInfo{}, err
	}
	ctx, op, svc, err := a.incidentOp(clusterID, "settings_get", auth.PermRead)
	if err != nil {
		return IncidentSettingsInfo{}, err
	}
	defer func() { op.End(err) }()
	st, err := svc.Settings(ctx, op.Scope.OrgID)
	if err != nil {
		return IncidentSettingsInfo{}, err
	}
	_, manageErr := a.auth.CheckCluster(auth.PermUsersManage, "")
	return IncidentSettingsInfo{Settings: st, Limits: incidentLimits(), CanManage: manageErr == nil}, nil
}

func incidentLimits() IncidentLimits {
	return IncidentLimits{FragmentLines: incident.MaxFragmentLines, FragmentBytes: incident.MaxFragmentBytes,
		LineBytes: incident.MaxLineBytes, FragmentsPerIncident: incident.MaxFragmentsPerIncident,
		IncidentFragmentBytes: incident.MaxIncidentFragmentBytes, MaxFragmentRetentionDays: incident.MaxFragmentRetentionDays,
		MaxIncidentRetentionDays: incident.MaxIncidentRetentionDays}
}

// IncidentSaveSettings сохраняет настройки хранения организации (приложения). Только администратор; изменение
// записывается в аудит.
func (a *App) IncidentSaveSettings(settings incident.Settings) (saved incident.Settings, err error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return incident.Settings{}, err
	}
	if a.incidents == nil {
		return incident.Settings{}, errIncidentsUnavailable
	}
	org := (opsEnv{a}).OrgID()
	if org == "" {
		return incident.Settings{}, auth.ErrClusterNotFound
	}
	ctx, cancel := a.opCtx(storeTimeout)
	defer cancel()
	saved, err = a.incidents.SetSettings(ctx, org, settings)
	if actor, aerr := a.auth.CheckCluster(auth.PermUsersManage, ""); aerr == nil && a.audit != nil {
		res := audit.ResultOK
		if err != nil {
			res = audit.ResultFailed
		}
		a.audit.Record(audit.Event{User: actor.Username, Role: string(actor.Role), Action: "IncidentSaveSettings", Result: res,
			Detail: fmt.Sprintf("log_evidence_disabled=%t excluded_namespaces=%d fragment_days=%d incident_days=%d",
				settings.DisableLogEvidence, len(settings.ExcludedNamespaces), settings.FragmentRetentionDays, settings.IncidentRetentionDays),
			Source: actor.Source})
	}
	return saved, err
}
