// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Ошибки хранилища и модели. Сравнивать через errors.Is: тексты не являются контрактом.
var (
	// ErrNotFound — записи нет. Запись чужой области отвечает так же (ADR-0026 §4): наличие чужого ID не раскрывается.
	ErrNotFound = errors.New("baseline record not found")
	// ErrExists — запись с таким идентификатором (или номером версии) уже есть: версии неизменяемы.
	ErrExists = errors.New("baseline record already exists")
	// ErrInvalid — запись или параметр не прошли проверку.
	ErrInvalid = errors.New("invalid baseline data")
	// ErrInvalidScope — область доступа не заполнена целиком: хранилище отказывает (fail closed), а не трактует
	// пустоту как «любая».
	ErrInvalidScope = errors.New("tenant scope is incomplete")
)

// Пределы хранения и значения по умолчанию (ADR-0027 §7).
const (
	// MinVersionsKept — меньше последних версий на baseline не хранится: PruneVersions не опускается ниже.
	MinVersionsKept = 20
	// DefaultResultsRetention — срок хранения результатов проверок.
	DefaultResultsRetention = 90 * 24 * time.Hour

	// DefaultPageLimit и MaxPageLimit — размер страницы истории.
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// Page — запрос страницы истории: курсор из предыдущего ответа и размер страницы (0 — DefaultPageLimit).
type Page struct {
	Cursor string
	Limit  int
}

// Size возвращает размер страницы в допустимых пределах.
func (p Page) Size() int {
	switch {
	case p.Limit <= 0:
		return DefaultPageLimit
	case p.Limit > MaxPageLimit:
		return MaxPageLimit
	default:
		return p.Limit
	}
}

// VersionPage — страница версий, от новых к старым. Next пуст на последней странице.
type VersionPage struct {
	Items []VersionSummary
	Next  string
}

// ResultPage — страница результатов проверок, от новых к старым. Next пуст на последней странице.
type ResultPage struct {
	Items []ResultSummary
	Next  string
}

// Backend — хранилище baseline одного домена (образец — audit.Backend). Реализации: файловая (NewFileBackend,
// desktop) и PostgreSQL (internal/pgstore, server). Одна семантика проверяется общим набором тестов baselinetest.Run.
//
// Контракт:
//   - область tenant.Scope входит в ключ каждой записи и передаётся в каждый вызов; неполная область — ErrInvalidScope,
//     запись чужой области — ErrNotFound;
//   - операций, которым нужна атомарность между несколькими записями, нет: согласованно меняемое — один документ
//     (исключения версии хранятся одним документом, список заменяется целиком);
//   - все вызовы принимают context первым аргументом; отменённый контекст прерывает операцию с ошибкой контекста.
type Backend interface {
	// PutBaseline создаёт или заменяет документ по (область, ID).
	PutBaseline(ctx context.Context, b Baseline) error
	GetBaseline(ctx context.Context, scope tenant.Scope, id string) (Baseline, error)
	// ListBaselines возвращает baseline кластера области, от старых к новым.
	ListBaselines(ctx context.Context, scope tenant.Scope) ([]Baseline, error)
	// DeleteBaseline удаляет baseline каскадом: версии, исключения, результаты. Нет baseline — ErrNotFound.
	DeleteBaseline(ctx context.Context, scope tenant.Scope, id string) error

	// PutVersion добавляет неизменяемую версию. Повтор того же ID или номера Seq — ErrExists; нет baseline — ErrNotFound.
	PutVersion(ctx context.Context, scope tenant.Scope, v Version) error
	GetVersion(ctx context.Context, scope tenant.Scope, baselineID, versionID string) (Version, error)
	// ListVersions — страница метаданных версий (без соли и содержимого), от новых к старым.
	ListVersions(ctx context.Context, scope tenant.Scope, baselineID string, page Page) (VersionPage, error)

	// PutExceptions заменяет список исключений версии целиком (пустой список очищает). Нет версии — ErrNotFound.
	PutExceptions(ctx context.Context, scope tenant.Scope, baselineID, versionID string, list []Exception) error
	// GetExceptions возвращает список исключений версии; у версии без исключений он пуст. Нет версии — ErrNotFound.
	GetExceptions(ctx context.Context, scope tenant.Scope, baselineID, versionID string) ([]Exception, error)

	// AppendResult добавляет результат проверки в историю. Повтор ID — ErrExists; нет baseline — ErrNotFound.
	AppendResult(ctx context.Context, scope tenant.Scope, r CheckResult) error
	GetResult(ctx context.Context, scope tenant.Scope, baselineID, resultID string) (CheckResult, error)
	// ListResults — страница строк истории проверок, от новых к старым.
	ListResults(ctx context.Context, scope tenant.Scope, baselineID string, page Page) (ResultPage, error)

	// DeleteResultsBefore удаляет результаты старше t по всем областям (retention) и возвращает их число.
	DeleteResultsBefore(ctx context.Context, t time.Time) (int, error)
	// PruneVersions по всем baseline оставляет не менее max(keep, MinVersionsKept) последних версий и всегда
	// оставляет активную; остальные удаляет вместе с их исключениями. Возвращает число удалённых версий.
	PruneVersions(ctx context.Context, keep int) (int, error)
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidateID проверяет идентификатор записи. Идентификаторы попадают в имена файлов, поэтому допустимы только
// буквы, цифры, «_» и «-»: «..», разделители пути и пустая строка отклоняются.
func ValidateID(id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("%w: malformed identifier", ErrInvalid)
	}
	return nil
}

var scopePartRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// ValidateScope проверяет, что область заполнена и безопасна как часть пути и ключа.
func ValidateScope(s tenant.Scope) error {
	if !s.Valid() {
		return ErrInvalidScope
	}
	for _, p := range []string{s.OrgID, s.TeamID, s.ClusterID} {
		if !scopePartRe.MatchString(p) {
			return ErrInvalidScope
		}
	}
	return nil
}

// NewID возвращает случайный идентификатор (128 бит, hex).
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("random source unavailable: %v", err)) // crypto/rand не возвращает ошибку на поддерживаемых ОС
	}
	return hex.EncodeToString(b[:])
}

// Normalize приводит время к виду хранилища: UTC с точностью до микросекунды (как timestamptz в PostgreSQL).
func Normalize(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC().Truncate(time.Microsecond)
}
