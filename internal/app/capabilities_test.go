// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import "testing"

type capsPlatform struct{ nopPlatform }

func (capsPlatform) Capabilities() Capabilities {
	return Capabilities{FileDialog: true, NodeShell: true}
}

func TestGetCapabilitiesComesFromPlatform(t *testing.T) {
	a := NewApp(capsPlatform{})
	got := a.GetCapabilities()
	want := Capabilities{FileDialog: true, NodeShell: true}
	if got != want {
		t.Fatalf("GetCapabilities = %+v, expected %+v", got, want)
	}
}

func TestGetCapabilitiesWithoutPlatformIsEmpty(t *testing.T) {
	if got := NewApp(nil).GetCapabilities(); got != (Capabilities{}) {
		t.Fatalf("expected empty capabilities without a platform, got %+v", got)
	}
}

// Метод доступен до входа: не требует сессии (иначе экран входа не узнает возможности).
func TestGetCapabilitiesNeedsNoSession(t *testing.T) {
	a := NewApp(capsPlatform{})
	a.initAuth(t.TempDir()) // хранилище есть, но никто не вошёл
	if got := a.GetCapabilities(); !got.FileDialog {
		t.Fatalf("capabilities must be served before login: %+v", got)
	}
}

type pfPlatform struct{ nopPlatform }

func (pfPlatform) Capabilities() Capabilities { return Capabilities{PortForward: true} }

func TestGetCapabilitiesReportsPortForwardMode(t *testing.T) {
	root := NewApp(pfPlatform{})
	if c := root.GetCapabilities(); !c.PortForward || c.PortForwardMode != "local" {
		t.Fatalf("default must stay local: %+v", c)
	}
	SetPortForwardMode(root, "off")
	if c := root.GetCapabilities(); c.PortForward || c.PortForwardMode != "off" {
		t.Fatalf("off must hide port-forward: %+v", c)
	}
}
