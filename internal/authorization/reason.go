// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization

// Стабильные безопасные reason-коды GrantDecision.ReasonCode (описание — REASONS.md). Коды — публичный
// контракт: значения не меняются и не переиспользуются. Они не содержат значений полей, имён чужих целей,
// владельцев и окружений.
const (
	ReasonGranted = "granted"

	ReasonRequestInvalid       = "request_invalid"
	ReasonPrincipalUnsupported = "principal_kind_unsupported"
	ReasonPrincipalUnknown     = "principal_unknown"
	ReasonPrincipalNotActive   = "principal_not_active"
	ReasonTargetNotAccessible  = "target_not_accessible"
	ReasonActionUnknown        = "action_unknown"

	ReasonDelegationRequired              = "delegation_required"
	ReasonDelegationInvalid               = "delegation_invalid"
	ReasonDelegationRevoked               = "delegation_revoked"
	ReasonDelegationSuspended             = "delegation_suspended"
	ReasonDelegationExpired               = "delegation_expired"
	ReasonDelegationConstraintUnsupported = "delegation_constraint_unsupported"
	ReasonDelegationConstraintNotMet      = "delegation_constraint_not_satisfied"
	ReasonDelegationAuditUnconfirmed      = "delegation_audit_unconfirmed"

	ReasonGrantRequired              = "grant_required"
	ReasonGrantInvalid               = "grant_invalid"
	ReasonGrantRevoked               = "grant_revoked"
	ReasonGrantExpired               = "grant_expired"
	ReasonGrantConstraintUnsupported = "grant_constraint_unsupported"
	ReasonGrantConstraintNotMet      = "grant_constraint_not_satisfied"
	ReasonGrantAuditUnconfirmed      = "grant_audit_unconfirmed"

	ReasonStaleRevision         = "stale_revision"
	ReasonDependencyUnavailable = "dependency_unavailable"
)

// ReasonCodes возвращает все стабильные коды (для документации и тестов синхронизации).
func ReasonCodes() []string {
	return []string{
		ReasonGranted, ReasonRequestInvalid, ReasonPrincipalUnsupported, ReasonPrincipalUnknown,
		ReasonPrincipalNotActive, ReasonTargetNotAccessible, ReasonActionUnknown,
		ReasonDelegationRequired, ReasonDelegationInvalid, ReasonDelegationRevoked, ReasonDelegationSuspended,
		ReasonDelegationExpired, ReasonDelegationConstraintUnsupported, ReasonDelegationConstraintNotMet,
		ReasonDelegationAuditUnconfirmed,
		ReasonGrantRequired, ReasonGrantInvalid, ReasonGrantRevoked, ReasonGrantExpired,
		ReasonGrantConstraintUnsupported, ReasonGrantConstraintNotMet, ReasonGrantAuditUnconfirmed,
		ReasonStaleRevision, ReasonDependencyUnavailable,
	}
}
