// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"errors"
	"fmt"
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Ошибки проблем. Хранилище отвечает ErrNotFound (чужая область и отсутствующая запись неразличимы, ErrConflict
// — ошибка версии); сервис отдаёт ErrProblemNotFound, который совпадает и с ErrNotFound по errors.Is.
var (
	// ErrProblemNotFound — проблема не найдена или у расследования нет проблемы.
	ErrProblemNotFound = fmt.Errorf("problem: %w", ErrNotFound)
	// ErrProblemFull — в проблеме уже MaxProblemMembers участников.
	ErrProblemFull = errors.New("problem has the maximum number of members")
	// ErrInvalidProblem — запрос к проблеме некорректен: пустой состав, слияние проблемы с собой, повторяющиеся
	// или недопустимые идентификаторы.
	ErrInvalidProblem = errors.New("problem request is invalid")
	// ErrProblemsDisabled — группировка не настроена: Service создан без ProblemBackend (WithProblems).
	ErrProblemsDisabled = errors.New("problems are not available")
)

// Пределы проблем. Заданы в коде и не настраиваются; изменение — только новым ADR (ADR-0033 §6).
const (
	// ProblemWindow — окно проблемы: автоприсоединение возможно, если проблема обновлялась не раньше чем
	// столько назад (ADR-0033 §6).
	ProblemWindow = 6 * time.Hour
	// MaxProblemMembers — участников в одной проблеме (ADR-0033 §6).
	MaxProblemMembers = 100
	// MaxProblemScan сохранён для совместимости; адресный FindOrCreateOpenProblem его не использует.
	// Deprecated: автоприсоединение больше не ограничивает поиск количеством записей.
	MaxProblemScan = 100
	// AutoAttachConfidence — минимальная уверенность лучшего кандидата корреляции для автоприсоединения
	// (ADR-0033 §6).
	AutoAttachConfidence = diagnose.ConfidenceMedium
	// AutoMember — значение ProblemMember.AddedBy для автоматического присоединения.
	AutoMember = "auto"
)

// ProblemStatus — состояние проблемы. Закрытая проблема не принимает участников автоматически.
type ProblemStatus string

const (
	ProblemOpen   ProblemStatus = "open"
	ProblemClosed ProblemStatus = "closed"
)

// ProblemOrigin — как создана проблема.
type ProblemOrigin string

const (
	ProblemAuto   ProblemOrigin = "auto"   // автоприсоединением при открытии расследования
	ProblemManual ProblemOrigin = "manual" // вручную из выбранных расследований
)

// ProblemMember — участник проблемы: идентификатор расследования, когда и кем добавлен. Данные расследования
// в проблему не копируются.
type ProblemMember struct {
	IncidentID string    `json:"incidentId"`
	AddedAt    time.Time `json:"addedAt"`
	AddedBy    string    `json:"addedBy"` // идентификатор пользователя или AutoMember
}

// Problem — группа расследований одного общего источника (ADR-0033 §6). Хранит только ключ и состав;
// расследования остаются отдельными документами и никогда не удаляются, не скрываются и не сливаются.
type Problem struct {
	ID    string       `json:"id"`
	Scope tenant.Scope `json:"scope"`
	// Key — «<вид>:<группа/вид/namespace/имя общего источника>» (correlation.Candidate.Key).
	Key    string               `json:"key"`
	Kind   string               `json:"kind"` // вид корреляции: shared_node, shared_storage, ...
	Source diagnose.ResourceRef `json:"source"`
	Status ProblemStatus        `json:"status"`
	Origin ProblemOrigin        `json:"origin"`

	Members []ProblemMember `json:"members"`
	// Detached — расследования, отсоединённые вручную: автоматически в эту проблему не возвращаются.
	Detached []string `json:"detached,omitempty"`
	// MergedInto — проблема, в которую эта объединена (задано у закрытой слиянием).
	MergedInto string `json:"mergedInto,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Version — версия записи для оптимистичной блокировки: ведёт её ProblemBackend, вызывающий её не меняет.
	Version int64 `json:"version"`
}
