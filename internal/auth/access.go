// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"sort"
)

// ErrClusterNotFound — кластера нет или он принадлежит чужой команде. Намеренно один и тот же ответ:
// пользователь не должен узнавать, что чужие кластеры существуют (этап 3, решение 2).
var ErrClusterNotFound = errors.New("cluster not found")

// Access — модель доступа сервиса «организация → команда → кластер» (этап 3, задача 3.2).
// Реализация (PostgreSQL) должна отвечать быстро: вызывается на каждый вызов API, поэтому кэширует снимок.
// Пока Access не задан (десктоп, личный режим), действует прежняя глобальная роль пользователя.
type Access interface {
	// ClusterTeam возвращает команду-владельца кластера; ok=false — кластера нет.
	ClusterTeam(clusterID string) (teamID string, ok bool)
	// TeamRole возвращает роль пользователя в команде; ok=false — он не её член.
	TeamRole(userID, teamID string) (Role, bool)
	// TeamRoles возвращает роли пользователя во всех его командах (ключ — id команды).
	TeamRoles(userID string) map[string]Role
	// IsOrgAdmin сообщает, что пользователь — администратор организации. Это управление пользователями и командами,
	// но не доступ к данным кластеров: его даёт только членство в команде.
	IsOrgAdmin(userID string) bool
}

// SetAccess включает проверку прав по членству в командах. Вызывается один раз при старте, до первого входа.
func (s *Service) SetAccess(a Access) {
	s.mu.Lock()
	s.access = a
	s.mu.Unlock()
}

// ClusterScoped сообщает, что право действует внутри кластера и определяется ролью в команде-владельце
// (остальные — права организации и управления кластерами).
func ClusterScoped(p Permission) bool {
	switch p {
	case PermRead, PermSecretsRead, PermOperate, PermWrite, PermExec, PermNodeShell, PermDebug, PermPortForward:
		return true
	}
	return false
}

// accessLocked решает, разрешено ли право p пользователю u. clusterID пуст для методов вне конкретного кластера.
// Возвращает роль, по которой принято решение (для журнала). Вызывать под s.mu; s.access != nil.
//
// Правила:
//   - с кластером: чужой или отсутствующий → ErrClusterNotFound; иначе право определяет роль в его команде
//     (администратор организации без членства доступа к кластеру не получает);
//   - без кластера, права кластера: достаточно иметь это право хотя бы в одной команде (списки, переключение;
//     сами операции всегда идут с активным кластером и проверяются по нему);
//   - clusters.manage без кластера (добавление): администратор хотя бы одной команды (команда назначается в 3.3);
//   - users.manage, audit.read: администратор организации.
func (s *Service) accessLocked(u *UserRecord, p Permission, clusterID string) (Role, error) {
	a := s.access
	if clusterID != "" {
		team, ok := a.ClusterTeam(clusterID)
		if !ok {
			return u.Role, ErrClusterNotFound
		}
		role, ok := a.TeamRole(u.ID, team)
		if !ok {
			return u.Role, ErrClusterNotFound
		}
		if !role.Has(p) {
			return role, ErrForbidden
		}
		return role, nil
	}
	switch {
	case ClusterScoped(p), p == PermClusterManage:
		for _, role := range a.TeamRoles(u.ID) {
			if role.Has(p) {
				return u.Role, nil
			}
		}
		return u.Role, ErrForbidden
	default: // users.manage, audit.read и будущие права организации
		if a.IsOrgAdmin(u.ID) {
			return u.Role, nil
		}
		return u.Role, ErrForbidden
	}
}

// CheckCluster — Check для действия над конкретным кластером. Неизвестный и чужой кластер дают
// ErrClusterNotFound. Без Access ведёт себя как Check (глобальная роль).
func (s *Service) CheckCluster(p Permission, clusterID string) (Actor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkLocked(p, clusterID)
}

// AuthorizeCluster — CheckCluster без возврата пользователя.
func (s *Service) AuthorizeCluster(p Permission, clusterID string) error {
	_, err := s.CheckCluster(p, clusterID)
	return err
}

// CanSeeCluster сообщает, видит ли вошедший пользователь кластер (его команда — команда пользователя).
// Без Access видны все кластеры, если пользователь вошёл. Используется для фильтрации списков.
func (s *Service) CanSeeCluster(clusterID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.sessionUserLocked()
	if u == nil || u.MustChangePassword {
		return false
	}
	if s.access == nil {
		return true
	}
	team, ok := s.access.ClusterTeam(clusterID)
	if !ok {
		return false
	}
	_, ok = s.access.TeamRole(u.ID, team)
	return ok
}

// ErrTeamNotFound — команды нет или пользователь не имеет к ней отношения (как и для кластера, ответ один).
var ErrTeamNotFound = errors.New("team not found")

// CheckTeam — проверка права p над командой teamID (управление её участниками и кластерами).
// Право действует, если его даёт роль пользователя в этой команде; PermUsersManage дополнительно даёт администратор
// организации (он управляет составом команд, но не читает данные кластеров). Не член и не админ организации
// получает ErrTeamNotFound. Без Access (десктоп) команд нет: действует глобальная роль.
func (s *Service) CheckTeam(p Permission, teamID string) (Actor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access == nil {
		return s.checkLocked(p, "")
	}
	u := s.sessionUserLocked()
	if u == nil {
		return Actor{Source: s.source}, ErrNotAuthenticated
	}
	actor := Actor{Username: u.Username, Role: u.Role, Source: s.source, TeamID: teamID}
	if u.MustChangePassword {
		return actor, ErrPasswordChangeRequired
	}
	role, member := s.access.TeamRole(u.ID, teamID)
	if member {
		actor.Role = role
	}
	switch {
	case member && role.Has(p):
		return actor, nil
	case p == PermUsersManage && s.access.IsOrgAdmin(u.ID):
		return actor, nil
	case !member && !s.access.IsOrgAdmin(u.ID):
		return actor, ErrTeamNotFound
	}
	return actor, ErrForbidden
}

// AuthorizeTeam — CheckTeam без возврата пользователя.
func (s *Service) AuthorizeTeam(p Permission, teamID string) error {
	_, err := s.CheckTeam(p, teamID)
	return err
}

// Team — команда в списках.
type Team struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Members  int    `json:"members"`
	Clusters int    `json:"clusters"`
}

// Member — участник команды.
type Member struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Role     Role   `json:"role"`
}

// Directory — управление командами и их составом (задача 3.3). Права проверяет вызывающий (App); реализация
// только хранит. Есть только в сервисном режиме.
type Directory interface {
	// DefaultTeam — команда для кластеров, у которых владелец не указан.
	DefaultTeam() string
	Teams() ([]Team, error)
	CreateTeam(name string) (*Team, error)
	RenameTeam(id, name string) error
	// DeleteTeam удаляет пустую команду (без кластеров); участники исключаются.
	DeleteTeam(id string) error
	Members(teamID string) ([]Member, error)
	// SetMember добавляет участника или меняет его роль.
	SetMember(teamID, userID string, role Role) error
	RemoveMember(teamID, userID string) error
	// MoveCluster передаёт кластер другой команде.
	MoveCluster(clusterID, teamID string) error
}

// Actor возвращает действующего пользователя сессии без проверки прав (для записи в журнал уже разрешённых действий).
func (s *Service) Actor() Actor {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.sessionUserLocked(); u != nil {
		return Actor{Username: u.Username, Role: u.Role, Source: s.source}
	}
	return Actor{Source: s.source}
}

// UserBrief — пользователь для выбора в составе команды: без ролей, хешей и времени входа.
type UserBrief struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// UserBriefs возвращает активных пользователей организации. Права проверяет вызывающий: имена нужны
// администратору команды, чтобы добавлять участников.
func (s *Service) UserBriefs() []UserBrief {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UserBrief, 0, len(s.st.users))
	for _, u := range s.st.users {
		if !u.Disabled {
			out = append(out, UserBrief{ID: u.ID, Username: u.Username})
		}
	}
	return out
}

// TeamsWith возвращает команды, в которых роль пользователя даёт право p (без Access — пусто).
func (s *Service) TeamsWith(p Permission) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.sessionUserLocked()
	if u == nil || s.access == nil {
		return nil
	}
	var out []string
	for team, role := range s.access.TeamRoles(u.ID) {
		if role.Has(p) {
			out = append(out, team)
		}
	}
	sort.Strings(out)
	return out
}
