// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fluxDeleteMaxPart — предел длины namespace и имени в ссылке от клиента (как у остальных действий Flux).
const fluxDeleteMaxPart = 200

// fluxDeleteVersions — источник обслуживаемых версий API; переменная нужна тестам.
var fluxDeleteVersions = fluxServedVersions

// DeleteFlux удаляет один Kustomization или HelmRelease (ADR-0023, раздел 6). Это обычный DELETE без опций
// (propagationPolicy, preconditions): что станет с созданными объектами, решает spec.prune и finalizer контроллера,
// а не Kubron. Перед удалением объект читается: проверяется, что он не удаляется уже, и в журнал пишется строка с
// видом, namespace, именем и spec.prune — по ней видно, что удаление могло снести объекты кластера.
//
// Ошибка «объект не найден» приходит от API как есть (apierrors.IsNotFound): отдельной ошибки контракта для неё нет.
func (m *Manager) DeleteFlux(ref FluxRef) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ref = FluxRef{Kind: ref.Kind, Namespace: strings.TrimSpace(ref.Namespace), Name: strings.TrimSpace(ref.Name)}
	if len(ref.Namespace) > fluxDeleteMaxPart || len(ref.Name) > fluxDeleteMaxPart {
		return errFluxInvalidRef
	}
	kind, err := checkFluxRef(ref, fluxActDelete)
	if err != nil {
		return fmt.Errorf("%w: %s", err, ref.Kind)
	}
	versions, err := fluxDeleteVersions(conn)
	if err != nil {
		return err
	}
	version := versions[kind.Kind]
	if version == "" {
		return fmt.Errorf("%w: %s", errFluxNotInstalled, kind.Kind)
	}
	if !kind.supports(version) {
		return fmt.Errorf("%w: %s", errFluxVersionUnsupported, kind.Kind)
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), fluxActionTimeout)
	defer cancel()
	res := conn.DynamicClient.Resource(kind.GVR(version)).Namespace(ref.Namespace)
	obj, err := res.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if obj.GetDeletionTimestamp() != nil {
		return errFluxDeleting
	}
	slog.Info("deleting Flux object", "kind", ref.Kind, "namespace", ref.Namespace, "name", ref.Name,
		"prune", boolAt(obj.Object, "spec", "prune"))
	return res.Delete(ctx, ref.Name, metav1.DeleteOptions{})
}
