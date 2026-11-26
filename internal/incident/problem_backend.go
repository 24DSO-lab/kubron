// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// ProblemBackend — хранилище проблем (ADR-0033 §6). Отдельный интерфейс, а не методы Backend: существующие
// реализации и подстановки не ломаются. Реализуют те же файловая (FileBackend) и PostgreSQL (pgstore)
// хранилища; контрактные тесты — incidenttest.RunProblems.
//
// Правила те же, что у Backend:
//   - область tenant.Scope входит в ключ записи и в каждый запрос; невалидная область — ErrInvalidScope,
//     запись чужой области — ErrNotFound;
//   - PutProblem защищён версией (ErrConflict); транзакций между записями нет;
//   - время хранится с точностью до микросекунды в UTC.
type ProblemBackend interface {
	// FindOrCreateOpenProblem атомарно находит самую свежую открытую проблему с таким же ключом в пределах
	// oldest (включительно), не отсоединённую от первого участника candidate, либо создаёт candidate.
	// Возвращает запись с версией; операция сериализуется между экземплярами backend.
	FindOrCreateOpenProblem(ctx context.Context, candidate *Problem, oldest time.Time) (*Problem, error)
	// PutProblem создаёт (Version == 0; ErrConflict, если запись уже есть) или заменяет запись: замена удаётся,
	// только если хранимая версия равна p.Version, иначе ErrConflict. При успехе p.Version увеличен.
	PutProblem(ctx context.Context, p *Problem) error
	GetProblem(ctx context.Context, scope tenant.Scope, id string) (*Problem, error)
	// ListProblems — страница проблем кластера области: новые (по времени обновления, затем по ID) первыми.
	ListProblems(ctx context.Context, scope tenant.Scope, q ProblemQuery) (ProblemPage, error)
	// DeleteProblem удаляет проблему. Отсутствующая — ErrNotFound.
	DeleteProblem(ctx context.Context, scope tenant.Scope, id string) error
	// PurgeProblems удаляет проблемы организации, обновлённые раньше before; возвращает число удалённых.
	PurgeProblems(ctx context.Context, orgID string, before time.Time) (int, error)
}

// ProblemQuery — запрос страницы проблем.
type ProblemQuery struct {
	Status ProblemStatus // пустой — любые
	Limit  int           // 0 — DefaultPageSize; больше MaxPageSize обрезается
	Cursor string        // пустой — первая страница
}

// PageSize — размер страницы с учётом умолчания и предела (как у ListQuery).
func (q ProblemQuery) PageSize() int { return ListQuery{Limit: q.Limit}.PageSize() }

// ProblemPage — страница проблем. NextCursor пуст на последней странице.
type ProblemPage struct {
	Items      []Problem `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// NormalizeProblem приводит запись к виду, который хранят обе реализации: время в UTC с микросекундами, список
// участников не nil (в JSON — [], а не null). Общая для файловой и PostgreSQL реализаций.
func NormalizeProblem(p *Problem) {
	p.CreatedAt, p.UpdatedAt = Micro(p.CreatedAt), Micro(p.UpdatedAt)
	if p.Members == nil {
		p.Members = []ProblemMember{}
	}
	for i := range p.Members {
		p.Members[i].AddedAt = Micro(p.Members[i].AddedAt)
	}
}

// ValidStatus сообщает, что состояние проблемы допустимо для хранения.
func (s ProblemStatus) ValidStatus() bool { return s == ProblemOpen || s == ProblemClosed }
