// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/24DSO-lab/kubron/internal/target"
	"github.com/24DSO-lab/kubron/internal/target/targettest"
)

// Property: при любой последовательности Update/Deactivate/stale-попыток revision монотонна, растёт ровно на 1 за
// принятую мутацию, не меняется при отказе, а Owner/CreatedBy/CreatedAt неизменны.
func TestPropertyRevisionIsMonotonicAndOwnerImmutable(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		b := targettest.NewMemoryBackend()
		ctx := context.Background()
		owner := []target.OwnerScope{orgA, teamA1}[rng.Intn(2)]
		g0, err := b.CreateGroup(ctx, targettest.ValidGroup("g", owner))
		ok(t, err)
		model := g0
		for step := 0; step < 80; step++ {
			expected := model.Revision
			switch rng.Intn(4) {
			case 0: // устаревший или нулевой/будущий ExpectedRevision
				expected = []uint64{0, model.Revision + 1, model.Revision + 7}[rng.Intn(3)]
				if model.Revision > 1 && rng.Intn(2) == 0 {
					expected = model.Revision - 1
				}
			}
			var got target.Group
			var err error
			if rng.Intn(10) == 0 {
				got, err = b.DeactivateGroup(ctx, target.DeactivateGroupRequest{ID: "g", ExpectedRevision: expected, Actor: user, At: t0})
			} else {
				got, err = b.UpdateGroup(ctx, target.UpdateGroupRequest{ID: "g", ExpectedRevision: expected,
					Content: targettest.ValidContent(fmt.Sprintf("n%d", step)), Actor: user, At: t0})
			}
			cur, gerr := b.GetGroup(ctx, viewerFor(owner), "g")
			ok(t, gerr)
			if err == nil {
				if expected != model.Revision || model.State != target.GroupActive {
					t.Fatalf("seed %d: mutation accepted with expected=%d current=%d state=%s", seed, expected, model.Revision, model.State)
				}
				if got.Revision != model.Revision+1 || cur.Revision != model.Revision+1 {
					t.Fatalf("seed %d: revision %d -> %d (not exactly +1)", seed, model.Revision, got.Revision)
				}
			} else if cur.Revision != model.Revision || cur.Name != model.Name || cur.State != model.State {
				t.Fatalf("seed %d: rejected mutation (%v) changed state", seed, err)
			}
			if cur.Owner != owner || cur.CreatedBy != g0.CreatedBy || !cur.CreatedAt.Equal(g0.CreatedAt) {
				t.Fatalf("seed %d: immutable fields changed", seed)
			}
			if cur.Revision < model.Revision {
				t.Fatalf("seed %d: revision went backwards", seed)
			}
			model = cur
		}
	}
}

func viewerFor(o target.OwnerScope) target.Viewer {
	return target.Viewer{Scopes: []target.OwnerScope{o}}
}

func randomReport(rng *rand.Rand) target.ResolutionReport {
	nClusters := 1 + rng.Intn(3)
	rep := target.ResolutionReport{GroupID: "g1", GroupRevision: uint64(1 + rng.Intn(3)), Owner: orgA, ResolvedAt: t0, Outcome: target.OutcomeComplete}
	for i := 0; i < nClusters; i++ {
		c := fmt.Sprintf("c%d", i)
		n := rng.Intn(4)
		rep.Coverage = append(rep.Coverage, target.ClusterCoverage{ClusterID: c, Outcome: target.OutcomeComplete, Targets: n})
		rep.Owners = append(rep.Owners, target.OwnerObservation{ClusterID: c, Owner: orgA})
		for j := 0; j < n; j++ {
			rep.Targets = append(rep.Targets, target.ResolvedTarget{
				Identity:        target.TargetIdentity{ClusterID: c, Version: "v1", Kind: "ConfigMap", Namespace: "ns", Name: fmt.Sprintf("n%d", j), UID: fmt.Sprintf("u-%s-%d", c, j)},
				ResourceVersion: fmt.Sprint(rng.Intn(100)),
			})
		}
	}
	return rep
}

// Property: NewSnapshot успешен только для полного report'а с UID у каждой цели; любая порча (partial, issue,
// пустой UID, дубликат, отсутствие owner observation) даёт отказ.
func TestPropertyOnlyCompleteReportsBecomeSnapshots(t *testing.T) {
	for seed := int64(1); seed <= 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		rep := randomReport(rng)
		if _, err := target.NewSnapshot(rep, "ts-1", user); err != nil {
			t.Fatalf("seed %d: valid complete report rejected: %v", seed, err)
		}
		corrupt := rep.Clone()
		switch rng.Intn(7) {
		case 0:
			corrupt.Outcome = target.OutcomePartial
		case 1:
			corrupt.Coverage[rng.Intn(len(corrupt.Coverage))].Outcome = target.OutcomePartial
		case 2:
			corrupt.Issues = []target.Issue{{ClusterID: "c0", Reason: target.ReasonTargetNotFound, Count: 1}}
		case 3:
			if len(corrupt.Targets) == 0 {
				continue
			}
			corrupt.Targets[rng.Intn(len(corrupt.Targets))].Identity.UID = ""
		case 4:
			if len(corrupt.Targets) == 0 {
				continue
			}
			corrupt.Targets = append(corrupt.Targets, corrupt.Targets[0]) // дубликат
		case 5:
			corrupt.Owners = corrupt.Owners[:len(corrupt.Owners)-1]
		case 6:
			corrupt.Coverage = nil
		}
		s, err := target.NewSnapshot(corrupt, "ts-1", user)
		if err == nil {
			t.Fatalf("seed %d: corrupted report became a snapshot: %+v", seed, s)
		}
		if !errors.Is(err, target.ErrIncomplete) && !errors.Is(err, target.ErrInvalid) {
			t.Fatalf("seed %d: unexpected error %v", seed, err)
		}
	}
}

func randomSelector(rng *rand.Rand) target.SelectorDefinition {
	pick := func(xs ...string) string { return xs[rng.Intn(len(xs))] }
	var sel target.SelectorDefinition
	for i, n := 0, rng.Intn(4); i < n; i++ {
		sel.Members = append(sel.Members, target.ObjectRef{
			ClusterID: pick("c1", "c2", "c3"), Version: "v1", Kind: pick("Pod", "ConfigMap"),
			Namespace: pick("a", "b", ""), Name: pick("x", "y", "z")})
	}
	for i, n := 0, rng.Intn(3); i < n; i++ {
		q := target.QuerySelector{Version: "v1", Kind: pick("Pod", "Node")}
		for j, m := 0, 1+rng.Intn(3); j < m; j++ {
			q.ClusterIDs = append(q.ClusterIDs, pick("c1", "c2", "c3"))
		}
		for j, m := 0, rng.Intn(3); j < m; j++ {
			q.Namespaces = append(q.Namespaces, pick("a", "b", "c"))
		}
		if rng.Intn(2) == 0 {
			q.Labels.MatchExpressions = []target.LabelRequirement{{Key: pick("k1", "k2"), Operator: target.OpIn, Values: []string{pick("1", "2"), pick("1", "2")}}}
		}
		sel.Queries = append(sel.Queries, q)
	}
	return sel
}

// Property: Canonical идемпотентна, сохраняет валидность и не зависит от порядка элементов.
func TestPropertyCanonicalSelector(t *testing.T) {
	lim := target.DefaultLimits()
	for seed := int64(1); seed <= 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sel := randomSelector(rng)
		c := sel.Canonical()
		if !c.Equal(c.Canonical()) || !sel.Equal(c) {
			t.Fatalf("seed %d: canonical form is not idempotent", seed)
		}
		if (sel.Validate(lim) == nil) != (c.Validate(lim) == nil) {
			t.Fatalf("seed %d: canonicalization changed validity", seed)
		}
		shuffled := sel.Canonical()
		rng.Shuffle(len(shuffled.Members), func(i, j int) { shuffled.Members[i], shuffled.Members[j] = shuffled.Members[j], shuffled.Members[i] })
		rng.Shuffle(len(shuffled.Queries), func(i, j int) { shuffled.Queries[i], shuffled.Queries[j] = shuffled.Queries[j], shuffled.Queries[i] })
		if !shuffled.Equal(sel) {
			t.Fatalf("seed %d: element order changed meaning", seed)
		}
	}
}

// Fuzz: валидация произвольного ввода не паникует, а принятый selector остаётся в серверных пределах.
func FuzzSelectorValidate(f *testing.F) {
	f.Add("c1", "v1", "Pod", "default", "app", "web", "metadata.name", "x")
	f.Add("", "", "", "", "", "", "", "")
	f.Add("c/1", "V1", "po d", "NS", "-k", "v v", "spec.x", "a b")
	lim := target.DefaultLimits()
	f.Fuzz(func(t *testing.T, cluster, version, kind, ns, lk, lv, field, fv string) {
		sel := target.SelectorDefinition{Queries: []target.QuerySelector{{
			ClusterIDs: []string{cluster}, Version: version, Kind: kind, Namespaces: []string{ns},
			Labels: target.LabelSelector{MatchLabels: map[string]string{lk: lv}},
			Fields: []target.FieldRequirement{{Field: field, Value: fv}},
		}}}
		if sel.Validate(lim) != nil {
			return
		}
		c := sel.Canonical()
		if c.Validate(lim) != nil || !c.Equal(sel) {
			t.Fatalf("accepted selector is unstable under canonicalization: %+v", sel)
		}
		if len(c.ClusterIDs()) > lim.MaxClusters {
			t.Fatal("limit bypass")
		}
	})
}
