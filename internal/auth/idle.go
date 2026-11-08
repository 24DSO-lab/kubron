// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
)

func (s *Service) idleMinutesLocked() int {
	if m := s.st.settings.IdleTimeoutMinutes; m != nil {
		return *m
	}
	return DefaultIdleTimeoutMinutes
}

// wall отбрасывает монотонные часы: время сна компьютера должно считаться бездействием.
func (s *Service) wall() time.Time { return s.now().Round(0) }

func (s *Service) touchLocked() { s.lastActive = s.wall() }

func (s *Service) idleExpiredLocked() bool {
	m := s.idleMinutesLocked()
	if m <= 0 {
		return false
	}
	return s.wall().Sub(s.lastActive) > time.Duration(m)*time.Minute+idleGrace
}

// expireLocked завершает сессию из-за бездействия и запускает очистку (терминалы, port-forward).
// Очистка идёт в отдельной горутине: она не должна выполняться под блокировкой Service.
func (s *Service) expireLocked(u *UserRecord) {
	s.record(u, "SessionTimeout", audit.ResultOK, fmt.Sprintf("inactive for more than %d min", s.idleMinutesLocked()))
	s.sessionID = ""
	if hook := s.onLogout; hook != nil {
		go hook()
	}
}

// ReportActivity сообщает, что пользователь работает с интерфейсом, и продлевает сессию.
// Обычные вызовы API сессию не продлевают: интерфейс сам опрашивает кластер по таймеру,
// и иначе сессия не истекла бы никогда.
func (s *Service) ReportActivity() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionUserLocked() == nil {
		return ErrNotAuthenticated
	}
	s.touchLocked()
	return nil
}

// ExpireIdle завершает сессию, если она простаивала дольше допустимого. Возвращает true, если завершила.
func (s *Service) ExpireIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID == "" {
		return false
	}
	return s.sessionUserLocked() == nil
}

// ExpireAllIdle завершает все сессии, простаивавшие дольше допустимого; возвращает их число.
func (s *Service) ExpireAllIdle() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for v := range s.sessions {
		if v.sessionID != "" && v.sessionUserLocked() == nil {
			n++
		}
	}
	return n
}

// RunIdleWatcher периодически завершает простаивающие сессии (всех видов), чтобы терминалы и
// port-forward закрывались вовремя, даже если интерфейс больше ничего не вызывает. Возвращается при отмене ctx.
func (s *Service) RunIdleWatcher(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ExpireAllIdle()
		}
	}
}

// SetIdleTimeout задаёт время автовыхода в минутах (0 — выключить) и возвращает сохранённое значение.
func (s *Service) SetIdleTimeout(minutes int) (int, error) {
	if minutes < 0 || minutes > MaxIdleTimeoutMinutes {
		return 0, ErrInvalidIdleTimeout
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	actor, err := s.authorizeLocked(PermUsersManage)
	if err != nil {
		return 0, err
	}
	prev := s.st.settings
	was := s.idleMinutesLocked()
	s.st.settings.IdleTimeoutMinutes = &minutes
	if err := s.st.saveSettings(); err != nil {
		s.st.settings = prev
		s.record(actor, "SetIdleTimeout", audit.ResultFailed, err.Error())
		return 0, fmt.Errorf("failed to save setting: %w", err)
	}
	s.record(actor, "SetIdleTimeout", audit.ResultOK, fmt.Sprintf("minutes=%d→%d", was, minutes))
	return minutes, nil
}
