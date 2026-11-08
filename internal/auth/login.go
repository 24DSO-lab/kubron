// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/24DSO-lab/kubron/internal/audit"
)

// Setup создаёт первого администратора. Работает только пока в системе нет ни одного пользователя.
func (s *Service) Setup(username, password string) (*UserInfo, error) {
	username = strings.TrimSpace(username)
	if err := validateCredentials(username, password); err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if len(s.st.users) > 0 {
		return nil, ErrSetupDone
	}
	now := s.now()
	rec := UserRecord{
		ID: newID(), Username: username, Role: RoleAdmin, PasswordHash: hash,
		CreatedAt: now, UpdatedAt: now, LastLoginAt: &now,
	}
	s.st.users = append(s.st.users, rec)
	created, err := s.st.saveFirstUser(&rec)
	if err != nil {
		s.st.users = nil
		return nil, fmt.Errorf("failed to save user: %w", err)
	}
	if !created { // первого администратора создала другая реплика
		s.st.users = nil
		s.refreshLocked()
		return nil, ErrSetupDone
	}
	s.sessionID = rec.ID
	s.touchLocked()
	s.record(&rec, "Setup", audit.ResultOK, "first administrator created")
	return toInfo(&rec), nil
}

func (s *Service) dummy() string {
	s.dummyOnce.Do(func() { s.dummyHash, _ = hashPassword("kubron-dummy-password") })
	return s.dummyHash
}

// Login проверяет логин и пароль и открывает сессию. ctx — граница отмены обращений к общему хранилищу блокировок.
func (s *Service) Login(ctx context.Context, username, password string) (*UserInfo, error) {
	key := normalizeUsername(username)

	shared, checkOK := s.sharedLockRemaining(ctx, key) // общее хранилище: вне блокировки, до проверки пароля
	s.mu.Lock()
	if len(s.st.users) == 0 {
		s.mu.Unlock()
		return nil, ErrSetupRequired
	}

	ipKey := s.ipKey()
	s.mirrorSharedLockLocked(key, shared) // блокировка из общего хранилища переживёт его сбой
	if remaining := max(s.lockRemainingLocked(key), shared, s.lockRemainingLocked(ipKey)); remaining > 0 {
		s.loginBlocked.Add(1)
		s.record(&UserRecord{Username: username}, "Login", audit.ResultDenied, "login temporarily blocked after failed attempts")

		s.mu.Unlock()
		return nil, &LockedError{RetryAfter: remaining}
	}
	var snapshot *UserRecord
	if rec := s.st.findByName(key); rec != nil {
		c := *rec
		snapshot = &c
	}
	s.mu.Unlock()
	if snapshot == nil && s.refreshOnMiss() { // пользователя могла создать другая реплика
		s.mu.Lock()
		if rec := s.st.findByName(key); rec != nil {
			c := *rec
			snapshot = &c
		}
		s.mu.Unlock()
	}

	// Хеширование — вне блокировки. Для несуществующего логина считаем фиктивный хеш,
	// чтобы время ответа не выдавало, существует ли пользователь.
	hash := s.dummy()
	if snapshot != nil {
		hash = snapshot.PasswordHash
	}
	ok, rehash := verifyPassword(password, hash)

	var newHash string
	if ok && snapshot != nil && rehash {
		newHash, _ = hashPassword(password)
	}

	failed := !ok || snapshot == nil
	degraded := !checkOK                            // общее хранилище не ответило на проверку блокировки
	if failed && !s.sharedRecordFailure(ctx, key) { // вне s.mu: ходит во внешнее хранилище
		degraded = true
	}

	s.mu.Lock()
	if failed {
		s.recordKeyFailureLocked(key, degraded)
		s.recordSourceFailureLocked(ipKey)
		s.record(&UserRecord{Username: username}, "Login", audit.ResultFailed, ErrInvalidCredentials.Error())
		s.mu.Unlock()
		return nil, ErrInvalidCredentials
	}
	rec := s.st.findByID(snapshot.ID)
	if rec == nil || rec.PasswordHash != snapshot.PasswordHash {
		// Пользователя удалили или сменили пароль, пока шла проверка.
		s.record(&UserRecord{Username: username}, "Login", audit.ResultFailed, ErrInvalidCredentials.Error())
		s.mu.Unlock()
		return nil, ErrInvalidCredentials
	}
	if rec.Disabled {
		s.record(rec, "Login", audit.ResultDenied, ErrAccountDisabled.Error())
		s.mu.Unlock()
		return nil, ErrAccountDisabled
	}
	delete(s.failures, key)

	replaced := s.sessionID != "" && s.sessionID != rec.ID
	now := s.now()
	rec.LastLoginAt = &now
	if newHash != "" {
		rec.PasswordHash = newHash
	}
	_ = s.st.saveLogin(rec, newHash) // не мешаем входу, если диск недоступен для записи
	s.sessionID = rec.ID
	s.touchLocked()
	s.record(rec, "Login", audit.ResultOK, "")
	info := toInfo(rec)
	hook := s.onLogout
	s.mu.Unlock()
	s.sharedClearFailures(ctx, key) // в общем хранилище — после снятия блокировки

	if replaced && hook != nil {
		hook()
	}
	return info, nil
}

// ChangePassword меняет пароль текущего пользователя.
func (s *Service) ChangePassword(ctx context.Context, oldPassword, newPassword string) error {
	s.mu.Lock()
	u := s.sessionUserLocked()
	if u == nil {
		s.mu.Unlock()
		return ErrNotAuthenticated
	}
	id, username, oldHash := u.ID, u.Username, u.PasswordHash
	// Подбор старого пароля (украденная сессия) ограничивается так же, как вход: по логину и по адресу
	pwKey, ipKey := "pw:"+normalizeUsername(username), s.ipKey()
	s.mu.Unlock()
	shared, checkOK := s.sharedLockRemaining(ctx, pwKey)
	s.mu.Lock()
	s.mirrorSharedLockLocked(pwKey, shared) // блокировка из общего хранилища переживёт его сбой
	if remaining := max(s.lockRemainingLocked(pwKey), shared, s.lockRemainingLocked(ipKey)); remaining > 0 {
		s.record(u, "ChangePassword", audit.ResultDenied, "password change temporarily blocked after failed attempts")
		s.mu.Unlock()
		return &LockedError{RetryAfter: remaining}
	}
	s.mu.Unlock()

	if err := validateCredentialsPassword(username, newPassword); err != nil {
		return err
	}
	if ok, _ := verifyPassword(oldPassword, oldHash); !ok {
		degraded := !checkOK
		if !s.sharedRecordFailure(ctx, pwKey) { // вне s.mu: ходит во внешнее хранилище
			degraded = true
		}
		s.mu.Lock()
		s.recordKeyFailureLocked(pwKey, degraded)
		s.recordSourceFailureLocked(ipKey)
		s.record(s.st.findByID(id), "ChangePassword", audit.ResultFailed, ErrWrongPassword.Error())
		s.mu.Unlock()
		return ErrWrongPassword
	}
	if oldPassword == newPassword {
		return ErrSamePassword
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}

	var hooks []func()
	defer func() { runAll(hooks) }() // после снятия блокировки (defer выполняются в обратном порядке)
	changed := false
	defer func() {
		if changed {
			s.sharedClearFailures(ctx, pwKey)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	rec := s.st.findByID(id)
	if rec == nil || s.sessionID != id {
		return ErrNotAuthenticated
	}
	prev := *rec
	rec.PasswordHash = newHash
	rec.MustChangePassword = false
	rec.UpdatedAt = s.now()
	if err := s.st.saveUser(rec); err != nil {
		*rec = prev
		return fmt.Errorf("failed to save password: %w", err)
	}
	delete(s.failures, pwKey)
	changed = true
	s.touchLocked()
	s.record(rec, "ChangePassword", audit.ResultOK, "")
	// Остальные сессии этого пользователя (другие браузеры) завершаются: пароль мог быть скомпрометирован
	hooks = s.endUserSessionsLocked(id, s, "ChangePassword")
	return nil
}

func validateCredentials(username, password string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	return validateCredentialsPassword(username, password)
}

func validateCredentialsPassword(username, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	if strings.EqualFold(password, username) {
		return ErrWeakPassword
	}
	return nil
}
