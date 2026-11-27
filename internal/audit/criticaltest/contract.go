// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package criticaltest — переиспользуемый контрактный набор тестов для реализаций audit.CriticalRecorder
// (File backend; PostgreSQL-адаптер интегратора подключает тот же набор).
package criticaltest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
)

// Factory создаёт чистый recorder для одного теста.
type Factory func(t *testing.T) audit.CriticalRecorder

var base = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

// Intent возвращает валидный intent; EventID и MutationID производны от id.
func Intent(org, id string) audit.CriticalIntent {
	return audit.CriticalIntent{
		EventID: "ev-" + id, MutationID: "mut-" + id, RequestDigest: "sha256:" + strings.Repeat("a", 64),
		ActorKind: "user", ActorID: "u-1", PrincipalKind: "technical", PrincipalID: "p-1",
		OrgID: org, TeamID: "t-1", ClusterID: "c-1", Action: "identity.create",
		ResourceType: "principal", ResourceID: "res-" + id, ExpectedRevision: 3, OccurredAt: base,
	}
}

// Outcome возвращает валидный outcome для intent id.
func Outcome(id string, r audit.CriticalResult) audit.CriticalOutcome {
	o := audit.CriticalOutcome{EventID: "ev-" + id, Result: r, ResourceRevision: 4, CompletedAt: base.Add(time.Second)}
	if r != audit.CriticalSucceeded {
		o.ReasonCode = "cas_conflict"
	}
	return o
}

// Run выполняет контрактный набор.
func Run(t *testing.T, newRecorder Factory) {
	ctx := context.Background()
	sub := func(name string, fn func(t *testing.T, r audit.CriticalRecorder)) {
		t.Run(name, func(t *testing.T) { fn(t, newRecorder(t)) })
	}

	sub("validation", func(t *testing.T, r audit.CriticalRecorder) {
		mut := map[string]func(*audit.CriticalIntent){
			"empty event":      func(i *audit.CriticalIntent) { i.EventID = "" },
			"empty org":        func(i *audit.CriticalIntent) { i.OrgID = "" },
			"empty digest":     func(i *audit.CriticalIntent) { i.RequestDigest = "" },
			"newline in field": func(i *audit.CriticalIntent) { i.ResourceID = "a\nb" },
			"space in field":   func(i *audit.CriticalIntent) { i.Action = "do it" },
			"too long":         func(i *audit.CriticalIntent) { i.ActorID = strings.Repeat("x", 129) },
			"half principal":   func(i *audit.CriticalIntent) { i.PrincipalID = "" },
			"zero time":        func(i *audit.CriticalIntent) { i.OccurredAt = time.Time{} },
		}
		for name, m := range mut {
			in := Intent("org-a", "v")
			m(&in)
			if err := r.AppendIntent(ctx, in); !errors.Is(err, audit.ErrCriticalInvalid) {
				t.Errorf("%s: want ErrCriticalInvalid, got %v", name, err)
			}
		}
		if p, _ := r.ListPending(ctx, "org-a", 10); len(p) != 0 {
			t.Fatalf("invalid intents must not be stored: %d", len(p))
		}
		if err := r.AppendIntent(ctx, Intent("org-a", "ok")); err != nil {
			t.Fatal(err)
		}
		bad := []audit.CriticalOutcome{
			{EventID: "ev-ok", Result: "bogus", CompletedAt: base},
			{EventID: "ev-ok", Result: audit.CriticalFailed, CompletedAt: base},  // нет reason
			{EventID: "ev-ok", Result: audit.CriticalUnknown, CompletedAt: base}, // нет reason
			{EventID: "ev-ok", Result: audit.CriticalFailed, ReasonCode: "Has Space", CompletedAt: base},
			{EventID: "ev-ok", Result: audit.CriticalSucceeded},  // нет времени
			{Result: audit.CriticalSucceeded, CompletedAt: base}, // нет EventID
		}
		for i, o := range bad {
			if err := r.AppendOutcome(ctx, o); !errors.Is(err, audit.ErrCriticalInvalid) {
				t.Errorf("outcome %d: want ErrCriticalInvalid, got %v", i, err)
			}
		}
		if _, err := r.GetOutcome(ctx, "", "ev-ok"); !errors.Is(err, audit.ErrCriticalInvalid) {
			t.Errorf("empty org: %v", err)
		}
		if _, err := r.ListPending(ctx, "", 1); !errors.Is(err, audit.ErrCriticalInvalid) {
			t.Errorf("empty org list: %v", err)
		}
	})

	sub("errors do not echo field values", func(t *testing.T, r audit.CriticalRecorder) {
		const canary = "SECRET-canary-9f3"
		in := Intent("org-a", "e")
		in.ResourceID = canary + " bad space"
		err := r.AppendIntent(ctx, in)
		if !errors.Is(err, audit.ErrCriticalInvalid) || strings.Contains(err.Error(), canary) {
			t.Fatalf("error must be invalid and must not echo values: %v", err)
		}
		good := Intent("org-a", "e2")
		good.ResourceID = canary
		if err := r.AppendIntent(ctx, good); err != nil {
			t.Fatal(err)
		}
		other := good
		other.ActorID = "u-2"
		if err := r.AppendIntent(ctx, other); err == nil || strings.Contains(err.Error(), canary) {
			t.Fatalf("conflict error must not echo values: %v", err)
		}
	})

	sub("intent idempotency and conflict matrix", func(t *testing.T, r audit.CriticalRecorder) {
		in := Intent("org-a", "1")
		if err := r.AppendIntent(ctx, in); err != nil {
			t.Fatal(err)
		}
		if err := r.AppendIntent(ctx, in); err != nil {
			t.Fatalf("same payload must be idempotent: %v", err)
		}
		// То же мгновение в другой зоне и с monotonic-частью — тот же канонический payload.
		same := in
		same.OccurredAt = in.OccurredAt.In(time.FixedZone("x", 7*3600))
		if err := r.AppendIntent(ctx, same); err != nil {
			t.Fatalf("same instant must be idempotent: %v", err)
		}
		if p, _ := r.ListPending(ctx, "org-a", 10); len(p) != 1 {
			t.Fatalf("idempotent repeat must not duplicate: %d", len(p))
		}
		diffs := map[string]func(*audit.CriticalIntent){
			"mutation":   func(i *audit.CriticalIntent) { i.MutationID = "other" },
			"digest":     func(i *audit.CriticalIntent) { i.RequestDigest = "sha256:b" },
			"actor kind": func(i *audit.CriticalIntent) { i.ActorKind = "service" },
			"actor":      func(i *audit.CriticalIntent) { i.ActorID = "u-9" },
			"principal":  func(i *audit.CriticalIntent) { i.PrincipalID = "p-9" },
			"org":        func(i *audit.CriticalIntent) { i.OrgID = "org-b" },
			"team":       func(i *audit.CriticalIntent) { i.TeamID = "t-9" },
			"cluster":    func(i *audit.CriticalIntent) { i.ClusterID = "c-9" },
			"action":     func(i *audit.CriticalIntent) { i.Action = "identity.delete" },
			"res type":   func(i *audit.CriticalIntent) { i.ResourceType = "grant" },
			"res id":     func(i *audit.CriticalIntent) { i.ResourceID = "res-9" },
			"revision":   func(i *audit.CriticalIntent) { i.ExpectedRevision++ },
			"time":       func(i *audit.CriticalIntent) { i.OccurredAt = i.OccurredAt.Add(time.Nanosecond) },
		}
		for name, m := range diffs {
			c := in
			m(&c)
			if err := r.AppendIntent(ctx, c); !errors.Is(err, audit.ErrCriticalConflict) {
				t.Errorf("%s: want ErrCriticalConflict, got %v", name, err)
			}
		}
	})

	sub("outcome requires intent and is unique", func(t *testing.T, r audit.CriticalRecorder) {
		if err := r.AppendOutcome(ctx, Outcome("none", audit.CriticalSucceeded)); !errors.Is(err, audit.ErrCriticalNotFound) {
			t.Fatalf("outcome without intent: %v", err)
		}
		if err := r.AppendIntent(ctx, Intent("org-a", "1")); err != nil {
			t.Fatal(err)
		}
		if _, err := r.GetOutcome(ctx, "org-a", "ev-1"); !errors.Is(err, audit.ErrCriticalNotFound) {
			t.Fatalf("pending intent has no outcome: %v", err)
		}
		ok := Outcome("1", audit.CriticalSucceeded)
		if err := r.AppendOutcome(ctx, ok); err != nil {
			t.Fatal(err)
		}
		if err := r.AppendOutcome(ctx, ok); err != nil {
			t.Fatalf("identical outcome must be idempotent: %v", err)
		}
		for _, c := range []audit.CriticalOutcome{
			Outcome("1", audit.CriticalFailed),
			Outcome("1", audit.CriticalUnknown),
			func() audit.CriticalOutcome { o := ok; o.ResourceRevision = 99; return o }(),
			func() audit.CriticalOutcome { o := ok; o.CompletedAt = o.CompletedAt.Add(time.Second); return o }(),
		} {
			if err := r.AppendOutcome(ctx, c); !errors.Is(err, audit.ErrCriticalConflict) {
				t.Errorf("conflicting outcome %+v: %v", c, err)
			}
		}
		got, err := r.GetOutcome(ctx, "org-a", "ev-1")
		if err != nil || got.Result != audit.CriticalSucceeded || got.ResourceRevision != 4 {
			t.Fatalf("outcome must stay the first one: %+v %v", got, err)
		}
	})

	sub("failed and unknown outcomes are not success", func(t *testing.T, r audit.CriticalRecorder) {
		for _, res := range []audit.CriticalResult{audit.CriticalFailed, audit.CriticalUnknown} {
			id := string(res)
			if err := r.AppendIntent(ctx, Intent("org-a", id)); err != nil {
				t.Fatal(err)
			}
			if err := r.AppendOutcome(ctx, Outcome(id, res)); err != nil {
				t.Fatal(err)
			}
			got, err := r.GetOutcome(ctx, "org-a", "ev-"+id)
			if err != nil || got.Result != res || got.ReasonCode != "cas_conflict" {
				t.Fatalf("%s: %+v %v", res, got, err)
			}
		}
		if p, _ := r.ListPending(ctx, "org-a", 10); len(p) != 0 {
			t.Fatalf("intents with any outcome are not pending: %d", len(p))
		}
	})

	sub("pending list order, limit and completion", func(t *testing.T, r audit.CriticalRecorder) {
		for i := 0; i < 5; i++ {
			if err := r.AppendIntent(ctx, Intent("org-a", fmt.Sprint(i))); err != nil {
				t.Fatal(err)
			}
		}
		p, err := r.ListPending(ctx, "org-a", 3)
		if err != nil || len(p) != 3 || p[0].EventID != "ev-0" || p[2].EventID != "ev-2" {
			t.Fatalf("oldest first, limited: %+v %v", p, err)
		}
		if err := r.AppendOutcome(ctx, Outcome("0", audit.CriticalSucceeded)); err != nil {
			t.Fatal(err)
		}
		p, _ = r.ListPending(ctx, "org-a", 0)
		if len(p) != 4 || p[0].EventID != "ev-1" {
			t.Fatalf("default limit, completed leaves pending: %+v", p)
		}
		if p, _ = r.ListPending(ctx, "org-a", 1<<30); len(p) != 4 {
			t.Fatalf("huge limit is clamped, not rejected: %d", len(p))
		}
		got := p[0]
		got.ActorID = "mutated"
		if again, _ := r.ListPending(ctx, "org-a", 1); again[0].ActorID == "mutated" {
			t.Fatal("returned slice must not alias internal state")
		}
	})

	sub("tenant isolation", func(t *testing.T, r audit.CriticalRecorder) {
		if err := r.AppendIntent(ctx, Intent("org-a", "a")); err != nil {
			t.Fatal(err)
		}
		if err := r.AppendIntent(ctx, Intent("org-b", "b")); err != nil {
			t.Fatal(err)
		}
		if err := r.AppendOutcome(ctx, Outcome("a", audit.CriticalSucceeded)); err != nil {
			t.Fatal(err)
		}
		if _, err := r.GetOutcome(ctx, "org-b", "ev-a"); !errors.Is(err, audit.ErrCriticalNotFound) {
			t.Fatalf("foreign org must not see outcome: %v", err)
		}
		if _, err := r.GetOutcome(ctx, "org-b", "ev-nonexistent"); !errors.Is(err, audit.ErrCriticalNotFound) {
			t.Fatalf("absent: %v", err)
		}
		if _, err := r.GetOutcome(ctx, "org-a", "ev-a"); err != nil {
			t.Fatal(err)
		}
		pb, _ := r.ListPending(ctx, "org-b", 10)
		if len(pb) != 1 || pb[0].EventID != "ev-b" {
			t.Fatalf("org-b pending: %+v", pb)
		}
		if pa, _ := r.ListPending(ctx, "org-a", 10); len(pa) != 0 {
			t.Fatalf("org-a pending must be empty: %+v", pa)
		}
	})

	sub("canceled context writes nothing", func(t *testing.T, r audit.CriticalRecorder) {
		c, cancel := context.WithCancel(ctx)
		cancel()
		if err := r.AppendIntent(c, Intent("org-a", "c")); !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if p, _ := r.ListPending(ctx, "org-a", 10); len(p) != 0 {
			t.Fatal("canceled append must not persist")
		}
	})

	sub("concurrent appends", func(t *testing.T, r audit.CriticalRecorder) {
		const n = 64
		var wg sync.WaitGroup
		errs := make(chan error, 2*n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id := fmt.Sprint(i)
				org := "org-a"
				if i%2 == 1 {
					org = "org-b"
				}
				if err := r.AppendIntent(ctx, Intent(org, id)); err != nil {
					errs <- err
					return
				}
				// Повторы того же EventID параллельно — идемпотентны.
				if err := r.AppendIntent(ctx, Intent(org, id)); err != nil {
					errs <- err
				}
				if i%4 == 0 {
					if err := r.AppendOutcome(ctx, Outcome(id, audit.CriticalSucceeded)); err != nil {
						errs <- err
					}
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		pa, _ := r.ListPending(ctx, "org-a", CriticalMax)
		pb, _ := r.ListPending(ctx, "org-b", CriticalMax)
		// org-a: чётные 32, из них кратные 4 завершены (16) → 16 pending; org-b: 32 pending.
		if len(pa) != 16 || len(pb) != 32 {
			t.Fatalf("pending a=%d b=%d", len(pa), len(pb))
		}
	})
}

// CriticalMax — верхняя граница выборки (audit.CriticalMaxPending) для проверок набора.
const CriticalMax = audit.CriticalMaxPending
