// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package teams — управление командами и их составом в сервисном режиме.
//
// Пакет не проверяет права на вызов: это делает вызывающий (App) первым действием каждого метода, чтобы имя
// метода попало в журнал и под проверку policy-тестов. Здесь — то, что идёт после разрешения: обращение к
// хранилищу команд, запись результата в журнал аудита и последствия (например, отзыв входов исключённого участника).
package teams

import (
	"errors"
	"fmt"
	"strings"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
)

// ErrNoTeams — команд нет вне сервисного режима (PostgreSQL): в десктопе и в личном режиме методы отвечают этой ошибкой.
var ErrNoTeams = errors.New("teams are available only in service mode")

// Access — то, что нужно сервису от сессии пользователя. Реализует *auth.Service.
type Access interface {
	// CheckTeam проверяет право p над командой и возвращает действующего пользователя.
	CheckTeam(p auth.Permission, teamID string) (auth.Actor, error)
	// Authorize проверяет глобальное право p.
	Authorize(p auth.Permission) error
	// Actor возвращает действующего пользователя без проверки прав (для журнала).
	Actor() auth.Actor
}

// Recorder принимает события журнала аудита. Реализует *audit.Logger.
type Recorder interface{ Record(audit.Event) }

// Entry — команда в списке пользователя: общие сведения и его роль в ней.
type Entry struct {
	Team auth.Team
	// MyRole — роль текущего пользователя в команде; пусто, если он не член (администратор организации видит все команды).
	MyRole string
}

// Service работает с командами от имени одной сессии. Дёшево создаётся на вызов и не хранит состояния.
type Service struct {
	dir auth.Directory
	acc Access
	rec Recorder
	// afterRemove вызывается после исключения участника: личные входы в кластеры команды больше не нужны.
	afterRemove func(userID, teamID, reason string) error
}

// New собирает сервис. dir == nil — команд нет (ErrNoTeams). acc и rec могут быть nil (тогда нет проверки роли
// в списке и записи в журнал соответственно); интерфейс не должен содержать типизированный nil.
func New(dir auth.Directory, acc Access, rec Recorder, afterRemove func(userID, teamID, reason string) error) *Service {
	return &Service{dir: dir, acc: acc, rec: rec, afterRemove: afterRemove}
}

// IsOrgAdmin — текущий пользователь администрирует организацию (право users.manage вне команд).
func (s *Service) IsOrgAdmin() bool {
	return s.acc != nil && s.acc.Authorize(auth.PermUsersManage) == nil
}

// record записывает в журнал результат уже разрешённого действия над командами.
func (s *Service) record(action string, err error, teamID, detail string) {
	if s.rec == nil || s.acc == nil {
		return
	}
	result := audit.ResultOK
	if err != nil {
		result = audit.ResultFailed
	}
	act := s.acc.Actor()
	s.rec.Record(audit.Event{User: act.Username, Role: string(act.Role), Action: action, Result: result, Detail: detail, Source: act.Source, TeamID: teamID})
}

// List — команды пользователя; администратор организации видит все.
func (s *Service) List() ([]Entry, error) {
	if s.dir == nil {
		return nil, ErrNoTeams
	}
	teams, err := s.dir.Teams()
	if err != nil {
		return nil, err
	}
	org := s.IsOrgAdmin()
	out := make([]Entry, 0, len(teams))
	for _, t := range teams {
		e := Entry{Team: t}
		if s.acc != nil {
			if actor, err := s.acc.CheckTeam(auth.PermRead, t.ID); err == nil {
				e.MyRole = string(actor.Role)
			}
		}
		if e.MyRole != "" || org { // чужие команды видит только администратор организации
			out = append(out, e)
		}
	}
	return out, nil
}

// Create создаёт команду.
func (s *Service) Create(name string) (*auth.Team, error) {
	if s.dir == nil {
		return nil, ErrNoTeams
	}
	t, err := s.dir.CreateTeam(name)
	id := ""
	if t != nil {
		id = t.ID
	}
	s.record("CreateTeam", err, id, "name="+strings.TrimSpace(name))
	return t, err
}

// Rename переименовывает команду.
func (s *Service) Rename(teamID, name string) error {
	if s.dir == nil {
		return ErrNoTeams
	}
	err := s.dir.RenameTeam(teamID, name)
	s.record("RenameTeam", err, teamID, "name="+strings.TrimSpace(name))
	return err
}

// Delete удаляет пустую команду без кластеров.
func (s *Service) Delete(teamID string) error {
	if s.dir == nil {
		return ErrNoTeams
	}
	err := s.dir.DeleteTeam(teamID)
	s.record("DeleteTeam", err, teamID, "")
	return err
}

// Members — участники команды.
func (s *Service) Members(teamID string) ([]auth.Member, error) {
	if s.dir == nil {
		return nil, ErrNoTeams
	}
	return s.dir.Members(teamID)
}

// SetMember добавляет пользователя в команду или меняет его роль в ней.
func (s *Service) SetMember(teamID, userID, role string) error {
	if s.dir == nil {
		return ErrNoTeams
	}
	err := s.dir.SetMember(teamID, userID, auth.Role(strings.TrimSpace(role)))
	s.record("SetTeamMember", err, teamID, fmt.Sprintf("user=%s role=%s", userID, role))
	return err
}

// RemoveMember исключает пользователя из команды и отзывает его личные входы в кластеры этой команды.
func (s *Service) RemoveMember(teamID, userID string) error {
	if s.dir == nil {
		return ErrNoTeams
	}
	err := s.dir.RemoveMember(teamID, userID)
	s.record("RemoveTeamMember", err, teamID, "user="+userID)
	if err == nil && s.afterRemove != nil {
		err = s.afterRemove(userID, teamID, "removed-from-team")
	}
	return err
}
