// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"cmp"
	"slices"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/feedback"
)

// summarizeFeedback дописывает в строку истории ссылки на правила (подтверждённая и отвергнутые гипотезы,
// помогшие и не помогшие рекомендации), признаки отчёта и оценку корреляции (ADR-0034 §5). Вызывается из
// Summarize; только коды и числа, без текстов, имён ресурсов и параметров. Ссылки без RuleID (отчёты до
// версии 2) пропускаются без догадок по коду. Безопасна для пустого отчёта и расследования без отметки.
func (in *Incident) summarizeFeedback(s *Summary) {
	rep := &in.Report
	if rep.Graph != nil {
		s.Features = append(s.Features, feedback.FeatureGraph)
	}
	if slices.ContainsFunc(rep.Facts, func(f diagnose.Fact) bool { return f.Source.Kind == diagnose.SourceMetric }) {
		s.Features = append(s.Features, feedback.FeatureMetrics)
	}
	if in.Correlation != nil {
		s.Features = append(s.Features, feedback.FeatureCorrelation)
	}

	o := in.Outcome
	if o == nil {
		return
	}
	hyps := map[string]RuleRef{}
	for _, h := range rep.Hypotheses {
		if ref, ok := ruleRefOf(h.RuleID, h.RuleEpoch, h.Code); ok {
			hyps[h.ID] = ref
		}
	}
	recs := map[string]RuleRef{}
	for _, r := range rep.Recommendations {
		if ref, ok := ruleRefOf(r.RuleID, r.RuleEpoch, r.Code); ok {
			recs[r.ID] = ref
		}
	}
	if ref, ok := hyps[o.ConfirmedHypothesisID]; ok && o.ConfirmedHypothesisID != "" {
		s.Confirmed = &ref
	}
	s.Rejected = refsOf(hyps, o.RejectedHypothesisIDs)
	s.Helpful = refsOf(recs, o.HelpfulRecommendationIDs)
	s.Useless = refsOf(recs, o.UselessRecommendationIDs)

	if c := o.Correlation; c != nil && in.Correlation != nil {
		for _, cand := range in.Correlation.Candidates {
			if cand.ID == c.CandidateID {
				s.CorrelationVerdict = &CorrelationVerdict{Kind: string(cand.Kind), Confidence: string(cand.Confidence), Verdict: c.Verdict}
				break
			}
		}
	}
}

// refsOf переводит идентификаторы записей отчёта в ссылки на правила: без повторов, по порядку ключа, не более
// MaxOutcomeRefs; nil, если ссылок нет.
func refsOf(byID map[string]RuleRef, ids []string) []RuleRef {
	var out []RuleRef
	for _, id := range ids {
		if ref, ok := byID[id]; ok && !slices.Contains(out, ref) {
			out = append(out, ref)
		}
	}
	slices.SortFunc(out, func(a, b RuleRef) int {
		return cmp.Or(cmp.Compare(a.RuleID, b.RuleID), cmp.Compare(a.Epoch, b.Epoch), cmp.Compare(a.Code, b.Code))
	})
	if len(out) > MaxOutcomeRefs {
		out = out[:MaxOutcomeRefs]
	}
	return out
}
