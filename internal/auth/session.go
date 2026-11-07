// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
)

// NewSession создаёт новую независимую сессию (вид) поверх того же хранилища. source — адрес
// клиента для блокировки перебора (может быть пустым). Освободить: Release.
func (s *Service) NewSession(source string) *Service {
	v := &Service{shared: s.shared, source: source}
	s.mu.Lock()
	s.sessions[v] = struct{}{}
	s.mu.Unlock()
	return v
}

// Release завершает сессию (с очисткой её ресурсов, если пользователь был вошёл) и убирает её
// из реестра. Вызывается, когда браузерная сессия закончилась.
func (s *Service) Release() {
	s.mu.Lock()
	had := s.sessionID != ""
	s.sessionID = ""
	hook := s.onLogout
	delete(s.sessions, s)
	s.mu.Unlock()
	if had && hook != nil {
		hook()
	}
}

// endUserSessionsLocked сбрасывает все сессии пользователя userID, кроме except, и возвращает их
// обработчики очистки — вызвать после снятия блокировки.
func (s *Service) endUserSessionsLocked(userID string, except *Service, action string) []func() {
	var hooks []func()
	for v := range s.sessions {
		if v == except || v.sessionID != userID {
			continue
		}
		s.recordFrom(v.source, s.st.findByID(userID), "SessionEnd", audit.ResultOK, "reason: "+action)
		v.sessionID = ""
		if v.onLogout != nil {
			hooks = append(hooks, v.onLogout)
		}
	}
	return hooks
}

func runAll(hooks []func()) {
	for _, h := range hooks {
		h()
	}
}

// SetOnLogout задаёт обработчик, вызываемый при завершении сессии
// (закрыть терминалы, остановить port-forward и т.п.).
func (s *Service) SetOnLogout(fn func()) {
	s.mu.Lock()
	s.onLogout = fn
	s.mu.Unlock()
}

// sessionUserLocked возвращает пользователя текущей сессии. Если его удалили или отключили —
// сессия сбрасывается.
func (s *Service) sessionUserLocked() *UserRecord {
	if s.sessionID == "" {
		return nil
	}
	u := s.st.findByID(s.sessionID)
	if u == nil || u.Disabled {
		s.sessionID = ""
		if hook := s.onLogout; hook != nil {
			go hook() // ресурсы сессии нельзя оставлять работать без пользователя
		}
		return nil
	}
	if s.idleExpiredLocked() {
		s.expireLocked(u)
		return nil
	}
	return u
}

// Active — у этого вида есть действующая сессия пользователя (не вышел, не завершена простоем
// или администратором). Нужен серверу, чтобы закрыть веб-сессию, у которой ядро уже сессию закончило.
func (s *Service) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID != ""
}

// Resume открывает сессию пользователя userID без пароля: веб-сессия уже проверена общим хранилищем
// сессий (ADR-0016), а этот вид ядра построен заново на другой реплике. lastActive — последнее действие
// пользователя, записанное в хранилище; простой считается от него. Пользователя, которого нет в
// памяти (его создала другая реплика), сервис перечитывает из хранилища.
func (s *Service) Resume(userID string, lastActive time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.st.findByID(userID)
	if u == nil {
		if err := s.reloadLocked(); err != nil {
			return err
		}
		u = s.st.findByID(userID)
	}
	if u == nil || u.Disabled {
		return ErrNotAuthenticated
	}
	s.sessionID = u.ID
	s.lastActive = lastActive.Round(0)
	if s.idleExpiredLocked() {
		s.sessionID = ""
		return ErrNotAuthenticated
	}
	return nil
}

// SyncActivity подтягивает время последнего действия пользователя, записанное другой репликой:
// пользователь работает с интерфейсом там, а этот вид ядра простаивает и иначе закрыл бы сессию.
func (s *Service) SyncActivity(at time.Time) {
	s.mu.Lock()
	if at = at.Round(0); at.After(s.lastActive) {
		s.lastActive = at
	}
	s.mu.Unlock()
}

// Logout завершает сессию и вызывает обработчик очистки.
func (s *Service) Logout() {
	s.mu.Lock()
	had := s.sessionID != ""
	if had {
		s.record(s.st.findByID(s.sessionID), "Logout", audit.ResultOK, "")
	}
	s.sessionID = ""
	hook := s.onLogout
	s.mu.Unlock()
	if had && hook != nil {
		hook()
	}
}
