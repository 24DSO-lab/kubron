// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/logredact"
	"github.com/24DSO-lab/kubron/internal/readiness/catalog"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

// Состояния компонента (ComponentDetail.State).
const (
	StateReady       = "ready"
	StateDegraded    = "degraded"
	StateUnavailable = "unavailable"
	StateUnknown     = "unknown"
)

var (
	nodeGVK        = schema.GroupVersionKind{Version: "v1", Kind: "Node"}
	workloadKinds  = []string{"Deployment", "DaemonSet", "StatefulSet"}
	componentNodes = "kubelet"
)

func workloadGVK(kind string) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: kind}
}

// checkComponents проверяет версии и состояние компонентов каталога (ADR-0025 §4): kubelet по объектам Node,
// остальные — по Deployment, DaemonSet и StatefulSet в пространствах имён каталога.
func (r *run) checkComponents(ctx context.Context) {
	cat := r.s.catalog
	if comp, ok := cat.ByID(componentNodes); ok && comp.Type == catalog.TypeNodes {
		r.checkNodes(ctx, comp)
	}

	var failed, truncated []string
	reported := map[string]bool{}
	for _, ns := range cat.Namespaces() {
		for _, kind := range workloadKinds {
			if ctx.Err() != nil {
				failed = append(failed, ns)
				break
			}
			res, err := r.rd.List(ctx, workloadGVK(kind), ListOptions{Namespace: ns})
			if err != nil {
				if !slices.Contains(failed, ns) {
					failed = append(failed, ns)
				}
				continue
			}
			r.method(MethodWorkloadImages)
			if res.Truncated && !slices.Contains(truncated, ns) {
				truncated = append(truncated, ns)
			}
			for i := range res.Items {
				r.checkWorkload(kind, &res.Items[i], res.ObservedAt, reported)
			}
		}
	}
	if len(failed) > 0 {
		r.sourceFinding("source/workloads-unavailable", StatusNotChecked, SeverityWarning, CodeSourceWorkloads,
			map[string]string{"namespaces": strings.Join(slices.Sorted(slices.Values(failed)), ", ")}, CodeRecRerun)
	}
	if len(truncated) > 0 {
		r.sourceFinding("source/workloads-truncated", StatusPartial, SeverityWarning, CodeSourceWorkloadsTrunc,
			map[string]string{"namespaces": strings.Join(slices.Sorted(slices.Values(truncated)), ", ")}, CodeRecRerun)
	}
}

// checkWorkload сопоставляет нагрузку с каталогом. Системная нагрузка вне каталога — «компонент не распознан»;
// нагрузки в пользовательских пространствах имён не рассматриваются.
func (r *run) checkWorkload(kind string, obj *unstructured.Unstructured, observed time.Time, reported map[string]bool) {
	w := catalog.Workload{Kind: kind, Namespace: obj.GetNamespace(), Name: obj.GetName(), Images: containerImages(obj)}
	if observed.IsZero() {
		observed = r.now
	}
	ready, desired := workloadReplicas(kind, obj)
	state := stateOf(ready, desired)
	key := fmt.Sprintf("%s/%s/%s", w.Namespace, strings.ToLower(kind), w.Name)

	comp, ok := r.s.catalog.Match(w)
	if !ok {
		if r.s.catalog.IsSystemNamespace(w.Namespace) {
			r.add(Finding{
				Key: "component/unrecognized/" + key, Type: TypeComponent, Status: StatusNotRecognized,
				Severity: catalog.DefaultSeverity(catalog.StatusNotRecognized), Source: SourceLiveAPI, ObservedAt: observed,
				Explanation:    Message{Code: CodeComponentNotRecognized, Params: map[string]string{"name": w.Name, "target": r.eff.String()}},
				Recommendation: &Message{Code: CodeRecComponentUnknown},
				Component: &ComponentDetail{
					Name: w.Name, Kind: kind, Namespace: w.Namespace, Workload: w.Name, Image: scrubImage(firstOf(w.Images)),
					State: state, Ready: ready, Desired: desired, TargetVersion: r.eff.String(),
				},
			})
		}
		return
	}

	ver, tag, known := comp.ComponentVersion(w)
	var v catalog.Verdict
	if comp.Skew != nil {
		if known {
			v = comp.EvaluateSkew(ver.AsMinor(), r.eff)
		} else {
			v = catalog.Verdict{Status: catalog.StatusInsufficient, Severity: catalog.SeverityWarning, Code: catalog.CodeVersionUnknown}
		}
	} else {
		v = comp.Evaluate(ver, known, r.eff)
	}
	shown := tag
	if known {
		shown = ver.String()
	}
	f := r.componentFinding(comp, v, shown, observed)
	f.Key = "component/" + comp.ID + "/" + key
	d := f.Component
	d.Kind, d.Namespace, d.Workload = kind, w.Namespace, w.Name
	d.Image = scrubImage(imageFor(comp, w))
	d.State, d.Ready, d.Desired = state, ready, desired
	r.finishComponent(&f, state)
	r.add(f)

	// Безусловные замечания каталога (например, проект закрыт) показываются один раз на компонент.
	if !reported[comp.ID] {
		reported[comp.ID] = true
		for _, a := range comp.Advisories {
			af := r.componentFinding(comp, catalog.Verdict{Status: catalog.StatusKnownLimitation, Severity: a.Severity, Code: a.Code, Note: a.Note}, shown, observed)
			af.Key = "component/" + comp.ID + "/advisory/" + a.Code
			af.Component.NoteURL = a.URL
			af.Component.Kind, af.Component.Namespace, af.Component.Workload = kind, w.Namespace, w.Name
			r.add(af)
		}
	}
}

// componentFinding строит finding компонента по вердикту каталога. Источник и дата матрицы берутся из записи.
func (r *run) componentFinding(comp *catalog.Component, v catalog.Verdict, version string, observed time.Time) Finding {
	code := map[catalog.Status]string{
		catalog.StatusConfirmed:       CodeComponentConfirmed,
		catalog.StatusKnownLimitation: CodeComponentKnownLimitation,
		catalog.StatusInsufficient:    CodeComponentInsufficient,
		catalog.StatusNotRecognized:   CodeComponentNotRecognized,
	}[v.Status]
	f := Finding{
		Type: TypeComponent, Status: Status(v.Status), Severity: v.Severity, Source: SourceLiveAPI, ObservedAt: observed,
		Explanation: Message{Code: code, Params: map[string]string{"name": comp.Name, "version": version, "target": r.eff.String()}},
		Component: &ComponentDetail{
			ID: comp.ID, Name: comp.Name, Version: version, TargetVersion: r.eff.String(),
			DocsURL: comp.Docs, CheckedAt: comp.CheckedAt, NoteCode: v.Code, Note: v.Note,
		},
	}
	switch v.Status {
	case catalog.StatusKnownLimitation:
		f.Recommendation = &Message{Code: CodeRecComponentLimit}
	case catalog.StatusInsufficient:
		f.Recommendation = &Message{Code: CodeRecComponentVerify}
	}
	return f
}

// finishComponent повышает важность до предупреждения, если компонент не готов: неработающий компонент
// не должен выглядеть безобидным только потому, что его версия подходит.
func (r *run) finishComponent(f *Finding, state string) {
	if (state == StateDegraded || state == StateUnavailable) && f.Severity.Rank() > SeverityWarning.Rank() {
		f.Severity = SeverityWarning
	}
	if state == StateDegraded || state == StateUnavailable {
		f.Recommendation = &Message{Code: CodeRecComponentUnhealth}
	}
}

// checkNodes оценивает kubelet по правилу version skew: после обновления control plane узлы не должны оказаться
// новее kube-apiserver или отстать больше допустимого. Берётся худший вердикт среди версий узлов.
func (r *run) checkNodes(ctx context.Context, comp *catalog.Component) {
	res, err := r.rd.List(ctx, nodeGVK, ListOptions{})
	if err != nil {
		r.sourceFinding("source/nodes-unavailable", StatusNotChecked, SeverityWarning, CodeSourceNodes, nil, CodeRecRerun)
		return
	}
	r.method(MethodNodeInfo)
	if res.Truncated {
		r.sourceFinding("source/nodes-truncated", StatusPartial, SeverityWarning, CodeSourceListTruncated,
			map[string]string{"kinds": "core/Node"}, CodeRecRerun)
	}
	observed := res.ObservedAt
	if observed.IsZero() {
		observed = r.now
	}

	var minors []kver.Minor
	var unknown, readyNodes int
	for i := range res.Items {
		n := &res.Items[i]
		if nodeReady(n) {
			readyNodes++
		}
		raw, _, _ := unstructured.NestedString(n.Object, "status", "nodeInfo", "kubeletVersion")
		m, err := kver.ParseGitVersion(raw)
		if err != nil {
			unknown++
			continue
		}
		minors = append(minors, m)
	}
	total := len(res.Items)
	if total == 0 {
		return
	}
	state := stateOf(readyNodes, total)

	var v catalog.Verdict
	shown := ""
	if len(minors) == 0 || unknown > 0 {
		v = catalog.Verdict{Status: catalog.StatusInsufficient, Severity: catalog.SeverityWarning, Code: catalog.CodeVersionUnknown}
	}
	if len(minors) > 0 {
		lo, hi := slices.MinFunc(minors, kver.Minor.Compare), slices.MaxFunc(minors, kver.Minor.Compare)
		shown = lo.String()
		if lo != hi {
			shown = lo.String() + "–" + hi.String()
		}
		// Худший вердикт — по самому старому и самому новому узлу: проблема бывает с обоих краёв.
		worst := comp.EvaluateSkew(lo, r.eff)
		if w2 := comp.EvaluateSkew(hi, r.eff); w2.Severity.Rank() < worst.Severity.Rank() {
			worst = w2
		}
		if v.Status == "" || worst.Severity.Rank() < v.Severity.Rank() {
			v = worst
		}
	}
	f := r.componentFinding(comp, v, shown, observed)
	f.Key = "component/" + comp.ID
	f.Component.Kind, f.Component.NodeCount = "Node", total
	f.Component.State, f.Component.Ready, f.Component.Desired = state, readyNodes, total
	r.finishComponent(&f, state)
	r.add(f)
}

func nodeReady(n *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(n.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if ok && m["type"] == "Ready" {
			return m["status"] == "True"
		}
	}
	return false
}

func stateOf(ready, desired int) string {
	switch {
	case desired <= 0:
		return StateUnknown
	case ready >= desired:
		return StateReady
	case ready == 0:
		return StateUnavailable
	}
	return StateDegraded
}

// workloadReplicas возвращает число готовых и желаемых экземпляров нагрузки.
func workloadReplicas(kind string, obj *unstructured.Unstructured) (ready, desired int) {
	get := func(path ...string) (int, bool) {
		v, found, _ := unstructured.NestedFieldNoCopy(obj.Object, path...)
		switch n := v.(type) {
		case int64:
			return int(n), found
		case float64:
			return int(n), found
		case int:
			return n, found
		}
		return 0, false
	}
	if kind == "DaemonSet" {
		desired, _ = get("status", "desiredNumberScheduled")
		ready, _ = get("status", "numberReady")
		return ready, desired
	}
	desired, ok := get("spec", "replicas")
	if !ok {
		desired = 1 // значение Kubernetes по умолчанию
	}
	ready, _ = get("status", "readyReplicas")
	return ready, desired
}

// containerImages возвращает образы контейнеров нагрузки (init-контейнеры не учитываются).
func containerImages(obj *unstructured.Unstructured) []string {
	cs, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	var out []string
	for _, c := range cs {
		if m, ok := c.(map[string]any); ok {
			if img, ok := m["image"].(string); ok && img != "" {
				out = append(out, img)
			}
		}
	}
	return out
}

func firstOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// imageFor возвращает образ, по которому определена версия компонента (первый подходящий по репозиторию).
func imageFor(comp *catalog.Component, w catalog.Workload) string {
	for _, m := range comp.Detect.Workloads {
		for _, img := range w.Images {
			repo, _ := catalog.SplitImage(img)
			for _, want := range m.Images {
				if repo == want || strings.HasSuffix(repo, "/"+want) {
					return img
				}
			}
		}
	}
	return firstOf(w.Images)
}

// scrubImage готовит ссылку на образ для отчёта (ADR-0022): без учётных данных в адресе реестра, без digest;
// остальное проходит редакцию logredact. Если редакция не удалась, значение не сохраняется (fail closed).
func scrubImage(image string) string {
	if image == "" {
		return ""
	}
	if i := strings.Index(image, "://"); i >= 0 {
		image = image[i+3:]
	}
	// Учётные данные в адресе реестра («user:pass@registry/…») отбрасываются до разбора digest.
	if i := strings.Index(image, "/"); i > 0 && strings.Contains(image[:i], "@") {
		image = image[strings.LastIndex(image[:i], "@")+1:]
	}
	image, _, _ = strings.Cut(image, "@")
	if len(image) > 300 {
		image = image[:300]
	}
	res, err := logredact.Redact([]byte(image))
	if err != nil {
		return ""
	}
	return res.Text
}
