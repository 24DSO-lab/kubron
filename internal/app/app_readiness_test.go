// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"

	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
	"github.com/24DSO-lab/kubron/internal/readiness"
	"github.com/24DSO-lab/kubron/internal/store"
)

func TestMapSourceErr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   error
		want error
	}{
		{opsplatform.ErrNotFound, readiness.ErrSourceNotFound},
		{opsplatform.ErrForbidden, readiness.ErrSourceForbidden},
		{opsplatform.ErrTooLarge, readiness.ErrSourceTooLarge},
		{opsplatform.ErrUnavailable, readiness.ErrSourceUnavailable},
		{fmt.Errorf("wrapped: %w", opsplatform.ErrForbidden), readiness.ErrSourceForbidden},
		{context.DeadlineExceeded, readiness.ErrSourceUnavailable},
		{&opsplatform.PartialError{Failed: []string{"a/v1"}}, readiness.ErrSourcePartial},
	}
	for _, tt := range tests {
		got := mapSourceErr(tt.in)
		if !errors.Is(got, tt.want) {
			t.Errorf("mapSourceErr(%v) = %v, want %v", tt.in, got, tt.want)
		}
		if errors.Is(tt.in, opsplatform.ErrForbidden) && !errors.Is(got, opsplatform.ErrForbidden) {
			t.Errorf("the original chain must stay available: %v", got)
		}
	}
	if mapSourceErr(nil) != nil {
		t.Error("nil must stay nil")
	}
	other := errors.New("other")
	if mapSourceErr(other) != other {
		t.Error("an unknown error must pass through")
	}
	var pe *readiness.PartialError
	if !errors.As(mapSourceErr(&opsplatform.PartialError{Failed: []string{"a/v1"}}), &pe) || len(pe.Failed) != 1 {
		t.Error("partial details must be carried over")
	}
}

type fakePlatformReader struct{ lastList opsplatform.ListOptions }

func (f *fakePlatformReader) Get(context.Context, schema.GroupVersionKind, string, string) (*unstructured.Unstructured, error) {
	return nil, opsplatform.ErrNotFound
}

func (f *fakePlatformReader) List(_ context.Context, _ schema.GroupVersionKind, o opsplatform.ListOptions) (*opsplatform.ListResult, error) {
	f.lastList = o
	return &opsplatform.ListResult{Truncated: true, ObservedAt: time.Unix(5, 0)}, nil
}

func (f *fakePlatformReader) PodLogs(context.Context, opsplatform.LogRequest) ([]byte, error) {
	return nil, errors.New("logs must never be requested by readiness")
}

func (f *fakePlatformReader) ServerVersion(context.Context) (version.Info, error) {
	return version.Info{GitVersion: "v1.35.0"}, nil
}

func (f *fakePlatformReader) ServerResources(context.Context) ([]*metav1.APIResourceList, error) {
	return nil, opsplatform.ErrUnavailable
}

func TestReadinessReaderAdapter(t *testing.T) {
	t.Parallel()
	fp := &fakePlatformReader{}
	var rd readiness.Reader = readinessReader{fp}
	if v, err := rd.ServerVersion(t.Context()); err != nil || v.GitVersion != "v1.35.0" {
		t.Fatalf("ServerVersion = %v, %v", v, err)
	}
	if _, err := rd.ServerResources(t.Context()); !errors.Is(err, readiness.ErrSourceUnavailable) {
		t.Fatalf("ServerResources err = %v", err)
	}
	res, err := rd.List(t.Context(), schema.GroupVersionKind{Kind: "Node", Version: "v1"}, readiness.ListOptions{Namespace: "kube-system", Limit: 7})
	if err != nil || !res.Truncated || res.ObservedAt.Unix() != 5 || fp.lastList.Namespace != "kube-system" || fp.lastList.Limit != 7 {
		t.Fatalf("List = %+v, %v, opts %+v", res, err, fp.lastList)
	}
}

// Делегаты требуют вход и право на кластер до всякой работы; чужой кластер неотличим от несуществующего.
func TestReadinessMethodsRequireAuthAndClusterAccess(t *testing.T) {
	k8s.SetKubronDir(t.TempDir())
	t.Cleanup(func() { k8s.SetKubronDir("") })
	a := newAuditedApp(t)
	a.auth.SetOnLogout(nil)
	a.initReadiness(store.NewFile(t.TempDir()))
	if _, err := a.auth.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	// Несуществующий кластер: ни проверка, ни чтение истории, ни экспорт не доходят до хранилища.
	if _, err := a.ReadinessRun("ghost", ""); err == nil {
		t.Error("ReadinessRun on an unknown cluster must fail")
	}
	if _, err := a.ReadinessHistory("ghost", "", 5); err == nil {
		t.Error("ReadinessHistory on an unknown cluster must fail")
	}
	if _, err := a.ReadinessExport("ghost", "x", "json"); err == nil {
		t.Error("ReadinessExport on an unknown cluster must fail")
	}
	info, err := a.ReadinessInfo()
	if err != nil || len(info.Window) != 3 || info.APIMatrix.Version == "" {
		t.Errorf("ReadinessInfo = %+v, %v", info, err)
	}
	a.auth.Logout()
	if _, err := a.ReadinessInfo(); err == nil {
		t.Error("ReadinessInfo must require a signed-in user")
	}
}

func TestReadinessUnavailableWithoutService(t *testing.T) {
	t.Parallel()
	a := &App{}
	if _, err := a.readinessService(); err == nil {
		t.Fatal("a missing service must be an error")
	}
	if _, err := ReadinessRetentionJob(a, nil); err == nil {
		t.Fatal("no job without a service")
	}
}
