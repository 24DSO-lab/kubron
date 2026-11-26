// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// RecomputeCorrelation пересчитывает снимок корреляции открытого расследования по живым данным кластера
// (ADR-0033 §5) — по явному действию пользователя, фонового пересчёта нет.
//
// Меняется только Incident.Correlation (и UpdatedAt). Отчёт, фрагменты, снимок Events и привязка к проблеме остаются
// прежними: проблему пересчёт не создаёт и не меняет, автоприсоединение — только при открытии. Закрытое расследование
// не пересчитывается (ErrClosed, как у Refresh). Повторный вызов на тех же данных даёт тот же снимок с новым временем
// наблюдения.
//
// Живой контекст собирается без логов и без метрик: корреляции нужны только объекты кластера. Если правила не дали
// снимка (отмена контекста, паника правил), прежний снимок не затирается и возвращается diagnose.ErrUnavailable —
// та же ошибка, что и при отсутствии источника данных кластера (у неё уже есть перевод). Неполный снимок
// (недоступный источник) сохраняется — его неполнота записана в самом снимке (Result.Partial, Result.Missing).
func (s *Service) RecomputeCorrelation(ctx context.Context, c Caller, id string) (*Incident, error) {
	if err := CheckScope(c.Scope); err != nil {
		return nil, err
	}
	if c.Reader == nil {
		return nil, diagnose.ErrUnavailable
	}
	cur, err := s.b.GetIncident(ctx, c.Scope, id)
	if err != nil {
		return nil, err
	}
	if cur.Status != StatusOpen {
		return nil, ErrClosed
	}
	dc, err := diagnose.CollectWith(ctx, c.Reader, cur.Target, diagnose.CollectOptions{Logs: false, Now: s.now})
	if err != nil {
		return nil, err
	}
	// Рабочая копия: correlate пишет результат в Incident, а прежний снимок должен пережить неудачный пересчёт.
	scratch := &Incident{ID: cur.ID, Scope: cur.Scope, Target: cur.Target}
	s.correlate(ctx, c.Reader, dc, scratch)
	fresh := scratch.Correlation
	if fresh == nil {
		return nil, diagnose.ErrUnavailable
	}
	return s.mutate(ctx, c.Scope, id, func(in *Incident) error {
		if in.Status != StatusOpen {
			return ErrClosed
		}
		in.Correlation = fresh
		return nil
	})
}
