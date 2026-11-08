// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// objectMeta — общие поля панели деталей (Age, Created, Labels, Annotations, Finalizers).
// Wails не разворачивает встроенные структуры в TS-модели надёжно, поэтому поля
// продублированы в каждом *Info, а заполняются одним вызовом.
type objectMeta struct {
	Age         string
	Created     string
	Labels      map[string]string
	Annotations map[string]string
	Finalizers  []string
}

func metaOf(o metav1.ObjectMeta) objectMeta {
	return objectMeta{
		Age:         formatAge(o.CreationTimestamp.Time),
		Created:     o.CreationTimestamp.Format(time.RFC3339),
		Labels:      o.Labels,
		Annotations: o.Annotations,
		Finalizers:  o.Finalizers,
	}
}

// labelSelectorInfo — структурный selector для секции Selector в панели.
func labelSelectorInfo(sel *metav1.LabelSelector) *PVCSelectorInfo {
	if sel == nil {
		return nil
	}
	out := &PVCSelectorInfo{MatchLabels: sel.MatchLabels, MatchExpressions: []MatchExpression{}}
	for _, e := range sel.MatchExpressions {
		out.MatchExpressions = append(out.MatchExpressions, MatchExpression{
			Key:      e.Key,
			Operator: string(e.Operator),
			Values:   e.Values,
		})
	}
	return out
}

func formatTime(t metav1.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func formatMicroTime(t *metav1.MicroTime) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func sortedKeys[K ~string, V any](m map[K]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	return keys
}

// controlledByOf — "Kind/name" контроллера объекта. Если controller=true нет, берётся первый владелец.
func controlledByOf(refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return ""
	}
	ref := refs[0]
	for _, r := range refs {
		if r.Controller != nil && *r.Controller {
			ref = r
			break
		}
	}
	return ref.Kind + "/" + ref.Name
}

// managedByOf — кто управляет объектом через GitOps/Helm: "Application/<app>" для Argo CD
// (tracking-id вида "app:group/Kind:ns/name" или label argocd.argoproj.io/instance),
// "Kustomization/<name>" и "HelmRelease/<name>" для Flux CD (labels kustomize.toolkit.fluxcd.io/name и
// helm.toolkit.fluxcd.io/name), "Helm/<release>" для Helm. Приоритет: Argo, затем Flux, затем Helm: релиз, который
// ставит helm-controller, несёт и метку managed-by=Helm, но управляет им HelmRelease. Пустая строка, если определить
// нельзя. Те же правила и та же таблица тестов — в frontend/src/resources/definitions/common.ts.
func managedByOf(o metav1.ObjectMeta) string {
	if id := o.Annotations["argocd.argoproj.io/tracking-id"]; id != "" {
		if app, _, ok := strings.Cut(id, ":"); ok && app != "" {
			return "Application/" + app
		}
	}
	if app := o.Labels["argocd.argoproj.io/instance"]; app != "" {
		return "Application/" + app
	}
	if name := o.Labels["kustomize.toolkit.fluxcd.io/name"]; name != "" {
		return "Kustomization/" + name
	}
	if name := o.Labels["helm.toolkit.fluxcd.io/name"]; name != "" {
		return "HelmRelease/" + name
	}
	if o.Labels["app.kubernetes.io/managed-by"] == "Helm" {
		if rel := o.Annotations["meta.helm.sh/release-name"]; rel != "" {
			return "Helm/" + rel
		}
	}
	return ""
}
