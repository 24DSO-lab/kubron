// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
	"github.com/24DSO-lab/kubron/internal/readiness/catalog"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// MaxResourcesPerFinding — сколько ресурсов перечисляется в одном API finding; остальные только считаются.
const MaxResourcesPerFinding = 50

// Service — Upgrade Readiness: запуск проверки, история, сравнение и экспорт. Проверка синхронная и короткая
// (ADR-0026: фоновых пользовательских заданий нет): все чтения идут под deadline контекста вызова.
type Service struct {
	backend Backend
	matrix  *apimatrix.Matrix
	catalog *catalog.Catalog
	now     func() time.Time
	newID   func() string
}

// Options — зависимости сервиса. Пустые Matrix и Catalog заменяются встроенными, Now и NewID — стандартными.
type Options struct {
	Backend Backend
	Matrix  *apimatrix.Matrix
	Catalog *catalog.Catalog
	Now     func() time.Time
	NewID   func() string
}

// New создаёт сервис. Встроенные матрица и каталог проверяются при загрузке; повреждённые — ошибка.
func New(o Options) (*Service, error) {
	if o.Backend == nil {
		return nil, errors.New("readiness backend is required")
	}
	s := &Service{backend: o.Backend, matrix: o.Matrix, catalog: o.Catalog, now: o.Now, newID: o.NewID}
	var err error
	if s.matrix == nil {
		if s.matrix, err = apimatrix.Default(); err != nil {
			return nil, err
		}
	}
	if s.catalog == nil {
		if s.catalog, err = catalog.Default(); err != nil {
			return nil, err
		}
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newID == nil {
		s.newID = randomID
	}
	return s, nil
}

func randomID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // источник случайности ОС недоступен: продолжать небезопасно
	}
	return hex.EncodeToString(b[:])
}

// Window возвращает окно целевых версий матрицы (для выбора цели в интерфейсе).
func (s *Service) Window() []kver.Minor { return slices.Clone(s.matrix.Window) }

// Matrix и Catalog возвращают сведения о встроенных данных для показа в интерфейсе.
func (s *Service) MatrixInfo() apimatrix.Info { return s.matrix.Info() }
func (s *Service) CatalogInfo() catalog.Info  { return s.catalog.Info() }

// Request — запрос проверки. Reader читает кластер области Scope; tenant.Scope определяет платформа по каталогу
// Kubron, а не клиент.
type Request struct {
	Scope         tenant.Scope
	Reader        Reader
	Initiator     string
	CorrelationID string
	// Target — целевая версия «1.36»; пусто — не указана.
	Target string
}

// ParseTarget разбирает целевую версию запроса и проверяет, что она входит в окно матрицы.
func (s *Service) ParseTarget(target string) (kver.Minor, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return kver.Minor{}, nil
	}
	m, err := kver.ParseMinor(target)
	if err != nil {
		return kver.Minor{}, fmt.Errorf("%w: %q", ErrInvalidTarget, target)
	}
	if !s.matrix.InWindow(m) {
		return kver.Minor{}, fmt.Errorf("%w: %s (supported targets: %s)", ErrTargetOutOfWindow, m, joinMinors(s.matrix.Window))
	}
	return m, nil
}

func joinMinors(ms []kver.Minor) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = m.String()
	}
	return strings.Join(parts, ", ")
}

// Run выполняет проверку, проверяет отчёт и сохраняет его в историю. Отчёт без coverage не сохраняется.
// Ошибка возвращается, если версию кластера определить нельзя или запрос неверен; недоступность остальных
// источников ошибкой не является — она становится частью отчёта («не проверено»).
func (s *Service) Run(ctx context.Context, req Request) (*Report, error) {
	if err := ValidateScope(req.Scope); err != nil {
		return nil, err
	}
	r, err := s.Check(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.backend.Save(ctx, req.Scope, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Get возвращает сохранённый отчёт области.
func (s *Service) Get(ctx context.Context, scope tenant.Scope, id string) (*Report, error) {
	return s.backend.Get(ctx, scope, id)
}

// History возвращает страницу истории кластера.
func (s *Service) History(ctx context.Context, scope tenant.Scope, q HistoryQuery) (Page, error) {
	return s.backend.List(ctx, scope, q)
}

// Prune применяет retention ко всем областям (периодическая задача).
func (s *Service) Prune(ctx context.Context, r Retention) (int, error) {
	return s.backend.Prune(ctx, s.now(), r)
}

// Compare сравнивает отчёт id с предыдущим отчётом того же кластера. previousID пуст — берётся ближайший более
// ранний в истории; ErrNotFound, если более раннего отчёта нет.
func (s *Service) Compare(ctx context.Context, scope tenant.Scope, id, previousID string) (Diff, error) {
	cur, err := s.backend.Get(ctx, scope, id)
	if err != nil {
		return Diff{}, err
	}
	if previousID == "" {
		page, err := s.backend.List(ctx, scope, HistoryQuery{Cursor: EncodeCursor(Cursor{CreatedAt: cur.CreatedAt, ID: cur.ID}), Limit: 1})
		if err != nil {
			return Diff{}, err
		}
		if len(page.Items) == 0 {
			return Diff{}, ErrNotFound
		}
		previousID = page.Items[0].ID
	}
	prev, err := s.backend.Get(ctx, scope, previousID)
	if err != nil {
		return Diff{}, err
	}
	return Compare(prev, cur), nil
}
