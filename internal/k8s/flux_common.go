// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"net/url"
	"strings"

	"github.com/24DSO-lab/kubron/internal/logging"
)

const (
	// fluxMaxMessageRunes — предел длины сообщения контроллера в DTO (ошибки kustomize build бывают очень длинными).
	fluxMaxMessageRunes = 4096

	// fluxLastAppliedAnnotation — аннотация с копией объекта целиком; наружу не отдаётся.
	fluxLastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"
)

// FluxStateOf вычисляет итоговое состояние по conditions и spec.suspend.
// Приоритет: Suspended > Stalled > Failed (Ready=False и не Reconciling) > Reconciling > Ready > Unknown.
func FluxStateOf(conditions []ConditionInfo, suspended bool) FluxState {
	if suspended {
		return FluxStateSuspended
	}
	var ready, reconciling, stalled string
	for _, c := range conditions {
		switch c.Type {
		case "Ready":
			ready = c.Status
		case "Reconciling":
			reconciling = c.Status
		case "Stalled":
			stalled = c.Status
		}
	}
	switch {
	case isTrue(stalled):
		return FluxStateStalled
	case isFalse(ready) && !isTrue(reconciling):
		return FluxStateFailed
	case isTrue(reconciling):
		return FluxStateReconciling
	case isTrue(ready):
		return FluxStateReady
	}
	return FluxStateUnknown
}

func isTrue(s string) bool  { return strings.EqualFold(s, "True") }
func isFalse(s string) bool { return strings.EqualFold(s, "False") }

// fluxConditions разбирает status.conditions в []ConditionInfo. Никогда не возвращает nil; сообщения проходят
// redactFluxMessage. Элементы неожиданного типа пропускаются.
func fluxConditions(obj map[string]any) []ConditionInfo {
	items, _ := sliceAt(obj, "status", "conditions")
	out := make([]ConditionInfo, 0, len(items))
	for _, it := range items {
		cm, ok := it.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, ConditionInfo{
			Type:               strAt(cm, "type"),
			Status:             strAt(cm, "status"),
			LastTransitionTime: strAt(cm, "lastTransitionTime"),
			Reason:             strAt(cm, "reason"),
			Message:            redactFluxMessage(strAt(cm, "message")),
		})
	}
	return out
}

// redactFluxMessage готовит текст контроллера к показу: общий редактор секретов (ADR-0022) и предел длины.
func redactFluxMessage(s string) string {
	s = logging.Redact(s)
	if r := []rune(s); len(r) > fluxMaxMessageRunes {
		s = string(r[:fluxMaxMessageRunes]) + "…"
	}
	return s
}

// fluxAddressHost оставляет от адреса только схему и хост (Provider.spec.address): в пути и query вебхуков бывают
// токены. Непарсируемый или относительный адрес не отдаётся вовсе.
func fluxAddressHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// fluxAnnotations копирует аннотации без last-applied-configuration; пустые — nil-безопасная пустая карта.
func fluxAnnotations(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k == fluxLastAppliedAnnotation {
			continue
		}
		out[k] = v
	}
	return out
}
