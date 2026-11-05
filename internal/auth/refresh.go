// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"log"
	"time"
)

// reloadLocked перечитывает пользователей и настройки из хранилища (общее состояние всех видов).
func (s *Service) reloadLocked() error {
	if err := s.st.reload(); err != nil {
		return err
	}
	return nil
}

// refreshLocked подтягивает изменения других реплик перед изменением пользователей: запись идёт точечными
// операциями, но решения (имя занято, последний администратор) принимаются по свежим данным. Для файлового
// хранилища (один процесс) ничего не делает. Ошибка чтения не мешает операции: запись всё равно точечная.
func (s *Service) refreshLocked() {
	if s.st.delta == nil {
		return
	}
	if err := s.reloadLocked(); err != nil {
		log.Printf("auth: users were not refreshed: %v", err)
	}
}

// refresh перечитывает пользователей без блокировки на время обращения к хранилищу. Результат отбрасывается,
// если за это время пользователей меняли здесь: локальное изменение новее прочитанного.
func (s *Service) refresh() error {
	s.mu.Lock()
	gen := s.st.gen
	s.mu.Unlock()
	fresh, err := s.st.backend.Load()
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.st.gen == gen {
		s.st.users, s.st.settings = fresh.Users, fresh.Settings
	}
	s.mu.Unlock()
	return nil
}

// refreshOnMiss перечитывает пользователей, если логин неизвестен: его могла создать другая реплика.
// Возвращает true, если чтение выполнено. Только для общего хранилища.
func (s *Service) refreshOnMiss() bool {
	if s.st.delta == nil {
		return false
	}
	now := time.Now().UnixNano()
	last := s.missAt.Load()
	if now-last < int64(missRefreshEvery) || !s.missAt.CompareAndSwap(last, now) {
		return false
	}
	if err := s.refresh(); err != nil {
		log.Printf("auth: users were not refreshed: %v", err)
		return false
	}
	return true
}

// Shared — пользователи лежат в хранилище, общем для нескольких процессов (PostgreSQL), и RunRefresher нужен.
func (s *Service) Shared() bool { return s.st.delta != nil }

// RefreshNow немедленно перечитывает пользователей из общего хранилища (сообщение от другой реплики об изменении
// пользователей или прав). Для файлового хранилища ничего не делает.
func (s *Service) RefreshNow() {
	if s.st.delta == nil {
		return
	}
	if err := s.refresh(); err != nil {
		log.Printf("auth: users were not refreshed: %v", err)
	}
}

// RunRefresher периодически перечитывает пользователей, пока ctx не отменён: роль, блокировка и новые
// пользователи, изменённые на другой реплике, становятся видны здесь не позже чем через every.
func (s *Service) RunRefresher(ctx context.Context, every time.Duration) {
	if s.st.delta == nil || every <= 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.refresh(); err != nil {
				log.Printf("auth: users were not refreshed: %v", err)
			}
		}
	}
}
