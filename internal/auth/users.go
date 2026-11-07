// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"
	"strings"

	"github.com/24DSO-labs/kubron/internal/audit"
)

// ListUsers возвращает всех пользователей.
func (s *Service) ListUsers() ([]UserInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.authorizeLocked(PermUsersManage); err != nil {
		return nil, err
	}
	out := make([]UserInfo, 0, len(s.st.users))
	for i := range s.st.users {
		out = append(out, *toInfo(&s.st.users[i]))
	}
	return out, nil
}

// CreateUser создаёт пользователя с временным паролем: при первом входе его придётся сменить.
func (s *Service) CreateUser(username, password string, role Role) (*UserInfo, error) {
	username = strings.TrimSpace(username)
	if !role.Valid() {
		return nil, ErrUnknownRole
	}
	if err := validateCredentials(username, password); err != nil {
		return nil, err
	}
	if err := s.Authorize(PermUsersManage); err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if _, err := s.authorizeLocked(PermUsersManage); err != nil {
		return nil, err
	}
	actor := s.sessionUserLocked()
	detail := fmt.Sprintf("user=%s role=%s", username, role)
	if s.st.findByName(normalizeUsername(username)) != nil {
		s.record(actor, "CreateUser", audit.ResultFailed, detail+": "+ErrUserExists.Error())
		return nil, ErrUserExists
	}
	now := s.now()
	rec := UserRecord{
		ID: newID(), Username: username, Role: role, PasswordHash: hash,
		MustChangePassword: true, CreatedAt: now, UpdatedAt: now,
	}
	s.st.users = append(s.st.users, rec)
	if err := s.st.saveUser(&rec); err != nil {
		s.st.users = s.st.users[:len(s.st.users)-1]
		s.record(actor, "CreateUser", audit.ResultFailed, detail+": "+err.Error())
		return nil, fmt.Errorf("failed to save user: %w", err)
	}
	s.record(actor, "CreateUser", audit.ResultOK, detail)
	return toInfo(&rec), nil
}

// mutate применяет изменение к пользователю с откатом при ошибке записи.
//
// fn возвращает описание изменения для журнала аудита (например, «admin → user»).
func (s *Service) mutate(id, action string, fn func(actor, target *UserRecord) (string, error)) (*UserInfo, error) {
	var hooks []func()
	defer func() { runAll(hooks) }() // после снятия блокировки
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	actor, err := s.authorizeLocked(PermUsersManage)
	if err != nil {
		return nil, err
	}
	target := s.st.findByID(id)
	if target == nil {
		s.record(actor, action, audit.ResultFailed, "id="+id+": "+ErrUserNotFound.Error())
		return nil, ErrUserNotFound
	}
	prev := *target
	who := "user=" + target.Username
	change, err := fn(actor, target)
	if err != nil {
		*target = prev
		s.record(actor, action, audit.ResultFailed, who+": "+err.Error())
		return nil, err
	}
	target.UpdatedAt = s.now()
	if err := s.st.saveUser(target); err != nil {
		*target = prev
		s.record(actor, action, audit.ResultFailed, who+": "+err.Error())
		return nil, fmt.Errorf("failed to save changes: %w", err)
	}
	if change != "" {
		who += " " + change
	}
	s.record(actor, action, audit.ResultOK, who)
	// Смена роли, отключение и новый временный пароль завершают сессии пользователя: уже открытые
	// терминалы и port-forward не должны пережить потерю прав или пароля.
	if prev.Role != target.Role || (!prev.Disabled && target.Disabled) || action == "ResetUserPassword" {
		hooks = s.endUserSessionsLocked(target.ID, nil, action)
	}
	return toInfo(target), nil
}

// UpdateRole меняет роль пользователя.
func (s *Service) UpdateRole(id string, role Role) (*UserInfo, error) {
	if !role.Valid() {
		return nil, ErrUnknownRole
	}
	return s.mutate(id, "UpdateUserRole", func(actor, t *UserRecord) (string, error) {
		// Свою роль менять нельзя: иначе уже запущенные терминалы и port-forward
		// пережили бы потерю прав. Понизить администратора может только другой администратор.
		if actor.ID == t.ID && role != t.Role {
			return "", ErrSelfAction
		}
		if t.Role == RoleAdmin && role != RoleAdmin && !t.Disabled && s.st.enabledAdmins() <= 1 {
			return "", ErrLastAdmin
		}
		change := fmt.Sprintf("role=%s→%s", t.Role, role)
		t.Role = role
		return change, nil
	})
}

// SetDisabled включает или отключает учётную запись.
func (s *Service) SetDisabled(id string, disabled bool) (*UserInfo, error) {
	return s.mutate(id, "SetUserDisabled", func(actor, t *UserRecord) (string, error) {
		if disabled {
			if actor.ID == t.ID {
				return "", ErrSelfAction
			}
			if t.Role == RoleAdmin && !t.Disabled && s.st.enabledAdmins() <= 1 {
				return "", ErrLastAdmin
			}
		}
		t.Disabled = disabled
		return fmt.Sprintf("disabled=%t", disabled), nil
	})
}

// ResetPassword задаёт пользователю временный пароль; при следующем входе его нужно сменить.
func (s *Service) ResetPassword(id, newPassword string) (*UserInfo, error) {
	if err := ValidatePassword(newPassword); err != nil {
		return nil, err
	}
	if err := s.Authorize(PermUsersManage); err != nil {
		return nil, err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	return s.mutate(id, "ResetUserPassword", func(actor, t *UserRecord) (string, error) {
		if actor.ID == t.ID {
			return "", ErrSelfAction // свой пароль меняют через ChangePassword
		}
		if strings.EqualFold(newPassword, t.Username) {
			return "", ErrWeakPassword
		}
		t.PasswordHash = hash
		t.MustChangePassword = true
		return "", nil
	})
}

// DeleteUser удаляет пользователя.
func (s *Service) DeleteUser(id string) error {
	var hooks []func()
	defer func() { runAll(hooks) }() // после снятия блокировки
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	actor, err := s.authorizeLocked(PermUsersManage)
	if err != nil {
		return err
	}
	target := s.st.findByID(id)
	if target == nil {
		s.record(actor, "DeleteUser", audit.ResultFailed, "id="+id+": "+ErrUserNotFound.Error())
		return ErrUserNotFound
	}
	who := "user=" + target.Username
	if actor.ID == target.ID {
		s.record(actor, "DeleteUser", audit.ResultFailed, who+": "+ErrSelfAction.Error())
		return ErrSelfAction
	}
	if target.Role == RoleAdmin && !target.Disabled && s.st.enabledAdmins() <= 1 {
		s.record(actor, "DeleteUser", audit.ResultFailed, who+": "+ErrLastAdmin.Error())
		return ErrLastAdmin
	}
	prev := append([]UserRecord(nil), s.st.users...)
	s.st.remove(id)
	if err := s.st.saveRemoved(id); err != nil {
		s.st.users = prev
		s.record(actor, "DeleteUser", audit.ResultFailed, who+": "+err.Error())
		return fmt.Errorf("failed to save changes: %w", err)
	}
	s.record(actor, "DeleteUser", audit.ResultOK, who)
	hooks = s.endUserSessionsLocked(id, nil, "DeleteUser")
	return nil
}
