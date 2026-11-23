// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/feedback"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/store"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

const e2eCluster = "c1"

// e2eClusterStore — каталог из одного кластера: операциям без подключения (StoredOnly) нужно лишь знать, что он есть.
type e2eClusterStore struct{}

func (e2eClusterStore) List() ([]k8s.ClusterMeta, error) {
	return []k8s.ClusterMeta{{ID: e2eCluster}}, nil
}

func (e2eClusterStore) Get(id string) (*k8s.ClusterMeta, error) {
	if id != e2eCluster {
		return nil, fmt.Errorf("cluster %s: %w", id, fs.ErrNotExist) // как файловое хранилище
	}
	return &k8s.ClusterMeta{ID: id}, nil
}

func (e2eClusterStore) Kubeconfig(string) ([]byte, error)   { return nil, errors.New("no kubeconfig") }
func (e2eClusterStore) Save(*k8s.ClusterMeta, []byte) error { return nil }
func (e2eClusterStore) Delete(string) error                 { return nil }

type e2eEnv struct {
	app   *App
	svc   *incident.Service
	be    incident.Backend
	scope tenant.Scope
}

// newE2EApp собирает App с настоящей авторизацией и файловым хранилищем расследований. Подключения к кластеру нет:
// проверяются RPC проблем и статистики, которые работают только с сохранёнными данными.
func newE2EApp(t *testing.T) *e2eEnv {
	t.Helper()
	a := newAuditedApp(t)
	a.auth.SetOnLogout(nil)
	if _, err := a.auth.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	k8s.SetClusterStore(e2eClusterStore{})
	t.Cleanup(func() { k8s.SetClusterStore(k8s.NewFileClusterStore()) })

	be := store.NewFile(t.TempDir()).Incidents()
	svc := incident.NewService(be, incidentRedactor()).WithProblems(problemBackendOf(be))
	a.incidents = svc

	scope := tenant.Scope{OrgID: localOrgID, TeamID: localDefaultTeam, ClusterID: e2eCluster}
	return &e2eEnv{app: a, svc: svc, be: be, scope: scope}
}

// seed кладёт открытое расследование с отчётом набора правил версии 2: по одной гипотезе и рекомендации.
func (e *e2eEnv) seed(t *testing.T, id, workload string) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Hour)
	target := diagnose.ResourceRef{Kind: "Deployment", Namespace: "app", Name: workload}
	in := &incident.Incident{
		ID: id, Scope: e.scope, Status: incident.StatusOpen, OpenedAt: now, UpdatedAt: now, OpenedBy: "admin",
		Target: target, Evidence: []incident.Evidence{}, Events: []diagnose.Event{},
		Report: diagnose.Report{
			RuleSetVersion: diagnose.RuleSetVersion, Target: target, ObservedAt: now,
			Symptoms: []string{"pod.crash_loop"},
			Hypotheses: []diagnose.Hypothesis{{ID: "h1", Code: "crash.oom", Resource: target, RuleID: "crash.oom", RuleEpoch: 1,
				Supporting: []string{}, Contradicting: []string{}}},
			Recommendations: []diagnose.Recommendation{{ID: "r1", Code: "raise.memory", Resource: target, RuleID: "crash.oom",
				RuleEpoch: 1, Hypotheses: []string{"h1"}}},
		},
	}
	if err := e.be.PutIncident(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func (e *e2eEnv) close(t *testing.T, id string, o incident.Outcome) {
	t.Helper()
	c := incident.Caller{Scope: e.scope, Actor: "admin"}
	if _, err := e.svc.MarkOutcome(context.Background(), c, id, o); err != nil {
		t.Fatalf("mark outcome %s: %v", id, err)
	}
}

// E2E: ручная группировка расследований в проблему через RPC App — создание, состав, отсоединение, объединение.
func TestE2EProblemLifecycleThroughApp(t *testing.T) {
	e := newE2EApp(t)
	for _, id := range []string{"inc-a", "inc-b", "inc-c", "inc-d"} {
		e.seed(t, id, "web-"+id)
	}

	p1, err := e.app.IncidentProblemCreate(e2eCluster, []string{"inc-a", "inc-b"})
	if err != nil {
		t.Fatal(err)
	}
	if p1.MemberCount != 2 || p1.Origin != incident.ProblemManual {
		t.Fatalf("unexpected problem: %+v", p1)
	}

	d, err := e.app.IncidentProblemAttach(e2eCluster, p1.ID, "inc-c")
	if err != nil {
		t.Fatal(err)
	}
	if d.MemberCount != 3 {
		t.Fatalf("after attach: %d members, want 3", d.MemberCount)
	}

	list, err := e.app.IncidentProblemList(e2eCluster, IncidentProblemListQuery{})
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != p1.ID {
		t.Fatalf("list: %v %+v", err, list)
	}

	if err := e.app.IncidentProblemDetach(e2eCluster, "inc-c"); err != nil {
		t.Fatal(err)
	}
	got, err := e.app.IncidentProblemGet(e2eCluster, p1.ID)
	if err != nil || got.MemberCount != 2 {
		t.Fatalf("after detach: %v %+v", err, got)
	}

	p2, err := e.app.IncidentProblemCreate(e2eCluster, []string{"inc-c", "inc-d"})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := e.app.IncidentProblemMerge(e2eCluster, p2.ID, p1.ID)
	if err != nil || merged.MemberCount != 4 {
		t.Fatalf("merge: %v %+v", err, merged)
	}
}

// E2E: кластер, которого нет в каталоге, неотличим от чужого.
func TestE2EUnknownClusterIsNotFound(t *testing.T) {
	e := newE2EApp(t)
	if _, err := e.app.IncidentProblemList("nope", IncidentProblemListQuery{}); !errors.Is(err, auth.ErrClusterNotFound) {
		t.Fatalf("got %v, want ErrClusterNotFound", err)
	}
	if _, err := e.app.IncidentRuleStats("nope"); !errors.Is(err, auth.ErrClusterNotFound) {
		t.Fatalf("got %v, want ErrClusterNotFound", err)
	}
}

// E2E: статистика правил по закрытым расследованиям и анонимная выгрузка.
func TestE2ERuleStatsAndExportThroughApp(t *testing.T) {
	e := newE2EApp(t)
	for i := range feedback.MinSample {
		id := fmt.Sprintf("inc-%02d", i)
		e.seed(t, id, "web-"+id)
		e.close(t, id, incident.Outcome{Result: incident.ResultResolved, ConfirmedHypothesisID: "h1",
			HelpfulRecommendationIDs: []string{"r1"}})
	}

	st, err := e.app.IncidentRuleStats(e2eCluster)
	if err != nil {
		t.Fatal(err)
	}
	if st.Records != feedback.MinSample {
		t.Fatalf("records = %d, want %d", st.Records, feedback.MinSample)
	}
	if len(st.Hypotheses) != 1 || st.Hypotheses[0].RuleID != "crash.oom" || !st.Hypotheses[0].Trusted {
		t.Fatalf("a rule with %d rated cases must be trusted: %+v", feedback.MinSample, st.Hypotheses)
	}

	file, err := e.app.IncidentRuleStatsExport(e2eCluster)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(file.DataBase64)
	if err != nil {
		t.Fatal(err)
	}
	if file.MIME != "application/json" || !strings.HasPrefix(file.Filename, "kubron-rule-stats-") || !json.Valid(raw) {
		t.Fatalf("unexpected export: %s %s", file.Filename, file.MIME)
	}
	// Выгрузка анонимна: ни идентификаторов расследований, ни кластера, ни имён нагрузок.
	for _, secret := range []string{"inc-00", e2eCluster, "web-inc", "admin"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the export leaks %q", secret)
		}
	}
}

// E2E: права. Обычный пользователь читает статистику, но не выгружает её (users.manage).
func TestE2EStatsExportNeedsUsersManage(t *testing.T) {
	e := newE2EApp(t)
	if _, err := e.app.auth.CreateUser("bob", testPass, auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	e.app.auth.Logout()
	if _, err := e.app.auth.Login(t.Context(), "bob", testPass); err != nil {
		t.Fatal(err)
	}
	if err := e.app.auth.ChangePassword(t.Context(), testPass, "another-pass-2"); err != nil {
		t.Fatal(err)
	}

	if _, err := e.app.IncidentRuleStats(e2eCluster); err != nil {
		t.Fatalf("a reader must see the statistics: %v", err)
	}
	if _, err := e.app.IncidentRuleStatsExport(e2eCluster); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("export by a regular user: got %v, want ErrForbidden", err)
	}
}
