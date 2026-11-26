// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
)

// lastAppliedAnnotation — аннотация kubectl apply: версия API, которой пользователь описал объект.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// maxListedKinds — сколько видов перечисляется в тексте одного предупреждения (полный список — в coverage).
const maxListedKinds = 10

// checkAPIs ищет использование устаревших и удалённых API в живых объектах (ADR-0025 §3).
//
// Способ обнаружения. Живой объект отдаётся сервером в версии, о которой просит клиент, поэтому по самой версии
// ответа использование не определить. Версию API, которой объект создан или изменён, помнят managedFields
// (поле apiVersion записи менеджера) и аннотация last-applied-configuration. Для каждого вида из матрицы, который
// кластер обслуживает и Kubron вправе читать, читаются объекты (cluster-wide) и в этих двух местах ищутся
// устаревшие и удалённые версии. Ограничения называются в coverage: клиенты, которые API только читают,
// не оставляют следов; объекты без managedFields и без аннотации не видны; виды вне allowlist платформы, пользовательские
// ресурсы и манифесты вне кластера (Git, GitOps, Helm) не проверяются.
func (r *run) checkAPIs(ctx context.Context) {
	lists, err := r.rd.ServerResources(ctx)
	switch {
	case err == nil:
	case errors.Is(err, ErrSourcePartial):
		r.sourceFinding("source/server-resources", StatusPartial, SeverityWarning, CodeSourceResourcesPart,
			map[string]string{"failed": strings.Join(partialDetails(err), ", ")}, CodeRecRerun)
	default:
		r.sourceFinding("source/server-resources", StatusNotChecked, SeverityWarning, CodeSourceResources, nil, CodeRecRerun)
		return
	}
	r.method(MethodServerResources)
	served := servedKinds(lists)

	// Кандидаты: записи матрицы, важные для перехода и обслуживаемые кластером, сгруппированные по виду.
	byGK := map[schema.GroupKind][]apimatrix.Entry{}
	for e := range r.s.matrix.Relevant(r.cur, r.eff) {
		if !served[schema.GroupVersion{Group: e.Group, Version: e.Version}][e.Kind] {
			continue // кластер этот API не обслуживает: объектов в этой версии быть не может
		}
		gk := schema.GroupKind{Group: e.Group, Kind: e.Kind}
		byGK[gk] = append(byGK[gk], e)
	}
	gks := slices.SortedFunc(maps.Keys(byGK), func(a, b schema.GroupKind) int { return strings.Compare(a.String(), b.String()) })

	var unavailable, truncated []string
	for _, gk := range gks {
		cands := byGK[gk]
		if ctx.Err() != nil {
			unavailable = append(unavailable, gkName(gk))
			r.notCover = append(r.notCover, gkName(gk))
			continue
		}
		res, err := r.rd.List(ctx, listGVK(gk, cands, served), ListOptions{})
		switch {
		case errors.Is(err, ErrSourceForbidden), errors.Is(err, ErrSourceNotFound):
			// Вид вне allowlist платформы или недоступен: это не сбой, а известный предел проверки.
			r.notCover = append(r.notCover, gkName(gk))
			continue
		case err != nil:
			unavailable = append(unavailable, gkName(gk))
			r.notCover = append(r.notCover, gkName(gk))
			continue
		}
		r.scanned = append(r.scanned, gkName(gk))
		r.method(MethodManagedFields)
		r.method(MethodLastApplied)
		if res.Truncated {
			truncated = append(truncated, gkName(gk))
		}
		r.scanKind(gk, cands, res)
	}

	if len(unavailable) > 0 {
		r.sourceFinding("source/list-unavailable", StatusNotChecked, SeverityWarning, CodeSourceListUnavail,
			map[string]string{"kinds": listKinds(unavailable)}, CodeRecRerun)
	}
	if len(truncated) > 0 {
		r.sourceFinding("source/list-truncated", StatusPartial, SeverityWarning, CodeSourceListTruncated,
			map[string]string{"kinds": listKinds(truncated)}, CodeRecRerun)
	}
	if blind := slices.DeleteFunc(slices.Clone(r.notCover), func(k string) bool { return slices.Contains(unavailable, k) }); len(blind) > 0 {
		blind = slices.Compact(slices.Sorted(slices.Values(blind)))
		r.add(Finding{
			Key: "source/api-not-covered", Type: TypeSource, Status: StatusNotChecked, Severity: SeverityInfo,
			Explanation: Message{Code: CodeSourceNotCovered, Params: map[string]string{
				"count": strconv.Itoa(len(blind)), "kinds": listKinds(blind)}},
			Recommendation: &Message{Code: CodeRecReadManually},
			Source:         SourceLiveAPI, ObservedAt: r.now,
		})
	}
}

func gkName(gk schema.GroupKind) string {
	if gk.Group == "" {
		return "core/" + gk.Kind
	}
	return gk.Group + "/" + gk.Kind
}

func listKinds(kinds []string) string {
	kinds = slices.Sorted(slices.Values(kinds))
	if len(kinds) > maxListedKinds {
		return strings.Join(kinds[:maxListedKinds], ", ") + ", …"
	}
	return strings.Join(kinds, ", ")
}

// listGVK выбирает версию, через которую читать вид: предпочтительна версия, не попавшая в кандидаты
// (устаревшая версия может обслуживаться хуже), иначе любая обслуживаемая.
func listGVK(gk schema.GroupKind, cands []apimatrix.Entry, served map[schema.GroupVersion]map[string]bool) schema.GroupVersionKind {
	all := versionsOf(served, gk)
	bad := make(map[string]bool, len(cands))
	for _, c := range cands {
		bad[c.Version] = true
	}
	// Стабильные версии (v1, v2) предпочтительнее бета-версий; при равенстве — старшая по порядку строки.
	ver := all[len(all)-1]
	for i := len(all) - 1; i >= 0; i-- {
		if !bad[all[i]] && !strings.Contains(all[i], "alpha") && !strings.Contains(all[i], "beta") {
			ver = all[i]
			break
		}
	}
	return gk.WithVersion(ver)
}

// scanKind ищет в объектах следы устаревших версий API и добавляет по одному finding на пару «apiVersion, Kind».
func (r *run) scanKind(gk schema.GroupKind, cands []apimatrix.Entry, res *ListResult) {
	byAPIVersion := make(map[string]apimatrix.Entry, len(cands))
	for _, c := range cands {
		byAPIVersion[c.APIVersion()] = c
	}
	usage := map[string][]ResourceRef{}
	for i := range res.Items {
		for av, ev := range apiVersionsUsed(&res.Items[i], byAPIVersion) {
			usage[av] = append(usage[av], ResourceRef{
				Namespace: res.Items[i].GetNamespace(), Name: res.Items[i].GetName(), Evidence: ev,
			})
		}
	}
	observed := res.ObservedAt
	if observed.IsZero() {
		observed = r.now
	}
	for _, av := range slices.Sorted(maps.Keys(usage)) {
		r.add(r.apiFinding(byAPIVersion[av], usage[av], observed))
	}
}

// apiVersionsUsed возвращает версии API из byAPIVersion, следы которых есть в объекте, и способы обнаружения.
func apiVersionsUsed(obj *unstructured.Unstructured, byAPIVersion map[string]apimatrix.Entry) map[string][]string {
	found := map[string][]string{}
	for _, mf := range obj.GetManagedFields() {
		if _, ok := byAPIVersion[mf.APIVersion]; ok && !slices.Contains(found[mf.APIVersion], EvidenceManagedFields) {
			found[mf.APIVersion] = append(found[mf.APIVersion], EvidenceManagedFields)
		}
	}
	if raw := obj.GetAnnotations()[lastAppliedAnnotation]; raw != "" {
		var la struct {
			APIVersion string `json:"apiVersion"`
		}
		if json.Unmarshal([]byte(raw), &la) == nil {
			if _, ok := byAPIVersion[la.APIVersion]; ok && !slices.Contains(found[la.APIVersion], EvidenceLastApplied) {
				found[la.APIVersion] = append(found[la.APIVersion], EvidenceLastApplied)
			}
		}
	}
	for av := range found {
		slices.Sort(found[av])
	}
	return found
}

func (r *run) apiFinding(e apimatrix.Entry, res []ResourceRef, observed time.Time) Finding {
	slices.SortFunc(res, func(a, b ResourceRef) int {
		if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	d := &APIDetail{
		APIVersion: e.APIVersion(), Kind: e.Kind,
		DeprecatedIn: minorString(e.Deprecated), RemovedIn: minorString(e.Removed),
		ResourceCount: len(res), GuideURL: r.s.matrix.Source.Guide,
	}
	if e.Replacement != nil {
		d.Replacement = e.Replacement.String()
	}
	if len(res) > MaxResourcesPerFinding {
		res, d.ResourcesTruncated = res[:MaxResourcesPerFinding], true
	}
	d.Resources = res

	f := Finding{
		Key:  "api/" + e.APIVersion() + "/" + e.Kind,
		Type: TypeAPI, Source: SourceLiveAPI, ObservedAt: observed, API: d,
	}
	p := map[string]string{
		"apiVersion": d.APIVersion, "kind": d.Kind, "target": r.eff.String(),
		"deprecatedIn": d.DeprecatedIn, "removedIn": d.RemovedIn, "count": strconv.Itoa(d.ResourceCount),
	}
	if e.StateAt(r.eff) == apimatrix.StateRemoved {
		f.Status, f.Severity, f.Explanation = StatusRemoved, SeverityBlocker, Message{Code: CodeAPIRemoved, Params: p}
	} else {
		f.Status, f.Severity, f.Explanation = StatusDeprecated, SeverityWarning, Message{Code: CodeAPIDeprecated, Params: p}
	}
	if d.Replacement != "" {
		f.Recommendation = &Message{Code: CodeRecMigrate, Params: map[string]string{"replacement": d.Replacement}}
	} else {
		f.Recommendation = &Message{Code: CodeRecMigrateNoReplace}
	}
	return f
}

func minorString(m interface {
	IsZero() bool
	String() string
}) string {
	if m.IsZero() {
		return ""
	}
	return m.String()
}

func partialDetails(err error) []string {
	var pe *PartialError
	if errors.As(err, &pe) {
		return slices.Clone(pe.Failed)
	}
	return nil
}
