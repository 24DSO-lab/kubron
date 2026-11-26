// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/incident/incidenttest"
	"github.com/24DSO-lab/kubron/internal/logredact"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

var secretRe = regexp.MustCompile(`SECRET[A-Z0-9]*`)

// fakeRedactor — двойник редактора: скрывает «SECRET…», умеет отказывать.
type fakeRedactor struct {
	mu   sync.Mutex
	err  error
	seen int
}

func (r *fakeRedactor) Redact(in []byte) (incident.RedactResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen++
	if r.err != nil {
		return incident.RedactResult{}, r.err
	}
	n := len(secretRe.FindAll(in, -1))
	return incident.RedactResult{Text: secretRe.ReplaceAllString(string(in), "[REDACTED]"), Replacements: n, Version: 7}, nil
}

// realRedactor — адаптер к настоящему logredact (как в слое связки).
func realRedactor() incident.Redactor {
	return incident.RedactorFunc(func(in []byte) (incident.RedactResult, error) {
		res, err := logredact.Redact(in)
		switch {
		case errors.Is(err, logredact.ErrBinary):
			return incident.RedactResult{}, incident.ErrRedactNotText
		case errors.Is(err, logredact.ErrTooLarge):
			return incident.RedactResult{}, incident.ErrRedactTooLarge
		case err != nil:
			return incident.RedactResult{}, err
		}
		return incident.RedactResult{Text: res.Text, Replacements: res.Replacements, Version: res.Version}, nil
	})
}

// stubReader — кластер из одного namespace с падающим Pod.
type stubReader struct {
	mu       sync.Mutex
	log      []byte
	logErr   error
	logCalls int
	message  string
	events   []map[string]any
}

func newStub() *stubReader {
	return &stubReader{log: []byte("boot\nconnecting token=SECRET123\nFATAL: connection refused\n"),
		message: "back-off restarting; password=SECRET999"}
}

func (r *stubReader) pod() map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "api-1", "namespace": "shop", "uid": "u1"},
		"spec":     map[string]any{"nodeName": "n1", "containers": []any{map[string]any{"name": "app", "image": "app:1"}}},
		"status": map[string]any{"phase": "Running",
			"conditions": []any{map[string]any{"type": "Ready", "status": "False"}},
			"containerStatuses": []any{map[string]any{"name": "app", "restartCount": int64(5),
				"state":     map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff", "message": r.message}},
				"lastState": map[string]any{"terminated": map[string]any{"exitCode": int64(1), "reason": "Error"}}}}},
	}
}

func (r *stubReader) Get(_ context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
	switch {
	case gvk.Kind == "Pod" && name == "api-1":
		return &unstructured.Unstructured{Object: r.pod()}, nil
	case gvk.Kind == "Namespace":
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns}}}, nil
	}
	return nil, fmt.Errorf("get: %w", diagnose.ErrNotFound)
}

func (r *stubReader) List(_ context.Context, gvk schema.GroupVersionKind, _ diagnose.ListOptions) (*diagnose.ListResult, error) {
	res := &diagnose.ListResult{ObservedAt: testBase}
	if gvk.Kind == "Event" {
		evs := r.events
		if evs == nil {
			evs = []map[string]any{r.event("BackOff", r.message, "2026-11-01T09:59:00Z")}
		}
		for _, e := range evs {
			res.Items = append(res.Items, unstructured.Unstructured{Object: e})
		}
	}
	return res, nil
}

func (r *stubReader) event(reason, msg, ts string) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Event", "metadata": map[string]any{"name": reason, "namespace": "shop"},
		"involvedObject": map[string]any{"kind": "Pod", "name": "api-1", "namespace": "shop"},
		"type":           "Warning", "reason": reason, "message": msg, "lastTimestamp": ts, "count": int64(3)}
}

func (r *stubReader) PodLogs(_ context.Context, _ diagnose.LogRequest) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logCalls++
	return r.log, r.logErr
}

var target = diagnose.ResourceRef{Kind: "Pod", Namespace: "shop", Name: "api-1"}

type env struct {
	svc  *incident.Service
	b    incident.Backend
	r    *stubReader
	red  *fakeRedactor
	call incident.Caller
}

func newEnv(t *testing.T) *env {
	t.Helper()
	b := incident.NewFileBackend(t.TempDir())
	red := &fakeRedactor{}
	r := newStub()
	n := 0
	svc := incident.NewService(b, red,
		incident.WithClock(func() time.Time { n++; return testBase.Add(time.Duration(n) * time.Second) }),
		incident.WithIDs(func() string { n++; return fmt.Sprintf("id%03d", n) }))
	return &env{svc: svc, b: b, r: r, red: red, call: incident.Caller{Scope: incidenttest.Scope, Actor: "alice", Reader: r}}
}

func TestOpenStoresRedactedEvidence(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != incident.StatusOpen || !in.SaveLogs || in.OpenedBy != "alice" || in.Scope != e.call.Scope {
		t.Errorf("incident = %+v", in)
	}
	if len(in.Evidence) != 1 || in.Evidence[0].Status != incident.EvidenceSaved || !in.Evidence[0].Previous {
		t.Fatalf("evidence = %+v", in.Evidence)
	}
	d, err := e.svc.Get(t.Context(), e.call.Scope, in.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Fragments) != 1 || d.Fragments[0].ContentHidden {
		t.Fatalf("fragments = %+v", d.Fragments)
	}
	f, err := e.svc.ReadFragment(t.Context(), e.call.Scope, in.ID, in.Evidence[0].FragmentID, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.Text, "SECRET") || !strings.Contains(f.Text, "[REDACTED]") {
		t.Errorf("secret reached the store: %q", f.Text)
	}
	sum := sha256.Sum256([]byte(f.Text))
	if f.SHA256 != hex.EncodeToString(sum[:]) || f.Replacements != 1 || f.RedactorVersion != 7 || !f.Auto ||
		f.Namespace != "shop" || f.Pod != "api-1" || f.Container != "app" || !f.Previous || f.Lines != 3 || f.AnchorLine != 3 {
		t.Errorf("fragment metadata = %+v", f.FragmentMeta)
	}
	// Ни в документе инцидента, ни в снимке Events секрета нет.
	raw, _ := json.Marshal(in)
	if strings.Contains(string(raw), "SECRET") {
		t.Errorf("secret in the incident document: %s", raw)
	}
	if len(in.Events) != 1 || strings.Contains(in.Events[0].Message, "SECRET") {
		t.Errorf("events = %+v", in.Events)
	}
	if !slices.Contains(in.Report.Symptoms, diagnose.SymptomCrashLoop) {
		t.Errorf("symptoms = %v", in.Report.Symptoms)
	}
}

func TestOpenFailClosedWhenRedactorFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"redactor failure", errors.New("boom"), incident.ReasonRedactionFailed},
		{"not text", incident.ErrRedactNotText, incident.ReasonNotText},
		{"too large", incident.ErrRedactTooLarge, incident.ReasonLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.red.err = tc.err
			in, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
			if err != nil {
				t.Fatal(err)
			}
			if ev := in.Evidence[0]; ev.Status != incident.EvidenceNotSaved || ev.Reason != tc.reason || ev.FragmentID != "" {
				t.Errorf("evidence = %+v", ev)
			}
			if m, _ := e.b.ListFragments(t.Context(), e.call.Scope, in.ID); len(m) != 0 {
				t.Errorf("a fragment was stored: %+v", m)
			}
			raw, _ := json.Marshal(in)
			if strings.Contains(string(raw), "SECRET") {
				t.Errorf("raw cluster text leaked when the redactor failed: %s", raw)
			}
			if len(in.Events) == 1 && in.Events[0].Message != incident.RedactedPlaceholder {
				t.Errorf("event message = %q", in.Events[0].Message)
			}
		})
	}
}

func TestOpenRejectsBinaryAndEmptyLogs(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		log    []byte
		reason string
	}{
		"invalid utf-8": {[]byte("ok\n\xff\xfe error\n"), incident.ReasonNotText},
		"nul byte":      {[]byte("ok\x00 error\n"), incident.ReasonNotText},
		"empty":         {[]byte(""), incident.ReasonEmptyLog},
		"only blanks":   {[]byte("\n \n\t\n"), incident.ReasonEmptyLog},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.r.log = tc.log
			in, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
			if err != nil {
				t.Fatal(err)
			}
			if ev := in.Evidence[0]; ev.Status != incident.EvidenceNotSaved || ev.Reason != tc.reason {
				t.Errorf("evidence = %+v", ev)
			}
		})
	}
}

func TestLogUnavailableMarksEvidence(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.r.logErr = diagnose.ErrForbidden
	in, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if ev := in.Evidence[0]; ev.Reason != incident.ReasonLogUnavailable {
		t.Errorf("evidence = %+v", ev)
	}
}

func TestPolicyDisablesLogStorage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		st     incident.Settings
		req    incident.OpenRequest
		reason string
	}{
		{"organization switch", incident.Settings{DisableLogEvidence: true}, incident.OpenRequest{Target: target}, incident.ReasonDisabledOrg},
		{"excluded namespace", incident.Settings{ExcludedNamespaces: []string{"shop"}}, incident.OpenRequest{Target: target}, incident.ReasonDisabledNamespace},
		{"per investigation", incident.Settings{}, incident.OpenRequest{Target: target, DisableLogs: true}, incident.ReasonDisabledIncident},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			if _, err := e.svc.SetSettings(t.Context(), e.call.Scope.OrgID, tc.st); err != nil {
				t.Fatal(err)
			}
			in, err := e.svc.Open(t.Context(), e.call, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if ev := in.Evidence[0]; ev.Status != incident.EvidenceNotSaved || ev.Reason != tc.reason {
				t.Errorf("evidence = %+v", ev)
			}
			if e.r.logCalls != 0 {
				t.Error("the log must not even be read when storing is forbidden")
			}
			if _, err := e.svc.SaveFragment(t.Context(), e.call, in.ID, incident.SaveFragmentRequest{Pod: "api-1", Container: "app", Previous: true}); !errors.Is(err, incident.ErrLogsDisabled) {
				t.Errorf("explicit save = %v, want ErrLogsDisabled", err)
			}
		})
	}
}

func TestFragmentLimits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var sb strings.Builder
	for i := range 1000 {
		switch {
		case i == 700:
			sb.WriteString("ERROR the anchor line\n")
		default:
			fmt.Fprintf(&sb, "line %04d %s\n", i, strings.Repeat("x", 5000)) // строки сильно больше 2 КиБ
		}
	}
	e.r.log = []byte(sb.String())
	in, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	f, err := e.svc.ReadFragment(t.Context(), e.call.Scope, in.ID, in.Evidence[0].FragmentID, func() error { return nil })
	if err != nil {
		t.Fatalf("%+v: %v", in.Evidence, err)
	}
	lines := strings.Split(f.Text, "\n")
	if f.Bytes > incident.MaxFragmentBytes || len(f.Text) > incident.MaxFragmentBytes || len(lines) > incident.MaxFragmentLines {
		t.Errorf("limits exceeded: %d bytes, %d lines", len(f.Text), len(lines))
	}
	for _, l := range lines {
		if len(l) > incident.MaxLineBytes {
			t.Fatalf("a line of %d bytes survived", len(l))
		}
	}
	if !f.Truncated || f.AnchorLine != 701 || !strings.Contains(f.Text, "ERROR the anchor line") {
		t.Errorf("window = %d..%d anchor %d truncated=%v", f.StartLine, f.EndLine, f.AnchorLine, f.Truncated)
	}
	if f.StartLine > f.AnchorLine || f.EndLine < f.AnchorLine {
		t.Errorf("the window does not contain the anchor: %+v", f.FragmentMeta)
	}
}

func TestReadFragmentChecksPermissionEveryTime(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in, _ := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	fid := in.Evidence[0].FragmentID
	calls := 0
	allow := true
	authz := func() error {
		calls++
		if allow {
			return nil
		}
		return auth.ErrForbidden
	}
	for range 3 {
		if _, err := e.svc.ReadFragment(t.Context(), e.call.Scope, in.ID, fid, authz); err != nil {
			t.Fatal(err)
		}
	}
	allow = false
	f, err := e.svc.ReadFragment(t.Context(), e.call.Scope, in.ID, fid, authz)
	if !errors.Is(err, auth.ErrForbidden) || f != nil {
		t.Errorf("revoked right: %v %v", f, err)
	}
	if calls != 4 {
		t.Errorf("the right must be checked on every read: %d calls", calls)
	}
	if _, err := e.svc.ReadFragment(t.Context(), e.call.Scope, in.ID, fid, nil); err == nil {
		t.Error("a nil authorizer must not grant access")
	}
	// Без права метаданные видны, содержимое скрыто.
	d, _ := e.svc.Get(t.Context(), e.call.Scope, in.ID, false)
	if len(d.Fragments) != 1 || !d.Fragments[0].ContentHidden || d.Fragments[0].Container != "app" {
		t.Errorf("fragments = %+v", d.Fragments)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "connection refused") || strings.Contains(string(raw), "connecting token") {
		t.Errorf("content leaked through Get: %s", raw)
	}
}

func TestExportExcludesFragmentsByDefault(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in, _ := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	ex, err := e.svc.ExportIncident(t.Context(), e.call.Scope, in.ID, false, nil)
	if err != nil || len(ex.Fragments) != 0 {
		t.Fatalf("default export: %+v %v", ex, err)
	}
	if _, err := e.svc.ExportIncident(t.Context(), e.call.Scope, in.ID, true, func() error { return auth.ErrForbidden }); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("export with fragments without the right: %v", err)
	}
	ex, err = e.svc.ExportIncident(t.Context(), e.call.Scope, in.ID, true, func() error { return nil })
	if err != nil || len(ex.Fragments) != 1 || ex.Fragments[0].Text == "" {
		t.Errorf("explicit export: %+v %v", ex, err)
	}
}

func TestScopeIsolationAtServiceLevel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in, _ := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	other := tenant.Scope{OrgID: "org1", TeamID: "team1", ClusterID: "cl-other"}
	if _, err := e.svc.Get(t.Context(), other, in.ID, true); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("get = %v", err)
	}
	if _, err := e.svc.ReadFragment(t.Context(), other, in.ID, in.Evidence[0].FragmentID, func() error { return nil }); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("read = %v", err)
	}
	if _, err := e.svc.MarkOutcome(t.Context(), incident.Caller{Scope: other, Actor: "x"}, in.ID, incident.Outcome{Result: incident.ResultResolved}); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("mark = %v", err)
	}
	if err := e.svc.Delete(t.Context(), other, in.ID); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("delete = %v", err)
	}
	if _, err := e.svc.Open(t.Context(), incident.Caller{Reader: e.r}, incident.OpenRequest{Target: target}); !errors.Is(err, incident.ErrInvalidScope) {
		t.Errorf("open without scope = %v", err)
	}
}

func TestMarkOutcome(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in, _ := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	hyp := in.Report.Hypotheses[0].ID
	for name, o := range map[string]incident.Outcome{
		"unknown result":     {Result: "done"},
		"unknown hypothesis": {Result: incident.ResultResolved, ConfirmedHypothesisID: "h999"},
		"long note":          {Result: incident.ResultResolved, Note: strings.Repeat("я", incident.MaxNoteRunes+1)},
	} {
		if _, err := e.svc.MarkOutcome(t.Context(), e.call, in.ID, o); !errors.Is(err, incident.ErrInvalidOutcome) {
			t.Errorf("%s: %v", name, err)
		}
	}
	got, err := e.svc.MarkOutcome(t.Context(), e.call, in.ID, incident.Outcome{Result: incident.ResultResolved,
		ConfirmedHypothesisID: hyp, Note: "fixed, token=SECRET42 rotated"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != incident.StatusClosed || got.Outcome == nil || got.Outcome.MarkedBy != "alice" ||
		got.Outcome.ConfirmedHypothesisID != hyp || strings.Contains(got.Outcome.Note, "SECRET") {
		t.Errorf("outcome = %+v", got.Outcome)
	}
	if _, err := e.svc.Refresh(t.Context(), e.call, in.ID); !errors.Is(err, incident.ErrClosed) {
		t.Errorf("refresh of a closed investigation: %v", err)
	}
	// Подтверждённая человеком гипотеза не меняет тип гипотезы в отчёте.
	if h := got.Report.Hypotheses[0]; h.Kind != diagnose.KindHypothesis {
		t.Errorf("hypothesis kind changed: %+v", h)
	}
}

func TestRefreshKeepsVanishedEvents(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.r.events = []map[string]any{e.r.event("BackOff", "old event", "2026-11-01T09:00:00Z")}
	in, _ := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	// Старое событие исчезло из кластера, появилось новое.
	e.r.events = []map[string]any{e.r.event("Unhealthy", "probe failed password=SECRET1", "2026-11-01T11:00:00Z")}
	got, err := e.svc.Refresh(t.Context(), e.call, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, ev := range got.Events {
		msgs = append(msgs, ev.Message)
	}
	if len(msgs) != 2 || msgs[0] != "old event" || strings.Contains(msgs[1], "SECRET") {
		t.Errorf("events = %v", msgs)
	}
	if !got.EventsUpdatedAt.After(in.EventsUpdatedAt) || got.Version != in.Version+1 {
		t.Errorf("refresh bookkeeping: %+v", got)
	}
}

func TestSaveAndDeleteFragmentExplicitly(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.r.logErr = diagnose.ErrUnavailable
	in, _ := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	if in.Evidence[0].Status != incident.EvidenceNotSaved {
		t.Fatal("setup: the automatic save must have failed")
	}
	e.r.logErr = nil
	if _, err := e.svc.SaveFragment(t.Context(), e.call, in.ID, incident.SaveFragmentRequest{Pod: "other-pod", Container: "app"}); !errors.Is(err, incident.ErrNotAffected) {
		t.Errorf("unrelated pod: %v", err)
	}
	meta, err := e.svc.SaveFragment(t.Context(), e.call, in.ID, incident.SaveFragmentRequest{Pod: "api-1", Container: "app", Previous: true, AnchorLine: 2})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Auto || meta.AnchorLine != 2 {
		t.Errorf("meta = %+v", meta)
	}
	got, _ := e.svc.Get(t.Context(), e.call.Scope, in.ID, true)
	if got.Incident.Evidence[0].Status != incident.EvidenceSaved || got.Incident.Evidence[0].FragmentID != meta.ID {
		t.Errorf("evidence = %+v", got.Incident.Evidence)
	}
	// Повторное сохранение заменяет, а не копит.
	meta2, err := e.svc.SaveFragment(t.Context(), e.call, in.ID, incident.SaveFragmentRequest{Pod: "api-1", Container: "app", Previous: true})
	if err != nil {
		t.Fatal(err)
	}
	if metas, _ := e.b.ListFragments(t.Context(), e.call.Scope, in.ID); len(metas) != 1 || metas[0].ID != meta2.ID {
		t.Errorf("fragments = %+v", metas)
	}
	// Redactor сломался: явное сохранение не записывает ничего и сообщает причину.
	e.red.err = errors.New("boom")
	_, err = e.svc.SaveFragment(t.Context(), e.call, in.ID, incident.SaveFragmentRequest{Pod: "api-1", Container: "app", Previous: true})
	if reason, ok := incident.NotSavedReason(err); !ok || reason != incident.ReasonRedactionFailed {
		t.Errorf("err = %v", err)
	}
	e.red.err = nil
	if err := e.svc.DeleteFragment(t.Context(), e.call.Scope, in.ID, meta2.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.Get(t.Context(), e.call.Scope, in.ID, true)
	if ev := got.Incident.Evidence[0]; ev.Status != incident.EvidenceNotSaved || ev.Reason != incident.ReasonDeleted || len(got.Fragments) != 0 {
		t.Errorf("after delete: %+v", got)
	}
}

func TestDisablingLogsForInvestigationDeletesFragments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in, _ := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	got, err := e.svc.SetSaveLogs(t.Context(), e.call.Scope, in.ID, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.SaveLogs || got.Evidence[0].Status != incident.EvidenceNotSaved {
		t.Errorf("incident = %+v", got)
	}
	if m, _ := e.b.ListFragments(t.Context(), e.call.Scope, in.ID); len(m) != 0 {
		t.Errorf("fragments = %+v", m)
	}
}

func TestDeleteIncidentRemovesFragments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in, _ := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target})
	fid := in.Evidence[0].FragmentID
	if err := e.svc.Delete(t.Context(), e.call.Scope, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.b.GetFragment(t.Context(), e.call.Scope, in.ID, fid); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("fragment survived: %v", err)
	}
}

func TestPurgeAppliesPerOrganizationRetention(t *testing.T) {
	t.Parallel()
	b := incident.NewFileBackend(t.TempDir())
	now := testBase.Add(60 * 24 * time.Hour)
	svc := incident.NewService(b, &fakeRedactor{}, incident.WithClock(func() time.Time { return now }))
	sc := incidenttest.Scope
	ctx := t.Context()
	// 3 дня назад: фрагмент старше срока 2 дня (организация сократила), инцидент моложе 90 дней.
	if err := b.PutIncident(ctx, incidenttest.NewIncident(sc, "i1", now.Add(-72*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := b.PutFragment(ctx, incidenttest.NewFragment(sc, "i1", "f1", now.Add(-72*time.Hour), "x")); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.Purge(ctx); err != nil || n != 0 {
		t.Fatalf("default retention (15 days) must keep a 3 days old fragment: n=%d err=%v", n, err)
	}
	if _, err := svc.SetSettings(ctx, sc.OrgID, incident.Settings{FragmentRetentionDays: 2}); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.Purge(ctx); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if _, err := b.GetIncident(ctx, sc, "i1"); err != nil {
		t.Errorf("the incident must outlive its fragment: %v", err)
	}
	if _, err := svc.SetSettings(ctx, sc.OrgID, incident.Settings{FragmentRetentionDays: 16}); !errors.Is(err, incident.ErrInvalidSettings) {
		t.Errorf("retention above 15 days accepted: %v", err)
	}
}

func TestRealRedactorKeepsSecretsOutOfTheStore(t *testing.T) {
	t.Parallel()
	b := incident.NewFileBackend(t.TempDir())
	r := newStub()
	r.log = []byte("GET /x Authorization: Bearer abcdefghijklmnop1234567890\n" +
		"dsn=postgres://admin:hunter2pass@db:5432/app\nerror: connection refused\n")
	r.message = "image pull failed password=hunter2pass"
	svc := incident.NewService(b, realRedactor())
	in, err := svc.Open(t.Context(), incident.Caller{Scope: incidenttest.Scope, Actor: "a", Reader: r}, incident.OpenRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if in.Evidence[0].Status != incident.EvidenceSaved {
		t.Fatalf("evidence = %+v", in.Evidence)
	}
	f, _ := svc.ReadFragment(t.Context(), incidenttest.Scope, in.ID, in.Evidence[0].FragmentID, func() error { return nil })
	doc, _ := json.Marshal(in)
	for _, leaked := range []string{"hunter2pass", "abcdefghijklmnop1234567890"} {
		if strings.Contains(f.Text, leaked) || strings.Contains(string(doc), leaked) {
			t.Errorf("%q leaked", leaked)
		}
	}
	if f.RedactorVersion != logredact.Version || f.Replacements == 0 {
		t.Errorf("redactor metadata: version=%d replacements=%d", f.RedactorVersion, f.Replacements)
	}
}

func TestOpenErrors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: diagnose.ResourceRef{Kind: "Pod", Namespace: "shop", Name: "ghost"}})
	if !errors.Is(err, diagnose.ErrNotFound) {
		t.Errorf("missing workload: %v", err)
	}
	_, err = e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: diagnose.ResourceRef{Kind: "Secret", Namespace: "shop", Name: "x"}})
	if !errors.Is(err, diagnose.ErrUnsupportedTarget) {
		t.Errorf("secret target: %v", err)
	}
	if p, _ := e.svc.List(t.Context(), e.call.Scope, incident.ListQuery{}); len(p.Items) != 0 {
		t.Error("a failed Open must not create an incident")
	}
}

func TestSettingsAndEnv(t *testing.T) {
	t.Parallel()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, tc := range []struct {
		name string
		env  map[string]string
		ok   bool
		want func(incident.Config) bool
	}{
		{"defaults", nil, true, func(c incident.Config) bool {
			return c.Defaults.FragmentRetentionDays == 15 && c.Defaults.IncidentRetentionDays == 90 && !c.Defaults.DisableLogEvidence &&
				len(c.Defaults.ExcludedNamespaces) == 0 && c.PurgeInterval == time.Hour
		}},
		{"all set", map[string]string{incident.EnvLogEvidence: "off", incident.EnvFragmentRetention: "3",
			incident.EnvRetention: "30", incident.EnvExcludedNS: "vault, kube-system,vault", incident.EnvPurgeInterval: "10m"}, true,
			func(c incident.Config) bool {
				return c.Defaults.DisableLogEvidence && c.Defaults.FragmentRetentionDays == 3 && c.Defaults.IncidentRetentionDays == 30 &&
					fmt.Sprint(c.Defaults.ExcludedNamespaces) == "[kube-system vault]" && c.PurgeInterval == 10*time.Minute
			}},
		{"fragments above 15 days", map[string]string{incident.EnvFragmentRetention: "30"}, false, nil},
		{"incidents above 365 days", map[string]string{incident.EnvRetention: "400"}, false, nil},
		{"zero days", map[string]string{incident.EnvRetention: "-1"}, false, nil},
		{"not a number", map[string]string{incident.EnvRetention: "many"}, false, nil},
		{"bad switch", map[string]string{incident.EnvLogEvidence: "maybe"}, false, nil},
		{"interval too small", map[string]string{incident.EnvPurgeInterval: "1s"}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := incident.ConfigFromEnv(env(tc.env))
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if tc.ok && !tc.want(c) {
				t.Errorf("config = %+v", c)
			}
		})
	}
}

func TestLogsAllowed(t *testing.T) {
	t.Parallel()
	st := incident.Settings{ExcludedNamespaces: []string{"vault"}}
	if ok, _ := st.LogsAllowed("shop"); !ok {
		t.Error("shop must be allowed")
	}
	if ok, r := st.LogsAllowed("vault"); ok || r != incident.ReasonDisabledNamespace {
		t.Errorf("vault: %v %q", ok, r)
	}
	if ok, r := (incident.Settings{DisableLogEvidence: true}).LogsAllowed("shop"); ok || r != incident.ReasonDisabledOrg {
		t.Errorf("switch: %v %q", ok, r)
	}
}

func TestListThroughService(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for range 3 {
		if _, err := e.svc.Open(t.Context(), e.call, incident.OpenRequest{Target: target, DisableLogs: true}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := e.svc.List(t.Context(), e.call.Scope, incident.ListQuery{Limit: 2})
	if err != nil || len(p.Items) != 2 || p.NextCursor == "" {
		t.Fatalf("page = %+v %v", p, err)
	}
	p2, _ := e.svc.List(t.Context(), e.call.Scope, incident.ListQuery{Limit: 2, Cursor: p.NextCursor})
	if len(p2.Items) != 1 || p2.Items[0].ID == p.Items[0].ID {
		t.Errorf("page 2 = %+v", p2)
	}
}

var _ = context.Background
