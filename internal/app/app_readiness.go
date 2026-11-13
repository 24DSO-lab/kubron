// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/jobs"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
	"github.com/24DSO-lab/kubron/internal/readiness/catalog"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
	"github.com/24DSO-lab/kubron/internal/store"
)

// Тонкие делегаты Upgrade Readiness (ADR-0025): право и область определяет платформа (Provider.Begin), логика — в
// internal/readiness. Все методы требуют только чтение: проверка ничего не меняет в кластере.

const readinessModule = "readiness"

// ReadinessInfo — сведения для экрана: окно целевых версий и данные, на которых строятся проверки.
type ReadinessInfo struct {
	Window    []kver.Minor   `json:"window"`
	APIMatrix apimatrix.Info `json:"apiMatrix"`
	Catalog   catalog.Info   `json:"catalog"`
}

// initReadiness создаёт сервис по хранилищу; вызывается при старте. Ошибка встроенных данных не роняет приложение:
// методы Readiness* вернут её, остальное продолжает работать.
func (a *App) initReadiness(st store.Store) {
	if st == nil {
		st = store.NewFile(k8s.GetKubronDir())
	}
	svc, err := readiness.New(readiness.Options{Backend: st.Readiness()})
	if err != nil {
		slog.Error("readiness is unavailable", "error", err)
		a.readinessErr = err
		return
	}
	a.readiness = svc
}

func (a *App) readinessService() (*readiness.Service, error) {
	if a.readiness == nil {
		if a.readinessErr != nil {
			return nil, a.readinessErr
		}
		return nil, errors.New("readiness service is not available")
	}
	return a.readiness, nil
}

// ReadinessInfo возвращает окно целевых версий и сведения о матрице и каталоге.
func (a *App) ReadinessInfo() (ReadinessInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return ReadinessInfo{}, err
	}
	svc, err := a.readinessService()
	if err != nil {
		return ReadinessInfo{}, err
	}
	return ReadinessInfo{Window: svc.Window(), APIMatrix: svc.MatrixInfo(), Catalog: svc.CatalogInfo()}, nil
}

// readinessOp начинает операцию модуля (storedOnly — без подключения к кластеру: история, экспорт) и возвращает сервис, контекст и Op; закрывать Op обязан вызывающий.
func (a *App) readinessOp(clusterID, operation string, storedOnly bool) (*readiness.Service, context.Context, *opsplatform.Op, error) {
	svc, err := a.readinessService()
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, op, err := a.beginOp(opsplatform.OpRequest{Module: readinessModule, Operation: operation, ClusterID: clusterID, Permission: auth.PermRead, StoredOnly: storedOnly})
	if err != nil {
		return nil, nil, nil, err
	}
	return svc, ctx, op, nil
}

// ReadinessRun выполняет проверку готовности кластера к обновлению и сохраняет отчёт в историю.
// target — «1.36» или пусто (цель не выбрана).
func (a *App) ReadinessRun(clusterID, target string) (rep *readiness.Report, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID, "target", target); err != nil {
		return nil, err
	}
	svc, ctx, op, err := a.readinessOp(clusterID, "run", false)
	if err != nil {
		return nil, err
	}
	defer func() {
		var partial []string
		if rep != nil {
			if live, ok := rep.Coverage.Source(readiness.SourceLiveAPI); ok && live.Partial {
				partial = append(partial, "live_api")
			}
		}
		// Аудит: кто, какой кластер и целевая версия; содержимого объектов и находок в нём нет.
		op.Audit("readiness.run", clusterID, err, slog.String("target", target))
		op.End(err, partial...)
	}()
	return svc.Run(ctx, readiness.Request{
		Scope: op.Scope, Reader: readinessReader{op.Reader},
		Initiator: op.Initiator, CorrelationID: op.CorrelationID, Target: target,
	})
}

// ReadinessHistory возвращает страницу истории проверок кластера (от новых к старым).
func (a *App) ReadinessHistory(clusterID, cursor string, limit int) (page readiness.Page, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return readiness.Page{}, err
	}
	svc, ctx, op, err := a.readinessOp(clusterID, "history", true)
	if err != nil {
		return readiness.Page{}, err
	}
	defer func() { op.End(err) }()
	return svc.History(ctx, op.Scope, readiness.HistoryQuery{Cursor: cursor, Limit: limit})
}

// ReadinessGet возвращает сохранённый отчёт.
func (a *App) ReadinessGet(clusterID, id string) (rep *readiness.Report, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return nil, err
	}
	svc, ctx, op, err := a.readinessOp(clusterID, "get", true)
	if err != nil {
		return nil, err
	}
	defer func() { op.End(err) }()
	return svc.Get(ctx, op.Scope, id)
}

// ReadinessCompare сравнивает отчёт с предыдущим (previousID пуст — с ближайшим более ранним).
func (a *App) ReadinessCompare(clusterID, id, previousID string) (d readiness.Diff, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return readiness.Diff{}, err
	}
	svc, ctx, op, err := a.readinessOp(clusterID, "compare", true)
	if err != nil {
		return readiness.Diff{}, err
	}
	defer func() { op.End(err) }()
	return svc.Compare(ctx, op.Scope, id, previousID)
}

// ReadinessExport выгружает отчёт: format — «json» или «markdown».
func (a *App) ReadinessExport(clusterID, id, format string) (out readiness.ExportedReport, err error) {
	if err := a.authzCluster(auth.PermRead, clusterID, "id", id, "format", format); err != nil {
		return readiness.ExportedReport{}, err
	}
	svc, ctx, op, err := a.readinessOp(clusterID, "export", true)
	if err != nil {
		return readiness.ExportedReport{}, err
	}
	defer func() {
		op.Audit("readiness.export", clusterID, err, slog.String("format", format))
		op.End(err)
	}()
	return svc.Export(ctx, op.Scope, id, format)
}

// readinessReader приводит opsplatform.Reader к узкому интерфейсу домена: типы и ошибки платформы переводятся в
// типы и ошибки readiness. Домен платформу не импортирует.
type readinessReader struct{ r opsplatform.Reader }

var _ readiness.Reader = readinessReader{}

func (rr readinessReader) ServerVersion(ctx context.Context) (version.Info, error) {
	v, err := rr.r.ServerVersion(ctx)
	return v, mapSourceErr(err)
}

func (rr readinessReader) ServerResources(ctx context.Context) ([]*metav1.APIResourceList, error) {
	l, err := rr.r.ServerResources(ctx)
	return l, mapSourceErr(err)
}

func (rr readinessReader) List(ctx context.Context, gvk schema.GroupVersionKind, o readiness.ListOptions) (*readiness.ListResult, error) {
	res, err := rr.r.List(ctx, gvk, opsplatform.ListOptions{Namespace: o.Namespace, Limit: o.Limit})
	if err != nil {
		return nil, mapSourceErr(err)
	}
	return &readiness.ListResult{Items: res.Items, Truncated: res.Truncated, ObservedAt: res.ObservedAt}, nil
}

// mapSourceErr переводит ошибки платформы в ошибки домена, сохраняя исходную цепочку для errors.Is.
func mapSourceErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, opsplatform.ErrPartial):
		return &readiness.PartialError{Failed: opsplatform.PartialDetails(err)}
	case errors.Is(err, opsplatform.ErrNotFound):
		return fmt.Errorf("%w: %w", readiness.ErrSourceNotFound, err)
	case errors.Is(err, opsplatform.ErrForbidden):
		return fmt.Errorf("%w: %w", readiness.ErrSourceForbidden, err)
	case errors.Is(err, opsplatform.ErrTooLarge):
		return fmt.Errorf("%w: %w", readiness.ErrSourceTooLarge, err)
	case errors.Is(err, opsplatform.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", readiness.ErrSourceUnavailable, err)
	}
	return err
}

// readinessPruneEvery — как часто задача retention просматривает историю.
const readinessPruneEvery = time.Hour

// ReadinessRetentionJob возвращает периодическую задачу retention отчётов. lock — исполнитель среди реплик
// (сервер с PostgreSQL); nil — десктоп и одна реплика.
func ReadinessRetentionJob(a *App, lock jobs.Locker) (*jobs.Job, error) {
	svc, err := a.readinessService()
	if err != nil {
		return nil, err
	}
	ret, err := readiness.RetentionFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	return &jobs.Job{
		Name: "readiness retention", Every: readinessPruneEvery, Lock: lock, Logf: log.Printf,
		Run: func(ctx context.Context) (int, error) { return svc.Prune(ctx, ret) },
	}, nil
}
