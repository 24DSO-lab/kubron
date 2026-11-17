// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	cSub = Subject{Namespace: "shop", Pod: "api-7d9f8-x2x4q", Container: "app"}
	nSub = Subject{Node: "worker-1"}
)

// Эталонные запросы: тест документирует точный PromQL каждого сигнала. Изменение запроса — осознанное изменение теста.
func TestGoldenQueries(t *testing.T) {
	const sel = `namespace="shop",pod="api-7d9f8-x2x4q",container="app"`
	for _, tc := range []struct {
		sig  Signal
		sub  Subject
		want string
	}{
		{CPUThrottlingRatio, cSub,
			`sum(increase(container_cpu_cfs_throttled_periods_total{` + sel + `}[15m])) / sum(increase(container_cpu_cfs_periods_total{` + sel + `}[15m]))`},
		{CPUUsageVsLimit, cSub,
			`max_over_time(sum(rate(container_cpu_usage_seconds_total{` + sel + `}[5m]))[15m:1m]) / max(kube_pod_container_resource_limits{` + sel + `,resource="cpu"})`},
		{MemoryUsageVsLimit, cSub,
			`max(max_over_time(container_memory_working_set_bytes{` + sel + `}[1h])) / max(kube_pod_container_resource_limits{` + sel + `,resource="memory"})`},
		{ContainerRestarts, cSub, `sum(increase(kube_pod_container_status_restarts_total{` + sel + `}[6h]))`},
		{OOMEvents, cSub, `sum(increase(container_oom_events_total{` + sel + `}[6h]))`},
		{NodeMemoryAvailableRatio, nSub,
			`min(min_over_time(node_memory_MemAvailable_bytes{kubernetes_node="worker-1"}[1h])) / max(node_memory_MemTotal_bytes{kubernetes_node="worker-1"})`},
		{NodeCPUUtilization, nSub,
			`clamp(1 - avg(rate(node_cpu_seconds_total{kubernetes_node="worker-1",mode="idle"}[15m])), 0, 1)`},
		{NodeDiskAvailableRatio, nSub,
			`min(node_filesystem_avail_bytes{kubernetes_node="worker-1",mountpoint="/",fstype!="tmpfs"} / node_filesystem_size_bytes{kubernetes_node="worker-1",mountpoint="/",fstype!="tmpfs"})`},
		{NodePressureHistory, nSub,
			`max(max_over_time(kube_node_status_condition{node="worker-1",condition=~"MemoryPressure|DiskPressure|PIDPressure",status="true"}[6h]))`},
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

// Каталог закрыт и полон: каждая константа сигнала этапа A описана, лишнего нет.
func TestCatalogCoversStageASignals(t *testing.T) {
	stageA := []Signal{
		CPUThrottlingRatio, CPUUsageVsLimit, MemoryUsageVsLimit, ContainerRestarts, OOMEvents,
		NodeMemoryAvailableRatio, NodeCPUUtilization, NodeDiskAvailableRatio, NodePressureHistory,
	}
	if len(catalog) != len(stageA) {
		t.Errorf("catalog has %d signals, want %d", len(catalog), len(stageA))
	}
	for _, s := range stageA {
		sp, ok := catalog[s]
		if !ok || !Known(s) {
			t.Errorf("%s is not in the catalog", s)
			continue
		}
		if sp.signal != s || sp.unit == "" || sp.agg == "" || sp.build == nil || len(sp.families) == 0 || len(sp.fields) == 0 {
			t.Errorf("%s: incomplete spec %+v", s, sp)
		}
		for _, f := range sp.families {
			if _, ok := familyProbe[f]; !ok {
				t.Errorf("%s: family %s has no probe", s, f)
			}
		}
	}
	if Known("pvc_usage_ratio") || Known("") {
		t.Error("a signal outside the catalog must be unknown")
	}
	if len(probeFamilies)+1 > MaxProbeQueries {
		t.Errorf("probes (%d families + coverage) exceed the budget of %d", len(probeFamilies), MaxProbeQueries)
	}
}

var selectorRE = regexp.MustCompile(`([a-zA-Z_:][a-zA-Z0-9_:]*)\{`)

// Метрики, объявленные сигналом, — ровно те, что стоят в его запросе: манифест сверяется с объявлением, а объявление —
// с запросом, поэтому запрос не может уйти от проверки.
func TestDeclaredMetricsMatchQueries(t *testing.T) {
	for sig, sp := range catalog {
		q := sp.build(Subject{Namespace: "ns", Pod: "p", Container: "c", Node: "n"}, sp.window)
		var used []string
		for _, m := range selectorRE.FindAllStringSubmatch(q, -1) {
			used = append(used, m[1])
		}
		slices.Sort(used)
		used = slices.Compact(used)
		want := slices.Clone(sp.metrics)
		slices.Sort(want)
		if !slices.Equal(used, want) {
			t.Errorf("%s: query uses %v, declared %v", sig, used, want)
		}
		if sp.window == 0 && strings.Contains(q, "[") {
			t.Errorf("%s: an instant signal must not have a range: %s", sig, q)
		}
	}
	for k, q := range familyProbe {
		if !strings.HasPrefix(q, "count(") || strings.Contains(q, "by (") {
			t.Errorf("probe %s must be a bare count: %s", k, q)
		}
	}
}

func TestRequirements(t *testing.T) {
	reqs := Requirements()
	if len(reqs) != len(catalog)+1 {
		t.Fatalf("got %d requirements, want %d", len(reqs), len(catalog)+1)
	}
	last := reqs[len(reqs)-1]
	if last.Signal != "" || !slices.Contains(last.Services, "prometheus") || len(last.Metrics) == 0 {
		t.Errorf("the last requirement must be the history coverage: %+v", last)
	}
	for i, r := range reqs[:len(reqs)-1] {
		if r.Signal == "" || len(r.Metrics) == 0 || len(r.Jobs) == 0 {
			t.Errorf("incomplete requirement %+v", r)
		}
		if i > 0 && reqs[i-1].Signal >= r.Signal {
			t.Error("requirements are not sorted by signal")
		}
	}
	// Возвращаются копии: правка результата не портит каталог.
	reqs[0].Metrics[0] = "tampered"
	if Requirements()[0].Metrics[0] == "tampered" {
		t.Error("Requirements() leaks internal slices")
	}
}

// Все подставляемые значения — литералы PromQL; даже если проверка параметров ослабнет, значение не выйдет за литерал.
// Fuzz проверяет обе линии защиты: проверка DNS-1123 и экранирование.
func FuzzBuildQueries(f *testing.F) {
	f.Add("shop", "api-1", "app", "node-1")
	f.Add(`x"} or vector(1) #`, `p"`, "c\n", `n\`)
	f.Add("", "", "", "")
	f.Add("ns", "p.*", "c|d", "n{")
	f.Fuzz(func(t *testing.T, ns, pod, ctr, node string) {
		sub := Subject{Namespace: ns, Pod: pod, Container: ctr, Node: node}
		constants := []string{"cpu", "memory", "idle", "/", "tmpfs", "MemoryPressure|DiskPressure|PIDPressure", "true"}
		for sig, sp := range catalog {
			// Построитель, вызванный в обход проверки: значения всё равно остаются внутри литералов.
			q := sp.build(sub, sp.window)
			lits, ok := literals(q)
			if !ok {
				t.Fatalf("%s: a literal is not closed: %s", sig, q)
			}
			allowed := append([]string{ns, pod, ctr, node}, constants...)
			for _, l := range lits {
				if !slices.Contains(allowed, l) {
					t.Fatalf("%s: unexpected literal %q in %s", sig, l, q)
				}
			}
			// Вне литералов нет ничего, кроме текста самого каталога: пробелы, скобки, имена метрик и функций.
			if rest := stripLiterals(q); strings.ContainsAny(rest, "\n\r\x00") || !balanced(rest) {
				t.Fatalf("%s: the query structure is broken: %s", sig, q)
			}
		}
		items, err := plan([]Want{{CPUThrottlingRatio, sub}, {NodeCPUUtilization, sub}})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if (it.gap == "") == (it.expr == "") {
				t.Fatalf("a want must have either a query or a gap: %+v", it)
			}
			if it.gap != "" && it.gap != ReasonInvalidRef {
				t.Fatalf("unexpected gap %q", it.gap)
			}
		}
	})
}

// stripLiterals убирает строковые литералы из запроса.
func stripLiterals(q string) string {
	var b strings.Builder
	for i := 0; i < len(q); i++ {
		if q[i] != '"' {
			b.WriteByte(q[i])
			continue
		}
		_, n, ok := scanLiteral(q[i:])
		if !ok {
			return q
		}
		b.WriteString(`""`)
		i += n - 1
	}
	return b.String()
}

func balanced(q string) bool {
	depth := 0
	for _, r := range q {
		switch r {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			if depth--; depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func TestPlanRejectsInvalidRefsWithoutQuery(t *testing.T) {
	for name, w := range map[string]Want{
		"quote in pod":         {ContainerRestarts, Subject{Namespace: "ns", Pod: `p"} or vector(1) #`, Container: "c"}},
		"uppercase node":       {NodeCPUUtilization, Subject{Node: "Node-1"}},
		"empty container":      {OOMEvents, Subject{Namespace: "ns", Pod: "p"}},
		"regex in node":        {NodePressureHistory, Subject{Node: "n.*"}},
		"newline in namespace": {CPUThrottlingRatio, Subject{Namespace: "n\ns", Pod: "p", Container: "c"}},
	} {
		items, err := plan([]Want{w})
		if err != nil || len(items) != 1 || items[0].gap != ReasonInvalidRef || items[0].expr != "" {
			t.Errorf("%s: %+v, %v", name, items, err)
		}
	}
}

func TestPlanDedupesAndKeepsOrder(t *testing.T) {
	other := Subject{Namespace: "shop", Pod: "api-2", Container: "app"}
	items, err := plan([]Want{
		{OOMEvents, cSub}, {ContainerRestarts, cSub}, {OOMEvents, cSub}, {OOMEvents, other}, {ContainerRestarts, cSub},
	})
	if err != nil || len(items) != 3 {
		t.Fatalf("%d items, %v", len(items), err)
	}
	if items[0].want.Signal != OOMEvents || items[1].want.Signal != ContainerRestarts || items[2].want.Subject != other {
		t.Errorf("order of first occurrences lost: %+v", items)
	}
	if _, err := plan([]Want{{Signal("nope"), cSub}}); err == nil {
		t.Error("an unknown signal is a programming error")
	}
}
