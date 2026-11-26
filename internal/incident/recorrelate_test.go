// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/incident/incidenttest"
)

func TestRecomputeCorrelationWritesNewSnapshot(t *testing.T) {
	r := &corrReader{stubReader: newStub()}
	e, id, _ := openWith(t, r)
	before, err := e.svc.Get(t.Context(), incidenttest.Scope, id, false)
	if err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.RecomputeCorrelation(t.Context(), e.call, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Correlation == nil || got.Correlation.Best() == nil {
		t.Fatalf("снимка нет: %+v", got.Correlation)
	}
	if !got.Correlation.ObservedAt.After(before.Incident.Correlation.ObservedAt) {
		t.Fatalf("время наблюдения не обновилось: %v → %v", before.Incident.Correlation.ObservedAt, got.Correlation.ObservedAt)
	}
	stored, err := e.svc.Get(t.Context(), incidenttest.Scope, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Incident.Correlation.ObservedAt != got.Correlation.ObservedAt {
		t.Fatal("снимок не записан в хранилище")
	}
}

// Events, отчёт и привязка к проблеме при пересчёте не меняются.
func TestRecomputeCorrelationKeepsEventsAndReport(t *testing.T) {
	e, id, _ := openWith(t, &corrReader{stubReader: newStub()})
	before, err := e.svc.Get(t.Context(), incidenttest.Scope, id, false)
	if err != nil {
		t.Fatal(err)
	}
	// Кластер «изменился» после открытия: появились бы новые Events, но пересчёт снимок Events не обновляет.
	e.r.events = append(e.r.events, map[string]any{"reason": "Late", "message": "later"})

	if _, err := e.svc.RecomputeCorrelation(t.Context(), e.call, id); err != nil {
		t.Fatal(err)
	}
	after, err := e.svc.Get(t.Context(), incidenttest.Scope, id, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]any{
		"Events":          {before.Incident.Events, after.Incident.Events},
		"EventsUpdatedAt": {before.Incident.EventsUpdatedAt, after.Incident.EventsUpdatedAt},
		"Report":          {before.Incident.Report, after.Incident.Report},
		"Evidence":        {before.Incident.Evidence, after.Incident.Evidence},
		"ProblemID":       {before.Incident.ProblemID, after.Incident.ProblemID},
		"Status":          {before.Incident.Status, after.Incident.Status},
	} {
		a, _ := json.Marshal(pair[0])
		b, _ := json.Marshal(pair[1])
		if string(a) != string(b) {
			t.Errorf("%s изменилось:\n%s\n%s", name, a, b)
		}
	}
	if !after.Incident.UpdatedAt.After(before.Incident.UpdatedAt) {
		t.Error("UpdatedAt не обновлён")
	}
}

// sameData — снимок без времени наблюдения (своего и у фактов): на одних данных два пересчёта отличаются только им.
func sameData(t *testing.T, c *correlation.Result) string {
	t.Helper()
	cp := *c
	cp.ObservedAt = time.Time{}
	cp.Facts = slices.Clone(c.Facts)
	for i := range cp.Facts {
		cp.Facts[i].ObservedAt = time.Time{}
	}
	raw, err := json.Marshal(&cp)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRecomputeCorrelationIdempotent(t *testing.T) {
	e, id, _ := openWith(t, &corrReader{stubReader: newStub()})
	first, err := e.svc.RecomputeCorrelation(t.Context(), e.call, id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.RecomputeCorrelation(t.Context(), e.call, id)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := sameData(t, first.Correlation), sameData(t, second.Correlation); a != b {
		t.Fatalf("повторный пересчёт дал другой снимок:\n%s\n%s", a, b)
	}
	if !second.Correlation.ObservedAt.After(first.Correlation.ObservedAt) {
		t.Fatal("время наблюдения повторного пересчёта не новее")
	}
}

func TestRecomputeCorrelationClosedIsRefused(t *testing.T) {
	e, id, _ := openWith(t, &corrReader{stubReader: newStub()})
	if _, err := e.svc.MarkOutcome(t.Context(), e.call, id, incident.Outcome{Result: incident.ResultResolved}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RecomputeCorrelation(t.Context(), e.call, id); !errors.Is(err, incident.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func TestRecomputeCorrelationUnknownIncidentAndScope(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.RecomputeCorrelation(t.Context(), e.call, "nope"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	bad := e.call
	bad.Scope.ClusterID = ""
	if _, err := e.svc.RecomputeCorrelation(t.Context(), bad, "x"); err == nil {
		t.Fatal("пустая область доступа принята")
	}
}

// Правила упали: расследование и прежний снимок остаются, пользователь получает ошибку.
func TestRecomputeCorrelationFailureKeepsOldSnapshot(t *testing.T) {
	r := &corrReader{stubReader: newStub()}
	e, id, _ := openWith(t, r)
	old, err := e.svc.Get(t.Context(), incidenttest.Scope, id, false)
	if err != nil {
		t.Fatal(err)
	}
	r.podListPanic = true
	if _, err := e.svc.RecomputeCorrelation(t.Context(), e.call, id); !errors.Is(err, diagnose.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	now, err := e.svc.Get(t.Context(), incidenttest.Scope, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if now.Incident.Correlation == nil || now.Incident.Correlation.ObservedAt != old.Incident.Correlation.ObservedAt {
		t.Fatalf("прежний снимок затёрт: %+v", now.Incident.Correlation)
	}
}

// Недоступный источник даёт неполный снимок, а не ошибку: неполнота записана в самом снимке.
func TestRecomputeCorrelationPartialSnapshot(t *testing.T) {
	r := &corrReader{stubReader: newStub()}
	e, id, _ := openWith(t, r)
	r.podListErr = diagnose.ErrForbidden
	got, err := e.svc.RecomputeCorrelation(t.Context(), e.call, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Correlation == nil || len(got.Correlation.Missing) == 0 {
		t.Fatalf("неполнота не отражена: %+v", got.Correlation)
	}
}
