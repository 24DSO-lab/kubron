// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"fmt"
)

// MaxOutcomeRefs — сколько отвергнутых гипотез и оценённых рекомендаций допускает одна отметка результата
// (ADR-0034 §2); столько же ссылок на правила попадает в строку истории. Не настраивается: изменить можно
// только новым ADR.
const MaxOutcomeRefs = 20

// prepareOutcome проверяет расширенные поля отметки результата относительно расследования и подставляет
// умолчания (ADR-0034 §2). Вызывается из MarkOutcome после базовой проверки, внутри чтения-изменения-записи
// расследования (при конфликте версий вызывается повторно, поэтому идемпотентна: исходные срезы и указатели
// o не изменяются, в o записываются новые). Ошибка — ErrInvalidOutcome; закрытое расследование отметке уже
// не подлежит (ErrClosed): повторная отметка молча переписала бы оценки, на которых строится статистика.
//
// Правила:
//   - идентификаторы гипотез и рекомендаций существуют в отчёте, без повторов, не более MaxOutcomeRefs;
//   - отвергнутые не содержат подтверждённой; «помогла» и «не помогла» не пересекаются;
//   - ResolvedAt допустим только при результате resolved и лежит между открытием и моментом отметки;
//     без него при resolved подставляется момент отметки;
//   - оценка корреляции ссылается на кандидата снимка Incident.Correlation и несёт известный вердикт.
func (*Service) prepareOutcome(in *Incident, o *Outcome) error {
	if in.Status == StatusClosed || in.Outcome != nil {
		return ErrClosed
	}

	rejected, err := cleanRefs("rejected hypothesis", o.RejectedHypothesisIDs, func(id string) bool {
		for _, h := range in.Report.Hypotheses {
			if h.ID == id {
				return true
			}
		}
		return false
	})
	if err != nil {
		return err
	}
	for _, id := range rejected {
		if id == o.ConfirmedHypothesisID {
			return fmt.Errorf("%w: hypothesis is both confirmed and rejected", ErrInvalidOutcome)
		}
	}
	knownRecommendation := func(id string) bool {
		for _, r := range in.Report.Recommendations {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	helpful, err := cleanRefs("helpful recommendation", o.HelpfulRecommendationIDs, knownRecommendation)
	if err != nil {
		return err
	}
	useless, err := cleanRefs("useless recommendation", o.UselessRecommendationIDs, knownRecommendation)
	if err != nil {
		return err
	}
	for _, id := range helpful {
		for _, u := range useless {
			if id == u {
				return fmt.Errorf("%w: recommendation is both helpful and useless", ErrInvalidOutcome)
			}
		}
	}

	if err := prepareResolvedAt(in, o); err != nil {
		return err
	}
	if c := o.Correlation; c != nil {
		if err := checkCorrelation(in, c); err != nil {
			return err
		}
		cp := *c
		o.Correlation = &cp
	}
	o.RejectedHypothesisIDs, o.HelpfulRecommendationIDs, o.UselessRecommendationIDs = rejected, helpful, useless
	return nil
}

// cleanRefs проверяет список идентификаторов и возвращает его копию без повторов (nil, если пуст).
func cleanRefs(what string, ids []string, known func(string) bool) ([]string, error) {
	if len(ids) > MaxOutcomeRefs {
		return nil, fmt.Errorf("%w: too many entries in %s list", ErrInvalidOutcome, what)
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || !known(id) {
			return nil, fmt.Errorf("%w: unknown %s", ErrInvalidOutcome, what)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func prepareResolvedAt(in *Incident, o *Outcome) error {
	switch {
	case o.ResolvedAt != nil:
		if o.Result != ResultResolved {
			return fmt.Errorf("%w: resolvedAt is allowed only for a resolved result", ErrInvalidOutcome)
		}
		t := Micro(*o.ResolvedAt)
		if t.Before(Micro(in.OpenedAt)) || t.After(Micro(o.MarkedAt)) {
			return fmt.Errorf("%w: resolvedAt is outside of the investigation period", ErrInvalidOutcome)
		}
		o.ResolvedAt = &t
	case o.Result == ResultResolved:
		t := Micro(o.MarkedAt)
		o.ResolvedAt = &t
	}
	return nil
}

func checkCorrelation(in *Incident, c *CorrelationFeedback) error {
	switch c.Verdict {
	case VerdictConfirmed, VerdictRejected, VerdictUnknown:
	default:
		return fmt.Errorf("%w: unknown correlation verdict", ErrInvalidOutcome)
	}
	if in.Correlation != nil {
		for _, cand := range in.Correlation.Candidates {
			if cand.ID == c.CandidateID && c.CandidateID != "" {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: unknown correlation candidate", ErrInvalidOutcome)
}

// ruleRefOf — ссылка на правило по гипотезе или рекомендации; ok=false — RuleID пуст (отчёт версии 1).
func ruleRefOf(ruleID string, epoch int, code string) (RuleRef, bool) {
	if ruleID == "" {
		return RuleRef{}, false
	}
	return RuleRef{RuleID: ruleID, Epoch: epoch, Code: code}, true
}
