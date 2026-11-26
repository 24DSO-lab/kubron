// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/incident/incidenttest"
)

// corrReader — кластер stubReader плюс узел n1 в состоянии NotReady и ещё два нагруженных Pod'а на нём.
type corrReader struct {
	*stubReader
	podListErr   error
	podListPanic bool
}

func barePod(name string) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": "shop"},
		"spec": map[string]any{"nodeName": "n1"},
		"status": map[string]any{"phase": "Running",
			"conditions": []any{map[string]any{"type": "Ready", "status": "False", "lastTransitionTime": "2026-11-01T09:50:00Z"}}}}
}

func (r *corrReader) Get(ctx context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
	if gvk.Kind == "Node" && name == "n1" {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]any{"name": "n1", "uid": "node-uid"},
			"status": map[string]any{"conditions": []any{
				map[string]any{"type": "Ready", "status": "False", "lastTransitionTime": "2026-11-01T09:40:00Z"}}}}}, nil
	}
	return r.stubReader.Get(ctx, gvk, ns, name)
}

func (r *corrReader) List(ctx context.Context, gvk schema.GroupVersionKind, o diagnose.ListOptions) (*diagnose.ListResult, error) {
	if gvk.Kind == "Pod" && strings.Contains(o.FieldSelector, "spec.nodeName=n1") {
		if r.podListPanic {
			panic("boom")
		}
		if r.podListErr != nil {
			return nil, r.podListErr
		}
		res := &diagnose.ListResult{ObservedAt: testBase}
		for _, p := range []map[string]any{r.pod(), barePod("web-1"), barePod("db-1")} {
			res.Items = append(res.Items, unstructured.Unstructured{Object: p})
		}
		return res, nil
	}
	return r.stubReader.List(ctx, gvk, o)
}

func openWith(t *testing.T, r *corrReader) (*env, string, []string) {
	t.Helper()
	e := newEnv(t)
	e.call.Reader = r
	in, err := e.svc.Open(context.Background(), e.call, incident.OpenRequest{Target: target})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return e, in.ID, in.Report.Partial
}

func TestOpenStoresCorrelationSnapshot(t *testing.T) {
	e, id, partial := openWith(t, &corrReader{stubReader: newStub()})
	if slices.Contains(partial, correlation.PartialName) {
		t.Fatalf("полный снимок помечен частичным: %v", partial)
	}
	d, err := e.svc.Get(context.Background(), incidenttest.Scope, id, false)
	if err != nil {
		t.Fatal(err)
	}
	c := d.Incident.Correlation
	if c == nil || c.Version != correlation.Version {
		t.Fatalf("снимок не сохранён: %+v", c)
	}
	b := c.Best()
	if b == nil || b.Kind != correlation.KindSharedNode || b.Total != 3 || b.Confidence != diagnose.ConfidenceHigh {
		t.Fatalf("кандидат: %+v", b)
	}
	if got := b.Key(); got != "shared_node:core/Node//n1" {
		t.Fatalf("Key = %q", got)
	}
	if strings.Contains(fmt.Sprint(c), "SECRET") {
		t.Fatal("в снимок попал текст кластера")
	}
}

func TestOpenSurvivesCorrelationDegradation(t *testing.T) {
	t.Run("forbidden на список Pod'ов узла: Open успешен, снимок частичный", func(t *testing.T) {
		_, _, partial := openWith(t, &corrReader{stubReader: newStub(), podListErr: diagnose.ErrForbidden})
		if !slices.Contains(partial, correlation.PartialName) {
			t.Fatalf("Partial = %v, want содержит %q", partial, correlation.PartialName)
		}
	})
	t.Run("паника в корреляции не роняет Open", func(t *testing.T) {
		e, id, partial := openWith(t, &corrReader{stubReader: newStub(), podListPanic: true})
		if !slices.Contains(partial, correlation.PartialName) {
			t.Fatalf("Partial = %v", partial)
		}
		d, err := e.svc.Get(context.Background(), incidenttest.Scope, id, false)
		if err != nil {
			t.Fatal(err)
		}
		if d.Incident.Correlation != nil {
			t.Fatalf("после сбоя снимок должен отсутствовать: %+v", d.Incident.Correlation)
		}
	})
}
