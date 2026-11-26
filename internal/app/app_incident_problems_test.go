// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/store"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

func TestAffectedAccDedupesAndSorts(t *testing.T) {
	var acc affectedAcc
	acc.add([]correlation.WorkloadRef{
		{Kind: "Deployment", Namespace: "b", Name: "x"},
		{Kind: "Deployment", Namespace: "a", Name: "y"},
	}, 2, false)
	acc.add([]correlation.WorkloadRef{
		{Kind: "Deployment", Namespace: "a", Name: "y"}, // повтор у другого участника
		{Kind: "StatefulSet", Namespace: "a", Name: "z"},
	}, 7, false)
	items, total, truncated := acc.result()
	if len(items) != 3 {
		t.Fatalf("got %d workloads, want 3 distinct: %+v", len(items), items)
	}
	if items[0].Namespace != "a" || items[0].Name != "y" || items[1].Name != "z" || items[2].Namespace != "b" {
		t.Errorf("order is not namespace, kind, name: %+v", items)
	}
	if total != 7 || truncated {
		t.Errorf("total = %d, truncated = %v; want the largest reported total and no truncation", total, truncated)
	}
}

func TestAffectedAccTruncatesAtLimit(t *testing.T) {
	var acc affectedAcc
	ws := make([]correlation.WorkloadRef, 0, maxProblemAffected+5)
	for i := range maxProblemAffected + 5 {
		ws = append(ws, correlation.WorkloadRef{Kind: "Pod", Namespace: "n", Name: fmt.Sprintf("p%03d", i)})
	}
	acc.add(ws, 0, false)
	items, total, truncated := acc.result()
	if len(items) != maxProblemAffected || total != maxProblemAffected+5 || !truncated {
		t.Fatalf("len = %d, total = %d, truncated = %v", len(items), total, truncated)
	}
}

func TestAffectedAccEmptyIsNotNil(t *testing.T) {
	items, total, truncated := (&affectedAcc{}).result()
	if items == nil || len(items) != 0 || total != 0 || truncated {
		t.Fatalf("empty result must be an empty slice: %v %d %v", items, total, truncated)
	}
}

func TestIncidentAffectedManualUsesTarget(t *testing.T) {
	p := &incident.Problem{Origin: incident.ProblemManual}
	in := &incident.Incident{Target: diagnose.ResourceRef{Kind: "Deployment", Namespace: "n", Name: "api"}}
	ws, total, truncated := incidentAffected(p, in)
	if len(ws) != 1 || ws[0].Kind != "Deployment" || ws[0].Namespace != "n" || ws[0].Name != "api" || total != 1 || truncated {
		t.Fatalf("manual problem must count the investigation target: %+v %d %v", ws, total, truncated)
	}
}

func TestIncidentAffectedAutoUsesMatchingCandidate(t *testing.T) {
	shared := correlation.Candidate{Kind: correlation.KindSharedNode, Source: diagnose.ResourceRef{Kind: "Node", Name: "n1"},
		Affected: []correlation.WorkloadRef{{Kind: "Deployment", Namespace: "n", Name: "a"}}, Total: 3, Truncated: true}
	other := correlation.Candidate{Kind: correlation.KindSharedStorage, Source: diagnose.ResourceRef{Kind: "StorageClass", Name: "fast"}, Total: 9}
	p := &incident.Problem{Origin: incident.ProblemAuto, Key: shared.Key()}

	in := &incident.Incident{Correlation: &correlation.Result{Candidates: []correlation.Candidate{other, shared}}}
	ws, total, truncated := incidentAffected(p, in)
	if len(ws) != 1 || ws[0].Name != "a" || total != 3 || !truncated {
		t.Fatalf("must take the candidate with the problem key: %+v %d %v", ws, total, truncated)
	}
	if ws, total, _ := incidentAffected(p, &incident.Incident{}); len(ws) != 0 || total != 0 {
		t.Errorf("an investigation without a snapshot adds nothing: %+v %d", ws, total)
	}
	p.Key = "shared_node:core/Node//other"
	if ws, total, _ := incidentAffected(p, in); len(ws) != 0 || total != 0 {
		t.Errorf("a snapshot without the problem key adds nothing: %+v %d", ws, total)
	}
}

// Если хранилище расследований не отдаёт ProblemBackend, группировка молча выключилась бы: тест не даёт этому
// случиться с файловым хранилищем десктопа.
func TestProblemBackendOfFileStore(t *testing.T) {
	if problemBackendOf(store.NewFile(t.TempDir()).Incidents()) == nil {
		t.Fatal("the file store does not provide a problem backend: problems would be silently disabled")
	}
	if problemBackendOf(nil) != nil {
		t.Fatal("no backend must mean no problem backend")
	}
}

func TestProblemDetailFromFileBackend(t *testing.T) {
	ctx := context.Background()
	be := store.NewFile(t.TempDir()).Incidents()
	svc := incident.NewService(be, incidentRedactor()).WithProblems(problemBackendOf(be))
	scope := tenant.Scope{OrgID: "local", TeamID: "default", ClusterID: "c1"}
	now := time.Now().UTC()
	for _, id := range []string{"inca", "incb"} {
		in := &incident.Incident{ID: id, Scope: scope, Status: incident.StatusOpen, OpenedAt: now, UpdatedAt: now, OpenedBy: "u1",
			Target:   diagnose.ResourceRef{Kind: "Deployment", Namespace: "app", Name: "web-" + id},
			Evidence: []incident.Evidence{}, Events: []diagnose.Event{}}
		if err := be.PutIncident(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	c := incident.Caller{Scope: scope, Actor: "u1"}

	p, err := svc.CreateProblem(ctx, c, []string{"inca", "incb"})
	if err != nil {
		t.Fatal(err)
	}
	d := problemDetail(ctx, svc, scope, p)
	if d.Origin != incident.ProblemManual || d.MemberCount != 2 || len(d.Members) != 2 || len(d.Affected) != 2 || d.AffectedTotal != 2 {
		t.Fatalf("unexpected detail: %+v", d)
	}
	for _, m := range d.Members {
		if m.Summary == nil || m.Summary.ID != m.IncidentID {
			t.Errorf("member %s has no summary", m.IncidentID)
		}
	}
	if d.Detached == nil || len(d.Detached) != 0 {
		t.Errorf("detached must be an empty list: %v", d.Detached)
	}

	page, err := svc.ListProblems(ctx, scope, incident.ProblemQuery{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list: %v %+v", err, page)
	}
	if got := listAffectedTotal(ctx, svc, scope, &page.Items[0]); got != 1 {
		t.Errorf("list estimate = %d, want 1 (the newest member only)", got)
	}

	p2, err := svc.DetachFromProblem(ctx, c, "incb")
	if err != nil {
		t.Fatal(err)
	}
	d2 := problemDetail(ctx, svc, scope, p2)
	if d2.MemberCount != 1 || len(d2.Members) != 1 || len(d2.Detached) != 1 || d2.Detached[0] != "incb" {
		t.Fatalf("after detach: %+v", d2)
	}
}
