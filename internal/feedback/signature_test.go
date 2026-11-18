// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package feedback_test

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/feedback"
)

func baseInput() feedback.SignatureInput {
	return feedback.SignatureInput{
		WorkloadKind: "Deployment", WorkloadNamespace: "shop", WorkloadName: "api",
		Symptoms: []string{"pod.crash_loop", "pod.oom_killed"},
		Hypotheses: []feedback.Ref{
			{RuleID: "pod.oom", Epoch: 1, Code: "memory_limit_too_low"},
			{RuleID: "pod.crash", Epoch: 1, Code: "app_exits"},
		},
	}
}

// Золотые значения фиксируют формат сериализации: изменение формата рвёт связь «повторение → предыдущее»
// у уже сохранённых расследований, поэтому оно должно быть осознанным (поднять signatureVersion).
func TestSignatureGolden(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		in   feedback.SignatureInput
		want string
	}{
		"full": {baseInput(), "19fb699597a15053ec40d7a7d7568b89"},
		"symptoms only": {feedback.SignatureInput{WorkloadKind: "Node", WorkloadName: "n1",
			Symptoms: []string{"pod.pending"}}, "7586c4bb215731f1e44c494f020b6947"},
		"hypotheses only": {feedback.SignatureInput{WorkloadKind: "Pod", WorkloadNamespace: "shop", WorkloadName: "api-1",
			Hypotheses: []feedback.Ref{{RuleID: "r", Code: "c"}}}, "1a21cc8c3286751dd144f00d5979cce0"},
	}
	for name, c := range cases {
		if got := feedback.Signature(c.in); got != c.want {
			t.Errorf("%s: signature = %q, want %q", name, got, c.want)
		}
	}
}

func TestSignatureIgnoresOrderDuplicatesAndEpoch(t *testing.T) {
	t.Parallel()
	want := feedback.Signature(baseInput())

	shuffled := baseInput()
	shuffled.Symptoms = []string{"pod.oom_killed", "pod.crash_loop", "pod.oom_killed"}
	shuffled.Hypotheses = []feedback.Ref{
		{RuleID: "pod.crash", Epoch: 7, Code: "app_exits"},
		{RuleID: "pod.oom", Epoch: 1, Code: "memory_limit_too_low"},
		{RuleID: "pod.crash", Epoch: 2, Code: "app_exits"},
	}
	if got := feedback.Signature(shuffled); got != want {
		t.Errorf("order/duplicates/epoch changed signature: %q != %q", got, want)
	}
}

func TestSignatureDistinguishesInputs(t *testing.T) {
	t.Parallel()
	base := feedback.Signature(baseInput())
	mutations := map[string]func(*feedback.SignatureInput){
		"kind":      func(in *feedback.SignatureInput) { in.WorkloadKind = "StatefulSet" },
		"namespace": func(in *feedback.SignatureInput) { in.WorkloadNamespace = "other" },
		"name":      func(in *feedback.SignatureInput) { in.WorkloadName = "web" },
		"symptom":   func(in *feedback.SignatureInput) { in.Symptoms = []string{"pod.crash_loop"} },
		"rule":      func(in *feedback.SignatureInput) { in.Hypotheses[0].RuleID = "pod.other" },
		"code":      func(in *feedback.SignatureInput) { in.Hypotheses[0].Code = "other" },
		"fewer":     func(in *feedback.SignatureInput) { in.Hypotheses = in.Hypotheses[:1] },
	}
	for name, mutate := range mutations {
		in := baseInput()
		mutate(&in)
		if got := feedback.Signature(in); got == base || got == "" {
			t.Errorf("%s: signature did not change (%q)", name, got)
		}
	}
	// Границы полей однозначны: перенос символа между соседними полями не даёт тот же хэш.
	a := feedback.Signature(feedback.SignatureInput{WorkloadKind: "ab", WorkloadName: "c", Symptoms: []string{"s"}})
	b := feedback.Signature(feedback.SignatureInput{WorkloadKind: "a", WorkloadName: "bc", Symptoms: []string{"s"}})
	if a == b {
		t.Error("field boundary is ambiguous")
	}
}

func TestSignatureEmpty(t *testing.T) {
	t.Parallel()
	cases := map[string]feedback.SignatureInput{
		"no workload name":     {WorkloadKind: "Pod", Symptoms: []string{"pod.pending"}},
		"no symptoms no hypos": {WorkloadKind: "Pod", WorkloadName: "x"},
	}
	for name, in := range cases {
		if got := feedback.Signature(in); got != "" {
			t.Errorf("%s: signature = %q, want empty", name, got)
		}
	}
}
