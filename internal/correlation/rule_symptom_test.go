// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"fmt"
	"testing"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// symptomWorld — n Deployment'ов namespace prod, Pod'ы которых в CrashLoopBackOff; первый зовётся api.
func symptomWorld(n int, nodeConds ...string) *world {
	w := &world{}
	names := []string{"api", "web", "worker", "cache", "queue", "search", "mail", "auth", "billing", "cron", "feed", "geo"}
	for _, d := range names[:n] {
		w.crashDeployment("prod", d, "n1", 1, 20)
	}
	w.node("n1", false, nodeConds...)
	return w
}

func TestSharedSymptomThreshold(t *testing.T) {
	for _, tt := range []struct {
		workloads int
		want      bool
	}{{4, false}, {5, true}, {8, true}} {
		t.Run(fmt.Sprintf("%d нагрузок", tt.workloads), func(t *testing.T) {
			res, _ := run(t, symptomWorld(tt.workloads), "prod", "api-hapi-0")
			c := byKind(res, correlation.KindSharedSymptom)
			if (c != nil) != tt.want {
				t.Fatalf("кандидат shared_symptom: есть=%v, ожидалось %v", c != nil, tt.want)
			}
			if c != nil && c.Total != tt.workloads {
				t.Fatalf("Total=%d, want %d", c.Total, tt.workloads)
			}
		})
	}
}

// Уверенность shared_symptom не поднимается выше low ни при каких условиях: ни при давлении на узле, ни при
// множестве нагрузок, ни при близости по времени (ADR-0033 §4).
func TestSharedSymptomNeverAboveLow(t *testing.T) {
	for _, c := range []struct {
		name string
		w    *world
	}{{"обычный узел", symptomWorld(12)}, {"давление памяти", symptomWorld(12, "MemoryPressure=True@30")}} {
		t.Run(c.name, func(t *testing.T) {
			res, _ := run(t, c.w, "prod", "api-hapi-0")
			cand := byKind(res, correlation.KindSharedSymptom)
			if cand == nil {
				t.Fatal("кандидата нет")
			}
			if cand.Confidence != diagnose.ConfidenceLow {
				t.Fatalf("confidence=%s, want low", cand.Confidence)
			}
			if cand.Total != 12 || len(cand.Supporting) == 0 {
				t.Fatalf("Total=%d, Supporting=%d", cand.Total, len(cand.Supporting))
			}
		})
	}
}

func TestSharedSymptomOtherNamespaceIgnored(t *testing.T) {
	w := symptomWorld(3)
	for _, d := range []string{"a", "b", "c", "d"} {
		w.crashDeployment("other", d, "n1", 1, 20)
	}
	res, _ := run(t, w, "prod", "api-hapi-0")
	if c := byKind(res, correlation.KindSharedSymptom); c != nil {
		t.Fatalf("нагрузки чужого namespace не должны учитываться: %+v", c)
	}
}

func TestSharedSymptomHealthyTarget(t *testing.T) {
	w := &world{}
	w.deployment("prod", "api", "n1", 1, 0) // цель здорова
	for _, d := range []string{"a", "b", "c", "d", "e", "f"} {
		w.crashDeployment("prod", d, "n1", 1, 20)
	}
	w.node("n1", false)
	res, _ := run(t, w, "prod", "api-hapi-0")
	if c := byKind(res, correlation.KindSharedSymptom); c != nil {
		t.Fatalf("у здоровой цели нет симптома: %+v", c)
	}
}

func TestSharedSymptomDeterministicAndGolden(t *testing.T) {
	res1, _ := run(t, symptomWorld(6), "prod", "api-hapi-0")
	res2, _ := run(t, symptomWorld(6), "prod", "api-hapi-0")
	if mustJSON(t, res1) != mustJSON(t, res2) {
		t.Fatal("результат недетерминирован")
	}
	if byKind(res1, correlation.KindSharedSymptom) == nil {
		t.Fatal("кандидата нет")
	}
	golden(t, "shared_symptom_low", res1)
}
