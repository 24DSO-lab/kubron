// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"sort"
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Run ищет по живым данным общий источник и затронутое множество нагрузок (ADR-0033 §2–§4).
// Вход — уже собранный diagnose.Context и Reader для дополнительных чтений (не более MaxExtraReads);
// now передаётся параметром: правила время не читают. Результат детерминирован при тех же входных данных.
// Ошибка возвращается только программная: недоступные данные дают Missing и Partial в результате, а при остатке
// до deadline меньше MinDeadlineReserve корреляция не запускается и возвращается пустой результат с Partial.
func Run(ctx context.Context, r diagnose.Reader, c *diagnose.Context, now time.Time) (*Result, error) {
	res := &Result{Version: Version, ObservedAt: now}
	if c == nil || r == nil {
		return res, nil
	}
	if dl, ok := ctx.Deadline(); ok && dl.Sub(now) < MinDeadlineReserve {
		res.Missing = []diagnose.MissingContext{{Code: codeUnavailable, Resource: c.Target,
			Params: map[string]string{"reason": reasonDeadline}}}
		res.Partial = []string{PartialName}
		return res, nil
	}

	e := &env{r: &budgetReader{r: r, left: MaxExtraReads}, dc: c, now: now}
	order := append([]namedRule(nil), registry...)
	sort.Slice(order, func(i, j int) bool { return order[i].name < order[j].name })

	var cands []Candidate
	for _, rule := range order {
		if err := ctx.Err(); err != nil {
			e.unavailable(c.Target, reasonDeadline)
			break
		}
		cands = append(cands, rule.fn(ctx, e)...)
	}
	sortCandidates(cands)
	if len(cands) > MaxCandidates {
		cands = cands[:MaxCandidates]
	}
	res.Candidates = cands
	res.Facts = retainFacts(e.facts, cands)
	res.Missing = e.missing
	if len(e.missing) > 0 {
		res.Partial = []string{PartialName}
	}
	return res, nil
}

// retainFacts оставляет факты, на которые ссылаются кандидаты (кандидаты вне MaxCandidates отброшены), и
// перенумеровывает их подряд, обновляя Supporting. Порядок фактов сохраняется.
func retainFacts(all []diagnose.Fact, cands []Candidate) []diagnose.Fact {
	used := map[string]bool{}
	for _, c := range cands {
		for _, id := range c.Supporting {
			used[id] = true
		}
	}
	renum := map[string]string{}
	var out []diagnose.Fact
	for _, f := range all {
		if !used[f.ID] {
			continue
		}
		id := "cf" + itoa(len(out)+1)
		renum[f.ID] = id
		f.ID = id
		out = append(out, f)
	}
	for i := range cands {
		for j, id := range cands[i].Supporting {
			cands[i].Supporting[j] = renum[id]
		}
	}
	return out
}
