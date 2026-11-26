// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"time"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/feedback"
	"github.com/24DSO-lab/kubron/internal/incident"
)

// DTO проблем и обратной связи (docs/intelligence-contracts.md, раздел 7). JSON — camelCase; фронтенд (D8) зеркалит эти
// типы в frontend/src/types/incident.ts. Поля менять только вместе с контрактом.

// IncidentProblemListQuery — страница проблем кластера.
type IncidentProblemListQuery struct {
	Status incident.ProblemStatus `json:"status"`
	Limit  int                    `json:"limit"`
	Cursor string                 `json:"cursor"`
}

// ProblemSummaryDTO — строка списка проблем.
type ProblemSummaryDTO struct {
	ID            string                 `json:"id"`
	Key           string                 `json:"key"`
	Kind          string                 `json:"kind"` // вид корреляции: shared_node, shared_storage, ...
	Source        diagnose.ResourceRef   `json:"source"`
	Status        incident.ProblemStatus `json:"status"`
	Origin        incident.ProblemOrigin `json:"origin"`
	MemberCount   int                    `json:"memberCount"`
	AffectedTotal int                    `json:"affectedTotal"`
	UpdatedAt     time.Time              `json:"updatedAt"`
}

// ProblemMemberDTO — расследование в составе проблемы. Summary пуст, если расследование уже удалено или недоступно.
type ProblemMemberDTO struct {
	IncidentID string            `json:"incidentId"`
	AddedAt    time.Time         `json:"addedAt"`
	AddedBy    string            `json:"addedBy"` // ID пользователя или "auto"
	Summary    *incident.Summary `json:"summary,omitempty"`
}

// ProblemListDTO — страница списка проблем.
type ProblemListDTO struct {
	Items      []ProblemSummaryDTO `json:"items"`
	NextCursor string              `json:"nextCursor,omitempty"`
}

// ProblemDetailDTO — проблема с составом и затронутыми workload'ами: ProblemSummaryDTO плюс поля ниже (JSON плоский).
type ProblemDetailDTO struct {
	ProblemSummaryDTO
	Members           []ProblemMemberDTO        `json:"members"`
	Affected          []correlation.WorkloadRef `json:"affected"`
	AffectedTruncated bool                      `json:"affectedTruncated"`
	Detached          []string                  `json:"detached"`
}

// RuleStatsDTO — статистика правил: ответ internal/feedback как есть (JSON camelCase, срезы не null).
type RuleStatsDTO = feedback.Stats

// ExportFileDTO — готовый файл выгрузки: имя, MIME и содержимое в base64 (десктоп и веб сохраняют его одинаково).
type ExportFileDTO struct {
	Filename   string `json:"filename"`
	MIME       string `json:"mime"`
	DataBase64 string `json:"dataBase64"`
}

// IncidentOutcomeRequest — отметка результата расследования. Первые три поля — исходные; остальные — расширение
// обратной связи (ADR-0034 §2), все необязательные. Правила проверки и умолчания — в internal/incident (outcome.go).
type IncidentOutcomeRequest struct {
	Result                incident.Result `json:"result"`
	ConfirmedHypothesisID string          `json:"confirmedHypothesisId"`
	Note                  string          `json:"note"`

	RejectedHypothesisIDs    []string                      `json:"rejectedHypothesisIds,omitempty"`
	HelpfulRecommendationIDs []string                      `json:"helpfulRecommendationIds,omitempty"`
	UselessRecommendationIDs []string                      `json:"uselessRecommendationIds,omitempty"`
	OtherAction              bool                          `json:"otherAction,omitempty"`
	ResolvedAt               *time.Time                    `json:"resolvedAt,omitempty"`
	Correlation              *incident.CorrelationFeedback `json:"correlation,omitempty"`
}

// outcome переносит запрос в доменную отметку; MarkedBy и MarkedAt заполняет сервис.
func (r IncidentOutcomeRequest) outcome() incident.Outcome {
	return incident.Outcome{
		Result:                   r.Result,
		ConfirmedHypothesisID:    r.ConfirmedHypothesisID,
		Note:                     r.Note,
		RejectedHypothesisIDs:    r.RejectedHypothesisIDs,
		HelpfulRecommendationIDs: r.HelpfulRecommendationIDs,
		UselessRecommendationIDs: r.UselessRecommendationIDs,
		OtherAction:              r.OtherAction,
		ResolvedAt:               r.ResolvedAt,
		Correlation:              r.Correlation,
	}
}
