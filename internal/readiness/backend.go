// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Retention по умолчанию (ADR-0025 §6): до 50 отчётов на кластер и не дольше 180 дней.
const (
	DefaultKeepPerCluster = 50
	DefaultMaxAge         = 180 * 24 * time.Hour
)

// Размер страницы истории.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Retention — политика хранения отчётов. Нулевое поле означает «без этого ограничения».
type Retention struct {
	KeepPerCluster int
	MaxAge         time.Duration
}

// DefaultRetention возвращает стартовые значения ADR-0025.
func DefaultRetention() Retention {
	return Retention{KeepPerCluster: DefaultKeepPerCluster, MaxAge: DefaultMaxAge}
}

// HistoryQuery — запрос страницы истории. Cursor пуст для первой страницы; Limit 0 — DefaultPageSize.
type HistoryQuery struct {
	Cursor string
	Limit  int
}

// Page — страница истории, от новых отчётов к старым. Next пуст на последней странице.
type Page struct {
	Items []Summary `json:"items"`
	Next  string    `json:"next,omitempty"`
}

// Backend — хранилище отчётов (ADR-0026 §4). Образец — audit.Backend и auth.Backend: узкий интерфейс домена,
// реализации — файлы (desktop) и PostgreSQL (server); один набор контрактных тестов (readinesstest.Run).
//
// Правила, общие для реализаций:
//   - tenant.Scope входит в ключ записи и в каждый запрос; неполный scope — ErrInvalidScope (fail closed);
//     отчёт чужой области отвечает как отсутствующий (ErrNotFound);
//   - Save проверяет отчёт (Report.Validate): отчёт без coverage не сохраняется (ErrInvalidReport);
//   - транзакций между записями нет: отчёт — один самодостаточный документ.
type Backend interface {
	// Save сохраняет отчёт в истории области; повторная запись с тем же ID заменяет прежнюю.
	// report.ClusterID обязан совпадать с scope.ClusterID.
	Save(ctx context.Context, scope tenant.Scope, report *Report) error
	// Get возвращает отчёт по ID.
	Get(ctx context.Context, scope tenant.Scope, id string) (*Report, error)
	// List возвращает страницу истории кластера scope.ClusterID, от новых к старым.
	List(ctx context.Context, scope tenant.Scope, q HistoryQuery) (Page, error)
	// Prune удаляет отчёты по retention во всех областях и возвращает их число. Это служебная операция
	// периодической задачи; пользовательских запросов она не обслуживает.
	Prune(ctx context.Context, now time.Time, r Retention) (int, error)
}

// ValidateScope возвращает ErrInvalidScope для неполной области.
func ValidateScope(s tenant.Scope) error {
	if !s.Valid() {
		return ErrInvalidScope
	}
	return nil
}

// ValidateForSave — общая проверка перед записью: область полна, отчёт валиден и принадлежит кластеру области.
func ValidateForSave(scope tenant.Scope, r *Report) error {
	if err := ValidateScope(scope); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("%w: nil report", ErrInvalidReport)
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r.ClusterID != scope.ClusterID {
		return fmt.Errorf("%w: the report belongs to another cluster", ErrInvalidScope)
	}
	if !idRe.MatchString(r.ID) {
		return fmt.Errorf("%w: malformed report id", ErrInvalidReport)
	}
	return nil
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ValidID сообщает, что строка может быть ID отчёта (защита путей файлового хранилища и запросов).
func ValidID(id string) bool { return idRe.MatchString(id) }

// PageSize приводит запрошенный размер страницы к допустимому.
func PageSize(n int) int {
	switch {
	case n <= 0:
		return DefaultPageSize
	default:
		return min(n, MaxPageSize)
	}
}

// Cursor — позиция в истории: отчёты строго старше (CreatedAt, ID) отдаются на следующей странице.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// EncodeCursor кодирует позицию в непрозрачную строку.
func EncodeCursor(c Cursor) string {
	raw := strconv.FormatInt(c.CreatedAt.UnixNano(), 10) + ":" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor разбирает курсор; пустая строка — начало истории (ok=false без ошибки).
func DecodeCursor(s string) (c Cursor, ok bool, err error) {
	if s == "" {
		return Cursor{}, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, false, ErrBadCursor
	}
	ts, id, found := strings.Cut(string(raw), ":")
	n, perr := strconv.ParseInt(ts, 10, 64)
	if !found || perr != nil || !idRe.MatchString(id) {
		return Cursor{}, false, ErrBadCursor
	}
	return Cursor{CreatedAt: time.Unix(0, n).UTC(), ID: id}, true, nil
}

// Before сообщает, что запись (createdAt, id) старше курсора.
func (c Cursor) Before(createdAt time.Time, id string) bool {
	switch {
	case createdAt.Before(c.CreatedAt):
		return true
	case createdAt.Equal(c.CreatedAt):
		return id < c.ID
	}
	return false
}

// SortNewestFirst упорядочивает сводки: новые первыми, при равном времени — по убыванию ID.
func SortNewestFirst(s []Summary) {
	sortSummaries(s)
}

// PageFrom нарезает уже упорядоченный список сводок по курсору и размеру страницы (общая логика файловой реализации).
func PageFrom(all []Summary, q HistoryQuery) (Page, error) {
	cur, ok, err := DecodeCursor(q.Cursor)
	if err != nil {
		return Page{}, err
	}
	limit := PageSize(q.Limit)
	page := Page{Items: []Summary{}}
	for _, s := range all {
		if ok && !cur.Before(s.CreatedAt, s.ID) {
			continue
		}
		if len(page.Items) == limit {
			last := page.Items[len(page.Items)-1]
			page.Next = EncodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
			break
		}
		page.Items = append(page.Items, s)
	}
	return page, nil
}
