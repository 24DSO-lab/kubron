// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestNormalizeDebugOptions(t *testing.T) {
	ok := DebugOptions{Namespace: "ns", Pod: "p", Image: " busybox ", Target: "app"}
	got, err := normalizeDebugOptions(ok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "busybox" || got.Profile != DebugProfileGeneral {
		t.Errorf("image must be trimmed and the profile defaulted: %+v", got)
	}

	for name, mut := range map[string]func(*DebugOptions){
		"no namespace":     func(o *DebugOptions) { o.Namespace = "" },
		"no pod":           func(o *DebugOptions) { o.Pod = "" },
		"no image":         func(o *DebugOptions) { o.Image = "  " },
		"image whitespace": func(o *DebugOptions) { o.Image = "busybox --privileged" },
		"unknown profile":  func(o *DebugOptions) { o.Profile = "sysadmin" },
		"bad target":       func(o *DebugOptions) { o.Target = "App_1" },
	} {
		o := ok
		mut(&o)
		if _, err := normalizeDebugOptions(o); err == nil {
			t.Errorf("%s: an error was expected", name)
		}
	}
}

func TestBuildDebugContainer(t *testing.T) {
	c := buildDebugContainer("kubron-debug-abcde", DebugOptions{Image: "busybox", Target: "app", Profile: DebugProfileGeneral})
	if !c.Stdin || !c.TTY {
		t.Error("the container must be interactive")
	}
	if !c.StdinOnce {
		t.Error("stdinOnce is what ends the shell when the tab is closed")
	}
	if c.TargetContainerName != "app" || c.Image != "busybox" || len(c.Command) != 0 {
		t.Errorf("unexpected container: %+v", c)
	}
	if sc := c.SecurityContext; sc == nil || sc.Capabilities == nil || len(sc.Capabilities.Add) != 1 || sc.Capabilities.Add[0] != "SYS_PTRACE" {
		t.Errorf("general must add SYS_PTRACE only: %+v", c.SecurityContext)
	}

	r := buildDebugContainer("kubron-debug-abcde", DebugOptions{Image: "busybox", Profile: DebugProfileRestricted}).SecurityContext
	if r == nil || r.RunAsNonRoot == nil || !*r.RunAsNonRoot || r.RunAsUser == nil || *r.RunAsUser == 0 {
		t.Errorf("restricted must run as a non-root user: %+v", r)
	}
	if r.AllowPrivilegeEscalation == nil || *r.AllowPrivilegeEscalation {
		t.Error("restricted must forbid privilege escalation")
	}
	if r.Capabilities == nil || len(r.Capabilities.Add) != 0 || len(r.Capabilities.Drop) != 1 || r.Capabilities.Drop[0] != "ALL" {
		t.Errorf("restricted must drop all capabilities: %+v", r.Capabilities)
	}
	if r.SeccompProfile == nil || r.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("restricted needs the RuntimeDefault seccomp profile: %+v", r.SeccompProfile)
	}
}

func TestDebugContainerName(t *testing.T) {
	re := regexp.MustCompile(`^kubron-debug-[bcdfghjklmnpqrstvwxz2456789]{5}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		n, err := newDebugContainerName()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(n) || !IsDebugContainer(n) {
			t.Fatalf("unexpected name %q", n)
		}
		seen[n] = true
	}
	if len(seen) < 45 {
		t.Errorf("names must be practically unique: %d of 50", len(seen))
	}
	if IsDebugContainer("app") || IsDebugContainer("shell") {
		t.Error("ordinary containers are not debug containers")
	}
}

func TestContainerTTYStdin(t *testing.T) {
	p := &corev1.Pod{Spec: corev1.PodSpec{
		Containers:     []corev1.Container{{Name: "app", TTY: true, Stdin: true}, {Name: "side"}},
		InitContainers: []corev1.Container{{Name: "init", Stdin: true}},
		EphemeralContainers: []corev1.EphemeralContainer{
			{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "kubron-debug-abcde", TTY: true, Stdin: true}},
		},
	}}
	for name, want := range map[string][2]bool{
		"app": {true, true}, "side": {false, false}, "init": {false, true},
		"kubron-debug-abcde": {true, true}, // иначе сессия к отладочному контейнеру молча стала бы «только чтение»
		"missing":            {false, false},
	} {
		if tty, stdin := containerTTYStdin(p, name); tty != want[0] || stdin != want[1] {
			t.Errorf("%s: tty=%v stdin=%v, expected %v", name, tty, stdin, want)
		}
	}
}

// debugFakeClient — clientset с одним подом. Фейк игнорирует subresource у patch и правит объект целиком, но не
// ведёт status, поэтому состояние ephemeral-контейнеров подставляется при чтении пода (state == nil — статуса нет).
func debugFakeClient(pod *corev1.Pod, state func(name string) *corev1.ContainerState) *fake.Clientset {
	cs := fake.NewSimpleClientset(pod)
	cs.PrependReactor("get", "pods", func(a clienttesting.Action) (bool, runtime.Object, error) {
		obj, err := cs.Tracker().Get(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, a.GetNamespace(), a.(clienttesting.GetAction).GetName())
		if err != nil {
			return true, nil, err
		}
		p := obj.(*corev1.Pod).DeepCopy()
		p.Status.EphemeralContainerStatuses = nil
		for _, ec := range p.Spec.EphemeralContainers {
			if state == nil {
				continue
			}
			if st := state(ec.Name); st != nil {
				p.Status.EphemeralContainerStatuses = append(p.Status.EphemeralContainerStatuses, corev1.ContainerStatus{Name: ec.Name, State: *st})
			}
		}
		return true, p, nil
	})
	return cs
}

func runningPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "app:1"}},
			EphemeralContainers: []corev1.EphemeralContainer{
				{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "old-debug", Image: "busybox"}},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func running(string) *corev1.ContainerState {
	return &corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
}

func TestAddDebugContainerAppendsAndWaits(t *testing.T) {
	cs := debugFakeClient(runningPod(), running)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dc, err := addDebugContainer(ctx, cs, DebugOptions{Namespace: "prod", Pod: "web", Target: "app", Image: "busybox", Profile: DebugProfileRestricted})
	if err != nil {
		t.Fatal(err)
	}
	if !IsDebugContainer(dc.Container) || dc.Pod != "web" || dc.Namespace != "prod" || dc.Target != "app" || dc.Profile != DebugProfileRestricted {
		t.Errorf("unexpected result: %+v", dc)
	}

	var patched bool
	for _, a := range cs.Actions() {
		if pa, ok := a.(clienttesting.PatchAction); ok {
			patched = true
			if pa.GetSubresource() != "ephemeralcontainers" {
				t.Errorf("the patch must go to the ephemeralcontainers subresource, got %q", pa.GetSubresource())
			}
		}
	}
	if !patched {
		t.Fatal("no patch was sent")
	}

	got, _ := cs.CoreV1().Pods("prod").Get(ctx, "web", metav1.GetOptions{})
	if len(got.Spec.EphemeralContainers) != 2 {
		t.Fatalf("the new container must be added to the existing ones: %d", len(got.Spec.EphemeralContainers))
	}
	var added *corev1.EphemeralContainer
	for i := range got.Spec.EphemeralContainers {
		if got.Spec.EphemeralContainers[i].Name == dc.Container {
			added = &got.Spec.EphemeralContainers[i]
		}
	}
	if added == nil || !added.StdinOnce || !added.TTY || added.TargetContainerName != "app" || added.Image != "busybox" {
		t.Errorf("unexpected container in the pod: %+v", added)
	}
}

func TestAddDebugContainerRefusals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := DebugOptions{Namespace: "prod", Pod: "web", Image: "busybox"}

	finished := runningPod()
	finished.Status.Phase = corev1.PodSucceeded
	if _, err := addDebugContainer(ctx, debugFakeClient(finished, running), opts); err == nil || !strings.Contains(err.Error(), "finished") {
		t.Errorf("a finished pod must be refused: %v", err)
	}

	deleting := runningPod()
	deleting.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	if _, err := addDebugContainer(ctx, debugFakeClient(deleting, running), opts); err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Errorf("a pod that is being deleted must be refused: %v", err)
	}

	withTarget := opts
	withTarget.Target = "nope"
	if _, err := addDebugContainer(ctx, debugFakeClient(runningPod(), running), withTarget); err == nil || !strings.Contains(err.Error(), "no container") {
		t.Errorf("an unknown target must be refused: %v", err)
	}

	if _, err := addDebugContainer(ctx, debugFakeClient(runningPod(), running), DebugOptions{Namespace: "prod", Pod: "ghost", Image: "busybox"}); err == nil {
		t.Error("a missing pod must be refused")
	}
}

func TestAddDebugContainerForbiddenExplainsRBAC(t *testing.T) {
	cs := debugFakeClient(runningPod(), running)
	cs.PrependReactor("patch", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods/ephemeralcontainers"}, "web", errors.New("denied"))
	})
	_, err := addDebugContainer(context.Background(), cs, DebugOptions{Namespace: "prod", Pod: "web", Image: "busybox"})
	if err == nil || !strings.Contains(err.Error(), "pods/ephemeralcontainers") || !apierrors.IsForbidden(err) {
		t.Errorf("the message must name the RBAC resource and keep the cause: %v", err)
	}
}

func TestWaitDebugContainerStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   func(string) *corev1.ContainerState
		wantErr string
	}{
		{"image pull failure fails fast", func(string) *corev1.ContainerState {
			return &corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "back-off pulling image"}}
		}, "ImagePullBackOff"},
		{"exited before attach", func(string) *corev1.ContainerState {
			return &corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 127, Reason: "Error"}}
		}, "exited"},
		{"still creating times out", func(string) *corev1.ContainerState {
			return &corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}
		}, "timed out"},
		{"no status yet times out", nil, "timed out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := runningPod()
			p.Spec.EphemeralContainers = append(p.Spec.EphemeralContainers,
				corev1.EphemeralContainer{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "kubron-debug-abcde", Image: "busybox"}})
			cs := debugFakeClient(p, tc.state)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			err := waitDebugContainerRunning(ctx, cs, "prod", "web", "kubron-debug-abcde")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected an error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestWaitDebugContainerPodDisappears(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitDebugContainerRunning(ctx, cs, "prod", "web", "kubron-debug-abcde"); err == nil || !strings.Contains(err.Error(), "disappeared") {
		t.Errorf("a vanished pod must stop the wait at once: %v", err)
	}
}

func TestConvertPodShowsEphemeralContainers(t *testing.T) {
	pod := *runningPod()
	pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{
		{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "kubron-debug-aaaaa", Image: "busybox"}},
		{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "kubron-debug-bbbbb", Image: "busybox"}},
		{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "kubron-debug-ccccc", Image: "busybox"}},
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}
	pod.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{
		{Name: "kubron-debug-aaaaa", Image: "docker.io/library/busybox:stable", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		{Name: "kubron-debug-bbbbb", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed"}}},
	}

	info := convertPod(pod)
	byName := map[string]ContainerInfo{}
	for _, c := range info.Containers {
		byName[c.Name] = c
	}
	if len(info.Containers) != 4 {
		t.Fatalf("the regular container and three debug ones are expected: %d", len(info.Containers))
	}
	if byName["app"].IsEphemeral {
		t.Error("a regular container is not ephemeral")
	}
	if a := byName["kubron-debug-aaaaa"]; !a.IsEphemeral || a.IsInit || a.State != "running" || a.Image != "docker.io/library/busybox:stable" {
		t.Errorf("running debug container: %+v", a)
	}
	if b := byName["kubron-debug-bbbbb"]; !b.IsEphemeral || b.State != "Completed" || b.Image != "busybox" {
		t.Errorf("finished debug container stays visible: %+v", b)
	}
	if c := byName["kubron-debug-ccccc"]; !c.IsEphemeral || c.State != "waiting" {
		t.Errorf("a debug container without a status yet is waiting: %+v", c)
	}
	if info.Ready != "1/1" || info.Restarts != 0 {
		t.Errorf("debug containers must not affect pod readiness or restarts: ready=%s restarts=%d", info.Ready, info.Restarts)
	}
}
