// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident_test

import (
	"errors"
	"testing"

	"github.com/24DSO-lab/kubron/internal/incident"
	"github.com/24DSO-lab/kubron/internal/incident/incidenttest"
)

// Ошибки проблем сопоставимы с ErrNotFound: слой связки маппит их одним правилом.
func TestProblemErrors(t *testing.T) {
	if !errors.Is(incident.ErrProblemNotFound, incident.ErrNotFound) {
		t.Error("ErrProblemNotFound must match ErrNotFound")
	}
	if errors.Is(incident.ErrProblemFull, incident.ErrNotFound) || errors.Is(incident.ErrProblemsDisabled, incident.ErrNotFound) {
		t.Error("unrelated errors must not match ErrNotFound")
	}
}

// WithProblems возвращает тот же сервис; без хранилища ручные операции отвечают ErrProblemsDisabled.
func TestWithProblemsChains(t *testing.T) {
	svc := incident.NewService(incident.NewFileBackend(t.TempDir()), &fakeRedactor{})
	if svc.WithProblems(nil) != svc {
		t.Error("WithProblems must return the same service")
	}
	if _, err := svc.GetProblem(t.Context(), incidenttest.Scope, "p1"); !errors.Is(err, incident.ErrProblemsDisabled) {
		t.Errorf("GetProblem without a backend: %v", err)
	}
}
