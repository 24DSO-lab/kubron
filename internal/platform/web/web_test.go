// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package webplatform

import (
	"errors"
	"testing"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

var _ ext.Platform = (*Platform)(nil)

func TestPlatformCapabilitiesAndFileDialog(t *testing.T) {
	p := &Platform{}
	c := p.Capabilities()
	if c.FileDialog || c.LocalKubeconfig || !c.PortForward || !c.NodeShell || c.SSO {
		t.Fatalf("web capabilities: %+v", c)
	}
	if _, err := p.PickFile("x"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("PickFile: %v", err)
	}
}

func TestEmitGoesToEmitterWhenSet(t *testing.T) {
	p := &Platform{}
	p.Emit("before", 1) // без эмиттера — не паникует

	var got []string
	p.SetEmitter(func(event string, data ...any) { got = append(got, event) })
	p.Emit("cluster:changed")
	if len(got) != 1 || got[0] != "cluster:changed" {
		t.Fatalf("events: %v", got)
	}
}
