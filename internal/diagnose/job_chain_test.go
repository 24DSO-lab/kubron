// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Тесты Job и CronJob (ADR-0031 §6, этап 3), общие помощники для job_chain_test.go и cronjob_chain_test.go.

// batchReport собирает контекст цели из манифестов и анализирует его на fixedNow.
func batchReport(t *testing.T, kind, name string, forbid []string, manifests ...string) Report {
	t.Helper()
	r := newChain(t, manifests...)
	for _, k := range forbid {
		gvk, ok := chainForbidGVK(k)
		if !ok {
			t.Fatalf("unknown kind %q", k)
		}
		r.Forbid(gvk)
	}
	c, err := Collect(t.Context(), r, ResourceRef{Kind: kind, Namespace: "app", Name: name}, chainOpts())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return Analyze(c)
}

func batchHasHypothesis(rep Report, code string, conf Confidence) bool {
	return slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool {
		return h.Code == code && (conf == "" || h.Confidence == conf)
	})
}

func batchHasMissing(rep Report, code string) bool {
	return slices.ContainsFunc(rep.Missing, func(m MissingContext) bool { return m.Code == code })
}

func jobManifest(cond, reason, extra string) string {
	status := "{}"
	if cond != "" {
		status = "{conditions: [{type: " + cond + ", status: \"True\", reason: " + reason + "}]}"
	}
	return `
apiVersion: batch/v1
kind: Job
metadata: {name: report, namespace: app, uid: j1, creationTimestamp: "2026-11-11T09:00:00Z"}
spec: {selector: {matchLabels: {controller-uid: j1}}` + extra + `}
status: ` + status + "\n"
}

const jobPodManifest = `
apiVersion: v1
kind: Pod
metadata:
  name: report-a
  namespace: app
  uid: p1
  creationTimestamp: "2026-11-11T09:01:00Z"
  labels: {controller-uid: j1}
  ownerReferences: [{apiVersion: batch/v1, kind: Job, name: report, uid: j1, controller: true}]
spec: {restartPolicy: Never, containers: [{name: main, image: example/report}]}
status:
  phase: Pending
  conditions: [{type: PodScheduled, status: "False", reason: Unschedulable, message: "0/3 nodes are available: 3 Insufficient cpu."}]
`

// Pod, который не смог запуститься, объясняет превышение срока, но не исчерпание попыток.
func TestJobPendingPodExplainsDeadlineButNotBackoff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason, hyp string
		explained         bool
	}{
		{"deadline", "DeadlineExceeded", "job.deadline_exceeded", true},
		{"backoff", "BackoffLimitExceeded", "job.backoff_limit_exceeded", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep := batchReport(t, "Job", "report", nil, jobManifest("Failed", tc.reason, ""), jobPodManifest)
			if !batchHasHypothesis(rep, tc.hyp, ConfidenceHigh) {
				t.Fatalf("no %s hypothesis: %+v", tc.hyp, rep.Hypotheses)
			}
			explained := slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool {
				return h.Code == "pending.insufficient_resources" && len(h.Explains) > 0
			})
			if explained != tc.explained {
				t.Errorf("pending Pod explains the Job = %v, want %v", explained, tc.explained)
			}
		})
	}
}

// Причина неизвестна: факт и пробел, но не гипотеза.
func TestJobUnknownFailureReasonIsAGapNotAHypothesis(t *testing.T) {
	t.Parallel()
	rep := batchReport(t, "Job", "report", nil, jobManifest("Failed", "PodFailurePolicy", ""))
	if !batchHasMissing(rep, "job.failure_reason_unrecognized") {
		t.Errorf("missing = %+v, want job.failure_reason_unrecognized", rep.Missing)
	}
	if slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool { return strings.HasPrefix(h.Code, "job.") }) {
		t.Errorf("unexpected job hypothesis: %+v", rep.Hypotheses)
	}
}

// Срок хранения Pod'ов истёк — MissingContext, а не Missing; отказ чтения Pod'ов — не «Pod'ов нет».
func TestJobPodsRetentionIsMissingContext(t *testing.T) {
	t.Parallel()
	failed := jobManifest("Failed", "BackoffLimitExceeded", ", backoffLimit: 0")
	if rep := batchReport(t, "Job", "report", nil, failed); !batchHasMissing(rep, "job.pods_not_retained") {
		t.Errorf("no job.pods_not_retained: %+v", rep.Missing)
	}
	rep := batchReport(t, "Job", "report", []string{"v1/Pod"}, failed)
	if batchHasMissing(rep, "job.pods_not_retained") {
		t.Errorf("forbidden Pods must not read as «Pods are gone»: %+v", rep.Missing)
	}
	if !batchHasMissing(rep, "source.unavailable") {
		t.Errorf("no source.unavailable for the forbidden Pod list: %+v", rep.Missing)
	}
}

// Приостановленный Job — факт: ни симптома, ни гипотезы.
func TestJobSuspendedIsAFactNotAProblem(t *testing.T) {
	t.Parallel()
	rep := batchReport(t, "Job", "report", nil, jobManifest("", "", ", suspend: true"))
	if slices.Contains(rep.Symptoms, SymptomJobFailed) || len(rep.Hypotheses) != 0 {
		t.Errorf("symptoms=%v hypotheses=%+v", rep.Symptoms, rep.Hypotheses)
	}
	if !slices.ContainsFunc(rep.Facts, func(f Fact) bool { return f.Code == "job.suspended" }) {
		t.Errorf("no job.suspended fact: %+v", rep.Facts)
	}
}

func TestJobTargetNeedsANamespace(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"Job", "CronJob"} {
		if _, err := Collect(t.Context(), newChain(t), ResourceRef{Kind: kind, Name: "x"}, chainOpts()); err == nil {
			t.Errorf("%s without a namespace must fail", kind)
		}
	}
}

func TestBatchTargetsAreRegistered(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"Job", "CronJob"} {
		if _, ok := targets[kind]; !ok {
			t.Errorf("target %s is not registered", kind)
		}
	}
	for _, id := range []string{ruleJobChainID, ruleCronJobChainID} {
		if !isChainRule(id) {
			t.Errorf("rule %s is not registered as a chain rule", id)
		}
	}
}

// Детерминизм: два прогона по одним данным дают одинаковый отчёт (включая порядок цепочек и связей).
func TestBatchReportsAreDeterministic(t *testing.T) {
	t.Parallel()
	dirs, err := filepath.Glob(filepath.Join("testdata", "chain", "*", "case.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases := loadChainCases(t)
	n := 0
	for _, f := range dirs {
		name := filepath.Base(filepath.Dir(f))
		if !strings.HasPrefix(name, "job_") && !strings.HasPrefix(name, "cronjob_") {
			continue
		}
		n++
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a, _ := json.Marshal(Analyze(chainContext(t, name, cases[name])))
			b, _ := json.Marshal(Analyze(chainContext(t, name, cases[name])))
			if string(a) != string(b) {
				t.Errorf("two runs differ:\n%s\n%s", a, b)
			}
		})
	}
	if n < 14 {
		t.Errorf("only %d batch fixtures found, want at least 14", n)
	}
}

// Время — только Context.ObservedAt: в файлах вида нет чтения часов.
func TestBatchFilesDoNotReadTheClock(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"targets_job.go", "targets_cronjob.go", "rules_chain_job.go", "rules_chain_cronjob.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "time.Now(") {
			t.Errorf("%s reads the clock with time.Now", f)
		}
	}
}
