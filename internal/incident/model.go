// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package incident — история расследований Kubron Intelligence (ADR-0024 §5, ADR-0026 §4).
//
// Пакет владеет моделью инцидента, узким хранилищем Backend (файловая реализация здесь, PostgreSQL — в pgstore),
// сервисом расследований и правилами сохранения логов как доказательств: ограниченные редактированные фрагменты,
// лимиты в коде, fail-closed редакция, отдельное право на содержимое, retention и выключатель.
//
// Пакет не знает про платформу (opsplatform), Store и приложение: область доступа приходит параметром
// (tenant.Scope), чтение кластера — через diagnose.Reader, редактор — через Redactor, проверка права — функцией.
package incident

import (
	"errors"
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Ошибки домена. Сравниваются через errors.Is. Чужая область доступа отвечает ErrNotFound: существование записи
// другой организации, команды или кластера не раскрывается.
var (
	ErrNotFound        = errors.New("incident not found")
	ErrConflict        = errors.New("incident was changed concurrently")
	ErrInvalidScope    = errors.New("tenant scope is not defined")
	ErrInvalidID       = errors.New("identifier contains unsupported characters")
	ErrClosed          = errors.New("investigation is closed")
	ErrInvalidOutcome  = errors.New("investigation outcome is invalid")
	ErrInvalidSettings = errors.New("incident settings are invalid")
	ErrLogsDisabled    = errors.New("saving log fragments is disabled")
	ErrNotAffected     = errors.New("pod is not an affected resource of the investigation")
)

// Status — состояние расследования. Открытое расследование обновляет снимок Events; закрытое — только читается.
type Status string

const (
	StatusOpen   Status = "open"
	StatusClosed Status = "closed"
)

// Result — результат проверки, который отмечает человек. Это единственное место, где причина может быть
// признана подтверждённой: решение принимает пользователь, а не правило и не модель.
type Result string

const (
	ResultResolved     Result = "resolved"     // проблема устранена
	ResultUnresolved   Result = "unresolved"   // не устранена
	ResultInconclusive Result = "inconclusive" // проверить не удалось
)

// Outcome — отметка результата расследования.
type Outcome struct {
	Result Result `json:"result"`
	// ConfirmedHypothesisID — гипотеза отчёта, которую человек подтвердил проверкой (необязательно).
	ConfirmedHypothesisID string    `json:"confirmedHypothesisId,omitempty"`
	Note                  string    `json:"note,omitempty"` // редактированный комментарий, до MaxNoteRunes
	MarkedBy              string    `json:"markedBy"`
	MarkedAt              time.Time `json:"markedAt"`
}

// EvidenceStatus — состояние доказательства-лога для finding.
type EvidenceStatus string

const (
	EvidenceSaved    EvidenceStatus = "saved"
	EvidenceNotSaved EvidenceStatus = "not_saved"
)

// Причины, по которым фрагмент не сохранён. Стабильные коды: интерфейс переводит их, решений по тексту нет.
const (
	ReasonNotText           = "not_text"           // не UTF-8 или бинарный лог
	ReasonLimitExceeded     = "limit_exceeded"     // фрагмент больше лимита после редакции
	ReasonRedactionFailed   = "redaction_failed"   // сбой редактора
	ReasonDisabledOrg       = "disabled_org"       // выключатель организации/приложения
	ReasonDisabledNamespace = "disabled_namespace" // namespace в списке исключений
	ReasonDisabledIncident  = "disabled_incident"  // отключено для этого расследования
	ReasonIncidentQuota     = "incident_quota"     // достигнут лимит фрагментов инцидента
	ReasonLogUnavailable    = "log_unavailable"    // лог не прочитан (RBAC, сеть, контейнер не запускался)
	ReasonEmptyLog          = "empty_log"          // в логе нет строк
)

// Evidence — состояние лога-доказательства для контейнера пода. Факты отчёта ссылаются на лог парой
// «под, контейнер, previous» (diagnose.LogRef), поэтому связь не зависит от нумерации фактов.
type Evidence struct {
	Pod        string         `json:"pod"`
	Container  string         `json:"container"`
	Previous   bool           `json:"previous,omitempty"`
	Status     EvidenceStatus `json:"status"`
	Reason     string         `json:"reason,omitempty"`     // для not_saved
	FragmentID string         `json:"fragmentId,omitempty"` // для saved
	UpdatedAt  time.Time      `json:"updatedAt"`
}

// Incident — запись расследования: один документ, меняемый согласованно (ADR-0026 §4).
type Incident struct {
	ID        string               `json:"id"`
	Scope     tenant.Scope         `json:"scope"`
	Target    diagnose.ResourceRef `json:"target"`
	Status    Status               `json:"status"`
	OpenedAt  time.Time            `json:"openedAt"`
	UpdatedAt time.Time            `json:"updatedAt"`
	OpenedBy  string               `json:"openedBy"`

	// Report — диагностический отчёт на момент открытия. Тексты кластера в нём отредактированы.
	Report diagnose.Report `json:"report"`
	// Events — снимок Events (отредактированный, не более MaxSnapshotEvents): события недолговечны в кластере.
	Events          []diagnose.Event `json:"events"`
	EventsUpdatedAt time.Time        `json:"eventsUpdatedAt"`

	// SaveLogs — сохранять ли фрагменты логов для этого расследования (пользователь может отключить).
	SaveLogs bool       `json:"saveLogs"`
	Evidence []Evidence `json:"evidence"`
	Outcome  *Outcome   `json:"outcome,omitempty"`

	// Version — версия записи для оптимистичной блокировки: ведёт её Backend, вызывающий её не меняет.
	Version int64 `json:"version"`
}

// Summary — строка истории: достаточно для списка, без отчёта и снимка Events.
type Summary struct {
	ID         string               `json:"id"`
	Target     diagnose.ResourceRef `json:"target"`
	Status     Status               `json:"status"`
	OpenedAt   time.Time            `json:"openedAt"`
	UpdatedAt  time.Time            `json:"updatedAt"`
	OpenedBy   string               `json:"openedBy"`
	Symptoms   []string             `json:"symptoms"`
	Facts      int                  `json:"facts"`
	Hypotheses int                  `json:"hypotheses"`
	Result     Result               `json:"result,omitempty"`
}

// Summarize строит строку истории из записи.
func (in *Incident) Summarize() Summary {
	s := Summary{ID: in.ID, Target: in.Target, Status: in.Status, OpenedAt: in.OpenedAt, UpdatedAt: in.UpdatedAt,
		OpenedBy: in.OpenedBy, Symptoms: append([]string{}, in.Report.Symptoms...),
		Facts: len(in.Report.Facts), Hypotheses: len(in.Report.Hypotheses)}
	if in.Outcome != nil {
		s.Result = in.Outcome.Result
	}
	return s
}

// Лимиты и пределы. Заданы в коде как верхняя граница и НЕ настраиваются: изменить их можно только новым ADR
// (ADR-0024 §5 п.2, п.7).
const (
	MaxFragmentLines         = 200       // строк на фрагмент
	MaxFragmentBytes         = 32 << 10  // байт редактированного текста на фрагмент
	MaxLineBytes             = 2 << 10   // строка усекается до 2 КиБ
	MaxFragmentsPerIncident  = 10        // фрагментов на инцидент
	MaxIncidentFragmentBytes = 256 << 10 // суммарно на инцидент

	MaxSnapshotEvents = 200     // событий в снимке инцидента
	MaxNoteRunes      = 2000    // длина комментария к результату
	MaxEventMessage   = 4 << 10 // сообщение события до редакции усекается до 4 КиБ

	MaxFragmentRetentionDays     = 15 // и значение по умолчанию, и верхняя граница
	DefaultIncidentRetentionDays = 90
	MaxIncidentRetentionDays     = 365
)

// Fragment — фрагмент-доказательство (ADR-0024 §5 п.10). Text — отредактированный текст; в server он шифруется.
type Fragment struct {
	FragmentMeta
	Text string `json:"text"`
}

// FragmentMeta — метаданные фрагмента без содержимого: их видит и пользователь без права diagnostics.logs.
type FragmentMeta struct {
	ID         string       `json:"id"`
	IncidentID string       `json:"incidentId"`
	Scope      tenant.Scope `json:"scope"`

	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Previous  bool   `json:"previous,omitempty"` // экземпляр: предыдущий (true) или текущий

	CapturedAt time.Time  `json:"capturedAt"`
	FromTime   *time.Time `json:"fromTime,omitempty"` // интервал времени по меткам строк, если они есть
	ToTime     *time.Time `json:"toTime,omitempty"`
	StartLine  int        `json:"startLine"` // строки в прочитанном хвосте лога, с 1
	EndLine    int        `json:"endLine"`
	AnchorLine int        `json:"anchorLine"`
	Lines      int        `json:"lines"`
	Bytes      int        `json:"bytes"`
	Truncated  bool       `json:"truncated"` // окно меньше лога или строки усечены

	RedactorVersion int    `json:"redactorVersion"`
	Replacements    int    `json:"replacements"`
	SHA256          string `json:"sha256"` // SHA-256 отредактированного текста, hex

	Auto      bool      `json:"auto"` // сохранён автоматически (по finding), а не явным действием
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
}
