// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package target

// ReasonCode — стабильный безопасный машинный код причины (ADR-0055). Это не текст Kubernetes API и не содержит
// значений полей, имён целей, владельцев чужих tenant'ов. Значения — публичный контракт: не меняются и не
// переиспользуются (описание — REASONS.md).
type ReasonCode string

const (
	ReasonClusterUnavailable    ReasonCode = "cluster_unavailable"
	ReasonClusterNotVisible     ReasonCode = "cluster_not_visible"
	ReasonSelectorInvalid       ReasonCode = "selector_invalid"
	ReasonSelectorLimitExceeded ReasonCode = "selector_limit_exceeded"
	ReasonTargetNotFound        ReasonCode = "target_not_found"
	ReasonTargetIdentityIncompl ReasonCode = "target_identity_incomplete"
	ReasonOwnershipUnresolved   ReasonCode = "ownership_unresolved"
	ReasonGroupRevisionChanged  ReasonCode = "group_revision_changed"
	ReasonResolutionTimeout     ReasonCode = "resolution_timeout"
)

// ReasonCodes возвращает все стабильные коды в фиксированном порядке (для документации и тестов синхронизации).
func ReasonCodes() []ReasonCode {
	return []ReasonCode{
		ReasonClusterUnavailable, ReasonClusterNotVisible, ReasonSelectorInvalid, ReasonSelectorLimitExceeded,
		ReasonTargetNotFound, ReasonTargetIdentityIncompl, ReasonOwnershipUnresolved, ReasonGroupRevisionChanged,
		ReasonResolutionTimeout,
	}
}
