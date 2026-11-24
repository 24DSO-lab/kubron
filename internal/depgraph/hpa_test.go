// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

var (
	hpaRef       = depgraph.Ref{Group: "autoscaling", Kind: "HorizontalPodAutoscaler", Namespace: "app", Name: "web"}
	hpaDeploy    = depgraph.Ref{Group: "apps", Kind: "Deployment", Namespace: "app", Name: "web"}
	hpaGVKHPA    = schema.GroupVersionKind{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"}
	hpaGVKDeploy = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
)

// hpaYAML — HPA app/web, который масштабирует target (kind, apiVersion, name) и несёт условия conditions
// (готовая YAML-запись элементов списка, пустая строка — без status).
func hpaYAML(apiVersion, kind, name, conditions string) string {
	status := ""
	if conditions != "" {
		status = "status:\n  conditions:\n" + conditions
	}
	return fmt.Sprintf(`
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: web, namespace: app, uid: hpa-1}
spec:
  scaleTargetRef: {apiVersion: %s, kind: %s, name: %s}
  minReplicas: 1
  maxReplicas: 5
%s
`, apiVersion, kind, name, status)
}

func hpaCond(typ, status, reason string) string {
	return fmt.Sprintf("    - {type: %s, status: %q, reason: %s}\n", typ, status, reason)
}

// Здоровый HPA: ребро scales ok к Deployment, дальше работают стандартные раскрытия (ReplicaSet → Pod).
func TestHPAScalesHealthyDeployment(t *testing.T) {
	f := world(t, hpaYAML("apps/v1", "Deployment", "web", hpaCond("ScalingActive", "True", "ValidMetricFound")))
	g := build(t, f, hpaRef, depgraph.Options{})
	root := mustNode(t, g, hpaRef)
	if root.Health != depgraph.Healthy || root.Reason != "" {
		t.Errorf("hpa = %s/%s, want Healthy", root.Health, root.Reason)
	}
	if e := edgeOf(t, g, hpaRef, hpaDeploy, depgraph.EdgeScales); e.State != depgraph.EdgeOK {
		t.Errorf("scales edge = %+v, want ok", e)
	}
	pod := depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "web-7d-x"}
	if !hasNode(g, pod) {
		t.Errorf("the target was not expanded down to its Pods: %v", keysOf(g))
	}
}

// Цели нет: заглушка Missing и ребро missing; здоровье HPA от этого не меняется.
func TestHPATargetMissing(t *testing.T) {
	f := world(t, hpaYAML("apps/v1", "Deployment", "gone", ""))
	g := build(t, f, hpaRef, depgraph.Options{})
	gone := depgraph.Ref{Group: "apps", Kind: "Deployment", Namespace: "app", Name: "gone"}
	n := mustNode(t, g, gone)
	if n.Health != depgraph.Missing || !n.Placeholder {
		t.Errorf("target = %+v, want a Missing placeholder", n)
	}
	if e := edgeOf(t, g, hpaRef, gone, depgraph.EdgeScales); e.State != depgraph.EdgeMissing {
		t.Errorf("scales edge = %+v, want missing", e)
	}
}

// Состояние HPA выводится по условиям. ScalingDisabled — не неисправность, а остальные причины ScalingActive=False — да.
func TestHPAHealthFromConditions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		conditions string
		health     depgraph.Health
		reason     string
	}{
		{"no status yet", "", depgraph.Healthy, ""},
		{"scaling active", hpaCond("ScalingActive", "True", "ValidMetricFound"), depgraph.Healthy, ""},
		{"metrics unavailable", hpaCond("ScalingActive", "False", "FailedGetResourceMetric"), depgraph.Degraded, depgraph.ReasonScalingInactive},
		{"pods metric unavailable", hpaCond("ScalingActive", "False", "FailedGetPodsMetric"), depgraph.Degraded, depgraph.ReasonScalingInactive},
		{"unknown inactive reason", hpaCond("ScalingActive", "False", "SomethingNew"), depgraph.Degraded, depgraph.ReasonScalingInactive},
		{"scaling disabled", hpaCond("ScalingActive", "False", "ScalingDisabled"), depgraph.Healthy, depgraph.ReasonScalingDisabled},
		{"limited only", hpaCond("ScalingLimited", "True", "TooManyReplicas"), depgraph.Healthy, ""},
		{"unable to scale only", hpaCond("AbleToScale", "False", "FailedGetScale"), depgraph.Healthy, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := build(t, world(t, hpaYAML("apps/v1", "Deployment", "web", tc.conditions)), hpaRef, depgraph.Options{})
			if n := mustNode(t, g, hpaRef); n.Health != tc.health || n.Reason != tc.reason {
				t.Errorf("health = %s/%q, want %s/%q", n.Health, n.Reason, tc.health, tc.reason)
			}
		})
	}
}

// StatefulSet и ReplicaSet — поддерживаемые цели.
func TestHPASupportedTargetKinds(t *testing.T) {
	for _, tc := range []struct{ kind, manifest string }{
		{"StatefulSet", `
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: db, namespace: app, uid: s1}
spec: {replicas: 1, selector: {matchLabels: {app: db}}}
status: {readyReplicas: 1, replicas: 1}
`},
		{"ReplicaSet", `
apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: db, namespace: app, uid: r9}
spec: {replicas: 1, selector: {matchLabels: {app: db}}}
status: {readyReplicas: 1, replicas: 1}
`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			f := world(t, tc.manifest, hpaYAML("apps/v1", tc.kind, "db", ""))
			g := build(t, f, hpaRef, depgraph.Options{})
			target := depgraph.Ref{Group: "apps", Kind: tc.kind, Namespace: "app", Name: "db"}
			if n := mustNode(t, g, target); n.Placeholder || n.Health != depgraph.Healthy {
				t.Errorf("target = %+v", n)
			}
			if e := edgeOf(t, g, hpaRef, target, depgraph.EdgeScales); e.State != depgraph.EdgeOK {
				t.Errorf("scales edge = %+v", e)
			}
		})
	}
}

// Вид вне Deployment, StatefulSet и ReplicaSet — Unknown/policy: не ошибка и не чтение.
func TestHPAUnsupportedTargetIsPolicyUnknown(t *testing.T) {
	for _, tc := range []struct{ name, apiVersion, kind, group string }{
		{"custom resource", "argoproj.io/v1alpha1", "Rollout", "argoproj.io"},
		{"replication controller", "v1", "ReplicationController", ""},
		{"deployment of a foreign group", "extensions/v1beta1", "Deployment", "extensions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := world(t, hpaYAML(tc.apiVersion, tc.kind, "web", ""))
			g, err := depgraph.Build(t.Context(), f, hpaRef, depgraph.Options{})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			target := depgraph.Ref{Group: tc.group, Kind: tc.kind, Namespace: "app", Name: "web"}
			n := mustNode(t, g, target)
			if n.Health != depgraph.Unknown || n.Unknown != depgraph.UnknownPolicy {
				t.Errorf("target = %s/%s, want Unknown/policy", n.Health, n.Unknown)
			}
			if e := edgeOf(t, g, hpaRef, target, depgraph.EdgeScales); e.State != depgraph.EdgeUnknown || e.Reason != string(depgraph.UnknownPolicy) {
				t.Errorf("scales edge = %+v, want unknown/policy", e)
			}
			for _, call := range f.CallLog() {
				if strings.Contains(call, tc.kind) && !strings.Contains(call, "Deployment app/web") {
					t.Errorf("the unsupported target was read: %q", call)
				}
			}
			if g.Truncated() {
				t.Errorf("graph truncated: %v", g.Warnings())
			}
		})
	}
}

// Отказ RBAC на цель — Unknown/forbidden (не Missing) с предупреждением; ребро unknown.
func TestHPATargetForbiddenIsUnknownNotMissing(t *testing.T) {
	f := world(t, hpaYAML("apps/v1", "Deployment", "web", ""))
	f.Forbid(hpaGVKDeploy)
	g := build(t, f, hpaRef, depgraph.Options{})
	n := mustNode(t, g, hpaDeploy)
	if n.Health != depgraph.Unknown || n.Unknown != depgraph.UnknownForbidden || n.Placeholder {
		t.Errorf("target = %+v, want Unknown/forbidden", n)
	}
	if e := edgeOf(t, g, hpaRef, hpaDeploy, depgraph.EdgeScales); e.State != depgraph.EdgeUnknown {
		t.Errorf("scales edge = %+v, want unknown", e)
	}
	if !slices.Contains(g.Warnings(), "forbidden:Deployment") {
		t.Errorf("warnings = %v", g.Warnings())
	}
}

// Нездоровая цель делает ребро broken с причиной цели.
func TestHPAEdgeIsBrokenWhenTargetDegraded(t *testing.T) {
	f := world(t, hpaYAML("apps/v1", "Deployment", "sick", ""), `
apiVersion: apps/v1
kind: Deployment
metadata: {name: sick, namespace: app, uid: d9, generation: 1}
spec: {replicas: 3, selector: {matchLabels: {app: sick}}}
status: {observedGeneration: 1, updatedReplicas: 3, availableReplicas: 0, readyReplicas: 0, replicas: 3}
`)
	g := build(t, f, hpaRef, depgraph.Options{})
	sick := depgraph.Ref{Group: "apps", Kind: "Deployment", Namespace: "app", Name: "sick"}
	if n := mustNode(t, g, sick); n.Health != depgraph.Degraded {
		t.Fatalf("target health = %s, want Degraded", n.Health)
	}
	if e := edgeOf(t, g, hpaRef, sick, depgraph.EdgeScales); e.State != depgraph.EdgeBroken || e.Reason == "" {
		t.Errorf("scales edge = %+v, want broken with the target's reason", e)
	}
}

// HPA без цели в spec (пустая ссылка) не даёт рёбер и не падает.
func TestHPAWithoutScaleTargetHasNoEdges(t *testing.T) {
	f := world(t, `
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: web, namespace: app}
spec: {minReplicas: 1, maxReplicas: 2}
`)
	g := build(t, f, hpaRef, depgraph.Options{})
	if edges := g.EdgesFrom(hpaRef, depgraph.EdgeScales); len(edges) != 0 {
		t.Errorf("edges = %+v, want none", edges)
	}
}

// Сам HPA недоступен — граф есть, корень Unknown, целью HPA быть может; не Missing без NotFound.
func TestHPASeedForbidden(t *testing.T) {
	f := world(t, hpaYAML("apps/v1", "Deployment", "web", ""))
	f.Forbid(hpaGVKHPA)
	g := build(t, f, hpaRef, depgraph.Options{})
	if n := mustNode(t, g, hpaRef); n.Health != depgraph.Unknown || n.Unknown != depgraph.UnknownForbidden {
		t.Errorf("seed = %+v", n)
	}
	if hasNode(g, hpaDeploy) {
		t.Error("the target must not be read when the HPA itself is unreadable")
	}
}

// HPA, которого нет, — заглушка Missing (NotFound), а не ошибка Build.
func TestHPASeedNotFound(t *testing.T) {
	g := build(t, world(t), hpaRef, depgraph.Options{})
	if n := mustNode(t, g, hpaRef); n.Health != depgraph.Missing || !n.Placeholder {
		t.Errorf("seed = %+v, want a Missing placeholder", n)
	}
}

// Вид зарегистрирован: цель без имени или с неполным namespace — программная ошибка, как у других видов.
func TestHPAIncompleteSeedIsRejected(t *testing.T) {
	f := world(t)
	for _, seed := range []depgraph.Ref{
		{Kind: "HorizontalPodAutoscaler", Namespace: "app"},
		{Kind: "HorizontalPodAutoscaler", Name: "web"},
	} {
		if _, err := depgraph.Build(t.Context(), f, seed, depgraph.Options{}); !errors.Is(err, depgraph.ErrUnsupportedSeed) {
			t.Errorf("seed %+v: err = %v, want ErrUnsupportedSeed", seed, err)
		}
	}
}

// Пределы графа соблюдаются: при MaxNodes=1 цель не помещается, граф помечен усечённым, ребра нет.
func TestHPARespectsNodeLimit(t *testing.T) {
	f := world(t, hpaYAML("apps/v1", "Deployment", "web", ""))
	g := build(t, f, hpaRef, depgraph.Options{Limits: depgraph.Limits{MaxNodes: 1, MaxEdges: 600, MaxDepth: 4, MaxReads: 40}})
	if !g.Truncated() || !slices.Contains(g.Warnings(), "limit.nodes") {
		t.Errorf("truncated = %v, warnings = %v", g.Truncated(), g.Warnings())
	}
	if len(g.Nodes()) > 1 {
		t.Errorf("nodes = %v, want at most 1", keysOf(g))
	}
}

// Для HPA читаются только он сам и цель (затем стандартные раскрытия цели), без выборок HPA и лишних видов.
func TestHPAReadsOnlyItselfAndItsTarget(t *testing.T) {
	f := world(t, hpaYAML("apps/v1", "Deployment", "web", ""))
	build(t, f, hpaRef, depgraph.Options{})
	log := f.CallLog()
	if len(log) < 2 || log[0] != "GET HorizontalPodAutoscaler app/web" || log[1] != "GET Deployment app/web" {
		t.Fatalf("calls = %v", log)
	}
	for _, c := range log {
		if strings.Contains(c, "LIST HorizontalPodAutoscaler") || strings.Contains(c, "Secret") {
			t.Errorf("unexpected read: %q", c)
		}
	}
}

// Сам по себе граф детерминирован: два построения дают один слепок.
func TestHPAGraphIsDeterministic(t *testing.T) {
	build1 := func() string {
		f := world(t, hpaYAML("apps/v1", "Deployment", "web", hpaCond("ScalingActive", "False", "FailedGetResourceMetric")))
		return jsonOf(build(t, f, hpaRef, depgraph.Options{}).Snapshot())
	}
	if a, b := build1(), build1(); a != b {
		t.Errorf("snapshots differ:\n%s\n%s", a, b)
	}
}

// Слепок не содержит условий и сообщений HPA — только состояние и код причины.
func TestHPASnapshotCarriesNoConditionText(t *testing.T) {
	f := world(t, `
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: web, namespace: app}
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: web}
  maxReplicas: 3
status:
  conditions:
    - {type: ScalingActive, status: "False", reason: FailedGetResourceMetric, message: "SECRET-MESSAGE-MUST-NOT-LEAK"}
`)
	g := build(t, f, hpaRef, depgraph.Options{})
	if s := jsonOf(g.Snapshot()); strings.Contains(s, "SECRET-MESSAGE-MUST-NOT-LEAK") {
		t.Errorf("the snapshot leaks a condition message: %s", s)
	}
}
