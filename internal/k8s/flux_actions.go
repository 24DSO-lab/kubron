// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Действия над объектами Flux: reconcile, suspend, resume (ADR-0023, раздел 6). Поведение повторяет flux CLI
// (reconcile, suspend, resume): reconcile — аннотация requestedAt (время RFC 3339 с наносекундами), которую
// контроллер замечает по смене значения; suspend и resume — spec.suspend. Правки только merge-patch с полевым
// менеджером «kubron»: чужие поля объекта не затрагиваются и конфликтов версий нет.

const (
	// fluxRequestedAtAnnotation — аннотация, по которой контроллер Flux запускает внеочередной reconcile.
	fluxRequestedAtAnnotation = "reconcile.fluxcd.io/requestedAt"
	// fluxFieldManager — имя полевого менеджера Kubron в patch.
	fluxFieldManager = "kubron"
	// fluxMaxRefPartLen — предел длины namespace и имени в ссылке от клиента (имя объекта в Kubernetes — до 253).
	fluxMaxRefPartLen = 200
)

// fluxActionVersions — источник обслуживаемых версий API; переменная нужна тестам.
var fluxActionVersions = fluxServedVersions

// ReconcileFlux запрашивает reconcile объектов (withSource — вместе с источником).
func (m *Manager) ReconcileFlux(refs []FluxRef, withSource bool) ([]FluxActionResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), fluxActionTimeout)
	defer cancel()
	return fluxReconcile(ctx, conn, refs, withSource, time.Now)
}

// SuspendFlux приостанавливает объекты (spec.suspend: true).
func (m *Manager) SuspendFlux(refs []FluxRef) ([]FluxActionResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), fluxActionTimeout)
	defer cancel()
	return fluxSuspend(ctx, conn, refs, true, time.Now)
}

// ResumeFlux возобновляет объекты (spec.suspend: false и requestedAt).
func (m *Manager) ResumeFlux(refs []FluxRef) ([]FluxActionResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), fluxActionTimeout)
	defer cancel()
	return fluxSuspend(ctx, conn, refs, false, time.Now)
}

func fluxReconcile(ctx context.Context, conn *ClusterConnection, refs []FluxRef, withSource bool, now func() time.Time) ([]FluxActionResult, error) {
	return fluxForEach(ctx, conn, refs, fluxActReconcile, func(c *fluxActionCtx, ref FluxRef, obj *unstructured.Unstructured) error {
		// Сначала источник: если он приостановлен или недоступен, сам объект не трогается (как в flux CLI).
		if withSource {
			if src, ok := fluxSourceRefOf(ref, obj); ok {
				if err := c.reconcileOne(src, now); err != nil {
					return fmt.Errorf("%s: %w", "source "+src.Kind+" "+src.Namespace+"/"+src.Name, err)
				}
			}
		}
		return c.reconcileObject(ref, obj, now)
	})
}

// fluxSuspend приостанавливает (suspend=true) или возобновляет (false) объекты.
func fluxSuspend(ctx context.Context, conn *ClusterConnection, refs []FluxRef, suspend bool, now func() time.Time) ([]FluxActionResult, error) {
	return fluxForEach(ctx, conn, refs, fluxActSuspend, func(c *fluxActionCtx, ref FluxRef, obj *unstructured.Unstructured) error {
		patch := map[string]any{"spec": map[string]any{"suspend": suspend}}
		if !suspend {
			// Возобновление сразу просит reconcile, как flux resume.
			patch["metadata"] = map[string]any{"annotations": map[string]any{fluxRequestedAtAnnotation: fluxStamp(now)}}
		}
		return c.patch(ref, patch)
	})
}

// fluxStamp — значение requestedAt: время в UTC, RFC 3339 с наносекундами.
func fluxStamp(now func() time.Time) string { return now().UTC().Format(time.RFC3339Nano) }

// fluxActionCtx — состояние одного вызова действия.
type fluxActionCtx struct {
	ctx      context.Context
	conn     *ClusterConnection
	versions map[string]string
}

// resolve возвращает вид и версию API объекта, которую обслуживает кластер.
func (c *fluxActionCtx) resolve(ref FluxRef, action fluxActions) (fluxKind, string, error) {
	kind, err := checkFluxRef(ref, action)
	if err != nil {
		return fluxKind{}, "", fmt.Errorf("%w: %s", err, ref.Kind)
	}
	version := c.versions[kind.Kind]
	if version == "" {
		return fluxKind{}, "", fmt.Errorf("%w: %s", errFluxNotInstalled, kind.Kind)
	}
	if !kind.supports(version) {
		return fluxKind{}, "", fmt.Errorf("%w: %s", errFluxVersionUnsupported, kind.Kind)
	}
	return kind, version, nil
}

func (c *fluxActionCtx) get(ref FluxRef, action fluxActions) (*unstructured.Unstructured, error) {
	kind, version, err := c.resolve(ref, action)
	if err != nil {
		return nil, err
	}
	obj, err := c.conn.DynamicClient.Resource(kind.GVR(version)).Namespace(ref.Namespace).
		Get(c.ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if obj.GetDeletionTimestamp() != nil {
		return nil, errFluxDeleting
	}
	return obj, nil
}

// patch отправляет merge-patch объекта; допустимость вида для действия уже проверена при чтении.
func (c *fluxActionCtx) patch(ref FluxRef, body map[string]any) error {
	kind, version, err := c.resolve(ref, fluxActReadOnly)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = c.conn.DynamicClient.Resource(kind.GVR(version)).Namespace(ref.Namespace).
		Patch(c.ctx, ref.Name, types.MergePatchType, raw, metav1.PatchOptions{FieldManager: fluxFieldManager})
	return err
}

// reconcileObject проверяет прочитанный объект и просит reconcile.
func (c *fluxActionCtx) reconcileObject(ref FluxRef, obj *unstructured.Unstructured, now func() time.Time) error {
	if boolAt(obj.Object, "spec", "suspend") {
		return errFluxSuspended
	}
	return c.patch(ref, map[string]any{"metadata": map[string]any{
		"annotations": map[string]any{fluxRequestedAtAnnotation: fluxStamp(now)},
	}})
}

// reconcileOne читает и пересчитывает объект целиком (используется для источника).
func (c *fluxActionCtx) reconcileOne(ref FluxRef, now func() time.Time) error {
	obj, err := c.get(ref, fluxActReconcile)
	if err != nil {
		return err
	}
	return c.reconcileObject(ref, obj, now)
}

// fluxSourceRefOf находит источник объекта: spec.sourceRef у Kustomization, spec.chart.spec.sourceRef либо
// spec.chartRef у HelmRelease. Namespace по умолчанию — namespace самого объекта. false — источника нет или его
// вид не из таблицы (например, ExternalArtifact): тогда пересчитывается только сам объект. Для остальных видов
// withSource ничего не значит.
func fluxSourceRefOf(ref FluxRef, obj *unstructured.Unstructured) (FluxRef, bool) {
	var raw map[string]any
	switch ref.Kind {
	case FluxKindKustomization:
		raw, _ = mapAt(obj.Object, "spec", "sourceRef")
	case FluxKindHelmRelease:
		raw, _ = mapAt(obj.Object, "spec", "chart", "spec", "sourceRef")
		if raw == nil {
			raw, _ = mapAt(obj.Object, "spec", "chartRef")
		}
	}
	kind, name := strAt(raw, "kind"), strAt(raw, "name")
	if kind == "" || name == "" {
		return FluxRef{}, false
	}
	if _, err := checkFluxRef(FluxRef{Kind: kind, Namespace: "x", Name: name}, fluxActReconcile); err != nil {
		return FluxRef{}, false
	}
	ns := strAt(raw, "namespace")
	if ns == "" {
		ns = obj.GetNamespace()
	}
	return FluxRef{Kind: kind, Namespace: ns, Name: name}, true
}

// normalizeFluxRefs проверяет список от клиента: не пуст, не больше fluxMaxActionObjects, поля без пробелов по
// краям и не длиннее fluxMaxRefPartLen, вид разрешён для действия; повторы убираются, порядок сохраняется.
// Вид не нормализуется (регистр важен). Ошибка любой ссылки отменяет весь вызов до обращения к кластеру.
func normalizeFluxRefs(refs []FluxRef, action fluxActions) ([]FluxRef, error) {
	if len(refs) == 0 {
		return nil, errFluxNoObjects
	}
	if len(refs) > fluxMaxActionObjects {
		return nil, errFluxTooManyObjects
	}
	seen := make(map[FluxRef]bool, len(refs))
	out := make([]FluxRef, 0, len(refs))
	for _, r := range refs {
		ref := FluxRef{Kind: r.Kind, Namespace: strings.TrimSpace(r.Namespace), Name: strings.TrimSpace(r.Name)}
		if len(ref.Namespace) > fluxMaxRefPartLen || len(ref.Name) > fluxMaxRefPartLen {
			return nil, errFluxInvalidRef
		}
		if _, err := checkFluxRef(ref, action); err != nil {
			return nil, fmt.Errorf("%w: %s", err, ref.Kind)
		}
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out, nil
}

// fluxForEach нормализует ссылки и применяет op к каждому объекту; ошибка одного не отменяет остальные.
func fluxForEach(
	ctx context.Context, conn *ClusterConnection, refs []FluxRef, action fluxActions,
	op func(c *fluxActionCtx, ref FluxRef, obj *unstructured.Unstructured) error,
) ([]FluxActionResult, error) {
	norm, err := normalizeFluxRefs(refs, action)
	if err != nil {
		return nil, err
	}
	versions, err := fluxActionVersions(conn)
	if err != nil {
		return nil, err
	}
	c := &fluxActionCtx{ctx: ctx, conn: conn, versions: versions}
	out := make([]FluxActionResult, 0, len(norm))
	for _, ref := range norm {
		res := FluxActionResult{Kind: ref.Kind, Namespace: ref.Namespace, Name: ref.Name}
		if err := ctx.Err(); err != nil {
			res.Error = err.Error()
		} else if obj, err := c.get(ref, action); err != nil {
			res.Error = err.Error()
		} else if err := op(c, ref, obj); err != nil {
			res.Error = err.Error()
		}
		out = append(out, res)
	}
	return out, nil
}
