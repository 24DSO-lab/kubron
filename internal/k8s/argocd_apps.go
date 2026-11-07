// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

const argoUnknown = "Unknown"

// GetArgoApplications возвращает Application из набора namespace (пусто — из всех namespace).
// Argo CD допускает Application в любом namespace (apps-in-any-namespace), поэтому «все»
// — осмысленное значение по умолчанию. Читается только через Kubernetes API (CRD, dynamic client).
func (m *Manager) GetArgoApplications(namespaces []string) ([]ArgoApplicationInfo, error) {
	scope, err := parseScope(namespaces)
	if err != nil {
		return nil, err
	}
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	// После синхронизации informer список строится из памяти без LIST к API-серверу.
	if ac := conn.argoCache(func() { m.emitCluster(EventArgoChanged, conn) }); ac != nil {
		if apps, ok := ac.list(scope); ok {
			return apps, nil
		}
	}
	return scopedListTimeout(m, 20*time.Second, "GetArgoApplications", "argoproj.io", "applications", namespaces,
		func(ctx context.Context, conn *ClusterConnection, ns string) ([]ArgoApplicationInfo, error) {
			return listArgoApplications(ctx, conn.DynamicClient, ns)
		},
		func(it ArgoApplicationInfo) string { return it.Namespace })
}

func listArgoApplications(ctx context.Context, dyn dynamic.Interface, namespace string) ([]ArgoApplicationInfo, error) {
	list, err := dyn.Resource(kindTable["application"].gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	apps := make([]ArgoApplicationInfo, 0, len(list.Items))
	for i := range list.Items {
		apps = append(apps, parseArgoApplication(&list.Items[i]))
	}
	sortArgoApplications(apps)
	return apps, nil
}

// parseArgoApplication защитно превращает unstructured Application в контракт для UI.
// Любое отсутствующее/неожиданного типа поле даёт нулевое значение, а не панику или ошибку:
// CRD живёт в чужих версиях Argo CD, и схема может отличаться.
func parseArgoApplication(u *unstructured.Unstructured) ArgoApplicationInfo {
	obj := u.Object
	info := ArgoApplicationInfo{
		Name:        u.GetName(),
		Namespace:   u.GetNamespace(),
		Project:     strAt(obj, "spec", "project"),
		Labels:      u.GetLabels(),
		Annotations: u.GetAnnotations(),
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		info.Age = formatAge(ts.Time)
		info.Created = ts.Format(time.RFC3339)
	}
	if info.Project == "" {
		info.Project = "default"
	}

	// Статусы
	info.SyncStatus = orDefault(strAt(obj, "status", "sync", "status"), argoUnknown)
	info.HealthStatus = orDefault(strAt(obj, "status", "health", "status"), argoUnknown)
	info.Revision = strAt(obj, "status", "sync", "revision")
	if info.Revision == "" {
		if revs, ok := sliceAt(obj, "status", "sync", "revisions"); ok && len(revs) > 0 {
			info.Revision, _ = revs[0].(string)
		}
	}

	// Источники: spec.sources (multi-source) имеет приоритет над spec.source.
	info.Sources = argoSources(obj, "spec")
	if len(info.Sources) > 0 {
		first := info.Sources[0]
		info.RepoURL, info.Path, info.Chart, info.TargetRevision = first.RepoURL, first.Path, first.Chart, first.TargetRevision
	}

	// Назначение
	info.DestServer = strAt(obj, "spec", "destination", "server")
	info.DestName = strAt(obj, "spec", "destination", "name")
	info.DestNamespace = strAt(obj, "spec", "destination", "namespace")

	// Политика синхронизации: наличие automated (даже пустого {}) = автосинк.
	if v, found, _ := unstructured.NestedFieldNoCopy(obj, "spec", "syncPolicy", "automated"); found && v != nil {
		info.AutoSync = true
		info.SelfHeal = boolAt(obj, "spec", "syncPolicy", "automated", "selfHeal")
		info.Prune = boolAt(obj, "spec", "syncPolicy", "automated", "prune")
	}

	// Операция: поле operation (инициирована) или фаза Running.
	phase := strAt(obj, "status", "operationState", "phase")
	if op, found, _ := unstructured.NestedFieldNoCopy(obj, "operation"); (found && op != nil) || phase == "Running" {
		info.OperationOn = true
	}
	info.LastSyncPhase = phase
	info.LastSyncAt = strAt(obj, "status", "operationState", "finishedAt")

	info.Message = strAt(obj, "status", "health", "message")
	if info.Message == "" {
		info.Message = strAt(obj, "status", "operationState", "message")
	}

	info.Conditions = argoConditions(obj)
	info.Resources = argoResources(obj)
	info.History = argoHistory(obj)
	return info
}

// argoSources читает <root>.sources или <root>.source (root: "spec" либо "status.history[i]").
func argoSources(m map[string]interface{}, path ...string) []ArgoSourceInfo {
	var out []ArgoSourceInfo
	if items, ok := sliceAt(m, append(append([]string{}, path...), "sources")...); ok {
		for _, it := range items {
			if sm, ok := it.(map[string]interface{}); ok {
				out = append(out, argoSource(sm))
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if sm, ok := mapAt(m, append(append([]string{}, path...), "source")...); ok {
		out = append(out, argoSource(sm))
	}
	return out
}

func argoSource(sm map[string]interface{}) ArgoSourceInfo {
	return ArgoSourceInfo{
		RepoURL:        stripURLUserinfo(strAt(sm, "repoURL")),
		Path:           strAt(sm, "path"),
		Chart:          strAt(sm, "chart"),
		TargetRevision: strAt(sm, "targetRevision"),
		Ref:            strAt(sm, "ref"),
	}
}

// stripURLUserinfo убирает user:password@ из URL репозитория, чтобы токен, вписанный в repoURL, не попал в UI.
func stripURLUserinfo(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func argoConditions(obj map[string]interface{}) []ConditionInfo {
	items, _ := sliceAt(obj, "status", "conditions")
	out := make([]ConditionInfo, 0, len(items))
	for _, it := range items {
		cm, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		// У Application condition нет поля status: присутствие в списке означает «активно».
		out = append(out, ConditionInfo{
			Type:               strAt(cm, "type"),
			Status:             "True",
			LastTransitionTime: strAt(cm, "lastTransitionTime"),
			Message:            strAt(cm, "message"),
		})
	}
	return out
}

func argoResources(obj map[string]interface{}) []ArgoResourceRef {
	items, _ := sliceAt(obj, "status", "resources")
	out := make([]ArgoResourceRef, 0, len(items))
	for _, it := range items {
		rm, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, ArgoResourceRef{
			Group:           strAt(rm, "group"),
			Version:         strAt(rm, "version"),
			Kind:            strAt(rm, "kind"),
			Namespace:       strAt(rm, "namespace"),
			Name:            strAt(rm, "name"),
			Status:          strAt(rm, "status"),
			Health:          strAt(rm, "health", "status"),
			Message:         strAt(rm, "health", "message"),
			Hook:            boolAt(rm, "hook"),
			RequiresPruning: boolAt(rm, "requiresPruning"),
		})
	}
	return out
}

func argoHistory(obj map[string]interface{}) []ArgoHistoryEntry {
	items, _ := sliceAt(obj, "status", "history")
	out := make([]ArgoHistoryEntry, 0, len(items))
	for _, it := range items {
		hm, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		e := ArgoHistoryEntry{
			ID:         int64At(hm, "id"),
			Revision:   strAt(hm, "revision"),
			DeployedAt: strAt(hm, "deployedAt"),
		}
		if e.Revision == "" {
			if revs, ok := sliceAt(hm, "revisions"); ok && len(revs) > 0 {
				e.Revision, _ = revs[0].(string)
			}
		}
		e.Source = formatArgoSource(argoSources(hm))
		out = append(out, e)
	}
	// Новые записи первыми.
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// formatArgoSource — "repoURL@targetRevision (path)" для первого источника, "+N" для остальных.
func formatArgoSource(srcs []ArgoSourceInfo) string {
	if len(srcs) == 0 {
		return ""
	}
	s := srcs[0]
	out := s.RepoURL
	if s.TargetRevision != "" {
		out += "@" + s.TargetRevision
	}
	switch {
	case s.Path != "":
		out += " (" + s.Path + ")"
	case s.Chart != "":
		out += " (chart " + s.Chart + ")"
	}
	if len(srcs) > 1 {
		out += fmt.Sprintf(" +%d", len(srcs)-1)
	}
	return out
}

// --- Безопасные аксессоры к unstructured (не паникуют на неожиданных типах) ---

func strAt(m map[string]interface{}, path ...string) string {
	v, found, err := unstructured.NestedFieldNoCopy(m, path...)
	if err != nil || !found {
		return ""
	}
	s, _ := v.(string)
	return s
}

func boolAt(m map[string]interface{}, path ...string) bool {
	v, found, err := unstructured.NestedFieldNoCopy(m, path...)
	if err != nil || !found {
		return false
	}
	b, _ := v.(bool)
	return b
}

func int64At(m map[string]interface{}, path ...string) int64 {
	v, found, err := unstructured.NestedFieldNoCopy(m, path...)
	if err != nil || !found {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func sliceAt(m map[string]interface{}, path ...string) ([]interface{}, bool) {
	v, found, err := unstructured.NestedFieldNoCopy(m, path...)
	if err != nil || !found {
		return nil, false
	}
	s, ok := v.([]interface{})
	return s, ok
}

func mapAt(m map[string]interface{}, path ...string) (map[string]interface{}, bool) {
	v, found, err := unstructured.NestedFieldNoCopy(m, path...)
	if err != nil || !found {
		return nil, false
	}
	mm, ok := v.(map[string]interface{})
	return mm, ok
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
