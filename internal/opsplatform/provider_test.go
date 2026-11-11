// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	discfake "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/logging"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// fakeEnv — каталог и сессия в памяти. grants: кластер → команда и права текущего пользователя.
type fakeEnv struct {
	mu          sync.Mutex
	teamOf      map[string]string        // кластер → команда-владелец
	member      map[string]auth.Role     // команда → роль пользователя (нет записи — не член)
	override    map[auth.Permission]bool // true — отозвать право посреди операции
	audits      []audit.Event
	notified    [][]any
	clientCalls int
	clientErr   error
	checkErr    error
	panicAudit  bool
}

func newFakeEnv() *fakeEnv {
	return &fakeEnv{
		teamOf:   map[string]string{"c1": "teamA", "c2": "teamB"},
		member:   map[string]auth.Role{"teamA": auth.RoleOperator},
		override: map[auth.Permission]bool{},
	}
}

func (e *fakeEnv) Check(p auth.Permission, clusterID string) (auth.Actor, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a := auth.Actor{Username: "alice", Role: auth.RoleOperator, Source: "10.0.0.1"}
	if e.checkErr != nil {
		return a, e.checkErr
	}
	team, ok := e.teamOf[clusterID]
	if !ok {
		return a, auth.ErrClusterNotFound
	}
	role, ok := e.member[team]
	if !ok {
		return a, auth.ErrClusterNotFound // чужой кластер неотличим от несуществующего
	}
	a.Role, a.TeamID, a.ClusterID = role, team, clusterID
	if e.override[p] || !role.Has(p) {
		return a, auth.ErrForbidden
	}
	return a, nil
}
func (e *fakeEnv) OrgID() string       { return "org1" }
func (e *fakeEnv) DefaultTeam() string { return "default" }
func (e *fakeEnv) SessionID() string   { return "sess1" }
func (e *fakeEnv) Clients(context.Context, string) (Clients, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clientCalls++
	if e.clientErr != nil {
		return Clients{}, e.clientErr
	}
	return Clients{
		Dynamic:   dynfake.NewSimpleDynamicClient(runtime.NewScheme()),
		Core:      kubefake.NewSimpleClientset(),
		Discovery: &discfake.FakeDiscovery{Fake: &clienttesting.Fake{}},
	}, nil
}
func (e *fakeEnv) RecordAudit(ev audit.Event) {
	if e.panicAudit {
		panic("audit sink is broken")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.audits = append(e.audits, ev)
}
func (e *fakeEnv) Notify(event string, data ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.notified = append(e.notified, append([]any{event}, data...))
}

func newProvider(t *testing.T, env *fakeEnv) (Provider, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewProvider(env, Config{Logger: lg, Limiter: NewClusterLimiter(2), OpTimeout: 5 * time.Second}), &buf
}

func req(cluster string, perm auth.Permission) OpRequest {
	return OpRequest{Module: "incident", Operation: "open", ClusterID: cluster, Permission: perm}
}

func TestBeginScopeComesFromCatalog(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, _ := newProvider(t, env)
	ctx, op, err := p.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	defer op.End(nil)
	want := tenant.Scope{OrgID: "org1", TeamID: "teamA", ClusterID: "c1"}
	if op.Scope != want {
		t.Fatalf("scope = %+v, want %+v", op.Scope, want)
	}
	if op.Initiator != "alice" || op.SessionID != "sess1" || op.Reader == nil || op.StartedAt.IsZero() {
		t.Fatalf("incomplete exec context: %+v", op)
	}
	if op.CorrelationID == "" || logging.RequestID(ctx) != op.CorrelationID {
		t.Fatalf("correlation id %q is not in the context (%q)", op.CorrelationID, logging.RequestID(ctx))
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("the operation context has no deadline")
	}
}

func TestBeginReusesRequestID(t *testing.T) {
	t.Parallel()
	p, _ := newProvider(t, newFakeEnv())
	ctx := logging.WithRequestID(t.Context(), "req-12345678")
	_, op, err := p.Begin(ctx, req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	defer op.End(nil)
	if op.CorrelationID != "req-12345678" {
		t.Fatalf("correlation id = %q", op.CorrelationID)
	}
}

func TestBeginForeignTeamClusterIsNotFound(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, _ := newProvider(t, env)
	for _, cluster := range []string{"c2", "ghost"} { // чужая команда и несуществующий кластер — один и тот же ответ
		_, op, err := p.Begin(t.Context(), req(cluster, auth.PermRead))
		if !errors.Is(err, auth.ErrClusterNotFound) || op != nil {
			t.Fatalf("%s: op=%v err=%v, want ErrClusterNotFound", cluster, op, err)
		}
	}
	if env.clientCalls != 0 {
		t.Fatal("the cluster connection was touched before the ownership check")
	}
}

func TestBeginWithoutPermissionDeniesBeforeRead(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, _ := newProvider(t, env)
	// operator не имеет baseline.manage
	_, op, err := p.Begin(t.Context(), OpRequest{Module: "baseline", Operation: "accept", ClusterID: "c1", Permission: auth.PermBaselineManage})
	if !errors.Is(err, auth.ErrForbidden) || op != nil {
		t.Fatalf("op=%v err=%v, want ErrForbidden", op, err)
	}
	if env.clientCalls != 0 {
		t.Fatal("the cluster connection was touched before the permission check")
	}
	if len(env.audits) != 1 {
		t.Fatalf("expected one audit record for the denial, got %d", len(env.audits))
	}
	ev := env.audits[0]
	if ev.Result != audit.ResultDenied || ev.Action != "BaselineAccept" || ev.ClusterID != "c1" || ev.User != "alice" ||
		!strings.Contains(ev.Detail, "need=baseline.manage") {
		t.Fatalf("denial record: %+v", ev)
	}
}

func TestBeginPermissionMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		role auth.Role
		perm auth.Permission
		ok   bool
	}{
		{auth.RoleUser, auth.PermRead, true},
		{auth.RoleUser, auth.PermDiagnosticsLogs, false},
		{auth.RoleOperator, auth.PermDiagnosticsLogs, true},
		{auth.RoleOperator, auth.PermBaselineManage, false},
		{auth.RoleAdmin, auth.PermBaselineManage, true},
	}
	for _, tc := range tests {
		t.Run(string(tc.role)+"/"+string(tc.perm), func(t *testing.T) {
			t.Parallel()
			env := newFakeEnv()
			env.member["teamA"] = tc.role
			p, _ := newProvider(t, env)
			_, op, err := p.Begin(t.Context(), req("c1", tc.perm))
			if tc.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if op != nil {
				op.End(nil)
			}
		})
	}
}

func TestBeginRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	p, _ := newProvider(t, newFakeEnv())
	tests := []struct {
		name string
		r    OpRequest
	}{
		{"no cluster", OpRequest{Module: "incident", Operation: "open", Permission: auth.PermRead}},
		{"bad module", OpRequest{Module: "Inc ident", Operation: "open", ClusterID: "c1", Permission: auth.PermRead}},
		{"empty operation", OpRequest{Module: "incident", ClusterID: "c1", Permission: auth.PermRead}},
		{"unknown permission", OpRequest{Module: "incident", Operation: "open", ClusterID: "c1", Permission: "nope"}},
		{"organization-level permission", OpRequest{Module: "incident", Operation: "open", ClusterID: "c1", Permission: auth.PermUsersManage}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, op, err := p.Begin(t.Context(), tc.r); err == nil || op != nil {
				t.Fatalf("op=%v err=%v, want an error", op, err)
			}
		})
	}
}

func TestBeginPassesThroughAuthErrors(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	env.checkErr = auth.ErrNotAuthenticated
	p, _ := newProvider(t, env)
	if _, _, err := p.Begin(t.Context(), req("c1", auth.PermRead)); !errors.Is(err, auth.ErrNotAuthenticated) {
		t.Fatalf("got %v", err)
	}
}

func TestBeginClusterUnavailable(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	env.clientErr = errors.New("sign-in required")
	p, _ := newProvider(t, env)
	_, op, err := p.Begin(t.Context(), req("c1", auth.PermRead))
	if !errors.Is(err, ErrUnavailable) || op != nil {
		t.Fatalf("op=%v err=%v, want ErrUnavailable", op, err)
	}
}

func TestBeginUsesDefaultTeamWithoutTeams(t *testing.T) {
	t.Parallel()
	env := &noTeamEnv{fakeEnv: newFakeEnv()}
	p := NewProvider(env, Config{Limiter: NewClusterLimiter(1), Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	_, op, err := p.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	defer op.End(nil)
	if op.Scope.TeamID != "default" || op.Scope.OrgID != "org1" {
		t.Fatalf("scope = %+v", op.Scope)
	}
}

// noTeamEnv — режим без команд (десктоп): Actor без TeamID.
type noTeamEnv struct{ *fakeEnv }

func (e *noTeamEnv) Check(auth.Permission, string) (auth.Actor, error) {
	return auth.Actor{Username: "local", Role: auth.RoleAdmin}, nil
}

func TestBeginRefusesIncompleteScope(t *testing.T) {
	t.Parallel()
	env := &emptyOrgEnv{noTeamEnv{newFakeEnv()}}
	p := NewProvider(env, Config{Limiter: NewClusterLimiter(1), Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))})
	if _, op, err := p.Begin(t.Context(), req("c1", auth.PermRead)); !errors.Is(err, auth.ErrClusterNotFound) || op != nil {
		t.Fatalf("op=%v err=%v: an incomplete scope must be refused", op, err)
	}
}

type emptyOrgEnv struct{ noTeamEnv }

func (*emptyOrgEnv) OrgID() string { return "" }

func TestOpRequireRechecksPermission(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, _ := newProvider(t, env)
	_, op, err := p.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	defer op.End(nil)
	if err := op.Require(auth.PermDiagnosticsLogs); err != nil {
		t.Fatalf("operator has diagnostics.logs: %v", err)
	}
	// Право отозвали уже после Begin: ранее пройденная проверка доступом не считается.
	env.mu.Lock()
	env.override[auth.PermDiagnosticsLogs] = true
	env.mu.Unlock()
	if err := op.Require(auth.PermDiagnosticsLogs); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("Require after revoke = %v, want ErrForbidden", err)
	}
	if n := len(env.audits); n != 1 || env.audits[0].Result != audit.ResultDenied {
		t.Fatalf("the denial must be audited: %+v", env.audits)
	}
}

func TestOpAuditHasNoContentLeaks(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, _ := newProvider(t, env)
	_, op, err := p.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	defer op.End(nil)

	secretLog := "2026-01-01 login failed password=hunter2hunter2 Authorization: Bearer abcdef123456.zzz"
	type payload struct{ Body string }
	op.Audit("IncidentOpen", "default/pod-1", nil,
		slog.Int("fragments", 3),
		slog.String("note", secretLog),                 // чувствительное скрывается
		slog.Any("object", payload{Body: secretLog}),   // структура отбрасывается целиком
		slog.Group("g", slog.String("x", secretLog)),   // группа отбрасывается
		slog.String("bad key!", "v"),                   // недопустимый ключ отбрасывается
		slog.String("big", strings.Repeat("x", 10000)), // длинное значение усекается
	)
	if len(env.audits) != 1 {
		t.Fatalf("audits = %d", len(env.audits))
	}
	ev := env.audits[0]
	for _, bad := range []string{"hunter2", "abcdef123456", "Body", "bad key", strings.Repeat("x", 200)} {
		if strings.Contains(ev.Detail, bad) {
			t.Errorf("audit detail leaks %q: %s", bad, ev.Detail)
		}
	}
	for _, want := range []string{"target=default/pod-1", "fragments=3", "correlation_id=" + op.CorrelationID} {
		if !strings.Contains(ev.Detail, want) {
			t.Errorf("audit detail lacks %q: %s", want, ev.Detail)
		}
	}
	if ev.Result != audit.ResultOK || ev.User != "alice" || ev.TeamID != "teamA" || ev.ClusterID != "c1" || ev.Action != "IncidentOpen" {
		t.Errorf("record: %+v", ev)
	}
}

func TestOpAuditFailureResultAndRedaction(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, _ := newProvider(t, env)
	_, op, _ := p.Begin(t.Context(), req("c1", auth.PermRead))
	defer op.End(nil)
	op.Audit("IncidentOpen", "x", errors.New("dial https://u:pass1234@host failed, token=abcdef"))
	ev := env.audits[0]
	if ev.Result != audit.ResultFailed || strings.Contains(ev.Detail, "pass1234") || strings.Contains(ev.Detail, "abcdef") {
		t.Fatalf("record: %+v", ev)
	}
}

// Отказ приёмника аудита не меняет результат операции.
func TestOpAuditSinkPanicDoesNotBreakOperation(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, _ := newProvider(t, env)
	_, op, err := p.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	env.panicAudit = true
	op.Audit("IncidentOpen", "x", nil) // не паникует
	op.End(nil)
}

func TestOpEndWritesTechnicalLogAndMetrics(t *testing.T) {
	// не параллелен: подменяет глобальный observer
	env := newFakeEnv()
	p, buf := newProvider(t, env)
	rec := &recordingObserver{}
	SetObserver(rec)
	defer SetObserver(nil)

	_, op, err := p.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	op.End(nil, "metrics.k8s.io/v1beta1: unavailable")
	op.End(errors.New("second call is ignored"))

	out := buf.String()
	for _, want := range []string{"module=incident", "operation=open", "status=partial", "correlation_id=" + op.CorrelationID, "cluster_id=c1", "duration="} {
		if !strings.Contains(out, want) {
			t.Errorf("technical log lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "ops operation finished") != 1 {
		t.Errorf("End must log once:\n%s", out)
	}
	if got := rec.snapshot(); len(got) != 1 || got[0] != "incident/open/partial" {
		t.Fatalf("metrics = %v", got)
	}
	if len(env.audits) != 0 {
		t.Fatal("the technical journal must not write to the audit")
	}
}

func TestOpEndFailedStatusAndRedactedError(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, buf := newProvider(t, env)
	_, op, _ := p.Begin(t.Context(), req("c1", auth.PermRead))
	op.End(errors.New("boom password=hunter2hunter2"))
	out := buf.String()
	if !strings.Contains(out, "status=failed") || !strings.Contains(out, "level=WARN") || strings.Contains(out, "hunter2") {
		t.Fatalf("log: %s", out)
	}
}

func TestOpEndCancelsContext(t *testing.T) {
	t.Parallel()
	p, _ := newProvider(t, newFakeEnv())
	ctx, op, err := p.Begin(t.Context(), req("c1", auth.PermRead))
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("context canceled before End")
	}
	op.End(nil)
	if ctx.Err() == nil {
		t.Fatal("End must release the operation context")
	}
}

func TestOpStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err     error
		partial []string
		want    string
	}{
		{nil, nil, "ok"},
		{nil, []string{"x"}, "partial"},
		{errors.New("x"), nil, "failed"},
		{context.Canceled, nil, "canceled"},
		{ErrUnavailable, []string{"x"}, "failed"},
	}
	for _, tc := range tests {
		if got := opStatus(tc.err, tc.partial); got != tc.want {
			t.Errorf("opStatus(%v, %v) = %q, want %q", tc.err, tc.partial, got, tc.want)
		}
	}
}

// Событие несёт только имя и ID и уходит через Env.Notify (адресная доставка владельцу сессии), без содержимого.
func TestOpNotifyCarriesOnlyIdentifiers(t *testing.T) {
	t.Parallel()
	env := newFakeEnv()
	p, _ := newProvider(t, env)
	_, op, _ := p.Begin(t.Context(), req("c1", auth.PermRead))
	defer op.End(nil)
	op.Notify("incident:updated", "inc-1")
	op.Notify("", "ignored")
	if len(env.notified) != 1 || len(env.notified[0]) != 2 || env.notified[0][0] != "incident:updated" || env.notified[0][1] != "inc-1" {
		t.Fatalf("notified = %v", env.notified)
	}
}

func TestActionName(t *testing.T) {
	t.Parallel()
	tests := map[[2]string]string{
		{"incident", "open"}:        "IncidentOpen",
		{"readiness", "run_report"}: "ReadinessRunReport",
		{"baseline", "accept"}:      "BaselineAccept",
	}
	for in, want := range tests {
		if got := actionName(in[0], in[1]); got != want {
			t.Errorf("actionName(%v) = %q, want %q", in, got, want)
		}
	}
}

type recordingObserver struct {
	mu  sync.Mutex
	got []string
}

func (r *recordingObserver) ObserveOp(module, operation, status string, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, module+"/"+operation+"/"+status)
}
func (r *recordingObserver) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}
