// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

// Job и CronJob (ADR-0031 §6, этап 3): Job → Pod'ы, CronJob → Job'ы → Pod'ы последнего неудавшегося.

var (
	refJob  = depgraph.Ref{Group: "batch", Kind: "Job", Namespace: "app", Name: "report"}
	refCron = depgraph.Ref{Group: "batch", Kind: "CronJob", Namespace: "app", Name: "nightly"}
)

// jobYAML — Job с селектором, как у настоящего контроллера. owner — имя CronJob-владельца (пусто — без владельца),
// cond — тип условия со статусом True (пусто — Job выполняется), ts — время создания (RFC 3339).
func jobYAML(name, owner, cond, ts string, extraSpec string) string {
	meta := fmt.Sprintf("name: %s, namespace: app, uid: j-%s, creationTimestamp: %q", name, name, ts)
	if owner != "" {
		meta += fmt.Sprintf(", ownerReferences: [{apiVersion: batch/v1, kind: CronJob, name: %s, uid: c-%s, controller: true}]", owner, owner)
	}
	status := "{}"
	if cond != "" {
		status = fmt.Sprintf("{conditions: [{type: %s, status: \"True\"}]}", cond)
	}
	return fmt.Sprintf(`
apiVersion: batch/v1
kind: Job
metadata: {%s}
spec: {selector: {matchLabels: {controller-uid: j-%s}}%s}
status: %s
`, meta, name, extraSpec, status)
}

func jobPodYAML(job, name, ts string) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: app
  uid: p-%s
  creationTimestamp: %q
  labels: {controller-uid: j-%s, batch.kubernetes.io/job-name: %s}
  ownerReferences: [{apiVersion: batch/v1, kind: Job, name: %s, uid: j-%s, controller: true}]
spec: {containers: [{name: main, image: example/job}]}
status: {phase: Failed}
`, name, name, ts, job, job, job, job)
}

const cronYAML = `
apiVersion: batch/v1
kind: CronJob
metadata: {name: nightly, namespace: app, uid: c-nightly}
spec: {schedule: "0 3 * * *", jobTemplate: {spec: {}}}
`

func podRefOf(name string) depgraph.Ref {
	return depgraph.Ref{Kind: "Pod", Namespace: "app", Name: name}
}

func TestJobIsSeedableAndOwnsItsPods(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, jobYAML("report", "", "Failed", "2026-11-11T09:00:00Z", ""))
	f.PutYAML(t, jobPodYAML("report", "report-a", "2026-11-11T09:01:00Z"))
	f.PutYAML(t, jobPodYAML("report", "report-b", "2026-11-11T09:02:00Z"))
	// Чужой Pod с теми же метками, но другим владельцем, в граф не попадает.
	f.PutYAML(t, strings.Replace(jobPodYAML("report", "stranger", "2026-11-11T09:03:00Z"), "name: report, uid: j-report", "name: other, uid: j-other", 1))
	g := build(t, f, refJob, depgraph.Options{})
	if n := mustNode(t, g, refJob); n.Health != depgraph.Degraded || n.Reason != depgraph.ReasonJobFailed {
		t.Fatalf("job = %s/%s, want Degraded/job_failed", n.Health, n.Reason)
	}
	for _, name := range []string{"report-a", "report-b"} {
		edgeOf(t, g, refJob, podRefOf(name), depgraph.EdgeOwns)
	}
	if hasNode(g, podRefOf("stranger")) {
		t.Errorf("a Pod owned by another Job is in the graph: %v", keysOf(g))
	}
	if g.Truncated() {
		t.Errorf("graph is truncated: %v", g.Warnings())
	}
}

func TestJobWithoutSelectorFallsBackToTheJobNameLabel(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, `
apiVersion: batch/v1
kind: Job
metadata: {name: report, namespace: app}
spec: {}
`)
	f.PutYAML(t, jobPodYAML("report", "report-a", "2026-11-11T09:01:00Z"))
	g := build(t, f, refJob, depgraph.Options{})
	edgeOf(t, g, refJob, podRefOf("report-a"), depgraph.EdgeOwns)
	if want := "batch.kubernetes.io/job-name=report"; !slices.ContainsFunc(f.CallLog(), func(c string) bool { return strings.Contains(c, want) }) {
		t.Errorf("no Pod list by %q: %v", want, f.CallLog())
	}
}

// Job выше предела Pod'ов: в граф идут самые новые, граф помечен усечённым с кодом предела.
func TestJobKeepsOnlyTheNewestPods(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, jobYAML("report", "", "Failed", "2026-11-11T09:00:00Z", ""))
	const total = 13
	for i := 1; i <= total; i++ {
		f.PutYAML(t, jobPodYAML("report", fmt.Sprintf("report-%02d", i), fmt.Sprintf("2026-11-11T09:%02d:00Z", i)))
	}
	g := build(t, f, refJob, depgraph.Options{})
	var pods []string
	for _, n := range g.Nodes() {
		if n.Kind == "Pod" {
			pods = append(pods, n.Name)
		}
	}
	if len(pods) != 10 {
		t.Fatalf("pods in the graph = %d (%v), want 10", len(pods), pods)
	}
	if hasNode(g, podRefOf("report-01")) || !hasNode(g, podRefOf("report-13")) {
		t.Errorf("the graph must keep the newest Pods: %v", pods)
	}
	if !g.Truncated() || !slices.Contains(g.Warnings(), "limit.job_pods") {
		t.Errorf("truncated=%v warnings=%v, want the limit.job_pods warning", g.Truncated(), g.Warnings())
	}
}

// spec.suspend без условия Suspended — всё равно приостановленный Job: факт, не проблема.
func TestJobSuspendedBySpecIsSuspended(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, jobYAML("report", "", "", "2026-11-11T09:00:00Z", ", suspend: true"))
	g := build(t, f, refJob, depgraph.Options{})
	if n := mustNode(t, g, refJob); n.Health != depgraph.Suspended || n.Reason != depgraph.ReasonSuspended {
		t.Errorf("job = %s/%s, want Suspended/suspended", n.Health, n.Reason)
	}
}

// Отказ чтения Pod'ов — не «Pod'ов нет»: граф записывает предупреждение, а вершин Pod нет.
func TestJobPodsForbiddenIsAWarningNotAnAbsence(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, jobYAML("report", "", "Failed", "2026-11-11T09:00:00Z", ""))
	f.PutYAML(t, jobPodYAML("report", "report-a", "2026-11-11T09:01:00Z"))
	f.Forbid(gvkOf("kind: Pod"))
	g := build(t, f, refJob, depgraph.Options{})
	if !slices.Contains(g.Warnings(), "forbidden:Pod") {
		t.Errorf("warnings = %v, want forbidden:Pod", g.Warnings())
	}
	if n := mustNode(t, g, refJob); n.Health != depgraph.Degraded {
		t.Errorf("job = %s, want Degraded", n.Health)
	}
}

// От Pod'а граф поднимается к Job и CronJob, но соседей Job не раскрывает.
func TestJobSiblingsAreNotExpandedFromAPod(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, cronYAML)
	f.PutYAML(t, jobYAML("report", "nightly", "Failed", "2026-11-11T09:00:00Z", ""))
	f.PutYAML(t, jobPodYAML("report", "report-a", "2026-11-11T09:01:00Z"))
	f.PutYAML(t, jobPodYAML("report", "report-b", "2026-11-11T09:02:00Z"))
	g := build(t, f, podRefOf("report-a"), depgraph.Options{})
	mustNode(t, g, refJob)
	mustNode(t, g, refCron)
	if hasNode(g, podRefOf("report-b")) {
		t.Errorf("a sibling Pod was expanded from a Pod seed: %v", keysOf(g))
	}
}

func TestCronJobOwnsItsJobsAndExpandsTheNewestFailedOne(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, cronYAML)
	f.PutYAML(t, jobYAML("nightly-1", "nightly", "Complete", "2026-11-09T03:00:00Z", ""))
	f.PutYAML(t, jobYAML("nightly-2", "nightly", "Failed", "2026-11-10T03:00:00Z", ""))
	f.PutYAML(t, jobYAML("nightly-3", "nightly", "Failed", "2026-11-11T03:00:00Z", ""))
	f.PutYAML(t, jobYAML("nightly-4", "nightly", "", "2026-11-12T03:00:00Z", ""))
	f.PutYAML(t, jobYAML("foreign-1", "someone-else", "Failed", "2026-11-12T04:00:00Z", ""))
	f.PutYAML(t, jobPodYAML("nightly-2", "nightly-2-x", "2026-11-10T03:01:00Z"))
	f.PutYAML(t, jobPodYAML("nightly-3", "nightly-3-x", "2026-11-11T03:01:00Z"))
	g := build(t, f, refCron, depgraph.Options{})
	for _, name := range []string{"nightly-1", "nightly-2", "nightly-3", "nightly-4"} {
		edgeOf(t, g, refCron, depgraph.Ref{Group: "batch", Kind: "Job", Namespace: "app", Name: name}, depgraph.EdgeOwns)
	}
	if hasNode(g, depgraph.Ref{Group: "batch", Kind: "Job", Namespace: "app", Name: "foreign-1"}) {
		t.Errorf("a Job of another owner is in the graph: %v", keysOf(g))
	}
	// Вниз раскрыт только последний неудавшийся Job.
	if !hasNode(g, podRefOf("nightly-3-x")) {
		t.Errorf("the newest failed Job was not expanded to its Pods: %v", keysOf(g))
	}
	if hasNode(g, podRefOf("nightly-2-x")) {
		t.Errorf("an older failed Job was expanded: %v", keysOf(g))
	}
}

func TestCronJobWithoutJobsHasNoChildren(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, cronYAML)
	g := build(t, f, refCron, depgraph.Options{})
	if len(g.EdgesFrom(refCron, depgraph.EdgeOwns)) != 0 || g.Truncated() {
		t.Errorf("edges=%+v truncated=%v", g.Edges(), g.Truncated())
	}
}

// История Job'ов ограничена: в граф идут самые новые, граф помечен усечённым с кодом предела.
func TestCronJobKeepsOnlyTheNewestJobs(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, cronYAML)
	const total = 14
	for i := 1; i <= total; i++ {
		f.PutYAML(t, jobYAML(fmt.Sprintf("nightly-%02d", i), "nightly", "Complete", fmt.Sprintf("2026-11-%02dT03:00:00Z", i), ""))
	}
	g := build(t, f, refCron, depgraph.Options{})
	jobs := 0
	for _, n := range g.Nodes() {
		if n.Kind == "Job" {
			jobs++
		}
	}
	if jobs != 10 {
		t.Fatalf("jobs in the graph = %d, want 10", jobs)
	}
	if hasNode(g, depgraph.Ref{Group: "batch", Kind: "Job", Namespace: "app", Name: "nightly-01"}) {
		t.Error("the oldest Job must not be kept")
	}
	if !g.Truncated() || !slices.Contains(g.Warnings(), "limit.cronjob_jobs") {
		t.Errorf("truncated=%v warnings=%v, want the limit.cronjob_jobs warning", g.Truncated(), g.Warnings())
	}
}

func TestCronJobSuspendedByTheSpec(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, strings.Replace(cronYAML, `schedule: "0 3 * * *"`, `schedule: "0 3 * * *", suspend: true`, 1))
	g := build(t, f, refCron, depgraph.Options{})
	if n := mustNode(t, g, refCron); n.Health != depgraph.Suspended {
		t.Errorf("cronjob = %s, want Suspended", n.Health)
	}
}

func TestCronJobJobsForbiddenIsAWarning(t *testing.T) {
	f := graphtest.NewFakeReader()
	f.PutYAML(t, cronYAML)
	f.Forbid(gvkOf("kind: Job"))
	g := build(t, f, refCron, depgraph.Options{})
	if !slices.Contains(g.Warnings(), "forbidden:Job") {
		t.Errorf("warnings = %v, want forbidden:Job", g.Warnings())
	}
}

// Один и тот же мир даёт один и тот же снимок.
func TestBatchGraphIsDeterministic(t *testing.T) {
	mk := func() string {
		f := graphtest.NewFakeReader()
		f.PutYAML(t, cronYAML)
		f.PutYAML(t, jobYAML("nightly-1", "nightly", "Failed", "2026-11-10T03:00:00Z", ""))
		f.PutYAML(t, jobYAML("nightly-2", "nightly", "Failed", "2026-11-10T03:00:00Z", ""))
		f.PutYAML(t, jobPodYAML("nightly-1", "nightly-1-x", "2026-11-10T03:01:00Z"))
		return jsonOf(build(t, f, refCron, depgraph.Options{}).Snapshot())
	}
	if a, b := mk(), mk(); a != b {
		t.Errorf("snapshots differ:\n%s\n%s", a, b)
	}
}
