// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"sort"
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Коды фактов правила shared_symptom.
const (
	factSymptomWorkloads  = "correlation.shared_symptom.workloads"
	factSymptomHypothesis = "correlation.shared_symptom.hypothesis"
	factSymptomTemporal   = "correlation.shared_symptom.temporal"
)

// minSymptomWorkloads — сколько нагрузок namespace должно показывать один симптом (ADR-0033 §3).
const minSymptomWorkloads = 5

// symptomConfidence — уверенность shared_symptom всегда low (ADR-0033 §4): структурной связи между нагрузками нет,
// есть лишь совпадение симптомов в одном namespace. Значение не вычисляется по признакам и выше не поднимается,
// поэтому кандидат этого вида никогда не создаёт проблему автоматически (порог автоприсоединения — medium).
const symptomConfidence = diagnose.ConfidenceLow

func init() { registerRule("shared_symptom", ruleSharedSymptom) }

// ruleSharedSymptom: пять и более нагрузок namespace показывают тот же симптом, что и цель расследования, а отчёт
// цели даёт гипотезу. Это подсказка «возможно, дело в общем», а не вывод. Коды гипотез остальных нагрузок правило не
// знает — их расследований нет, — поэтому подтверждается совпадение симптомов, а гипотеза взята только у цели;
// факт кандидата прямо об этом говорит.
func ruleSharedSymptom(ctx context.Context, e *env) []Candidate {
	ns := e.dc.Target.Namespace
	if ns == "" {
		return nil
	}
	targetSymptoms := map[string]bool{}
	for _, p := range e.dc.Pods {
		if p == nil {
			continue
		}
		for _, s := range inspectPod(p).symptoms {
			targetSymptoms[s] = true
		}
	}
	if len(targetSymptoms) == 0 {
		return nil
	}
	if rep := diagnose.Analyze(e.dc); len(rep.Hypotheses) == 0 {
		return nil
	}

	src := diagnose.ResourceRef{Kind: "Pod", Namespace: ns}
	res, err := e.r.List(ctx, gvkPod, diagnose.ListOptions{Namespace: ns, Limit: podListLimit})
	if err != nil {
		e.unavailable(src, reasonOf(err))
		return nil
	}
	if res.Truncated {
		e.unavailable(src, reasonTooLarge)
	}
	bySymptom := map[string]*workloadSet{}
	for i := range res.Items {
		p, ok := toPod(&res.Items[i])
		if !ok {
			continue
		}
		info := inspectPod(p)
		for _, s := range info.symptoms {
			if !targetSymptoms[s] {
				continue
			}
			set := bySymptom[s]
			if set == nil {
				set = newWorkloadSet()
				bySymptom[s] = set
			}
			// В нагрузку попадает только общий симптом: остальные у неё могут быть свои.
			set.add(e.workloadOf(ctx, p), podInfo{pod: p, symptoms: []string{s}, failedAt: info.failedAt})
		}
	}

	var best string
	for s, set := range bySymptom {
		if set.total() < minSymptomWorkloads {
			continue
		}
		if best == "" || set.total() > bySymptom[best].total() || set.total() == bySymptom[best].total() && s < best {
			best = s
		}
	}
	if best == "" {
		return nil
	}
	set := bySymptom[best]

	ref := diagnose.ResourceRef{Kind: "Namespace", Name: ns}
	specs := []factSpec{
		{factSymptomWorkloads, ref, map[string]string{"workloads": itoa(set.total()), "pods": itoa(set.pods)}},
		{factSymptomHypothesis, ref, nil},
	}
	if span, ok := symptomSpan(set); ok {
		specs = append(specs, factSymptomSpec(ref, span))
	}
	since := set.firstFailure()
	if since.IsZero() {
		since = e.now
	}
	c := Candidate{
		Kind: KindSharedSymptom, Source: ref, Confidence: symptomConfidence, Since: since,
		Affected: set.sorted(), Total: set.total(), Truncated: res.Truncated,
		Supporting: e.addFacts(specs),
	}
	c.ID = candidateID(c)
	return []Candidate{c}
}

// symptomSpan — разброс времени первых сбоев нагрузок, если он укладывается в окно корреляции. Это справка к
// кандидату: на уверенность она не влияет.
func symptomSpan(set *workloadSet) (time.Duration, bool) {
	var times []time.Time
	for _, w := range set.m {
		if !w.Since.IsZero() {
			times = append(times, w.Since)
		}
	}
	if len(times) < 2 {
		return 0, false
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	span := times[len(times)-1].Sub(times[0])
	return span, span <= CorrelationWindow
}

func factSymptomSpec(ref diagnose.ResourceRef, span time.Duration) factSpec {
	return factSpec{factSymptomTemporal, ref, map[string]string{"spanSeconds": itoa(int(span.Seconds()))}}
}
