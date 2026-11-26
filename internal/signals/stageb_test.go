// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"slices"
	"strings"
	"testing"
)

// Сигналы этапа B (ADR-0032 §4): эталонные запросы, опциональное семейство ingress, проверка параметров.

var (
	pvcSub = Subject{Namespace: "shop", PVC: "data-db-0"}
	hpaSub = Subject{Namespace: "shop", HPA: "api"}
	depSub = Subject{Namespace: "shop", Deployment: "api"}
	ingSub = Subject{Namespace: "shop", Ingress: "web"}
)

// Эталонные запросы этапа B: изменение запроса — осознанное изменение теста.
func TestGoldenQueriesStageB(t *testing.T) {
	for _, tc := range []struct {
		sig  Signal
		sub  Subject
		want string
	}{
		{PVCUsageRatio, pvcSub,
			`max(max_over_time((kubelet_volume_stats_used_bytes{namespace="shop",persistentvolumeclaim="data-db-0"} / ` +
				`kubelet_volume_stats_capacity_bytes{namespace="shop",persistentvolumeclaim="data-db-0"})[1h:1m]))`},
		{HPAAtMaxReplicas, hpaSub,
			`max(max_over_time((kube_horizontalpodautoscaler_status_current_replicas{namespace="shop",horizontalpodautoscaler="api"} >= bool on(namespace,horizontalpodautoscaler) ` +
				`kube_horizontalpodautoscaler_spec_max_replicas{namespace="shop",horizontalpodautoscaler="api"})[15m:1m]))`},
		{DeploymentUnavailableReplicas, depSub,
			`max(max_over_time(kube_deployment_status_replicas_unavailable{namespace="shop",deployment="api"}[15m]))`},
		{Ingress5xxRatio, ingSub,
			`sum(increase(nginx_ingress_controller_requests{namespace="shop",ingress="web",status=~"5.."}[15m])) / ` +
				`sum(increase(nginx_ingress_controller_requests{namespace="shop",ingress="web"}[15m]))`},
		{IngressLatencyP95, ingSub,
			`histogram_quantile(0.95, sum by (le) (rate(nginx_ingress_controller_request_duration_seconds_bucket{namespace="shop",ingress="web"}[15m])))`},
	} {
		items, err := plan([]Want{{tc.sig, tc.sub}})
		if err != nil || len(items) != 1 || items[0].gap != "" {
			t.Fatalf("%s: plan = %+v, %v", tc.sig, items, err)
		}
		if items[0].expr != tc.want {
			t.Errorf("%s:\n got  %s\n want %s", tc.sig, items[0].expr, tc.want)
		}
	}
}

// Запросы этапа B не содержат значений полей объектов: только имена из субъекта, экранированные как литералы.
func TestStageBQueriesCarryOnlySubjectValues(t *testing.T) {
	for _, sig := range []Signal{PVCUsageRatio, HPAAtMaxReplicas, DeploymentUnavailableReplicas, Ingress5xxRatio, IngressLatencyP95} {
		sp := catalog[sig]
		q := sp.build(Subject{Namespace: "ns", PVC: "pv", HPA: "hp", Deployment: "dp", Ingress: "ig"}, sp.window)
		lits, ok := literals(q)
		if !ok {
			t.Fatalf("%s: a literal is not closed: %s", sig, q)
		}
		for _, l := range lits {
			if !slices.Contains([]string{"ns", "pv", "hp", "dp", "ig", "5.."}, l) {
				t.Errorf("%s: unexpected literal %q in %s", sig, l, q)
			}
		}
	}
}

// Недопустимое имя (кавычка, регулярное выражение, заглавные) не доходит до запроса: invalid_ref, а не выражение.
func TestStageBPlanRejectsInvalidRefs(t *testing.T) {
	for name, w := range map[string]Want{
		"quote in pvc":         {PVCUsageRatio, Subject{Namespace: "ns", PVC: `v"} or vector(1) #`}},
		"regex in hpa":         {HPAAtMaxReplicas, Subject{Namespace: "ns", HPA: "a.*"}},
		"uppercase deployment": {DeploymentUnavailableReplicas, Subject{Namespace: "ns", Deployment: "Api"}},
		"newline in ingress":   {Ingress5xxRatio, Subject{Namespace: "ns", Ingress: "w\neb"}},
		"empty ingress":        {IngressLatencyP95, Subject{Namespace: "ns"}},
		"empty pvc":            {PVCUsageRatio, Subject{Namespace: "ns"}},
	} {
		items, err := plan([]Want{w})
		if err != nil || len(items) != 1 || items[0].gap != ReasonInvalidRef || items[0].expr != "" {
			t.Errorf("%s: %+v, %v", name, items, err)
		}
	}
}

// Каждое семейство этапа B проверяется лениво и только когда нужно: сигнал контейнера не трогает volume и ingress.
func TestStageBFamiliesAreProbedLazily(t *testing.T) {
	q := &fakeQuerier{}
	fetch(t, q, podWant(OOMEvents))
	for _, e := range q.probeCalls() {
		if e == familyProbe[FamilyVolume] || e == familyProbe[FamilyIngress] {
			t.Errorf("an unneeded family was probed: %s", e)
		}
	}
	q = &fakeQuerier{}
	fetch(t, q, Want{PVCUsageRatio, pvcSub})
	if p := q.probeCalls(); !slices.Contains(p, familyProbe[FamilyVolume]) || slices.Contains(p, familyProbe[FamilyIngress]) {
		t.Errorf("probes = %v", p)
	}
}

// Без ingress-nginx семейства нет: сигналы Ingress получают family_missing, запросов сигналов нет, операция не частичная.
func TestIngressSignalsNeedTheIngressFamily(t *testing.T) {
	q := &fakeQuerier{answer: func(expr string) ([]Sample, error) {
		if expr == familyProbe[FamilyIngress] {
			return nil, nil // контроллера нет
		}
		return healthy(0.5)(expr)
	}}
	set := fetch(t, q, Want{Ingress5xxRatio, ingSub}, Want{IngressLatencyP95, ingSub}, Want{DeploymentUnavailableReplicas, depSub})
	for _, sig := range []Signal{Ingress5xxRatio, IngressLatencyP95} {
		if g, ok := set.GapOf(sig, ingSub); !ok || g.Reason != ReasonFamilyMissing {
			t.Errorf("%s: gap = %+v, want family_missing", sig, g)
		}
	}
	if _, ok := set.Get(DeploymentUnavailableReplicas, depSub); !ok {
		t.Error("a signal of another family must not be affected")
	}
	if set.PartialSource() != "" || set.StackReason() != "" {
		t.Errorf("a missing optional family must not make the operation partial: %q %q", set.PartialSource(), set.StackReason())
	}
	for _, c := range q.signalCalls() {
		if strings.Contains(c, "nginx_ingress_controller") {
			t.Errorf("an ingress query ran without the family: %s", c)
		}
	}
}

// Нет трафика — нет серий: это пробел no_series, а не нулевая доля ошибок.
func TestIngressNoTrafficIsNoSeries(t *testing.T) {
	q := &fakeQuerier{answer: func(expr string) ([]Sample, error) {
		if strings.Contains(expr, "nginx_ingress_controller") && !isProbe(expr) {
			return nil, nil
		}
		return healthy(0.5)(expr)
	}}
	set := fetch(t, q, Want{Ingress5xxRatio, ingSub})
	if _, ok := set.Get(Ingress5xxRatio, ingSub); ok {
		t.Error("an empty result must not become a value")
	}
	if g, ok := set.GapOf(Ingress5xxRatio, ingSub); !ok || g.Reason != ReasonNoSeries {
		t.Errorf("gap = %+v", g)
	}
}

// Значения этапа B несут единицу и агрегат; порядок значений детерминирован (сигнал, затем цель).
func TestStageBValuesCarryMetadata(t *testing.T) {
	q := &fakeQuerier{answer: healthy(0.25)}
	set := fetch(t, q, Want{IngressLatencyP95, ingSub}, Want{PVCUsageRatio, pvcSub}, Want{HPAAtMaxReplicas, hpaSub})
	v, ok := set.Get(IngressLatencyP95, ingSub)
	if !ok || v.Unit != "seconds" || v.Agg != "p95" || v.Window != windowShort {
		t.Errorf("latency = %+v", v)
	}
	if v, _ := set.Get(PVCUsageRatio, pvcSub); v.Unit != "ratio" || v.Window != windowMedium {
		t.Errorf("pvc = %+v", v)
	}
	if v, _ := set.Get(HPAAtMaxReplicas, hpaSub); v.Unit != "flag" || v.Window != windowShort {
		t.Errorf("hpa = %+v", v)
	}
	got := set.Values()
	if len(got) != 3 || got[0].Signal > got[1].Signal || got[1].Signal > got[2].Signal {
		t.Errorf("values are not ordered: %+v", got)
	}
}

// Все пять проверок семейств и проверка покрытия укладываются в бюджет проверок.
func TestAllProbesFitTheBudget(t *testing.T) {
	q := &fakeQuerier{}
	fetch(t, q, podWant(OOMEvents), nodeWant(NodeCPUUtilization), Want{PVCUsageRatio, pvcSub}, Want{Ingress5xxRatio, ingSub}, Want{HPAAtMaxReplicas, hpaSub})
	if n := len(q.probeCalls()); n != len(probeFamilies)+1 || n > MaxProbeQueries {
		t.Errorf("%d probes, budget %d", n, MaxProbeQueries)
	}
}
