// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/opsplatform"
)

func TestMapReadErrKeepsBothSentinelsAndTheText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ from, to error }{
		{opsplatform.ErrNotFound, diagnose.ErrNotFound},
		{opsplatform.ErrForbidden, diagnose.ErrForbidden},
		{opsplatform.ErrUnavailable, diagnose.ErrUnavailable},
		{opsplatform.ErrTooLarge, diagnose.ErrTooLarge},
	} {
		wrapped := fmt.Errorf("pods/log: %w", tc.from)
		got := mapReadErr(wrapped)
		if !errors.Is(got, tc.to) || !errors.Is(got, tc.from) {
			t.Errorf("%v: not matched by both sentinels", tc.from)
		}
		if got.Error() != wrapped.Error() {
			t.Errorf("the message changed: %q", got.Error())
		}
	}
	other := errors.New("boom")
	if mapReadErr(other) != other || mapReadErr(nil) != nil {
		t.Error("unknown errors pass through untouched")
	}
}

func TestIncidentRedactorIsFailClosedAndMapsErrors(t *testing.T) {
	t.Parallel()
	red := incidentRedactor()
	res, err := red.Redact([]byte("password=hunter2hunter2 error: refused"))
	if err != nil || strings.Contains(res.Text, "hunter2hunter2") || res.Version == 0 {
		t.Errorf("res=%+v err=%v", res, err)
	}
	if _, err := red.Redact([]byte("bad \x00 binary")); !errors.Is(err, incident.ErrRedactNotText) {
		t.Errorf("binary input: %v", err)
	}
	if _, err := red.Redact(make([]byte, 4<<20)); err == nil {
		t.Error("an oversized input must fail, not pass through")
	}
}

func TestSetupIncidentsRejectsBadEnvironment(t *testing.T) {
	t.Parallel()
	root := &App{}
	if _, err := SetupIncidents(root, func(k string) string {
		if k == incident.EnvFragmentRetention {
			return "99"
		}
		return ""
	}); err == nil || root.incidents != nil {
		t.Errorf("retention above 15 days must be refused: %v", err)
	}
}

func TestIncidentMethodsWithoutServiceFailClosed(t *testing.T) {
	t.Parallel()
	a := &App{}
	if _, err := a.IncidentSaveSettings(incident.DefaultSettings()); err == nil {
		t.Error("settings change without authorization/service must fail")
	}
}
