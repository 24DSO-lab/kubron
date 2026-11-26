// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

// ADR-0031 §6, этап 3: StatefulSet — цель и вид графа: Pod'ы по ordinal, PVC шаблонов, ревизии из status.

var gvkPodForTest = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}

var refSTS = depgraph.Ref{Group: "apps", Kind: "StatefulSet", Namespace: "app", Name: "db"}

// stsYAML — StatefulSet db: три реплики, один шаблон data; spec и status — по аргументам.
func stsYAML(specExtra, status string) string {
	return fmt.Sprintf(`
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: db, namespace: app, uid: s1}
spec:
  replicas: 3
  serviceName: db
  selector: {matchLabels: {app: db}}
  volumeClaimTemplates: [{metadata: {name: data}}]
%s
status: %s
`, specExtra, status)
}

func stsPod(ordinal int, ready bool) string {
	cond := "False"
	if ready {
		cond = "True"
	}
	return fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: db-%d
  namespace: app
  uid: p%d
  labels: {app: db}
  ownerReferences: [{apiVersion: apps/v1, kind: StatefulSet, name: db, uid: s1, controller: true}]
spec:
  nodeName: n1
  containers: [{name: db, image: example/db}]
  volumes: [{name: data, persistentVolumeClaim: {claimName: data-db-%d}}]
status:
  phase: Running
  conditions: [{type: Ready, status: "%s"}]
`, ordinal, ordinal, ordinal, cond)
}

func stsClaim(ordinal int) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: data-db-%d, namespace: app}
spec: {storageClassName: fast, volumeName: pv-%d}
status: {phase: Bound}
`, ordinal, ordinal)
}

func stsRef(kind string, name string) depgraph.Ref {
	return depgraph.Ref{Kind: kind, Namespace: "app", Name: name}
}

func claimRef(ordinal int) depgraph.Ref {
	return stsRef("PersistentVolumeClaim", fmt.Sprintf("data-db-%d", ordinal))
}

func stsWorld(t *testing.T, objs ...string) *graphtest.FakeReader {
	t.Helper()
	f := graphtest.NewFakeReader()
	f.PutYAML(t, "apiVersion: v1\nkind: Node\nmetadata: {name: n1}\nstatus: {conditions: [{type: Ready, status: \"True\"}]}\n")
	for _, o := range objs {
		f.PutYAML(t, o)
	}
	return f
}

// Вид зарегистрирован: StatefulSet можно задать целью (до регистрации Build отвечал ErrUnsupportedSeed).
func TestStatefulSetIsSeedable(t *testing.T) {
	f := stsWorld(t, stsYAML("", "{replicas: 3, readyReplicas: 3}"))
	g, err := depgraph.Build(t.Context(), f, refSTS, depgraph.Options{})
	if errors.Is(err, depgraph.ErrUnsupportedSeed) {
		t.Fatal("StatefulSet is not seedable")
	}
	if err != nil {
		t.Fatal(err)
	}
	if n := mustNode(t, g, refSTS); n.Health != depgraph.Healthy {
		t.Errorf("sts = %+v", n)
	}
	// Тот же вид без группы в цели (так задают цель старые вызовы).
	if _, err := depgraph.Build(t.Context(), f, depgraph.Ref{Kind: "StatefulSet", Namespace: "app", Name: "db"}, depgraph.Options{}); err != nil {
		t.Errorf("seed without group: %v", err)
	}
}

// Pod'ы связаны owns, PVC шаблонов — owns по имени <шаблон>-<sts>-<ordinal>; PVC ordinal'а без Pod'а в граф не входит.
func TestStatefulSetOwnsPodsAndClaims(t *testing.T) {
	f := stsWorld(t, stsYAML("", "{replicas: 3, readyReplicas: 1}"),
		stsPod(0, true), stsPod(1, false), stsClaim(0), stsClaim(1), stsClaim(2))
	g := build(t, f, refSTS, depgraph.Options{})

	for o := range 2 {
		edgeOf(t, g, refSTS, stsRef("Pod", fmt.Sprintf("db-%d", o)), depgraph.EdgeOwns)
		if e := edgeOf(t, g, refSTS, claimRef(o), depgraph.EdgeOwns); e.State != depgraph.EdgeOK {
			t.Errorf("claim %d edge = %+v", o, e)
		}
		// PVC связан и с Pod'ом, который его монтирует: это тот же узел, а не копия.
		edgeOf(t, g, stsRef("Pod", fmt.Sprintf("db-%d", o)), claimRef(o), depgraph.EdgeMounts)
	}
	// Для ordinal'а 2 Pod'а ещё нет: его PVC существует, а в граф попадает как часть шаблона при текущем числе реплик.
	edgeOf(t, g, refSTS, claimRef(2), depgraph.EdgeOwns)
	if n := mustNode(t, g, refSTS); n.Health != depgraph.Degraded || n.Reason != depgraph.ReasonReplicasUnavailable {
		t.Errorf("sts health = %+v", n)
	}
	if n := mustNode(t, g, stsRef("Pod", "db-1")); n.Health != depgraph.Degraded || n.Reason != depgraph.ReasonNotReady {
		t.Errorf("db-1 = %+v", n)
	}
}

// Отсутствующий PVC — заглушка Missing, но только там, где Pod уже есть: у ещё не созданного Pod'а PVC может
// законно не существовать.
func TestStatefulSetMissingClaimOnlyForExistingPod(t *testing.T) {
	f := stsWorld(t, stsYAML("", "{replicas: 3, readyReplicas: 1}"), stsPod(0, true), stsPod(1, false), stsClaim(0))
	g := build(t, f, refSTS, depgraph.Options{})

	miss := mustNode(t, g, claimRef(1))
	if !miss.Placeholder || miss.Health != depgraph.Missing {
		t.Fatalf("claim of an existing Pod = %+v, want Missing placeholder", miss)
	}
	edgeOf(t, g, refSTS, claimRef(1), depgraph.EdgeOwns)
	if hasNode(g, claimRef(2)) {
		t.Error("a Missing claim must not be invented for an ordinal without a Pod")
	}
	// Pod видит тот же PVC отсутствующим: ребро mounts — missing.
	if e := edgeOf(t, g, stsRef("Pod", "db-1"), claimRef(1), depgraph.EdgeMounts); e.State != depgraph.EdgeMissing {
		t.Errorf("pod edge = %+v", e)
	}
}

// Усечённый список PVC ничего не доказывает: «нет» не объявляется, граф помечен.
func TestStatefulSetTruncatedClaimListProvesNothing(t *testing.T) {
	objs := []string{stsYAML("", "{replicas: 2, readyReplicas: 1}"), stsPod(0, true), stsPod(1, false), stsClaim(0), stsClaim(1)}
	for i := range 101 { // имена a-… раньше data-db-…: список обрезается до нужных PVC
		objs = append(objs, fmt.Sprintf("apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: a-%03d, namespace: app}\nstatus: {phase: Bound}\n", i))
	}
	g := build(t, stsWorld(t, objs...), refSTS, depgraph.Options{})
	if n, ok := g.Node(claimRef(0)); ok && n.Placeholder {
		t.Errorf("claim declared missing from a truncated list: %+v", n)
	}
	if !g.Truncated() || !slices.Contains(g.Warnings(), "list_truncated:PersistentVolumeClaim") {
		t.Errorf("truncated=%v warnings=%v", g.Truncated(), g.Warnings())
	}
}

// Число PVC шаблонов ограничено: большой StatefulSet не раздувает граф, но говорит об этом.
func TestStatefulSetClaimsAreCapped(t *testing.T) {
	f := stsWorld(t, strings.Replace(stsYAML("", "{replicas: 40, readyReplicas: 40}"), "replicas: 3", "replicas: 40", 1))
	g := build(t, f, refSTS, depgraph.Options{})
	if !g.Truncated() || !slices.Contains(g.Warnings(), "limit.nodes") {
		t.Errorf("truncated=%v warnings=%v", g.Truncated(), g.Warnings())
	}
}

// Здоровье по ревизиям: идущее обновление — Progressing; остановленное partition и OnDelete — намеренное расхождение.
func TestStatefulSetHealthFollowsRevisions(t *testing.T) {
	updating := "{replicas: 3, readyReplicas: 3, currentRevision: db-1, updateRevision: db-2, updatedReplicas: 1}"
	for _, tc := range []struct {
		name, spec, status string
		want               depgraph.Health
		reason             string
	}{
		{"same revision", "", "{replicas: 3, readyReplicas: 3, currentRevision: db-1, updateRevision: db-1, updatedReplicas: 3}", depgraph.Healthy, ""},
		{"rolling update", "", updating, depgraph.Progressing, depgraph.ReasonRolloutInProgress},
		{"all updated", "", "{replicas: 3, readyReplicas: 3, currentRevision: db-1, updateRevision: db-2, updatedReplicas: 3}", depgraph.Healthy, ""},
		{"partitioned", "  updateStrategy: {type: RollingUpdate, rollingUpdate: {partition: 2}}", updating, depgraph.Healthy, ""},
		{"on delete", "  updateStrategy: {type: OnDelete}", updating, depgraph.Healthy, ""},
		{"no revisions yet", "", "{replicas: 3, readyReplicas: 3}", depgraph.Healthy, ""},
		{"not ready stays degraded", "", "{replicas: 3, readyReplicas: 1, currentRevision: db-1, updateRevision: db-2, updatedReplicas: 1}", depgraph.Degraded, depgraph.ReasonReplicasUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := build(t, stsWorld(t, stsYAML(tc.spec, tc.status)), refSTS, depgraph.Options{})
			if n := mustNode(t, g, refSTS); n.Health != tc.want || n.Reason != tc.reason {
				t.Errorf("health = %s/%s, want %s/%s", n.Health, n.Reason, tc.want, tc.reason)
			}
		})
	}
}

// Как владелец Pod'а StatefulSet попадает в граф вершиной, но соседей не перечисляет: этим занимается цель-StatefulSet.
func TestStatefulSetAsOwnerDoesNotListSiblings(t *testing.T) {
	f := graphtest.NewFakeReader() // без Node: список Pod'ов узла (штатное расширение Node) соседей бы добавил
	for _, o := range []string{stsYAML("", "{replicas: 3, readyReplicas: 3}"), stsPod(0, true), stsPod(1, true), stsClaim(0)} {
		f.PutYAML(t, o)
	}
	g := build(t, f, stsRef("Pod", "db-0"), depgraph.Options{})
	edgeOf(t, g, refSTS, stsRef("Pod", "db-0"), depgraph.EdgeOwns)
	if hasNode(g, stsRef("Pod", "db-1")) {
		t.Error("a sibling Pod was pulled into the graph")
	}
	for _, c := range f.CallLog() {
		if strings.HasPrefix(c, "LIST Pod") && strings.Contains(c, "label=") {
			t.Errorf("Pods listed for an owner-only expansion: %s", c)
		}
	}
}

// Pod, чьё имя не <sts>-<ordinal>, не считается Pod'ом StatefulSet, даже если метки и владелец совпали.
func TestStatefulSetIgnoresPodsWithoutOrdinal(t *testing.T) {
	odd := strings.Replace(stsPod(0, true), "name: db-0", "name: db-backup", 1)
	g := build(t, stsWorld(t, stsYAML("", "{replicas: 1, readyReplicas: 1}"), odd), refSTS, depgraph.Options{})
	if hasNode(g, stsRef("Pod", "db-backup")) {
		t.Error("Pod without an ordinal was attached to the StatefulSet")
	}
}

// Раскрываются дальше неготовые Pod'ы, а не первые по имени: иначе проблемный db-7 остался бы без Node и Service.
func TestStatefulSetExpandsUnreadyPodsFirst(t *testing.T) {
	objs := []string{stsYAML("", "{replicas: 8, readyReplicas: 7}")}
	for o := range 8 {
		objs = append(objs, stsPod(o, o != 7), stsClaim(o))
	}
	g := build(t, stsWorld(t, objs...), refSTS, depgraph.Options{})
	// Раскрытый Pod получает ребро mounts к своему PVC (его добавляет расширение Pod'а, а не StatefulSet'а).
	edgeOf(t, g, stsRef("Pod", "db-7"), claimRef(7), depgraph.EdgeMounts) // неготовый раскрыт
	if es := g.EdgesFrom(stsRef("Pod", "db-6"), depgraph.EdgeMounts); len(es) != 0 {
		t.Errorf("db-6 was expanded beyond the Pod budget: %+v", es)
	}
	if es := g.EdgesFrom(stsRef("Pod", "db-0"), depgraph.EdgeMounts); len(es) == 0 {
		t.Error("db-0 must be expanded: healthy Pods go by ordinal")
	}
}

// Отказ чтения Pod'ов — «не знаем»: граф остаётся с самим StatefulSet и предупреждением.
func TestStatefulSetPodsForbidden(t *testing.T) {
	f := stsWorld(t, stsYAML("", "{replicas: 3, readyReplicas: 0}"), stsPod(0, false))
	f.Forbid(gvkPodForTest)
	g := build(t, f, refSTS, depgraph.Options{})
	if !hasNode(g, refSTS) || hasNode(g, stsRef("Pod", "db-0")) {
		t.Errorf("nodes = %v", keysOf(g))
	}
	if !slices.Contains(g.Warnings(), "forbidden:Pod") {
		t.Errorf("warnings = %v", g.Warnings())
	}
}

// spec.ordinals.start: ordinal'ы реплик — [start, start+replicas). Единый расчёт для PVC графа и правил diagnose.
func TestStatefulSetOrdinalRange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		start     *int32
		replicas  *int32
		wantStart int
		wantEnd   int
		in, out   []int
		created   map[int]int // последний созданный ordinal → сколько создано
	}{
		{name: "start 0, replicas 3", start: ptr32(0), replicas: ptr32(3), wantStart: 0, wantEnd: 3,
			in: []int{0, 1, 2}, out: []int{-1, 3, 5}, created: map[int]int{-1: 0, 0: 1, 1: 2, 2: 3, 9: 3}},
		{name: "start 5, replicas 3", start: ptr32(5), replicas: ptr32(3), wantStart: 5, wantEnd: 8,
			in: []int{5, 6, 7}, out: []int{0, 4, 8}, created: map[int]int{4: 0, 5: 1, 6: 2, 7: 3, 12: 3}},
		{name: "no ordinals, default replicas", wantStart: 0, wantEnd: 1, in: []int{0}, out: []int{1}},
		{name: "replicas 0", start: ptr32(5), replicas: ptr32(0), wantStart: 5, wantEnd: 5, out: []int{4, 5}},
		{name: "negative values are clamped", start: ptr32(-2), replicas: ptr32(-1), wantStart: 0, wantEnd: 0, out: []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sts := &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Replicas: tc.replicas}}
			if tc.start != nil {
				sts.Spec.Ordinals = &appsv1.StatefulSetOrdinals{Start: *tc.start}
			}
			r := depgraph.StatefulSetOrdinals(sts)
			if r.Start != tc.wantStart || r.End() != tc.wantEnd {
				t.Fatalf("range = [%d, %d), want [%d, %d)", r.Start, r.End(), tc.wantStart, tc.wantEnd)
			}
			for _, o := range tc.in {
				if !r.Contains(o) {
					t.Errorf("ordinal %d must be inside %+v", o, r)
				}
			}
			for _, o := range tc.out {
				if r.Contains(o) {
					t.Errorf("ordinal %d must be outside %+v", o, r)
				}
			}
			for last, want := range tc.created {
				if got := r.Created(last); got != want {
					t.Errorf("Created(%d) = %d, want %d", last, got, want)
				}
			}
		})
	}
}

func ptr32(v int32) *int32 { return &v }

func TestPodOrdinal(t *testing.T) {
	for _, tc := range []struct {
		pod  string
		want int
		ok   bool
	}{{"db-0", 0, true}, {"db-5", 5, true}, {"db-12", 12, true}, {"db-05", 0, false}, {"db--1", 0, false},
		{"db-x", 0, false}, {"web-1", 0, false}, {"db-", 0, false}, {"db", 0, false}} {
		if got, ok := depgraph.PodOrdinal(tc.pod, "db"); got != tc.want || ok != tc.ok {
			t.Errorf("PodOrdinal(%q) = %d, %v; want %d, %v", tc.pod, got, ok, tc.want, tc.ok)
		}
	}
}

const startFive = "  ordinals: {start: 5}"

// start=5, replicas=3: Pod'ы db-5..db-7, PVC data-db-5..data-db-7. PVC с ordinal'ами от нуля (они остались от прежних
// реплик) в граф не входят: контроллер их для этого диапазона не ждёт.
func TestStatefulSetClaimsFollowOrdinalsStart(t *testing.T) {
	f := stsWorld(t, stsYAML(startFive, "{replicas: 3, readyReplicas: 3}"),
		stsPod(5, true), stsPod(6, true), stsPod(7, true),
		stsClaim(5), stsClaim(6), stsClaim(7), stsClaim(0), stsClaim(1), stsClaim(2))
	g := build(t, f, refSTS, depgraph.Options{})
	for o := 5; o <= 7; o++ {
		edgeOf(t, g, refSTS, stsRef("Pod", fmt.Sprintf("db-%d", o)), depgraph.EdgeOwns)
		if e := edgeOf(t, g, refSTS, claimRef(o), depgraph.EdgeOwns); e.State != depgraph.EdgeOK {
			t.Errorf("claim data-db-%d edge = %+v", o, e)
		}
	}
	for o := range 3 {
		if hasNode(g, claimRef(o)) {
			t.Errorf("claim data-db-%d is outside [5, 8) and must not be in the graph", o)
		}
	}
}

// Без ordinals.start имена PVC считаются от нуля, как и раньше (start=0 неотличим от отсутствия поля).
func TestStatefulSetClaimsStartAtZeroWithoutOrdinals(t *testing.T) {
	for name, extra := range map[string]string{"absent": "", "explicit zero": "  ordinals: {start: 0}"} {
		t.Run(name, func(t *testing.T) {
			f := stsWorld(t, stsYAML(extra, "{replicas: 3, readyReplicas: 1}"), stsPod(0, true), stsClaim(0), stsClaim(1), stsClaim(2), stsClaim(5))
			g := build(t, f, refSTS, depgraph.Options{})
			for o := range 3 {
				edgeOf(t, g, refSTS, claimRef(o), depgraph.EdgeOwns)
			}
			if hasNode(g, claimRef(5)) {
				t.Error("data-db-5 is outside [0, 3)")
			}
		})
	}
}

// Отсутствующий PVC у существующего Pod'а db-5 — Missing; у ещё не созданных db-6 и db-7 PVC не требуется.
func TestStatefulSetMissingClaimWithOrdinalsStart(t *testing.T) {
	f := stsWorld(t, stsYAML(startFive, "{replicas: 3, readyReplicas: 0}"), stsPod(5, false))
	g := build(t, f, refSTS, depgraph.Options{})
	miss := mustNode(t, g, claimRef(5))
	if !miss.Placeholder || miss.Health != depgraph.Missing {
		t.Fatalf("claim of the existing Pod db-5 = %+v, want Missing placeholder", miss)
	}
	edgeOf(t, g, refSTS, claimRef(5), depgraph.EdgeOwns)
	for _, o := range []int{0, 6, 7} {
		if hasNode(g, claimRef(o)) {
			t.Errorf("claim data-db-%d: its Pod does not exist, a Missing claim must not be invented", o)
		}
	}
}

// Pod за пределами диапазона (остался после сдвига start) в граф попадает как есть, а его PVC — по его собственному
// ordinal'у: отсутствующий PVC существующего Pod'а остаётся Missing независимо от диапазона.
func TestStatefulSetPodOutsideRangeKeepsItsClaim(t *testing.T) {
	f := stsWorld(t, stsYAML(startFive, "{replicas: 3, readyReplicas: 3}"),
		stsPod(2, true), stsPod(5, true), stsClaim(5), stsClaim(2))
	g := build(t, f, refSTS, depgraph.Options{})
	edgeOf(t, g, refSTS, stsRef("Pod", "db-2"), depgraph.EdgeOwns)
	if e := edgeOf(t, g, refSTS, claimRef(2), depgraph.EdgeOwns); e.State != depgraph.EdgeOK {
		t.Errorf("claim of the leftover Pod = %+v", e)
	}
	edgeOf(t, g, refSTS, claimRef(5), depgraph.EdgeOwns)
}
