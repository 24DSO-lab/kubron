// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Backend — узкое хранилище домена (образец audit.Backend, auth.Backend; ADR-0026 §4). Две реализации с одной
// семантикой: файловая (desktop, этот пакет) и PostgreSQL (server, pgstore). Контрактные тесты —
// incident/incidenttest.Run.
//
// Правила, одинаковые для обеих реализаций:
//   - область tenant.Scope входит в ключ каждой записи и в каждый запрос; невалидная область — ErrInvalidScope
//     (fail closed), запись чужой области — ErrNotFound;
//   - транзакций между записями нет: инцидент — один документ, фрагмент — отдельная запись; PutIncident защищён
//     версией (ErrConflict), порядок «сначала фрагмент, потом документ» оставляет в худшем случае сирот, которые
//     убирает retention;
//   - время хранится с точностью до микросекунды в UTC.
type Backend interface {
	// PutIncident создаёт (Version == 0) или заменяет запись. Замена удаётся, только если хранимая версия
	// равна in.Version; иначе ErrConflict. При успехе in.Version увеличен.
	PutIncident(ctx context.Context, in *Incident) error
	GetIncident(ctx context.Context, scope tenant.Scope, id string) (*Incident, error)
	// ListIncidents — страница истории кластера области, новые первыми. Пустой Cursor — первая страница.
	ListIncidents(ctx context.Context, scope tenant.Scope, q ListQuery) (Page, error)
	// DeleteIncident удаляет инцидент вместе с его фрагментами. Отсутствующий — ErrNotFound.
	DeleteIncident(ctx context.Context, scope tenant.Scope, id string) error

	// PutFragment сохраняет фрагмент (создание или замена по ID). Текст в server шифруется.
	PutFragment(ctx context.Context, f *Fragment) error
	GetFragment(ctx context.Context, scope tenant.Scope, incidentID, fragmentID string) (*Fragment, error)
	ListFragments(ctx context.Context, scope tenant.Scope, incidentID string) ([]FragmentMeta, error)
	DeleteFragment(ctx context.Context, scope tenant.Scope, incidentID, fragmentID string) error

	// Settings возвращает сохранённые настройки организации; ok=false — не сохранялись (действуют значения по
	// умолчанию процесса).
	Settings(ctx context.Context, orgID string) (s Settings, ok bool, err error)
	PutSettings(ctx context.Context, orgID string, s Settings) error

	// OrgIDs — организации, у которых есть записи или настройки (для задачи retention).
	OrgIDs(ctx context.Context) ([]string, error)
	// Purge удаляет фрагменты, созданные раньше fragmentsBefore, и инциденты, обновлённые раньше incidentsBefore
	// (с их фрагментами), в рамках организации. Возвращает числа удалённых.
	Purge(ctx context.Context, orgID string, fragmentsBefore, incidentsBefore time.Time) (PurgeResult, error)
}

// PurgeResult — итог Purge.
type PurgeResult struct{ Fragments, Incidents int }

// ListQuery — запрос страницы истории.
type ListQuery struct {
	Limit  int    // 0 — DefaultPageSize; больше MaxPageSize обрезается
	Cursor string // пустой — первая страница
	Status Status // пустой — любые
}

// Страницы истории.
const (
	DefaultPageSize = 25
	MaxPageSize     = 100
)

func (q ListQuery) PageSize() int {
	switch {
	case q.Limit <= 0:
		return DefaultPageSize
	case q.Limit > MaxPageSize:
		return MaxPageSize
	}
	return q.Limit
}

// Page — страница истории. NextCursor пуст на последней странице.
type Page struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// cursor — позиция в истории: порядок «время открытия, ID» по убыванию.
type cursor struct {
	T  int64  `json:"t"` // UnixMicro
	ID string `json:"i"`
}

// EncodeCursor строит непрозрачный курсор. Общий для обеих реализаций.
func EncodeCursor(openedAt time.Time, id string) string {
	raw, _ := json.Marshal(cursor{T: openedAt.UnixMicro(), ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor разбирает курсор; пустой даёт ok=false без ошибки.
func DecodeCursor(s string) (openedAt time.Time, id string, ok bool, err error) {
	if s == "" {
		return time.Time{}, "", false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", false, fmt.Errorf("%w: cursor", ErrInvalidID)
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil || c.ID == "" {
		return time.Time{}, "", false, fmt.Errorf("%w: cursor", ErrInvalidID)
	}
	return time.UnixMicro(c.T).UTC(), c.ID, true, nil
}

// Before сообщает, что (t, id) строго «старше» позиции курсора в порядке убывания.
func Before(t time.Time, id string, curT time.Time, curID string) bool {
	if !t.Equal(curT) {
		return t.Before(curT)
	}
	return id < curID
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidID проверяет идентификатор (часть области, инцидент, фрагмент): он становится частью пути и ключа,
// поэтому разделители, «..» и пустота недопустимы.
func ValidID(s string) bool { return idRe.MatchString(s) }

// CheckScope проверяет область: все три части заданы и безопасны.
func CheckScope(s tenant.Scope) error {
	if !s.Valid() {
		return ErrInvalidScope
	}
	for _, p := range []string{s.OrgID, s.TeamID, s.ClusterID} {
		if !ValidID(p) {
			return fmt.Errorf("%w: scope", ErrInvalidID)
		}
	}
	return nil
}

// Micro приводит время к виду, который хранят обе реализации: UTC, микросекунды.
func Micro(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }
