// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/drift"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
)

// Делегаты baseline конфигурации (ADR-0027). Доменная логика лежит в internal/drift и internal/baseline и платформы не
// знает; здесь — только связка: право и кластер проверяет Provider.Begin (и повторно Op.Require), чтение кластера идёт
// через Reader платформы (read-only, Secret исключён), аудит — через Op.Audit без содержимого ресурсов.
//
// Права: просмотр, история и запуск проверки — read; предпросмотр, создание версии, смена активной версии, исключения и
// удаление — baseline.manage.

const baselineModule = "baseline"

// Имена действий аудита. Значения ресурсов и конфигурации в запись не попадают: только идентификаторы и счётчики.
const (
	auditBaselineCreate          = "BaselineCreate"
	auditBaselineVersionAdd      = "BaselineVersionAdd"
	auditBaselineActivate        = "BaselineActivate"
	auditBaselineExceptionAdd    = "BaselineExceptionAdd"
	auditBaselineExceptionRemove = "BaselineExceptionRemove"
	auditBaselineDelete          = "BaselineDelete"
	auditDriftCheck              = "DriftCheck"
)

// Пределы входных данных: клиент не может заставить сервер читать или хранить без меры.
const (
	maxSpecNamespaces = 100
	maxSpecKinds      = 20
	maxSpecRefs       = 500
	maxSelectorLen    = 1000
)

// baselinePreviewKey — ключ токенов предпросмотра, общий для процесса: предпросмотр и подтверждение могут прийти
// в разные виды одной сессии.
var baselinePreviewKey = sync.OnceValue(drift.NewPreviewKey)

// BaselineSpecInput — что снимать: пространства имён, селектор, типы либо явный список ресурсов.
type BaselineSpecInput struct {
	Namespaces []string       `json:"namespaces"`
	Selector   string         `json:"selector"`
	Kinds      []string       `json:"kinds"`
	Refs       []baseline.Ref `json:"refs"`
}

// BaselineCreateInput — создание baseline с первой версией.
type BaselineCreateInput struct {
	Name      string            `json:"name"`
	Spec      BaselineSpecInput `json:"spec"`
	Comment   string            `json:"comment"`
	Token     string            `json:"token"`
	Confirmed bool              `json:"confirmed"`
}

// BaselineVersionInput — новая версия существующего baseline.
type BaselineVersionInput struct {
	ID        string `json:"id"`
	Comment   string `json:"comment"`
	Token     string `json:"token"`
	Confirmed bool   `json:"confirmed"`
}

// BaselineExceptionInput — новое исключение.
type BaselineExceptionInput struct {
	Scope     string       `json:"scope"`
	Ref       baseline.Ref `json:"ref"`
	Path      string       `json:"path"`
	Comment   string       `json:"comment"`
	ExpiresAt *time.Time   `json:"expiresAt"`
}

func (s BaselineSpecInput) spec() (drift.Spec, error) {
	if len(s.Namespaces) > maxSpecNamespaces || len(s.Kinds) > maxSpecKinds || len(s.Refs) > maxSpecRefs || len(s.Selector) > maxSelectorLen {
		return drift.Spec{}, fmt.Errorf("%w: the selection is too large", baseline.ErrInvalid)
	}
	for _, r := range s.Refs {
		if err := r.Validate(); err != nil {
			return drift.Spec{}, err
		}
	}
	return drift.Spec{Namespaces: s.Namespaces, Selector: s.Selector, Kinds: s.Kinds, Refs: s.Refs}, nil
}

// clusterReader приводит Reader платформы к узкому интерфейсу домена и переводит ошибки платформы в ошибки домена.
type clusterReader struct{ r opsplatform.Reader }

func domainErr(err error, notFound error) error {
	switch {
	case errors.Is(err, opsplatform.ErrNotFound):
		return fmt.Errorf("%w: %w", notFound, err)
	case errors.Is(err, opsplatform.ErrForbidden):
		return fmt.Errorf("%w: %w", drift.ErrForbidden, err)
	case errors.Is(err, opsplatform.ErrTooLarge):
		return fmt.Errorf("%w: %w", drift.ErrTooLarge, err)
	default:
		return fmt.Errorf("%w: %w", drift.ErrUnavailable, err)
	}
}

func (c clusterReader) Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	u, err := c.r.Get(ctx, gvk, namespace, name)
	if err != nil {
		return nil, domainErr(err, drift.ErrNotFound)
	}
	return u, nil
}

func (c clusterReader) List(ctx context.Context, gvk schema.GroupVersionKind, namespace, selector string, limit int64) (drift.ListResult, error) {
	res, err := c.r.List(ctx, gvk, opsplatform.ListOptions{Namespace: namespace, LabelSelector: selector, Limit: limit})
	if err != nil {
		// У List «не найдено» означает, что вида ресурса в кластере нет.
		return drift.ListResult{}, domainErr(err, drift.ErrKindUnavailable)
	}
	return drift.ListResult{Items: res.Items, Truncated: res.Truncated}, nil
}

// auditApplyLookup находит запись аудита Kubron о применении YAML к ресурсу. Возвращается только время: имя автора
// показывать нельзя, это право аудита.
type auditApplyLookup struct {
	log       *audit.Logger
	clusterID string
}

const auditApplyAction = "ApplyYAML"

func (l auditApplyLookup) LastApply(ctx context.Context, ref baseline.Ref, since time.Time) (time.Time, bool, error) {
	if l.log == nil {
		return time.Time{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, false, err
	}
	page, err := l.log.Query(audit.Filter{
		Action: auditApplyAction, Result: audit.ResultOK, Since: since.UTC().Format(time.RFC3339),
		Text: "name=" + ref.Name, Limit: 200,
	})
	if err != nil {
		return time.Time{}, false, err
	}
	kind := strings.ToLower(ref.Kind)
	for _, e := range page.Events { // от новых к старым
		if e.ClusterID != "" && e.ClusterID != l.clusterID {
			continue // запись другого кластера
		}
		fields := detailFields(e.Detail)
		if fields["name"] != ref.Name || fields["namespace"] != ref.Namespace || strings.ToLower(fields["kind"]) != kind {
			continue
		}
		return e.Time, true, nil
	}
	return time.Time{}, false, nil
}

// detailFields разбирает «ключ=значение ключ=значение» из поля detail записи аудита.
func detailFields(detail string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Fields(detail) {
		if k, v, ok := strings.Cut(part, "="); ok {
			out[k] = v
		}
	}
	return out
}

// runBaseline выполняет операцию модуля: Begin проверяет право и кластер до любого чтения, Op.Require повторяет проверку
// на стороне сервера, сервис собирается над хранилищем и Reader этой операции.
func runBaseline[T any](a *App, operation string, perm auth.Permission,
	fn func(ctx context.Context, svc *drift.Service, sess drift.Session, op *opsplatform.Op) (T, error)) (out T, err error) {
	if a.store == nil {
		return out, errors.New("storage is not available")
	}
	clusterID := a.activeClusterID()
	ctx, op, err := a.beginOp(opsplatform.OpRequest{Module: baselineModule, Operation: operation, ClusterID: clusterID, Permission: perm})
	if err != nil {
		return out, err
	}
	defer func() { op.End(err) }()
	if err = op.Require(perm); err != nil {
		return out, err
	}
	svc := drift.NewService(a.store.Baselines(), drift.ServiceOptions{
		Audit:      auditApplyLookup{log: a.audit, clusterID: clusterID},
		Log:        slog.Default(),
		PreviewKey: baselinePreviewKey(),
	})
	sess := drift.Session{Scope: op.Scope, Actor: op.Initiator, Reader: clusterReader{op.Reader}, CorrelationID: op.CorrelationID}
	return fn(ctx, svc, sess, op)
}

// BaselineKinds возвращает типы ресурсов, которые поддерживает baseline (Secret среди них нет).
func (a *App) BaselineKinds() ([]string, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return drift.SupportedKinds(), nil
}

// BaselineList возвращает baseline активного кластера.
func (a *App) BaselineList() ([]baseline.Baseline, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return runBaseline(a, "list", auth.PermRead, func(ctx context.Context, svc *drift.Service, sess drift.Session, _ *opsplatform.Op) ([]baseline.Baseline, error) {
		return svc.Baselines(ctx, sess)
	})
}

// BaselinePreviewNew показывает, что будет зафиксировано первой версией нового baseline. Ничего не сохраняет.
func (a *App) BaselinePreviewNew(spec BaselineSpecInput) (drift.Preview, error) {
	if err := a.authz(auth.PermBaselineManage); err != nil {
		return drift.Preview{}, err
	}
	ds, err := spec.spec()
	if err != nil {
		return drift.Preview{}, err
	}
	return runBaseline(a, "preview", auth.PermBaselineManage, func(ctx context.Context, svc *drift.Service, sess drift.Session, _ *opsplatform.Op) (drift.Preview, error) {
		return svc.PreviewNew(ctx, sess, ds)
	})
}

// BaselineCreate создаёт baseline с первой версией. Без комментария, подтверждения и токена предпросмотра не создаёт ничего.
func (a *App) BaselineCreate(in BaselineCreateInput) (baseline.Baseline, error) {
	if err := a.authz(auth.PermBaselineManage); err != nil {
		return baseline.Baseline{}, err
	}
	ds, err := in.Spec.spec()
	if err != nil {
		return baseline.Baseline{}, err
	}
	return runBaseline(a, "create", auth.PermBaselineManage, func(ctx context.Context, svc *drift.Service, sess drift.Session, op *opsplatform.Op) (b baseline.Baseline, err error) {
		var v baseline.Version
		defer func() {
			op.Audit(auditBaselineCreate, b.ID, err, slog.Int("objects", len(v.Objects)), slog.Int("version", v.Seq))
		}()
		b, v, err = svc.Create(ctx, sess, drift.CreateInput{Name: in.Name, Spec: ds, Comment: in.Comment, Token: in.Token, Confirmed: in.Confirmed})
		return b, err
	})
}

// BaselineVersions возвращает страницу истории версий (без содержимого).
func (a *App) BaselineVersions(id, cursor string, limit int) (baseline.VersionPage, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return baseline.VersionPage{}, err
	}
	return runBaseline(a, "versions", auth.PermRead, func(ctx context.Context, svc *drift.Service, sess drift.Session, _ *opsplatform.Op) (baseline.VersionPage, error) {
		return svc.Versions(ctx, sess, id, baseline.Page{Cursor: cursor, Limit: limit})
	})
}

// BaselinePreviewVersion показывает, что изменится относительно активной версии, если снять новую.
func (a *App) BaselinePreviewVersion(id string) (drift.Preview, error) {
	if err := a.authz(auth.PermBaselineManage); err != nil {
		return drift.Preview{}, err
	}
	return runBaseline(a, "preview", auth.PermBaselineManage, func(ctx context.Context, svc *drift.Service, sess drift.Session, _ *opsplatform.Op) (drift.Preview, error) {
		return svc.PreviewVersion(ctx, sess, id)
	})
}

// BaselineAddVersion снимает текущее состояние в новую активную версию.
func (a *App) BaselineAddVersion(in BaselineVersionInput) (baseline.VersionSummary, error) {
	if err := a.authz(auth.PermBaselineManage); err != nil {
		return baseline.VersionSummary{}, err
	}
	return runBaseline(a, "version", auth.PermBaselineManage, func(ctx context.Context, svc *drift.Service, sess drift.Session, op *opsplatform.Op) (s baseline.VersionSummary, err error) {
		defer func() {
			op.Audit(auditBaselineVersionAdd, in.ID, err, slog.Int("version", s.Seq), slog.Int("objects", s.ObjectCount))
		}()
		v, err := svc.AddVersion(ctx, sess, drift.VersionInput{BaselineID: in.ID, Comment: in.Comment, Token: in.Token, Confirmed: in.Confirmed})
		if err != nil {
			return baseline.VersionSummary{}, err
		}
		return v.Summary(), nil
	})
}

// BaselineSetActive делает активной одну из версий baseline.
func (a *App) BaselineSetActive(id, versionID string) (baseline.Baseline, error) {
	if err := a.authz(auth.PermBaselineManage); err != nil {
		return baseline.Baseline{}, err
	}
	return runBaseline(a, "activate", auth.PermBaselineManage, func(ctx context.Context, svc *drift.Service, sess drift.Session, op *opsplatform.Op) (b baseline.Baseline, err error) {
		defer func() { op.Audit(auditBaselineActivate, id, err, slog.String("version_id", versionID)) }()
		return svc.SetActive(ctx, sess, id, versionID)
	})
}

// BaselineExceptions возвращает исключения активной версии.
func (a *App) BaselineExceptions(id string) ([]baseline.Exception, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return runBaseline(a, "exceptions", auth.PermRead, func(ctx context.Context, svc *drift.Service, sess drift.Session, _ *opsplatform.Op) ([]baseline.Exception, error) {
		list, err := svc.Exceptions(ctx, sess, id)
		if list == nil {
			list = []baseline.Exception{}
		}
		return list, err
	})
}

// BaselineAddException добавляет исключение к активной версии.
func (a *App) BaselineAddException(id string, in BaselineExceptionInput) (baseline.Exception, error) {
	if err := a.authz(auth.PermBaselineManage); err != nil {
		return baseline.Exception{}, err
	}
	return runBaseline(a, "exception", auth.PermBaselineManage, func(ctx context.Context, svc *drift.Service, sess drift.Session, op *opsplatform.Op) (e baseline.Exception, err error) {
		defer func() {
			op.Audit(auditBaselineExceptionAdd, id, err, slog.String("exception_id", e.ID), slog.String("scope", in.Scope))
		}()
		return svc.AddException(ctx, sess, id, drift.ExceptionInput{
			Scope: baseline.ExceptionScope(in.Scope), Ref: in.Ref, Path: in.Path, Comment: in.Comment, ExpiresAt: in.ExpiresAt,
		})
	})
}

// BaselineRemoveException удаляет исключение активной версии.
func (a *App) BaselineRemoveException(id, exceptionID string) error {
	if err := a.authz(auth.PermBaselineManage); err != nil {
		return err
	}
	_, err := runBaseline(a, "exception", auth.PermBaselineManage, func(ctx context.Context, svc *drift.Service, sess drift.Session, op *opsplatform.Op) (struct{}, error) {
		err := svc.RemoveException(ctx, sess, id, exceptionID)
		op.Audit(auditBaselineExceptionRemove, id, err, slog.String("exception_id", exceptionID))
		return struct{}{}, err
	})
	return err
}

// BaselineDelete удаляет baseline со всеми версиями, исключениями и историей проверок.
func (a *App) BaselineDelete(id string) error {
	if err := a.authz(auth.PermBaselineManage); err != nil {
		return err
	}
	_, err := runBaseline(a, "delete", auth.PermBaselineManage, func(ctx context.Context, svc *drift.Service, sess drift.Session, op *opsplatform.Op) (struct{}, error) {
		err := svc.Delete(ctx, sess, id)
		op.Audit(auditBaselineDelete, id, err)
		return struct{}{}, err
	})
	return err
}

// DriftCheck сравнивает активную версию baseline с кластером и записывает результат в историю. Кластер не меняется.
func (a *App) DriftCheck(id string) (baseline.CheckResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return baseline.CheckResult{}, err
	}
	return runBaseline(a, "check", auth.PermRead, func(ctx context.Context, svc *drift.Service, sess drift.Session, op *opsplatform.Op) (res baseline.CheckResult, err error) {
		defer func() {
			op.Audit(auditDriftCheck, id, err, slog.Int("changed", res.Summary.Changed), slog.Int("missing", res.Summary.Missing), slog.Int("unchecked", res.Summary.Unchecked))
		}()
		return svc.Check(ctx, sess, id)
	})
}

// DriftResults возвращает страницу истории проверок.
func (a *App) DriftResults(id, cursor string, limit int) (baseline.ResultPage, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return baseline.ResultPage{}, err
	}
	return runBaseline(a, "results", auth.PermRead, func(ctx context.Context, svc *drift.Service, sess drift.Session, _ *opsplatform.Op) (baseline.ResultPage, error) {
		return svc.Results(ctx, sess, id, baseline.Page{Cursor: cursor, Limit: limit})
	})
}

// DriftResult возвращает полный результат одной проверки.
func (a *App) DriftResult(id, resultID string) (baseline.CheckResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return baseline.CheckResult{}, err
	}
	return runBaseline(a, "result", auth.PermRead, func(ctx context.Context, svc *drift.Service, sess drift.Session, _ *opsplatform.Op) (baseline.CheckResult, error) {
		return svc.Result(ctx, sess, id, resultID)
	})
}
