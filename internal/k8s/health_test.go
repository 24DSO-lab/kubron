// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-labs/kubron/internal/clusterhealth"
)

func TestClassifyProbeError(t *testing.T) {
	gr := schema.GroupResource{Resource: "namespaces"}
	cases := []struct {
		name string
		err  error
		want clusterhealth.State
	}{
		{"nil", nil, clusterhealth.OK},
		{"unauthorized", apierrors.NewUnauthorized("token expired"), clusterhealth.Unauthorized},
		{"forbidden still means reachable", apierrors.NewForbidden(gr, "", errors.New("no")), clusterhealth.OK},
		{"timeout", fmt.Errorf("list: %w", context.DeadlineExceeded), clusterhealth.Unreachable},
		{"network", errors.New("dial tcp 10.0.0.1:6443: connect: connection refused"), clusterhealth.Unreachable},
		{"server error", apierrors.NewServiceUnavailable("etcd"), clusterhealth.Unreachable},
	}
	for _, c := range cases {
		got, msg := classifyProbeError(c.err)
		if got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
		if got == clusterhealth.OK && msg != "" {
			t.Errorf("%s: a healthy cluster has no message, got %q", c.name, msg)
		}
		if got != clusterhealth.OK && msg == "" {
			t.Errorf("%s: an unhealthy cluster needs a reason", c.name)
		}
	}
}

func TestStatusesCarryHealthFromTheSharedBoard(t *testing.T) {
	root := NewManager()
	view := root.ForSession()
	if view.Health() != root.Health() {
		t.Fatal("session views must share one health board")
	}
}
