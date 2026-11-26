// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"fmt"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Status — итог проверки одного ресурса (ADR-0027 §5).
type Status string

const (
	// StatusMatch — «соответствует».
	StatusMatch Status = "match"
	// StatusChanged — «изменён»: есть отличия по нормализованным путям.
	StatusChanged Status = "changed"
	// StatusMissing — «отсутствует в кластере».
	StatusMissing Status = "missing"
	// StatusUnchecked — «не проверено»: нет прав Kubernetes, ошибка API, тип недоступен и т. п. Это не «соответствует».
	StatusUnchecked Status = "unchecked"
	// StatusNew — «новый ресурс вне baseline». Режим выключен и в MVP не реализован: значение зарезервировано, чтобы
	// добавить режим без изменения контракта статусов.
	StatusNew Status = "new"
)

// Reason — причина пропуска ресурса: «не проверено» при проверке, «не снято» при создании версии.
type Reason string

const (
	ReasonForbidden         Reason = "forbidden"              // нет прав Kubernetes (или вне allowlist платформы)
	ReasonUnavailable       Reason = "unavailable"            // ошибка API, таймаут, сеть
	ReasonTooLarge          Reason = "too-large"              // ответ превысил лимит размера
	ReasonKindUnavailable   Reason = "kind-unavailable"       // тип ресурса отсутствует в кластере
	ReasonRulesMismatch     Reason = "rules-version-mismatch" // версия правил приложения не совпадает с версией baseline
	ReasonSecretUnsupported Reason = "secret-unsupported"     // Secret не поддерживается и не читается
	ReasonUnsupportedKind   Reason = "unsupported-kind"       // тип вне списка MVP
	ReasonTruncated         Reason = "truncated"              // список ресурсов не поместился в лимит выборки
	ReasonNotFound          Reason = "not-found"              // ресурс из явного списка отсутствует (при снимке)
)

// Priority — приоритет изменения по правилам типа (не AI): image, resources, securityContext выше labels.
type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

// Rank — порядковый вес для сортировки (больше — важнее).
func (p Priority) Rank() int {
	switch p {
	case PriorityHigh:
		return 3
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 1
	default:
		return 0
	}
}

// ChangeOp — вид отличия пути.
type ChangeOp string

const (
	OpChanged ChangeOp = "changed"
	OpAdded   ChangeOp = "added"
	OpRemoved ChangeOp = "removed"
)

// SourceKind — вид доказательства источника изменения (ADR-0027 §6).
type SourceKind string

const (
	// SourceManagedFields — «по данным managedFields»: последний менеджер поля и время. Менеджер не приписывается
	// пользователю: это имя клиента (kubectl, контроллер GitOps), а не человек.
	SourceManagedFields SourceKind = "managedFields"
	// SourceKubronAudit — запись аудита Kubron о применении YAML этим приложением, подтверждённая данными кластера.
	SourceKubronAudit SourceKind = "kubron-audit"
)

// ChangeSource — доказательство источника изменения. Без доказательства источника нет (Change.Source == nil):
// само отличие не доказывает, кто его внёс.
type ChangeSource struct {
	Kind SourceKind `json:"kind"`
	// Manager — имя менеджера поля из managedFields (kubectl, argocd-controller…); человека не называет.
	Manager string    `json:"manager,omitempty"`
	At      time.Time `json:"at"`
}

// Change — одно отличие. Для чувствительных значений (Sensitive) содержимого нет: сообщается только, что значение
// ключа Key изменено, добавлено или удалено.
type Change struct {
	Path string   `json:"path"`
	Op   ChangeOp `json:"op"`
	// Old и New — нормализованные значения для показа (JSON-литерал, после редакции ADR-0022). Пусты у Sensitive.
	Old       string        `json:"old,omitempty"`
	New       string        `json:"new,omitempty"`
	Sensitive bool          `json:"sensitive,omitempty"`
	Key       string        `json:"key,omitempty"` // имя ключа: env-переменная, ключ ConfigMap, аннотация
	Priority  Priority      `json:"priority"`
	Source    *ChangeSource `json:"source,omitempty"`
}

// ItemResult — результат проверки одного ресурса baseline.
type ItemResult struct {
	Ref      Ref      `json:"ref"`
	Status   Status   `json:"status"`
	Reason   Reason   `json:"reason,omitempty"` // для StatusUnchecked
	Priority Priority `json:"priority,omitempty"`
	Changes  []Change `json:"changes,omitempty"`
	// Suppressed — идентификаторы исключений, скрывших часть изменений этого ресурса.
	Suppressed []string `json:"suppressed,omitempty"`
}

// Summary — счётчики результата: проверенные и пропущенные ресурсы с причинами.
type Summary struct {
	Total     int `json:"total"`
	Matched   int `json:"matched"`
	Changed   int `json:"changed"`
	Missing   int `json:"missing"`
	Unchecked int `json:"unchecked"`
	// Checked — ресурсы, по которым получен ответ кластера (соответствует, изменён, отсутствует).
	Checked           int            `json:"checked"`
	UncheckedByReason map[Reason]int `json:"uncheckedByReason,omitempty"`
	// SuppressedChanges — число изменений, скрытых действующими исключениями.
	SuppressedChanges int `json:"suppressedChanges"`
}

// AppliedException — применённое исключение: видно в результате, сколько изменений оно скрыло.
type AppliedException struct {
	ID        string         `json:"id"`
	Scope     ExceptionScope `json:"scope"`
	Ref       Ref            `json:"ref"`
	Path      string         `json:"path"`
	Comment   string         `json:"comment"`
	ExpiresAt *time.Time     `json:"expiresAt,omitempty"`
	Hits      int            `json:"hits"`
}

// ExceptionReport — что произошло с исключениями при проверке.
type ExceptionReport struct {
	Applied []AppliedException `json:"applied,omitempty"`
	// Expired — идентификаторы просроченных исключений: они не применялись.
	Expired []string `json:"expired,omitempty"`
}

// Caveat — оговорка к результату, которая не делает ресурс «не проверенным», но влияет на доверие к нему.
type Caveat string

const (
	// CaveatHPAUnreadable — не удалось прочитать HorizontalPodAutoscaler: spec.replicas сравнивался как есть,
	// у нагрузок под HPA возможны ложные отличия.
	CaveatHPAUnreadable Caveat = "hpa-unreadable"
)

// CheckResult — результат проверки по версии baseline (ADR-0027 §5).
type CheckResult struct {
	ID         string       `json:"id"`
	BaselineID string       `json:"baselineId"`
	VersionID  string       `json:"versionId"`
	Scope      tenant.Scope `json:"scope"`
	// RulesVersion — версия правил приложения, по которым выполнено сравнение; VersionRulesVersion — версия правил,
	// с которыми создана версия baseline. При несовпадении сравнение не выполняется (все ресурсы «не проверено»).
	RulesVersion        int       `json:"rulesVersion"`
	VersionRulesVersion int       `json:"versionRulesVersion"`
	CheckedAt           time.Time `json:"checkedAt"`
	Initiator           string    `json:"initiator"`
	CorrelationID       string    `json:"correlationId,omitempty"`

	Summary    Summary         `json:"summary"`
	Items      []ItemResult    `json:"items"`
	Exceptions ExceptionReport `json:"exceptions"`
	Caveats    []Caveat        `json:"caveats,omitempty"`
}

// Validate проверяет результат перед записью.
func (r CheckResult) Validate() error {
	if err := ValidateID(r.ID); err != nil {
		return err
	}
	if err := ValidateID(r.BaselineID); err != nil {
		return err
	}
	if err := ValidateID(r.VersionID); err != nil {
		return err
	}
	if r.CheckedAt.IsZero() {
		return fmt.Errorf("%w: a check result needs a time", ErrInvalid)
	}
	return nil
}

// Summarize возвращает метаданные результата для списков истории.
func (r CheckResult) Summarize() ResultSummary {
	return ResultSummary{
		ID: r.ID, BaselineID: r.BaselineID, VersionID: r.VersionID, CheckedAt: r.CheckedAt,
		Initiator: r.Initiator, RulesVersion: r.RulesVersion, Summary: r.Summary,
	}
}

// ResultSummary — строка истории проверок: без списка ресурсов.
type ResultSummary struct {
	ID           string    `json:"id"`
	BaselineID   string    `json:"baselineId"`
	VersionID    string    `json:"versionId"`
	CheckedAt    time.Time `json:"checkedAt"`
	Initiator    string    `json:"initiator"`
	RulesVersion int       `json:"rulesVersion"`
	Summary      Summary   `json:"summary"`
}
