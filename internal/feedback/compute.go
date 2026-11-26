// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// symptomNone — код «симптомов нет» (diagnose.SymptomNone). Покрытие по нему не считается: «ничего не найдено»
// не пробел в правилах. Пакет не импортирует diagnose, поэтому код повторён здесь.
const symptomNone = "none"

// Compute считает статистику правил по закрытым расследованиям (ADR-0034 §6). Чистая функция: результат зависит
// только от recs и now, вывод отсортирован.
//
// Правила подсчёта:
//   - в знаменатели входят только явные оценки человека; расследование без оценок — Unevaluated;
//   - точность гипотезы и корреляции считается по расследованиям с результатом resolved: unresolved и
//     inconclusive означают «проверить не удалось», это не оценка гипотезы; в покрытии симптома они учитываются;
//   - польза рекомендации считается по любым расследованиям с явной отметкой «помогла» или «не помогла»;
//   - записи без RuleID (отчёты до версии 2) пропускаются без догадок по коду;
//   - ниже MinSample долей нет (nil, Trusted == false), остаются только числа случаев.
func Compute(recs []Record, now time.Time) Stats {
	st := Stats{
		Records: len(recs), ComputedAt: now, RuleSetVersions: []int{},
		Hypotheses: []HypothesisStat{}, Recommendations: []RecommendationStat{},
		Symptoms: []SymptomStat{}, Correlations: []CorrelationStat{}, Features: []FeatureStat{},
	}
	if len(recs) == 0 {
		return st
	}

	recurred := map[string]bool{} // расследования, за которыми последовало повторение
	versions := map[int]bool{}
	for _, r := range recs {
		if r.RecurrenceOf != "" {
			recurred[r.RecurrenceOf] = true
		}
		if r.RuleSetVersion > 0 {
			versions[r.RuleSetVersion] = true
		}
	}
	for v := range versions {
		st.RuleSetVersions = append(st.RuleSetVersions, v)
	}
	slices.Sort(st.RuleSetVersions)

	type hypAgg struct {
		confirmed, rejected, resolved, recurred int
		durations                               []int64
	}
	type pair struct{ a, b int }
	var (
		hyps   = map[Ref]*hypAgg{}
		recsAg = map[Ref]*pair{}    // helpful, useless
		sympt  = map[string]*pair{} // closed, withConfirmed
		corr   = map[[2]string]*pair{}
		feat   = map[string]*[2]FeatureCounts{} // [0] — с признаком, [1] — без
	)
	for _, f := range []string{FeatureCorrelation, FeatureGraph, FeatureMetrics} {
		feat[f] = &[2]FeatureCounts{}
	}

	for _, r := range recs {
		resolved := r.Result == ResultResolved
		confirmed := validRef(r.Confirmed)
		rejected := dedupRefs(r.Rejected)
		helpful, useless := dedupRefs(r.Helpful), dedupRefs(r.Useless)
		verdict := r.CorrelationVerdict == VerdictConfirmed || r.CorrelationVerdict == VerdictRejected

		if !confirmed && len(rejected) == 0 && len(helpful) == 0 && len(useless) == 0 && !verdict {
			st.Unevaluated++
		}

		// Гипотезы: только resolved.
		if resolved {
			if confirmed {
				a := aggOf(hyps, *r.Confirmed, func() *hypAgg { return &hypAgg{} })
				a.confirmed++
				a.resolved++
				if r.ResolvedAt != nil && !r.ResolvedAt.Before(r.OpenedAt) {
					a.durations = append(a.durations, int64(r.ResolvedAt.Sub(r.OpenedAt)/time.Second))
				}
				if r.ID != "" && recurred[r.ID] {
					a.recurred++
				}
			}
			for _, ref := range rejected {
				aggOf(hyps, ref, func() *hypAgg { return &hypAgg{} }).rejected++
			}
		}

		// Рекомендации: любые результаты, только явные отметки.
		for _, ref := range helpful {
			aggOf(recsAg, ref, func() *pair { return &pair{} }).a++
		}
		for _, ref := range useless {
			aggOf(recsAg, ref, func() *pair { return &pair{} }).b++
		}

		// Покрытие симптомов: любые результаты.
		for _, code := range uniqueSorted(r.Symptoms) {
			if code == symptomNone || code == "" {
				continue
			}
			p := aggOf(sympt, code, func() *pair { return &pair{} })
			p.a++
			if confirmed {
				p.b++
			}
		}

		// Корреляция: только resolved.
		if resolved && verdict && r.CorrelationKind != "" {
			p := aggOf(corr, [2]string{r.CorrelationKind, r.CorrelationConfidence}, func() *pair { return &pair{} })
			if r.CorrelationVerdict == VerdictConfirmed {
				p.a++
			} else {
				p.b++
			}
		}

		// Признаки: точность гипотез с источником и без него.
		for name, groups := range feat {
			g := &groups[1]
			if slices.Contains(r.Features, name) {
				g = &groups[0]
			}
			g.Records++
			if resolved {
				if confirmed {
					g.Confirmed++
				}
				g.Rejected += len(rejected)
			}
		}
	}

	for ref, a := range hyps {
		h := HypothesisStat{Ref: ref, Confirmed: a.confirmed, Rejected: a.rejected,
			Sample: a.confirmed + a.rejected, Resolved: a.resolved, Recurred: a.recurred}
		h.Trusted = h.Sample >= MinSample
		h.Precision = share(a.confirmed, h.Sample)
		h.MedianResolveSeconds = median(a.durations)
		st.Hypotheses = append(st.Hypotheses, h)
	}
	for ref, p := range recsAg {
		s := RecommendationStat{Ref: ref, Helpful: p.a, Useless: p.b, Sample: p.a + p.b}
		s.Trusted = s.Sample >= MinSample
		s.Ratio = share(p.a, s.Sample)
		s.PreviouslyUnhelpful = s.Trusted && float64(p.b)/float64(s.Sample) >= UselessShare
		st.Recommendations = append(st.Recommendations, s)
	}
	for code, p := range sympt {
		st.Symptoms = append(st.Symptoms, SymptomStat{Code: code, Closed: p.a, WithConfirmed: p.b, Coverage: share(p.b, p.a)})
	}
	for key, p := range corr {
		st.Correlations = append(st.Correlations, CorrelationStat{Kind: key[0], Confidence: key[1],
			Confirmed: p.a, Rejected: p.b, Precision: share(p.a, p.a+p.b)})
	}
	for name, groups := range feat {
		fs := FeatureStat{Feature: name, With: groups[0], Without: groups[1]}
		fs.With.Precision = share(fs.With.Confirmed, fs.With.Confirmed+fs.With.Rejected)
		fs.Without.Precision = share(fs.Without.Confirmed, fs.Without.Confirmed+fs.Without.Rejected)
		st.Features = append(st.Features, fs)
	}
	sortStats(&st)
	return st
}

// sortStats приводит все списки к каноническому порядку: вывод Compute и Export не зависит от порядка входа.
func sortStats(st *Stats) {
	slices.SortFunc(st.Hypotheses, func(a, b HypothesisStat) int { return cmpRef(a.Ref, b.Ref) })
	slices.SortFunc(st.Recommendations, func(a, b RecommendationStat) int { return cmpRef(a.Ref, b.Ref) })
	slices.SortFunc(st.Symptoms, func(a, b SymptomStat) int { return cmp.Compare(a.Code, b.Code) })
	slices.SortFunc(st.Correlations, func(a, b CorrelationStat) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Confidence, b.Confidence))
	})
	slices.SortFunc(st.Features, func(a, b FeatureStat) int { return cmp.Compare(a.Feature, b.Feature) })
	slices.Sort(st.RuleSetVersions)
}

func cmpRef(a, b Ref) int {
	return cmp.Or(cmp.Compare(a.RuleID, b.RuleID), cmp.Compare(a.Epoch, b.Epoch), cmp.Compare(a.Code, b.Code))
}

// aggOf возвращает накопитель по ключу, создавая его при первом обращении.
func aggOf[K comparable, V any](m map[K]*V, k K, mk func() *V) *V {
	v, ok := m[k]
	if !ok {
		v = mk()
		m[k] = v
	}
	return v
}

// validRef — ссылка пригодна для статистики: без RuleID (старые отчёты) записи пропускаются, по Code не угадываем.
func validRef(r *Ref) bool { return r != nil && r.RuleID != "" }

// dedupRefs оставляет в расследовании по одному вхождению каждой ссылки и отбрасывает ссылки без RuleID.
func dedupRefs(in []Ref) []Ref {
	var out []Ref
	for _, r := range in {
		if r.RuleID == "" || slices.Contains(out, r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func uniqueSorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// share возвращает долю num/den с точностью до 4 знаков; nil, пока den ниже порога доверия.
func share(num, den int) *float64 {
	if den < MinSample {
		return nil
	}
	v := math.Round(float64(num)/float64(den)*1e4) / 1e4
	return &v
}

// median — медиана целых секунд; при чётном числе значений — полусумма двух средних (целочисленная). nil — нет данных.
func median(d []int64) *int64 {
	if len(d) == 0 {
		return nil
	}
	s := slices.Clone(d)
	slices.Sort(s)
	var m int64
	if n := len(s); n%2 == 1 {
		m = s[n/2]
	} else {
		m = (s[n/2-1] + s[n/2]) / 2
	}
	return &m
}
