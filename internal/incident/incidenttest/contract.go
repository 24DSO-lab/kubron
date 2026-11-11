// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package incidenttest — контрактные тесты incident.Backend (ADR-0026 §4): один набор запускается против файловой
// и PostgreSQL реализаций; расхождение семантики — ошибка теста, а не «особенность режима».
package incidenttest

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Scope — область по умолчанию контрактных тестов.
var Scope = tenant.Scope{OrgID: "org1", TeamID: "team1", ClusterID: "cl1"}

var base = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

// NewIncident строит инцидент для тестов: id и время открытия задаёт вызывающий.
func NewIncident(scope tenant.Scope, id string, opened time.Time) *incident.Incident {
	return &incident.Incident{
		ID: id, Scope: scope, Status: incident.StatusOpen, OpenedAt: opened, UpdatedAt: opened, OpenedBy: "alice",
		Target: diagnose.ResourceRef{Kind: "Deployment", Namespace: "shop", Name: "web"},
		Report: diagnose.Report{RuleSetVersion: 1, Symptoms: []string{"pod.crash_loop"},
			Facts: []diagnose.Fact{{Kind: diagnose.KindFact, ID: "f1", Code: "x"}}},
		Events:   []diagnose.Event{{Reason: "BackOff", Message: "back-off ünïcode ✓", Count: 2, LastSeen: opened}},
		SaveLogs: true, Evidence: []incident.Evidence{},
	}
}

// NewFragment строит фрагмент для тестов.
func NewFragment(scope tenant.Scope, incidentID, id string, created time.Time, text string) *incident.Fragment {
	return &incident.Fragment{
		FragmentMeta: incident.FragmentMeta{ID: id, IncidentID: incidentID, Scope: scope, Namespace: "shop", Pod: "web-1",
			Container: "app", Previous: true, CapturedAt: created, StartLine: 1, EndLine: 3, AnchorLine: 2, Lines: 3,
			Bytes: len(text), RedactorVersion: 1, Replacements: 2, SHA256: "abc", CreatedAt: created, CreatedBy: "alice"},
		Text: text,
	}
}

// Run выполняет контрактные тесты. newBackend возвращает ЧИСТОЕ хранилище для каждого подтеста.
func Run(t *testing.T, newBackend func(t *testing.T) incident.Backend) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(t *testing.T, b incident.Backend)
	}{
		{"incident roundtrip", roundtrip},
		{"optimistic version", versions},
		{"scope isolation", isolation},
		{"invalid scope fails closed", invalidScope},
		{"unsafe identifiers are rejected", unsafeIDs},
		{"history paging", paging},
		{"delete cascades to fragments", deleteCascade},
		{"fragments", fragments},
		{"settings", settings},
		{"purge by retention", purge},
		{"concurrent puts", concurrent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.fn(t, newBackend(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func roundtrip(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	in := NewIncident(Scope, "inc1", base)
	must(t, b.PutIncident(ctx, in))
	if in.Version != 1 {
		t.Fatalf("version after create = %d", in.Version)
	}
	got, err := b.GetIncident(ctx, Scope, "inc1")
	must(t, err)
	if got.Version != 1 || got.OpenedBy != "alice" || got.Target.Name != "web" || got.Events[0].Message != "back-off ünïcode ✓" ||
		!got.OpenedAt.Equal(base) || got.Scope != Scope || len(got.Report.Facts) != 1 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if _, err := b.GetIncident(ctx, Scope, "nope"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("missing incident: %v", err)
	}
}

func versions(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	in := NewIncident(Scope, "inc1", base)
	must(t, b.PutIncident(ctx, in))
	dup := NewIncident(Scope, "inc1", base)
	if err := b.PutIncident(ctx, dup); !errors.Is(err, incident.ErrConflict) {
		t.Errorf("create over existing: %v", err)
	}
	stale := *in
	in.Status = incident.StatusClosed
	must(t, b.PutIncident(ctx, in))
	if in.Version != 2 {
		t.Errorf("version = %d", in.Version)
	}
	if err := b.PutIncident(ctx, &stale); !errors.Is(err, incident.ErrConflict) {
		t.Errorf("stale replace: %v", err)
	}
	ghost := NewIncident(Scope, "ghost", base)
	ghost.Version = 5
	if err := b.PutIncident(ctx, ghost); !errors.Is(err, incident.ErrConflict) {
		t.Errorf("replace of a missing incident: %v", err)
	}
	got, _ := b.GetIncident(ctx, Scope, "inc1")
	if got.Status != incident.StatusClosed {
		t.Error("the stale write must not change the record")
	}
}

func isolation(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	must(t, b.PutIncident(ctx, NewIncident(Scope, "inc1", base)))
	must(t, b.PutFragment(ctx, NewFragment(Scope, "inc1", "fr1", base, "secret-ish text")))
	for name, other := range map[string]tenant.Scope{
		"other cluster": {OrgID: Scope.OrgID, TeamID: Scope.TeamID, ClusterID: "cl2"},
		"other team":    {OrgID: Scope.OrgID, TeamID: "team2", ClusterID: Scope.ClusterID},
		"other org":     {OrgID: "org2", TeamID: Scope.TeamID, ClusterID: Scope.ClusterID},
	} {
		if _, err := b.GetIncident(ctx, other, "inc1"); !errors.Is(err, incident.ErrNotFound) {
			t.Errorf("%s: get = %v, want ErrNotFound", name, err)
		}
		if err := b.DeleteIncident(ctx, other, "inc1"); !errors.Is(err, incident.ErrNotFound) {
			t.Errorf("%s: delete = %v", name, err)
		}
		if _, err := b.GetFragment(ctx, other, "inc1", "fr1"); !errors.Is(err, incident.ErrNotFound) {
			t.Errorf("%s: fragment get = %v", name, err)
		}
		if err := b.DeleteFragment(ctx, other, "inc1", "fr1"); !errors.Is(err, incident.ErrNotFound) {
			t.Errorf("%s: fragment delete = %v", name, err)
		}
		if p, err := b.ListIncidents(ctx, other, incident.ListQuery{}); err != nil || len(p.Items) != 0 {
			t.Errorf("%s: list = %+v, %v", name, p, err)
		}
		if m, err := b.ListFragments(ctx, other, "inc1"); err != nil || len(m) != 0 {
			t.Errorf("%s: list fragments = %+v, %v", name, m, err)
		}
	}
	// Тот же ID в другой области — другая запись.
	other := tenant.Scope{OrgID: "org2", TeamID: "team1", ClusterID: "cl1"}
	must(t, b.PutIncident(ctx, NewIncident(other, "inc1", base)))
	a, _ := b.GetIncident(ctx, Scope, "inc1")
	c, _ := b.GetIncident(ctx, other, "inc1")
	if a.Scope == c.Scope {
		t.Error("records of different scopes must be independent")
	}
	if _, err := b.GetIncident(ctx, Scope, "inc1"); err != nil {
		t.Errorf("own record became unreachable: %v", err)
	}
}

func invalidScope(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	for _, s := range []tenant.Scope{{}, {OrgID: "o"}, {OrgID: "o", TeamID: "t"}, {TeamID: "t", ClusterID: "c"}} {
		if err := b.PutIncident(ctx, NewIncident(s, "i", base)); !errors.Is(err, incident.ErrInvalidScope) {
			t.Errorf("put with %+v: %v", s, err)
		}
		if _, err := b.GetIncident(ctx, s, "i"); !errors.Is(err, incident.ErrInvalidScope) {
			t.Errorf("get with %+v: %v", s, err)
		}
		if _, err := b.ListIncidents(ctx, s, incident.ListQuery{}); !errors.Is(err, incident.ErrInvalidScope) {
			t.Errorf("list with %+v: %v", s, err)
		}
	}
}

func unsafeIDs(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	for _, id := range []string{"", "..", "a/b", `a\b`, "../x", ".hidden", "a b", strings.Repeat("a", 200)} {
		if err := b.PutIncident(ctx, NewIncident(Scope, id, base)); err == nil {
			t.Errorf("incident id %q accepted", id)
		}
		if _, err := b.GetIncident(ctx, Scope, id); err == nil {
			t.Errorf("incident id %q accepted on read", id)
		}
	}
	bad := tenant.Scope{OrgID: "../org", TeamID: "t", ClusterID: "c"}
	if err := b.PutIncident(ctx, NewIncident(bad, "i", base)); err == nil {
		t.Error("path traversal in scope accepted")
	}
}

func paging(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	// 7 инцидентов; два с одинаковым временем (порядок по ID), один закрытый.
	ids := []string{"a", "b", "c", "d", "e", "f", "g"}
	for i, id := range ids {
		opened := base.Add(time.Duration(i/2) * time.Minute)
		in := NewIncident(Scope, id, opened)
		if id == "c" {
			in.Status = incident.StatusClosed
			in.Outcome = &incident.Outcome{Result: incident.ResultResolved}
		}
		must(t, b.PutIncident(ctx, in))
	}
	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging does not terminate")
		}
		p, err := b.ListIncidents(ctx, Scope, incident.ListQuery{Limit: 3, Cursor: cursor})
		must(t, err)
		for _, s := range p.Items {
			got = append(got, s.ID)
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if want := "g f e d c b a"; strings.Join(got, " ") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
	closed, err := b.ListIncidents(ctx, Scope, incident.ListQuery{Status: incident.StatusClosed})
	must(t, err)
	if len(closed.Items) != 1 || closed.Items[0].ID != "c" || closed.Items[0].Result != incident.ResultResolved {
		t.Errorf("closed = %+v", closed.Items)
	}
	if _, err := b.ListIncidents(ctx, Scope, incident.ListQuery{Cursor: "!!"}); err == nil {
		t.Error("garbage cursor accepted")
	}
	empty, err := b.ListIncidents(ctx, tenant.Scope{OrgID: "x", TeamID: "y", ClusterID: "z"}, incident.ListQuery{})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != "" {
		t.Errorf("empty page = %+v, %v", empty, err)
	}
	exact, _ := b.ListIncidents(ctx, Scope, incident.ListQuery{Limit: 7})
	if len(exact.Items) != 7 || exact.NextCursor != "" {
		t.Errorf("a full page without a remainder must have no cursor: %d %q", len(exact.Items), exact.NextCursor)
	}
}

func deleteCascade(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	must(t, b.PutIncident(ctx, NewIncident(Scope, "inc1", base)))
	must(t, b.PutIncident(ctx, NewIncident(Scope, "inc2", base)))
	must(t, b.PutFragment(ctx, NewFragment(Scope, "inc1", "fr1", base, "one")))
	must(t, b.PutFragment(ctx, NewFragment(Scope, "inc1", "fr2", base, "two")))
	must(t, b.PutFragment(ctx, NewFragment(Scope, "inc2", "fr3", base, "three")))
	must(t, b.DeleteIncident(ctx, Scope, "inc1"))
	if _, err := b.GetFragment(ctx, Scope, "inc1", "fr1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("fragment survived the incident: %v", err)
	}
	if m, _ := b.ListFragments(ctx, Scope, "inc1"); len(m) != 0 {
		t.Errorf("fragments survived: %v", m)
	}
	if _, err := b.GetFragment(ctx, Scope, "inc2", "fr3"); err != nil {
		t.Errorf("another incident's fragment was deleted: %v", err)
	}
	if err := b.DeleteIncident(ctx, Scope, "inc1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func fragments(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	text := "line one\nпривет мир ✓\n{\"k\":\"<v>\"}"
	f := NewFragment(Scope, "inc1", "fr1", base, text)
	to := base.Add(time.Minute)
	f.ToTime = &to
	must(t, b.PutFragment(ctx, f))
	got, err := b.GetFragment(ctx, Scope, "inc1", "fr1")
	must(t, err)
	if got.Text != text || got.Pod != "web-1" || !got.Previous || got.Replacements != 2 || got.RedactorVersion != 1 ||
		got.SHA256 != "abc" || got.ToTime == nil || !got.ToTime.Equal(to) || got.FromTime != nil || got.AnchorLine != 2 {
		t.Errorf("fragment mismatch: %+v", got)
	}
	// Замена по ID.
	f.Text, f.Bytes = "replaced", 8
	must(t, b.PutFragment(ctx, f))
	got, _ = b.GetFragment(ctx, Scope, "inc1", "fr1")
	if got.Text != "replaced" {
		t.Errorf("replace failed: %q", got.Text)
	}
	must(t, b.PutFragment(ctx, NewFragment(Scope, "inc1", "fr0", base.Add(-time.Hour), "older")))
	metas, err := b.ListFragments(ctx, Scope, "inc1")
	must(t, err)
	if len(metas) != 2 || metas[0].ID != "fr0" || metas[1].ID != "fr1" {
		t.Errorf("metas = %+v", metas)
	}
	// Фрагмент можно положить до документа инцидента (порядок записи Open).
	if _, err := b.GetIncident(ctx, Scope, "inc1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("fragment must not create an incident: %v", err)
	}
	must(t, b.DeleteFragment(ctx, Scope, "inc1", "fr1"))
	if err := b.DeleteFragment(ctx, Scope, "inc1", "fr1"); !errors.Is(err, incident.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func settings(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	if _, ok, err := b.Settings(ctx, "org1"); err != nil || ok {
		t.Fatalf("fresh settings: ok=%v err=%v", ok, err)
	}
	want := incident.Settings{DisableLogEvidence: true, ExcludedNamespaces: []string{"kube-system", "vault"},
		FragmentRetentionDays: 5, IncidentRetentionDays: 200}
	must(t, b.PutSettings(ctx, "org1", want))
	got, ok, err := b.Settings(ctx, "org1")
	must(t, err)
	if !ok || !got.DisableLogEvidence || got.FragmentRetentionDays != 5 || got.IncidentRetentionDays != 200 ||
		fmt.Sprint(got.ExcludedNamespaces) != "[kube-system vault]" {
		t.Errorf("settings = %+v", got)
	}
	if _, ok, _ := b.Settings(ctx, "org2"); ok {
		t.Error("settings leaked between organizations")
	}
	for _, bad := range []incident.Settings{
		{FragmentRetentionDays: 16, IncidentRetentionDays: 90},
		{FragmentRetentionDays: 0 - 1, IncidentRetentionDays: 90},
		{FragmentRetentionDays: 15, IncidentRetentionDays: 366},
	} {
		if err := b.PutSettings(ctx, "org1", bad); !errors.Is(err, incident.ErrInvalidSettings) {
			t.Errorf("settings %+v accepted: %v", bad, err)
		}
	}
	got, _, _ = b.Settings(ctx, "org1")
	if got.FragmentRetentionDays != 5 {
		t.Error("rejected settings changed the stored ones")
	}
}

func purge(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	now := base.Add(100 * 24 * time.Hour)
	old, fresh := now.Add(-20*24*time.Hour), now.Add(-time.Hour)
	// inc-old: давно не обновлялся → удаляется вместе с фрагментами; inc-new: свежий, но со старым фрагментом.
	must(t, b.PutIncident(ctx, NewIncident(Scope, "inc-old", now.Add(-95*24*time.Hour))))
	must(t, b.PutFragment(ctx, NewFragment(Scope, "inc-old", "f1", fresh, "x")))
	must(t, b.PutIncident(ctx, NewIncident(Scope, "inc-new", fresh)))
	must(t, b.PutFragment(ctx, NewFragment(Scope, "inc-new", "f-old", old, "old")))
	must(t, b.PutFragment(ctx, NewFragment(Scope, "inc-new", "f-new", fresh, "new")))
	foreign := tenant.Scope{OrgID: "org2", TeamID: "t", ClusterID: "c"}
	must(t, b.PutIncident(ctx, NewIncident(foreign, "other", now.Add(-200*24*time.Hour))))

	orgs, err := b.OrgIDs(ctx)
	must(t, err)
	if fmt.Sprint(orgs) != "[org1 org2]" {
		t.Errorf("orgs = %v", orgs)
	}
	res, err := b.Purge(ctx, "org1", now.AddDate(0, 0, -15), now.AddDate(0, 0, -90))
	must(t, err)
	if res.Incidents != 1 || res.Fragments != 2 { // f1 каскадом + f-old по сроку
		t.Errorf("purge = %+v", res)
	}
	if _, err := b.GetIncident(ctx, Scope, "inc-old"); !errors.Is(err, incident.ErrNotFound) {
		t.Error("expired incident survived")
	}
	if _, err := b.GetFragment(ctx, Scope, "inc-old", "f1"); !errors.Is(err, incident.ErrNotFound) {
		t.Error("fragment of an expired incident survived")
	}
	if _, err := b.GetFragment(ctx, Scope, "inc-new", "f-old"); !errors.Is(err, incident.ErrNotFound) {
		t.Error("expired fragment survived")
	}
	if _, err := b.GetFragment(ctx, Scope, "inc-new", "f-new"); err != nil {
		t.Errorf("fresh fragment removed: %v", err)
	}
	if _, err := b.GetIncident(ctx, foreign, "other"); err != nil {
		t.Errorf("purge of org1 touched org2: %v", err)
	}
	if again, _ := b.Purge(ctx, "org1", now.AddDate(0, 0, -15), now.AddDate(0, 0, -90)); again != (incident.PurgeResult{}) {
		t.Errorf("second purge = %+v", again)
	}
}

func concurrent(t *testing.T, b incident.Backend) {
	ctx := t.Context()
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			id := fmt.Sprintf("inc%02d", i)
			if err := b.PutIncident(ctx, NewIncident(Scope, id, base.Add(time.Duration(i)*time.Second))); err != nil {
				t.Errorf("put %s: %v", id, err)
			}
			if err := b.PutFragment(ctx, NewFragment(Scope, id, "fr", base, "t")); err != nil {
				t.Errorf("fragment %s: %v", id, err)
			}
		})
	}
	wg.Wait()
	p, err := b.ListIncidents(ctx, Scope, incident.ListQuery{Limit: 50})
	must(t, err)
	if len(p.Items) != 12 {
		t.Errorf("items = %d", len(p.Items))
	}
	// Одновременная замена одного документа: ровно одна запись с данной версией побеждает.
	in, _ := b.GetIncident(ctx, Scope, "inc00")
	var ok, conflicts int
	var mu sync.Mutex
	for range 8 {
		cp := *in
		wg.Go(func() {
			err := b.PutIncident(ctx, &cp)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if errors.Is(err, incident.ErrConflict) {
				conflicts++
			} else {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()
	if ok != 1 || conflicts != 7 {
		t.Errorf("ok=%d conflicts=%d, want exactly one winner", ok, conflicts)
	}
}
