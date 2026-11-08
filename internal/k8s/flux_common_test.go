// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func cond(typ, status string) ConditionInfo { return ConditionInfo{Type: typ, Status: status} }

func TestFluxStateOf(t *testing.T) {
	cases := []struct {
		name      string
		conds     []ConditionInfo
		suspended bool
		want      FluxState
	}{
		{"no conditions", nil, false, FluxStateUnknown},
		{"empty slice", []ConditionInfo{}, false, FluxStateUnknown},
		{"ready", []ConditionInfo{cond("Ready", "True")}, false, FluxStateReady},
		{"ready status is case-insensitive", []ConditionInfo{cond("Ready", "true")}, false, FluxStateReady},
		{"failed", []ConditionInfo{cond("Ready", "False")}, false, FluxStateFailed},
		{"failed with Reconciling=False", []ConditionInfo{cond("Ready", "False"), cond("Reconciling", "False")}, false, FluxStateFailed},
		{"reconciling beats failed", []ConditionInfo{cond("Ready", "False"), cond("Reconciling", "True")}, false, FluxStateReconciling},
		{"reconciling with Ready unknown", []ConditionInfo{cond("Ready", "Unknown"), cond("Reconciling", "True")}, false, FluxStateReconciling},
		{"reconciling beats ready", []ConditionInfo{cond("Ready", "True"), cond("Reconciling", "True")}, false, FluxStateReconciling},
		{"ready unknown without Reconciling", []ConditionInfo{cond("Ready", "Unknown")}, false, FluxStateUnknown},
		{"stalled beats failed", []ConditionInfo{cond("Ready", "False"), cond("Stalled", "True")}, false, FluxStateStalled},
		{"stalled beats reconciling", []ConditionInfo{cond("Stalled", "True"), cond("Reconciling", "True")}, false, FluxStateStalled},
		{"Stalled=False is not stalled", []ConditionInfo{cond("Ready", "True"), cond("Stalled", "False")}, false, FluxStateReady},
		{"suspended beats ready", []ConditionInfo{cond("Ready", "True")}, true, FluxStateSuspended},
		{"suspended beats stalled", []ConditionInfo{cond("Ready", "False"), cond("Stalled", "True")}, true, FluxStateSuspended},
		{"suspended without conditions", nil, true, FluxStateSuspended},
		{"foreign conditions only", []ConditionInfo{cond("Healthy", "True")}, false, FluxStateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FluxStateOf(tc.conds, tc.suspended); got != tc.want {
				t.Errorf("FluxStateOf = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFluxConditionsDefensive(t *testing.T) {
	for name, obj := range map[string]map[string]any{
		"nil object":            nil,
		"no status":             {},
		"status not a map":      {"status": "oops"},
		"conditions not a list": {"status": map[string]any{"conditions": "oops"}},
		"items of wrong type":   {"status": map[string]any{"conditions": []any{"x", 1, nil}}},
	} {
		got := fluxConditions(obj)
		if got == nil || len(got) != 0 {
			t.Errorf("%s: expected an empty non-nil slice, got %#v", name, got)
		}
	}

	got := fluxConditions(map[string]any{"status": map[string]any{"conditions": []any{
		map[string]any{"type": "Ready", "status": "False", "reason": "BuildFailed", "lastTransitionTime": "2026-11-01T10:00:00Z", "message": "build failed"},
		"junk",
		map[string]any{"type": 5, "status": true}, // неожиданные типы полей дают нулевые значения
	}}})
	want := []ConditionInfo{
		{Type: "Ready", Status: "False", Reason: "BuildFailed", LastTransitionTime: "2026-11-01T10:00:00Z", Message: "build failed"},
		{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("conditions: got %+v, want %+v", got, want)
	}
}

func TestFluxMessagesAreRedacted(t *testing.T) {
	const s1, s2, s3 = "S3CRET-pass", "S3CRET-url", "S3CRET-bearer"
	obj := map[string]any{"status": map[string]any{"conditions": []any{
		map[string]any{"type": "Ready", "status": "False",
			"message": "clone failed: password=" + s1 + " https://user:" + s2 + "@git.example.com/repo.git Authorization: Bearer " + s3},
	}}}
	raw, err := json.Marshal(fluxConditions(obj))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{s1, s2, s3} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("secret %q leaked through a condition message: %s", secret, raw)
		}
	}
	if !strings.Contains(string(raw), "git.example.com") {
		t.Errorf("the harmless part of the message must stay: %s", raw)
	}
}

func TestRedactFluxMessageTruncates(t *testing.T) {
	long := strings.Repeat("я", fluxMaxMessageRunes+500)
	got := redactFluxMessage(long)
	if r := []rune(got); len(r) != fluxMaxMessageRunes+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("a long message is cut to %d runes plus an ellipsis, got %d runes", fluxMaxMessageRunes, len(r))
	}
	if redactFluxMessage("") != "" || redactFluxMessage("short") != "short" {
		t.Error("short and empty messages stay unchanged")
	}
}

func TestStripURLUserinfoForFluxSources(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "",
		"https://github.com/org/repo":          "https://github.com/org/repo",
		"https://user:S3CRET-t@github.com/o/r": "https://github.com/o/r",
		"https://S3CRET-token@github.com/o/r":  "https://github.com/o/r",
		"ssh://git@github.com/org/repo":        "ssh://github.com/org/repo",
		"oci://ghcr.io/org/manifests":          "oci://ghcr.io/org/manifests",
		"not a url \x7f":                       "not a url \x7f",
	} {
		if got := stripURLUserinfo(in); got != want {
			t.Errorf("stripURLUserinfo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFluxAddressHost(t *testing.T) {
	for in, want := range map[string]string{
		"https://hooks.slack.com/services/T000/B000/S3CRET-path":           "https://hooks.slack.com",
		"https://user:S3CRET-pw@chat.example.com:8443/hook?token=S3CRET-q": "https://chat.example.com:8443",
		"http://webhook.local":   "http://webhook.local",
		"relative/path?token=x":  "",
		"":                       "",
		"://broken":              "",
		"mailto:ops@example.com": "",
	} {
		got := fluxAddressHost(in)
		if got != want {
			t.Errorf("fluxAddressHost(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(got, "S3CRET") {
			t.Errorf("fluxAddressHost(%q) leaked a secret: %q", in, got)
		}
	}
}

func TestFluxAnnotationsDropLastApplied(t *testing.T) {
	got := fluxAnnotations(map[string]string{
		fluxLastAppliedAnnotation:         `{"spec":{"values":{"password":"S3CRET-last"}}}`,
		"reconcile.fluxcd.io/requestedAt": "2026-11-07T00:00:00Z",
	})
	if _, ok := got[fluxLastAppliedAnnotation]; ok || len(got) != 1 {
		t.Errorf("last-applied-configuration must be dropped: %v", got)
	}
	if got := fluxAnnotations(nil); got == nil || len(got) != 0 {
		t.Errorf("nil input gives an empty non-nil map: %#v", got)
	}
}
