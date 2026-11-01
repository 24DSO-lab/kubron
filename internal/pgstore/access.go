// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"time"

	"github.com/24DSO-labs/kubron/internal/auth"
)

const (
	// accessTTL — как долго снимок прав считается свежим. Изменения, сделанные этим процессом, сбрасывают его сразу;
	// изменения других реплик (этап 4) вступают в силу не позже чем через accessTTL.
	accessTTL = 2 * time.Second
	// accessMaxStale — предел, после которого при недоступной БД права отзываются (fail closed).
	accessMaxStale = 30 * time.Second
)

type accessSnap struct {
	loaded   time.Time
	clusters map[string]string               // кластер → команда
	members  map[string]map[string]auth.Role // пользователь → команда → роль
	orgAdmin map[string]bool
}

// Access возвращает auth.Access поверх таблиц clusters, memberships и users. Снимок целиком (он невелик:
// команды × пользователи) обновляется не чаще раза в accessTTL, поэтому проверка права на каждый вызов API
// не обращается к БД.
func (s *Store) Access() auth.Access { return &pgAccess{s: s} }

type pgAccess struct{ s *Store }

func (a *pgAccess) snap() *accessSnap {
	s := a.s
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	now := time.Now()
	if s.accessSnap != nil && now.Sub(s.accessSnap.loaded) < s.accessTTL() {
		return s.accessSnap
	}
	fresh, err := s.loadAccess()
	switch {
	case err == nil:
		s.accessSnap = fresh
	case s.accessSnap == nil || now.Sub(s.accessSnap.loaded) > accessMaxStale:
		s.accessSnap = &accessSnap{loaded: now, clusters: map[string]string{}, members: map[string]map[string]auth.Role{}, orgAdmin: map[string]bool{}}
	}
	return s.accessSnap
}

func (s *Store) accessTTL() time.Duration {
	if s.accessTTLOverride != nil {
		return *s.accessTTLOverride
	}
	return accessTTL
}

// invalidateAccess сбрасывает снимок после изменения прав или кластеров этим процессом и сообщает об этом другим
// репликам (SetAccessNotifier): им не нужно ждать истечения своего снимка. Вызывается после фиксации изменения.
func (s *Store) invalidateAccess() {
	s.dropAccessSnapshot()
	s.accessMu.Lock()
	fn := s.accessNotify
	s.accessMu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *Store) dropAccessSnapshot() {
	s.accessMu.Lock()
	s.accessSnap = nil
	s.accessMu.Unlock()
}

// ApplyRemoteAccessChange сбрасывает снимок прав по сообщению другой реплики (без повторной рассылки).
func (s *Store) ApplyRemoteAccessChange() { s.dropAccessSnapshot() }

// SetAccessNotifier задаёт функцию, которую вызывает каждое локальное изменение прав, команд, кластеров или
// пользователей (после фиксации в БД). Функция не должна блокироваться.
func (s *Store) SetAccessNotifier(fn func()) {
	s.accessMu.Lock()
	s.accessNotify = fn
	s.accessMu.Unlock()
}

func (s *Store) loadAccess() (*accessSnap, error) {
	ctx, cancel := opCtx()
	defer cancel()
	out := &accessSnap{loaded: time.Now(), clusters: map[string]string{}, members: map[string]map[string]auth.Role{}, orgAdmin: map[string]bool{}}
	if err := s.scan(ctx, `SELECT id, team_id FROM clusters`, func(sc scanner) error {
		var id, team string
		if err := sc.Scan(&id, &team); err != nil {
			return err
		}
		out.clusters[id] = team
		return nil
	}); err != nil {
		return nil, err
	}
	// Отключённый пользователь не получает доступа, даже если сессия ещё жива.
	if err := s.scan(ctx, `SELECT m.user_id, m.team_id, m.role FROM memberships m JOIN users u ON u.id = m.user_id WHERE NOT u.disabled`, func(sc scanner) error {
		var user, team, role string
		if err := sc.Scan(&user, &team, &role); err != nil {
			return err
		}
		if out.members[user] == nil {
			out.members[user] = map[string]auth.Role{}
		}
		out.members[user][team] = auth.Role(role)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id FROM users WHERE org_role = 'org-admin' AND NOT disabled`, func(sc scanner) error {
		var id string
		if err := sc.Scan(&id); err != nil {
			return err
		}
		out.orgAdmin[id] = true
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

type scanner interface{ Scan(dest ...any) error }

func (s *Store) scan(ctx context.Context, q string, fn func(scanner) error) error {
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (a *pgAccess) ClusterTeam(id string) (string, bool) {
	t, ok := a.snap().clusters[id]
	return t, ok
}

func (a *pgAccess) TeamRole(user, team string) (auth.Role, bool) {
	r, ok := a.snap().members[user][team]
	return r, ok
}

func (a *pgAccess) TeamRoles(user string) map[string]auth.Role {
	src := a.snap().members[user]
	out := make(map[string]auth.Role, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (a *pgAccess) IsOrgAdmin(user string) bool { return a.snap().orgAdmin[user] }
