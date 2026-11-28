// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/24DSO-lab/kubron/internal/authorization"
)

func TestTargetRef_Validate(t *testing.T) {
	obj := authorization.TargetRef{ClusterID: "c-1", Group: "apps", Version: "v1", Resource: "deployments", Namespace: "ns", Name: "web", UID: "uid-1"}
	valid := map[string]authorization.TargetRef{
		"cluster":         {ClusterID: "c-1"},
		"namespace":       {ClusterID: "c-1", Namespace: "ns"},
		"collection":      {ClusterID: "c-1", Group: "apps", Version: "v1", Resource: "deployments", Namespace: "ns"},
		"core collection": {ClusterID: "c-1", Version: "v1", Resource: "pods"},
		"object":          obj,
		"cluster object":  {ClusterID: "c-1", Version: "v1", Resource: "nodes", Name: "n-1", UID: "uid-9"},
		"colon in name":   {ClusterID: "c-1", Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles", Name: "system:node", UID: "u"},
	}
	for name, ref := range valid {
		if err := ref.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	mut := func(f func(*authorization.TargetRef)) authorization.TargetRef { r := obj; f(&r); return r }
	invalid := map[string]authorization.TargetRef{
		"empty":                  {},
		"bad cluster":            {ClusterID: "c 1"},
		"name without uid":       mut(func(r *authorization.TargetRef) { r.UID = "" }),
		"uid without name":       mut(func(r *authorization.TargetRef) { r.Name = "" }),
		"name without resource":  {ClusterID: "c-1", Namespace: "ns", Name: "web", UID: "u"},
		"resource without ver":   mut(func(r *authorization.TargetRef) { r.Version = "" }),
		"group without resource": {ClusterID: "c-1", Group: "apps"},
		"version without res":    {ClusterID: "c-1", Version: "v1"},
		"path traversal name":    mut(func(r *authorization.TargetRef) { r.Name = ".." }),
		"slash in namespace":     mut(func(r *authorization.TargetRef) { r.Namespace = "a/b" }),
		"percent in name":        mut(func(r *authorization.TargetRef) { r.Name = "a%2fb" }),
		"space in name":          mut(func(r *authorization.TargetRef) { r.Name = "a b" }),
		"control char":           mut(func(r *authorization.TargetRef) { r.Name = "a\x00b" }),
		"non-ascii":              mut(func(r *authorization.TargetRef) { r.Name = "имя" }),
		"too long name":          mut(func(r *authorization.TargetRef) { r.Name = strings.Repeat("a", 254) }),
		"bad group":              mut(func(r *authorization.TargetRef) { r.Group = "ap ps" }),
		"too long uid":           mut(func(r *authorization.TargetRef) { r.UID = strings.Repeat("u", 129) }),
	}
	for name, ref := range invalid {
		if err := ref.Validate(); !errors.Is(err, authorization.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestObjectRef_RequiresUID(t *testing.T) {
	ok := authorization.ObjectRef{ClusterID: "c-1", Version: "v1", Resource: "pods", Namespace: "ns", Name: "p", UID: "u"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, o := range map[string]authorization.ObjectRef{
		"no uid":      {ClusterID: "c-1", Version: "v1", Resource: "pods", Name: "p"},
		"no name":     {ClusterID: "c-1", Version: "v1", Resource: "pods", UID: "u"},
		"no resource": {ClusterID: "c-1", Version: "v1", Name: "p", UID: "u"},
		"cluster":     {ClusterID: "c-1"},
	} {
		if err := o.Validate(); !errors.Is(err, authorization.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestTargetScope_Validate(t *testing.T) {
	good := authorization.TargetScope{OrgID: "org-a", ClusterIDs: []string{"c-1"}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	many := make([]string, 1025)
	for i := range many {
		many[i] = "c-" + strings.Repeat("x", 1) + string(rune('a'+i%26))
	}
	for name, s := range map[string]authorization.TargetScope{
		"empty":             {},
		"org only":          {OrgID: "org-a"},
		"no org":            {ClusterIDs: []string{"c-1"}},
		"bad team":          {OrgID: "org-a", TeamIDs: []string{"a b"}},
		"empty cluster id":  {OrgID: "org-a", ClusterIDs: []string{""}},
		"bad namespace ref": {OrgID: "org-a", Namespaces: []authorization.NamespaceRef{{ClusterID: "c-1"}}},
		"bad object ref":    {OrgID: "org-a", Objects: []authorization.ObjectRef{{ClusterID: "c-1"}}},
		"too many entries":  {OrgID: "org-a", ClusterIDs: many},
	} {
		if err := s.Validate(); !errors.Is(err, authorization.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestConstraintSet_Validate(t *testing.T) {
	zero := time.Time{}
	for name, c := range map[string]authorization.ConstraintSet{
		"bad env":         {AllowedEnvironments: []string{"a b"}},
		"zero not before": {NotBefore: &zero},
		"too many envs":   {AllowedEnvironments: strings.Split(strings.Repeat("e,", 33), ",")},
	} {
		if err := c.Validate(); !errors.Is(err, authorization.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if err := (authorization.ConstraintSet{}).Validate(); err != nil {
		t.Errorf("empty constraints are valid: %v", err)
	}
}

func TestCanonical_DeepCopyAndOrder(t *testing.T) {
	nb := t0.In(time.FixedZone("x", 3*3600))
	g := authorization.Grant{
		ID: "g", Principal: tpA,
		Actions: []authorization.OperationAction{{OperationID: "b", ActionID: "1"}, {OperationID: "a", ActionID: "2"}, {OperationID: "a", ActionID: "2"}},
		Targets: authorization.TargetScope{OrgID: "org-a", TeamIDs: []string{"z", "a"}, ClusterIDs: []string{"c2", "c1", "c2"},
			Namespaces: []authorization.NamespaceRef{{ClusterID: "c2", Namespace: "n"}, {ClusterID: "c1", Namespace: "n"}}},
		Constraints: authorization.ConstraintSet{AllowedEnvironments: []string{"prod", "dev", "prod"}, NotBefore: &nb},
		ExpiresAt:   t0.In(time.FixedZone("x", -5*3600)),
	}
	c := g.Canonical()
	if len(c.Actions) != 2 || c.Actions[0].OperationID != "a" {
		t.Errorf("actions: %+v", c.Actions)
	}
	if strings.Join(c.Targets.TeamIDs, ",") != "a,z" || strings.Join(c.Targets.ClusterIDs, ",") != "c1,c2" ||
		c.Targets.Namespaces[0].ClusterID != "c1" || strings.Join(c.Constraints.AllowedEnvironments, ",") != "dev,prod" {
		t.Errorf("lists not sorted: %+v", c)
	}
	if c.ExpiresAt.Location() != time.UTC || c.Constraints.NotBefore.Location() != time.UTC {
		t.Errorf("times must be UTC")
	}
	if !c.ExpiresAt.Equal(g.ExpiresAt) || !c.Constraints.NotBefore.Equal(nb) {
		t.Errorf("canonicalization must not change the instant")
	}
	// глубокая копия: правка результата не затрагивает исходник
	c.Targets.ClusterIDs[0], c.Actions[0].ActionID = "X", "X"
	*c.Constraints.NotBefore = time.Time{}
	if g.Targets.ClusterIDs[0] != "c2" || g.Actions[1].ActionID != "2" || nb.IsZero() {
		t.Errorf("Canonical must not alias the source: %+v", g)
	}
	if again := g.Canonical(); strings.Join(again.Targets.ClusterIDs, ",") != "c1,c2" {
		t.Errorf("source must stay intact")
	}
}
