// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/url"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// helmReleaseNameMaxLen — предел длины имени релиза Helm; более длинные имена Flux сокращает хэшем.
	helmReleaseNameMaxLen = 53
	// helmReleaseNamePrefixLen — сколько символов имени остаётся при сокращении.
	helmReleaseNamePrefixLen = 40
	// helmReleaseNameHashLen — сколько hex-символов SHA-256 дописывается после дефиса.
	helmReleaseNameHashLen = 12
)

// GetFluxHelmReleases возвращает HelmRelease из набора namespace (пусто — из всех).
// Читается только через Kubernetes API (dynamic client, unstructured), список идёт через fluxList.
// Секреты и ConfigMap из valuesFrom не читаются.
func (m *Manager) GetFluxHelmReleases(namespaces []string) ([]FluxHelmReleaseInfo, error) {
	kind, _ := fluxKindByName(FluxKindHelmRelease)
	items, err := fluxList(m, kind, namespaces, parseFluxHelmRelease,
		func(i FluxHelmReleaseInfo) string { return i.Namespace })
	if err != nil {
		return nil, err
	}
	// Стабильный порядок для интерфейса: namespace, затем имя.
	slices.SortFunc(items, func(a, b FluxHelmReleaseInfo) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	if items == nil {
		items = []FluxHelmReleaseInfo{}
	}
	return items, nil
}

// helmHistoryRow — запись status.history вместе с именем релиза (имя в DTO не отдаётся отдельно, но по нему
// определяется реальное имя релиза).
type helmHistoryRow struct {
	FluxHelmHistoryEntry
	releaseName string
}

// parseFluxHelmRelease защитно превращает unstructured HelmRelease в контракт для UI: отсутствующее или
// неожиданного типа поле даёт нулевое значение, а не панику (CRD живёт в чужих версиях Flux).
//
// Наружу НЕ отдаются (ADR-0023, раздел 4): spec.values, содержимое spec.valuesFrom (в DTO нет и имён ссылок),
// секреты spec.kubeConfig, аннотация last-applied-configuration, userinfo в URL чарта. Тексты сообщений уже прошли
// redactFluxMessage в fluxConditions. Digest-поля истории не заполняются: интерфейсу они не нужны.
func parseFluxHelmRelease(u *unstructured.Unstructured) FluxHelmReleaseInfo {
	obj := u.Object
	ns := u.GetNamespace()
	conds := fluxConditions(obj)
	suspended := boolAt(obj, "spec", "suspend")

	labels := maps.Clone(u.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}

	history := helmReleaseHistory(obj)
	targetNS := cmp.Or(strAt(obj, "spec", "targetNamespace"), ns)

	info := FluxHelmReleaseInfo{
		FluxObjectInfo: FluxObjectInfo{
			Kind:      FluxKindHelmRelease,
			Name:      u.GetName(),
			Namespace: ns,

			Interval:  strAt(obj, "spec", "interval"),
			Suspended: suspended,
			State:     FluxStateOf(conds, suspended),

			DependsOn:  helmReleaseDependsOn(obj, ns),
			Conditions: conds,

			Revision:              strAt(obj, "status", "lastAppliedRevision"),
			LastAttemptedRevision: strAt(obj, "status", "lastAttemptedRevision"),
			LastReconcileAt:       helmReleaseReconcileAt(obj, conds),
			Message:               helmReleaseMessage(conds),

			Labels:      labels,
			Annotations: fluxAnnotations(u.GetAnnotations()),
		},
		// TargetNamespace и StorageNamespace отдаются с учётом значения по умолчанию (namespace самого HelmRelease):
		// по ним и по имени релиза HelmRelease сопоставляется с релизом Helm.
		TargetNamespace:  targetNS,
		StorageNamespace: cmp.Or(strAt(obj, "spec", "storageNamespace"), ns),
		History:          make([]FluxHelmHistoryEntry, 0, len(history)),
	}
	for _, h := range history {
		info.History = append(info.History, h.FluxHelmHistoryEntry)
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		info.Age = formatAge(ts.Time)
		info.Created = ts.Format(time.RFC3339)
	}

	// Чарт: spec.chartRef (OCIRepository, HelmChart) или spec.chart.spec; задан должен быть ровно один.
	if ref := helmReleaseRef(obj, ns, "spec", "chartRef"); ref != nil {
		info.ChartRef = ref
		src := *ref
		info.SourceRef = &src // источник тот же объект, чтобы цепочка в UI не зависела от способа задания чарта
		// Название и версия чарта у chartRef известны только из статуса.
		info.Chart = ref.Name
		info.ChartVersion = info.Revision
		if len(history) > 0 {
			info.Chart = cmp.Or(history[0].ChartName, info.Chart)
			info.ChartVersion = cmp.Or(history[0].ChartVersion, info.ChartVersion)
		}
	} else if spec, ok := mapAt(obj, "spec", "chart", "spec"); ok {
		info.Chart = cmp.Or(strAt(spec, "chart"), helmChartURL(strAt(spec, "url")))
		info.ChartVersion = strAt(spec, "version")
		info.SourceRef = helmReleaseRef(spec, ns, "sourceRef")
	}

	// Имя релиза: у установленного релиза это имя из истории (реальное имя в Helm), иначе — по правилу из документации.
	if len(history) > 0 && history[0].releaseName != "" {
		info.ReleaseName = history[0].releaseName
	} else {
		info.ReleaseName = helmReleaseName(info.Name, strAt(obj, "spec", "releaseName"), strAt(obj, "spec", "targetNamespace"))
	}

	// Remote: релиз ставится в другой кластер. Содержимое kubeConfig (ссылка на секрет) не читается и не отдаётся.
	if kc, ok := mapAt(obj, "spec", "kubeConfig"); ok && len(kc) > 0 {
		info.Remote = true
	}

	// status.inventory есть с Flux 2.8; без него вкладка ресурсов недоступна. Отдаются только флаг и число записей.
	if inv, ok := mapAt(obj, "status", "inventory"); ok {
		info.HasInventory = true
		if entries, ok := sliceAt(inv, "entries"); ok {
			info.InventoryCount = len(entries)
		}
	}
	return info
}

// helmReleaseName вычисляет имя релиза Helm по правилу из документации Flux (spec.releaseName):
// явный spec.releaseName, иначе «[<targetNamespace>-]<name>»; имя длиннее 53 символов сокращается —
// первые 40 символов, дефис и первые 12 hex-символов SHA-256 полного имени.
// Для уже установленного релиза имя надёжнее брать из status.history (см. parseFluxHelmRelease).
func helmReleaseName(name, specReleaseName, targetNamespace string) string {
	switch {
	case specReleaseName != "":
		name = specReleaseName
	case targetNamespace != "":
		name = targetNamespace + "-" + name
	}
	if len(name) <= helmReleaseNameMaxLen {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return name[:helmReleaseNamePrefixLen] + "-" + hex.EncodeToString(sum[:])[:helmReleaseNameHashLen]
}

// helmReleaseHistory разбирает status.history: новые записи первыми (по номеру версии релиза). Записи неожиданного
// типа и полностью пустые пропускаются. Никогда не nil.
func helmReleaseHistory(obj map[string]any) []helmHistoryRow {
	items, _ := sliceAt(obj, "status", "history")
	rows := make([]helmHistoryRow, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		row := helmHistoryRow{
			FluxHelmHistoryEntry: FluxHelmHistoryEntry{
				Version:       int64At(m, "version"),
				Status:        strAt(m, "status"),
				ChartName:     strAt(m, "chartName"),
				ChartVersion:  strAt(m, "chartVersion"),
				AppVersion:    strAt(m, "appVersion"),
				FirstDeployed: strAt(m, "firstDeployed"),
				LastDeployed:  strAt(m, "lastDeployed"),
			},
			releaseName: strAt(m, "name"),
		}
		if row.FluxHelmHistoryEntry == (FluxHelmHistoryEntry{}) && row.releaseName == "" {
			continue
		}
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(a, b helmHistoryRow) int { return cmp.Compare(b.Version, a.Version) })
	return rows
}

// helmReleaseRef разбирает ссылку на объект (sourceRef, chartRef). Namespace по умолчанию — namespace самого
// объекта, как делает контроллер. Без имени ссылки нет: nil.
func helmReleaseRef(obj map[string]any, defaultNS string, path ...string) *FluxRef {
	m, ok := mapAt(obj, path...)
	if !ok {
		return nil
	}
	name := strAt(m, "name")
	if name == "" {
		return nil
	}
	return &FluxRef{Kind: strAt(m, "kind"), Namespace: cmp.Or(strAt(m, "namespace"), defaultNS), Name: name}
}

// helmReleaseDependsOn разбирает spec.dependsOn. Вид у зависимостей не указывается (Kind пуст), namespace по
// умолчанию — namespace объекта. Записи без имени и неожиданного типа пропускаются. Никогда не nil.
func helmReleaseDependsOn(obj map[string]any, defaultNS string) []FluxRef {
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

// helmReleaseReconcileAt выбирает момент «последнего reconcile»: lastTransitionTime условия Ready, которое ставит
// сам контроллер при каждом значимом результате (смена статуса, причины или сообщения). status.lastHandledReconcileAt
// — токен запроса, который пишет клиент, он появляется только после ручного reconcile и не обязан быть временем;
// он берётся лишь как запасной вариант и только если разбирается как время.
func helmReleaseReconcileAt(obj map[string]any, conds []ConditionInfo) string {
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

// helmReleaseMessage — сообщение для списка: условие Ready, при его отсутствии Stalled, Reconciling или Released.
// Тексты в conds уже прошли redactFluxMessage.
func helmReleaseMessage(conds []ConditionInfo) string {
	for _, typ := range [...]string{"Ready", "Stalled", "Reconciling", "Released"} {
		for _, c := range conds {
			if c.Type == typ && c.Message != "" {
				return c.Message
			}
		}
	}
	return ""
}

// helmChartURL готовит адрес OCI-чарта (spec.chart.spec.url) к показу без userinfo. Непарсируемый адрес не
// отдаётся вовсе: по обрывку нельзя поручиться, что в нём нет пароля.
func helmChartURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}
