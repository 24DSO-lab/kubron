// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"
)

func newDebugApp(t *testing.T, p InteractivePolicy) *App {
	t.Helper()
	a := NewApp(shellPlatform{})
	a.initAuth(t.TempDir())
	if _, err := a.SetupAdmin("admin", "correct-horse-battery-1"); err != nil { // создаёт администратора и входит
		t.Fatal(err)
	}
	SetInteractivePolicy(a, p)
	return a
}

func TestCheckDebugImage(t *testing.T) {
	open := NewApp(shellPlatform{})
	for _, img := range []string{"busybox", "ghcr.io/acme/tools:1"} {
		if err := open.checkDebugImage(img); err != nil {
			t.Errorf("without a list any image is allowed: %v", err)
		}
	}
	if err := open.checkDebugImage("  "); err == nil {
		t.Error("an empty image must be refused")
	}

	strict := NewApp(shellPlatform{})
	SetInteractivePolicy(strict, InteractivePolicy{DebugImages: []string{"busybox:1", "alpine:3"}})
	if err := strict.checkDebugImage(" busybox:1 "); err != nil {
		t.Errorf("a listed image must pass: %v", err)
	}
	err := strict.checkDebugImage("evil/miner:latest")
	if err == nil || !strings.Contains(err.Error(), "busybox:1") {
		t.Errorf("an unlisted image must be refused and the allowed ones named: %v", err)
	}
}

func TestGetDebugOptions(t *testing.T) {
	info, err := newDebugApp(t, InteractivePolicy{}).GetDebugOptions()
	if err != nil {
		t.Fatal(err)
	}
	if info.Strict || len(info.Images) == 0 || info.DefaultProfile != "general" || len(info.Profiles) != 2 {
		t.Errorf("defaults: %+v", info)
	}

	info, err = newDebugApp(t, InteractivePolicy{DebugImages: []string{"busybox:1"}}).GetDebugOptions()
	if err != nil || !info.Strict || len(info.Images) != 1 || info.Images[0] != "busybox:1" {
		t.Errorf("the administrator's list must replace the defaults: %+v %v", info, err)
	}

	if _, err := newDebugApp(t, InteractivePolicy{DisableDebug: true}).GetDebugOptions(); err != errDebugDisabled {
		t.Errorf("disabled debug: %v", err)
	}
	if _, err := newDebugApp(t, InteractivePolicy{DisableExec: true}).GetDebugOptions(); err != errExecDisabled {
		t.Errorf("exec off takes debug containers with it: %v", err)
	}
}

func TestStartDebugContainerRefusesBeforeTouchingTheCluster(t *testing.T) {
	// У вида нет подключения к кластеру: любой отказ ниже наступает до обращения к API.
	for name, tc := range map[string]struct {
		p     InteractivePolicy
		ns    string
		pod   string
		image string
		want  string
	}{
		"debug disabled":     {InteractivePolicy{DisableDebug: true}, "prod", "web", "busybox", errDebugDisabled.Error()},
		"exec disabled":      {InteractivePolicy{DisableExec: true}, "prod", "web", "busybox", errExecDisabled.Error()},
		"image not allowed":  {InteractivePolicy{DebugImages: []string{"busybox:1"}}, "prod", "web", "alpine", "not allowed"},
		"node shell pod off": {InteractivePolicy{DisableNodeShell: true}, "kube-system", "kubron-node-shell-abc12", "busybox", errNodeShellDisabled.Error()},
	} {
		_, err := newDebugApp(t, tc.p).StartDebugContainer(tc.ns, tc.pod, "", tc.image, "")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: expected an error containing %q, got %v", name, tc.want, err)
		}
	}
}
