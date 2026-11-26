// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"context"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/correlation"
)

var gvkJob = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}

// jobPod добавляет Job (с владельцем-CronJob, если cron != "") и его неготовый Pod на узле.
func (w *world) jobPod(ns, job, cron, node string) {
	owner := ""
	if cron != "" {
		owner = fmt.Sprintf("\n ownerReferences: [{apiVersion: batch/v1, kind: CronJob, name: %s, controller: true}]", cron)
	}
	w.add("apiVersion: batch/v1\nkind: Job\nmetadata:\n name: %s\n namespace: %s%s", job, ns, owner)
	w.add("apiVersion: v1\nkind: Pod\nmetadata:\n name: %s-x1\n namespace: %s\n ownerReferences: [{apiVersion: batch/v1, kind: Job, name: %s, controller: true}]\n"+
		"spec: {nodeName: %s}\nstatus:\n phase: Running\n conditions:\n - {type: Ready, status: \"False\", lastTransitionTime: \"%s\"}",
		job, ns, job, node, ts(20))
}

func cronWorld() *world {
	w := &world{}
	w.node("n1", false, "Ready=False@30")
	w.jobPod("prod", "nightly-29001", "nightly", "n1")
	w.jobPod("prod", "report-29002", "report", "n1")
	w.jobPod("prod", "adhoc-migration", "", "n1") // самостоятельный Job
	return w
}

func TestJobResolvesToCronJob(t *testing.T) {
	res, reads := run(t, cronWorld(), "prod", "adhoc-migration-x1")
	b := res.Best()
	if b == nil || b.Total != 3 {
		t.Fatalf("кандидат: %+v", b)
	}
	want := []struct{ kind, name string }{{"CronJob", "nightly"}, {"CronJob", "report"}, {"Job", "adhoc-migration"}}
	for i, w := range want {
		if a := b.Affected[i]; a.Kind != w.kind || a.Name != w.name {
			t.Fatalf("Affected[%d] = %s/%s, want %s/%s", i, a.Kind, a.Name, w.kind, w.name)
		}
	}
	if res.Partial != nil {
		t.Fatalf("полный результат помечен частичным: %v", res.Partial)
	}
	if reads > correlation.MaxExtraReads {
		t.Fatalf("чтений %d, предел %d", reads, correlation.MaxExtraReads)
	}
}

func TestJobListUnavailableKeepsJob(t *testing.T) {
	w := cronWorld()
	f := w.reader(t)
	c := diagCtx(t, f, "prod", "adhoc-migration-x1")
	f.Forbid(gvkJob)
	res, err := correlation.Run(context.Background(), reader{f}, c, now)
	if err != nil {
		t.Fatal(err)
	}
	b := res.Best()
	if b == nil || b.Total != 3 {
		t.Fatalf("кандидат: %+v", b)
	}
	for _, a := range b.Affected {
		if a.Kind != "Job" {
			t.Fatalf("при недоступных Job нагрузкой должен оставаться Job, получено %s/%s", a.Kind, a.Name)
		}
	}
	var found bool
	for _, m := range res.Missing {
		found = found || (m.Resource.Kind == "Job" && m.Params["reason"] == "forbidden")
	}
	if !found || len(res.Partial) == 0 {
		t.Fatalf("недоступность Job не отражена: missing=%+v partial=%v", res.Missing, res.Partial)
	}
}
