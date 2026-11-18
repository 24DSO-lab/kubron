// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"slices"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// correlate строит снимок корреляции (ADR-0033 §5) на уже собранном контексте и кладёт его в in.Correlation.
// Возвращает имена частично прочитанных источников для Report.Partial. Вызывается из Open после Analyze и до
// сохранения; сбой корреляции расследование не роняет: вторичный источник, его неудача — только Partial.
//
// Reader — тот же, что собирал контекст: корреляция делает до correlation.MaxExtraReads дополнительных чтений.
// Размер снимка ограничен константами пакета correlation; полей объектов, меток, аннотаций и текстов логов в нём
// нет по построению (ADR-0022), поэтому повторной редакции не требуется.
func (s *Service) correlate(ctx context.Context, r diagnose.Reader, dc *diagnose.Context, in *Incident) (partial []string) {
	defer func() {
		if recover() != nil { // программный сбой правил не должен ронять Open
			in.Correlation, partial = nil, []string{correlation.PartialName}
		}
	}()
	res, err := correlation.Run(ctx, r, dc, s.clock())
	if err != nil || res == nil {
		in.Correlation = nil
		return []string{correlation.PartialName}
	}
	in.Correlation = res
	return slices.Clone(res.Partial)
}
