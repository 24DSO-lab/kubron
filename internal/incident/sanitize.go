// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"maps"
	"slices"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// RedactedPlaceholder заменяет текст, который не удалось отредактировать: сырое значение не сохраняется никогда.
const RedactedPlaceholder = "[unavailable]"

// redactText пропускает текст через редактор (ADR-0022). Fail-closed: сбой редактора — заменитель, а не оригинал.
func (s *Service) redactText(in string) string {
	if in == "" {
		return ""
	}
	in = truncateUTF8(in, MaxEventMessage)
	res, err := s.red.Redact([]byte(in))
	if err != nil {
		return RedactedPlaceholder
	}
	return res.Text
}

func (s *Service) redactParams(p map[string]string) map[string]string {
	if p == nil {
		return nil
	}
	out := make(map[string]string, len(p))
	for k, v := range p {
		out[k] = s.redactText(v)
	}
	return out
}

// sanitizeReport редактирует значения параметров отчёта (сообщения условий и событий, имена образов и т. п.):
// всё, что пришло из кластера свободным текстом. Возвращает копию.
func (s *Service) sanitizeReport(r diagnose.Report) diagnose.Report {
	r.Facts = slices.Clone(r.Facts)
	for i := range r.Facts {
		r.Facts[i].Params = s.redactParams(r.Facts[i].Params)
	}
	r.Hypotheses = slices.Clone(r.Hypotheses)
	for i := range r.Hypotheses {
		r.Hypotheses[i].Params = s.redactParams(r.Hypotheses[i].Params)
	}
	r.Recommendations = slices.Clone(r.Recommendations)
	for i := range r.Recommendations {
		r.Recommendations[i].Params = s.redactParams(r.Recommendations[i].Params)
	}
	r.Steps = slices.Clone(r.Steps)
	for i := range r.Steps {
		r.Steps[i].Params = s.redactParams(r.Steps[i].Params)
	}
	r.Missing = slices.Clone(r.Missing)
	for i := range r.Missing {
		r.Missing[i].Params = s.redactParams(r.Missing[i].Params)
	}
	return r
}

// sanitizeEvents редактирует сообщения событий и ограничивает снимок самыми новыми MaxSnapshotEvents.
func (s *Service) sanitizeEvents(in []diagnose.Event) []diagnose.Event {
	out := make([]diagnose.Event, len(in))
	for i, e := range in {
		e.Message = s.redactText(e.Message)
		out[i] = e
	}
	return capEvents(out)
}

func capEvents(ev []diagnose.Event) []diagnose.Event {
	slices.SortStableFunc(ev, func(a, b diagnose.Event) int { return a.LastSeen.Compare(b.LastSeen) })
	if len(ev) > MaxSnapshotEvents {
		ev = ev[len(ev)-MaxSnapshotEvents:]
	}
	return ev
}

type eventKey struct{ kind, name, reason, msg string }

// mergeEvents объединяет прежний снимок с новыми событиями: события, уже исчезнувшие из кластера, остаются —
// ради этого снимок и существует. Одинаковое событие обновляется (счётчик и время).
func mergeEvents(old, fresh []diagnose.Event) []diagnose.Event {
	idx := map[eventKey]diagnose.Event{}
	for _, e := range old {
		idx[eventKey{e.Object.Kind, e.Object.Name, e.Reason, e.Message}] = e
	}
	for _, e := range fresh {
		k := eventKey{e.Object.Kind, e.Object.Name, e.Reason, e.Message}
		if prev, ok := idx[k]; ok && prev.LastSeen.After(e.LastSeen) {
			continue
		}
		idx[k] = e
	}
	return capEvents(slices.Collect(maps.Values(idx)))
}
