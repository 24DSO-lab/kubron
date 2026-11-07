// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

func (s *Service) authorizeLocked(p Permission) (*UserRecord, error) {
	return s.authorizeScopedLocked(p, "")
}

// authorizeScopedLocked — проверка права; clusterID задаёт кластер, к которому относится действие (пусто — вне кластера).
func (s *Service) authorizeScopedLocked(p Permission, clusterID string) (*UserRecord, error) {
	u := s.sessionUserLocked()
	if u == nil {
		return nil, ErrNotAuthenticated
	}
	if u.MustChangePassword {
		return nil, ErrPasswordChangeRequired
	}
	if s.access != nil {
		_, err := s.accessLocked(u, p, clusterID)
		return u, err
	}
	if !u.Role.Has(p) {
		return u, ErrForbidden
	}
	return u, nil
}

// checkLocked — Check для заданного кластера; под s.mu.
func (s *Service) checkLocked(p Permission, clusterID string) (Actor, error) {
	u, err := s.authorizeScopedLocked(p, clusterID)
	if u == nil {
		if cu := s.sessionUserLocked(); cu != nil {
			return Actor{Username: cu.Username, Role: cu.Role, Source: s.source}, err
		}
		return Actor{Source: s.source}, err
	}
	role := u.Role
	team := ""
	if s.access != nil && clusterID != "" {
		if tm, ok := s.access.ClusterTeam(clusterID); ok {
			// Команда попадает в контекст только если пользователь её член: чужая остаётся «не найдена».
			if r, ok := s.access.TeamRole(u.ID, tm); ok {
				role, team = r, tm
			}
		}
	}
	return Actor{Username: u.Username, Role: role, Source: s.source, ClusterID: clusterID, TeamID: team}, err
}

// Authorize проверяет, что есть вошедший пользователь и у него есть право p.
// Это единственная точка проверки прав для всех защищённых методов приложения.
func (s *Service) Authorize(p Permission) error {
	_, err := s.Check(p)
	return err
}

// Check — то же, что Authorize, но возвращает ещё и действующего пользователя для журнала аудита.
// При ErrForbidden пользователь возвращается тоже: отказ записывается от его имени.
func (s *Service) Check(p Permission) (Actor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkLocked(p, "")
}
