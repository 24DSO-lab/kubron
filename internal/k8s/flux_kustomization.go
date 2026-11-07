// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GetFluxKustomizations возвращает Kustomization из набора namespace (пусто — из всех).
// Читается только через Kubernetes API (dynamic client, unstructured), список идёт через fluxList.
func (m *Manager) GetFluxKustomizations(namespaces []string) ([]FluxKustomizationInfo, error) {
	kind, _ := fluxKindByName(FluxKindKustomization)
	items, err := fluxList(m, kind, namespaces, parseFluxKustomization,
		func(i FluxKustomizationInfo) string { return i.Namespace })
	if err != nil {
		return nil, err
	}
	// Стабильный порядок для интерфейса: namespace, затем имя.
	slices.SortFunc(items, func(a, b FluxKustomizationInfo) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	if items == nil {
		items = []FluxKustomizationInfo{}
	}
	return items, nil
}

// parseFluxKustomization защитно превращает unstructured Kustomization в контракт для UI: отсутствующее или
// неожиданного типа поле даёт нулевое значение, а не панику (CRD живёт в чужих версиях Flux).
//
// Наружу НЕ отдаются (ADR-0023, раздел 4): значения spec.postBuild.substitute и содержимое substituteFrom,
// секреты spec.kubeConfig и spec.decryption (их в DTO нет совсем, даже имён), аннотация last-applied-configuration.
// Тексты сообщений уже прошли redactFluxMessage в fluxConditions.
func parseFluxKustomization(u *unstructured.Unstructured) FluxKustomizationInfo {
	obj := u.Object
	ns := u.GetNamespace()
	conds := fluxConditions(obj)
	suspended := boolAt(obj, "spec", "suspend")

	labels := maps.Clone(u.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}

	info := FluxKustomizationInfo{
		FluxObjectInfo: FluxObjectInfo{
			Kind:      FluxKindKustomization,
			Name:      u.GetName(),
			Namespace: ns,

			Interval:  strAt(obj, "spec", "interval"),
			Suspended: suspended,
			State:     FluxStateOf(conds, suspended),

			SourceRef:  kustomizationRef(obj, ns, "spec", "sourceRef"),
			DependsOn:  kustomizationDependsOn(obj, ns),
			Conditions: conds,

			Revision:              strAt(obj, "status", "lastAppliedRevision"),
			LastAttemptedRevision: strAt(obj, "status", "lastAttemptedRevision"),
			LastReconcileAt:       kustomizationReconcileAt(obj, conds),
			Message:               kustomizationMessage(conds),

			Labels:      labels,
			Annotations: fluxAnnotations(u.GetAnnotations()),
		},
		Path:            strAt(obj, "spec", "path"),
		Prune:           boolAt(obj, "spec", "prune"),
		Wait:            boolAt(obj, "spec", "wait"),
		TargetNamespace: strAt(obj, "spec", "targetNamespace"),
		ServiceAccount:  strAt(obj, "spec", "serviceAccountName"),
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		info.Age = formatAge(ts.Time)
		info.Created = ts.Format(time.RFC3339)
	}

	// Remote: ресурсы применяются в другой кластер. Содержимое kubeConfig (ссылка на секрет) не читается и не отдаётся.
	if kc, ok := mapAt(obj, "spec", "kubeConfig"); ok && len(kc) > 0 {
		info.Remote = true
	}
	if entries, ok := sliceAt(obj, "status", "inventory", "entries"); ok {
		info.InventoryCount = len(entries)
	}
	return info
}

// kustomizationRef разбирает ссылку на объект (spec.sourceRef). Namespace по умолчанию — namespace самого объекта,
// как делает контроллер. Без имени ссылки нет: nil.
func kustomizationRef(obj map[string]any, defaultNS string, path ...string) *FluxRef {
	m, ok := mapAt(obj, path...)
	if !ok {
		return nil
	}
	name := strAt(m, "name")
	if name == "" {
		return nil
	}
	return &FluxRef{
		Kind:      strAt(m, "kind"),
		Namespace: cmp.Or(strAt(m, "namespace"), defaultNS),
		Name:      name,
	}
}

// kustomizationDependsOn разбирает spec.dependsOn. Вид у зависимостей не указывается (Kind пуст), namespace по
// умолчанию — namespace объекта. Записи без имени и неожиданного типа пропускаются. Никогда не nil.
func kustomizationDependsOn(obj map[string]any, defaultNS string) []FluxRef {
	items, _ := sliceAt(obj, "spec", "dependsOn")
	out := make([]FluxRef, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		name := strAt(m, "name")
		if name == "" {
			continue
		}
		out = append(out, FluxRef{Namespace: cmp.Or(strAt(m, "namespace"), defaultNS), Name: name})
	}
	return out
}

// kustomizationReconcileAt выбирает момент «последнего reconcile».
//
// Берётся lastTransitionTime условия Ready, а не status.lastHandledReconcileAt. Причины:
//   - lastHandledReconcileAt — не время, а токен запроса: его пишет клиент в аннотацию reconcile.fluxcd.io/requestedAt
//     (flux CLI кладёт туда время, но подойдёт любая строка), и он появляется только после ручного reconcile.
//     Для объекта, который обновляется по интервалу, он пуст или давно устарел;
//   - Ready ставит сам контроллер при каждом исходе reconcile, и время в нём обновляется, когда меняется статус,
//     причина или сообщение (в сообщении — применённая ревизия). То есть это время последнего значимого результата.
//
// Если Ready-условия ещё нет, берётся lastHandledReconcileAt, но только если он разбирается как время.
func kustomizationReconcileAt(obj map[string]any, conds []ConditionInfo) string {
	for _, c := range conds {
		if c.Type == "Ready" && c.LastTransitionTime != "" {
			return c.LastTransitionTime
		}
	}
	if token := strAt(obj, "status", "lastHandledReconcileAt"); token != "" {
		if _, err := time.Parse(time.RFC3339Nano, token); err == nil {
			return token
		}
	}
	return ""
}

// kustomizationMessage — сообщение для списка: условие Ready, при его отсутствии Stalled или Reconciling.
// Тексты в conds уже прошли redactFluxMessage.
func kustomizationMessage(conds []ConditionInfo) string {
	for _, typ := range [...]string{"Ready", "Stalled", "Reconciling"} {
		for _, c := range conds {
			if c.Type == typ && c.Message != "" {
				return c.Message
			}
		}
	}
	return ""
}
