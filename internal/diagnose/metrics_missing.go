// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"

	"github.com/24DSO-lab/kubron/internal/signals"
)

// Коды подсказок о недостающем контексте метрик. Подсказка спокойная: метрики необязательны, отчёт полон и без них.
const (
	missingMetricsUnavailable = "metrics.unavailable"        // стек метрик целиком недоступен или не запрашивался
	missingMetricsSignal      = "metrics.signal_unavailable" // отдельный сигнал не получен
)

// addMetricsMissing записывает в отчёт, чего не хватило из метрик. Подсказки добавляются только когда метрики
// запрашивались (есть симптом, которому они нужны). Причина стека (не установлен, нет прав, недоступен) выводится
// одной записью вместо записи на каждый сигнал; иначе — по одной записи на пару (сигнал, причина). Дедупликация
// собственная: builder.missing сравнивает только код и ресурс и склеил бы разные сигналы.
func addMetricsMissing(b *builder, o *signalOutcome) {
	if o == nil || len(o.Wants) == 0 {
		return
	}
	reason := signals.ReasonUnavailable
	switch {
	case o.Skipped || o.Set == nil:
		// не запрашивали или программная сбой провайдера: для человека это «метрики недоступны».
	case o.Set.StackReason() != "":
		reason = o.Set.StackReason()
	default:
		for _, w := range o.Wants {
			g, ok := o.Set.GapOf(w.Signal, w.Subject)
			if !ok {
				continue
			}
			addMissing(b, MissingContext{
				Code:     missingMetricsSignal,
				Resource: subjectRef(b.c, w.Subject),
				Params:   map[string]string{"signal": string(w.Signal), "reason": string(g.Reason)},
			}, true)
		}
		return
	}
	addMissing(b, MissingContext{Code: missingMetricsUnavailable, Resource: b.rep.Target, Params: map[string]string{"reason": string(reason)}}, false)
}

// addMissing добавляет запись, если такой ещё нет. bySignal — различать записи одного кода по сигналу и причине, но
// не по цели: один и тот же пробел у двух контейнеров не множит подсказки.
func addMissing(b *builder, m MissingContext, bySignal bool) {
	same := func(x MissingContext) bool {
		if x.Code != m.Code {
			return false
		}
		if bySignal {
			return x.Params["signal"] == m.Params["signal"] && x.Params["reason"] == m.Params["reason"]
		}
		return x.Resource == m.Resource
	}
	if !slices.ContainsFunc(b.rep.Missing, same) {
		b.rep.Missing = append(b.rep.Missing, m)
	}
}
