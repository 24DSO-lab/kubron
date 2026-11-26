// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/drift"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
)

func TestAuditApplyLookup(t *testing.T) {
	t.Parallel()
	log := audit.Open(t.TempDir())
	at := time.Now().UTC().Add(-time.Minute)
	log.Record(audit.Event{Time: at, User: "alice", Action: "ApplyYAML", Result: audit.ResultOK, Detail: "namespace=prod kind=deployment name=web", ClusterID: "c1"})
	log.Record(audit.Event{Time: at.Add(time.Second), User: "bob", Action: "ApplyYAML", Result: audit.ResultOK, Detail: "namespace=prod kind=deployment name=web", ClusterID: "other"})
	log.Record(audit.Event{Time: at.Add(2 * time.Second), User: "bob", Action: "ApplyYAML", Result: audit.ResultFailed, Detail: "namespace=prod kind=deployment name=web", ClusterID: "c1"})

	ref := baseline.Ref{Version: "v1", Group: "apps", Kind: "Deployment", Namespace: "prod", Name: "web"}
	l := auditApplyLookup{log: log, clusterID: "c1"}
	got, found, err := l.LastApply(t.Context(), ref, at.Add(-time.Hour))
	if err != nil || !found || got.Sub(at).Abs() > time.Second {
		t.Fatalf("lookup = %v %v %v: must take only the successful record of this cluster", got, found, err)
	}
	ref.Name = "api"
	if _, found, _ := l.LastApply(t.Context(), ref, at.Add(-time.Hour)); found {
		t.Error("another resource must not match")
	}
	// Для другого кластера запись c1 чужая и не подтверждает ничего.
	other := auditApplyLookup{log: log, clusterID: "zzz"}
	if _, found, _ := other.LastApply(t.Context(), baseline.Ref{Kind: "Deployment", Namespace: "prod", Name: "web"}, at.Add(-time.Hour)); found {
		t.Error("records of other clusters must not match")
	}
}

type errReader struct {
	opsplatform.Reader // остальные методы не вызываются
	err                error
}

func (e errReader) Get(context.Context, schema.GroupVersionKind, string, string) (*unstructured.Unstructured, error) {
	return nil, e.err
}
func (e errReader) List(context.Context, schema.GroupVersionKind, opsplatform.ListOptions) (*opsplatform.ListResult, error) {
	return nil, e.err
}

func TestClusterReaderTranslatesErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       error
		wantGet  error
		wantList error
	}{
		{opsplatform.ErrNotFound, drift.ErrNotFound, drift.ErrKindUnavailable},
		{opsplatform.ErrForbidden, drift.ErrForbidden, drift.ErrForbidden},
		{opsplatform.ErrTooLarge, drift.ErrTooLarge, drift.ErrTooLarge},
		{opsplatform.ErrUnavailable, drift.ErrUnavailable, drift.ErrUnavailable},
		{errors.New("boom"), drift.ErrUnavailable, drift.ErrUnavailable},
	}
	for _, c := range cases {
		r := clusterReader{errReader{err: c.in}}
		if _, err := r.Get(t.Context(), schema.GroupVersionKind{}, "", ""); !errors.Is(err, c.wantGet) {
			t.Errorf("Get(%v) = %v, want %v", c.in, err, c.wantGet)
		}
		if _, err := r.List(t.Context(), schema.GroupVersionKind{}, "", "", 1); !errors.Is(err, c.wantList) {
			t.Errorf("List(%v) = %v, want %v", c.in, err, c.wantList)
		}
	}
}

func TestBaselineSpecInputLimits(t *testing.T) {
	t.Parallel()
	big := make([]string, maxSpecNamespaces+1)
	if _, err := (BaselineSpecInput{Namespaces: big}).spec(); !errors.Is(err, baseline.ErrInvalid) {
		t.Errorf("too many namespaces: %v", err)
	}
	if _, err := (BaselineSpecInput{Refs: []baseline.Ref{{Kind: "Deployment"}}}).spec(); !errors.Is(err, baseline.ErrInvalid) {
		t.Errorf("incomplete ref: %v", err)
	}
	if _, err := (BaselineSpecInput{Namespaces: []string{"a"}}).spec(); err != nil {
		t.Errorf("valid spec: %v", err)
	}
}
