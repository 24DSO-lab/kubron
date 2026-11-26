// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FuzzAnalyzeEvidence — Analyze не паникует и не нарушает инварианты отчёта на произвольном тексте событий, условий и логов:
// это недоверенный ввод из кластера.
func FuzzAnalyzeEvidence(f *testing.F) {
	f.Add("FailedScheduling", "0/3 nodes are available: pod has unbound immediate PersistentVolumeClaims.", "CrashLoopBackOff", "panic: boom")
	f.Add("FailedCreate", "exceeded quota: compute", "ErrImagePull", "")
	f.Add("", "", "", "\x00\xff")
	f.Fuzz(func(t *testing.T, reason, message, waiting, log string) {
		ref := ResourceRef{Kind: "Pod", Namespace: "a", Name: "p"}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "a"},
			Spec:       corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "c", Image: "i"}}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodPending,
				Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Message: message}},
				ContainerStatuses: []corev1.ContainerStatus{{Name: "c", RestartCount: 3,
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: waiting, Message: message}}}},
			},
		}
		c := &Context{
			Target: ref, ObservedAt: fixedNow, Pods: []*corev1.Pod{pod},
			Events: []Event{{Type: "Warning", Reason: reason, Message: message, Count: 1, FirstSeen: fixedNow, LastSeen: fixedNow, Object: ref}},
			Logs:   []LogCapture{{Pod: ref, Container: "c", Raw: []byte(log)}},
		}
		checkInvariants(t, Analyze(c))
	})
}

// Хотя бы один сценарий должен показывать противоречащий факт: иначе ветка «против гипотезы» не проверяется.
func TestScenariosExerciseContradicting(t *testing.T) {
	t.Parallel()
	found := false
	for _, sc := range loadScenarios(t) {
		if sc.Expect.Error != "" {
			continue
		}
		r := newFakeReader(sc.Objects, sc.Logs)
		c, err := Collect(t.Context(), r, sc.Expect.Target, CollectOptions{Logs: true, Now: func() time.Time { return fixedNow }})
		if err != nil {
			continue
		}
		if slices.ContainsFunc(Analyze(c).Hypotheses, func(h Hypothesis) bool { return len(h.Contradicting) > 0 }) {
			found = true
			break
		}
	}
	if !found {
		t.Error("ни один сценарий не даёт непустой Contradicting")
	}
}
