// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"testing"

	"github.com/24DSO-lab/kubron/internal/pfproxy"
	"github.com/24DSO-lab/kubron/internal/streamid"
)

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

type shellPlatform struct{ nopPlatform }

func (shellPlatform) Capabilities() Capabilities { return Capabilities{Exec: true, NodeShell: true} }

func TestGetCapabilitiesReflectsDisableFlags(t *testing.T) {
	cases := []struct {
		name      string
		p         InteractivePolicy
		exec, nsh bool
	}{
		{"nothing disabled", InteractivePolicy{}, true, true},
		{"node shell only", InteractivePolicy{DisableNodeShell: true}, true, false},
		{"exec takes the node shell with it", InteractivePolicy{DisableExec: true}, false, false},
	}
	for _, tc := range cases {
		root := NewApp(shellPlatform{})
		SetInteractivePolicy(root, tc.p)
		if c := root.GetCapabilities(); c.Exec != tc.exec || c.NodeShell != tc.nsh {
			t.Errorf("%s: exec=%v nodeShell=%v, expected %v/%v", tc.name, c.Exec, c.NodeShell, tc.exec, tc.nsh)
		}
	}
}

func TestDisabledTerminalsReturnClearErrors(t *testing.T) {
	a := NewApp(shellPlatform{})
	SetInteractivePolicy(a, InteractivePolicy{DisableExec: true})
	if err := a.checkTerminalTarget("default", "web"); err != errExecDisabled {
		t.Errorf("a pod terminal must be refused when exec is disabled: %v", err)
	}
	a = NewApp(shellPlatform{})
	SetInteractivePolicy(a, InteractivePolicy{DisableNodeShell: true})
	if err := a.checkTerminalTarget("default", "web"); err != nil {
		t.Errorf("a pod terminal must stay available when only the node shell is disabled: %v", err)
	}
	if err := a.checkTerminalTarget("kube-system", "kubron-node-shell-abc12"); err != errNodeShellDisabled {
		t.Errorf("the node shell pod must be refused: %v", err)
	}
}

func TestPortForwardMethodsRefuseAStreamOfAnotherReplica(t *testing.T) {
	a := NewApp(pfPlatform{})
	a.initAuth(t.TempDir())
	if _, err := a.SetupAdmin("admin", "correct-horse-battery-1"); err != nil { // создаёт администратора и входит
		t.Fatal(err)
	}
	SetReplicaID(a, "replica-a")
	foreign := pfproxy.NewIDFor("replica-b")
	if _, err := a.GetPortForwardURL(foreign); !errors.Is(err, streamid.ErrElsewhere) {
		t.Errorf("GetPortForwardURL must answer stream_elsewhere: %v", err)
	}
	if err := a.StopPortForward(foreign); !errors.Is(err, streamid.ErrElsewhere) {
		t.Errorf("StopPortForward must answer stream_elsewhere: %v", err)
	}
	own := pfproxy.NewIDFor("replica-a") // свой, но не существующий поток — обычная ошибка, а не «на другой реплике»
	if _, err := a.GetPortForwardURL(own); err == nil || errors.Is(err, streamid.ErrElsewhere) {
		t.Errorf("an own unknown stream is simply not found: %v", err)
	}
}
