// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Ошибки отдаются во фронтенд как текст (Wails), поэтому сообщения — на языке интерфейса.
var (
	ErrNotAuthenticated       = errors.New("authentication required")
	ErrForbidden              = errors.New("insufficient permissions for this action")
	ErrInvalidCredentials     = errors.New("invalid username or password")
	ErrAccountDisabled        = errors.New("account is disabled")
	ErrSetupDone              = errors.New("initial setup is already complete")
	ErrSetupRequired          = errors.New("initial setup required: create an administrator")
	ErrPasswordChangeRequired = errors.New("password change required")
	ErrUserExists             = errors.New("a user with this username already exists")
	ErrUserNotFound           = errors.New("user not found")
	ErrUnknownRole            = errors.New("unknown role")
	ErrLastAdmin              = errors.New("cannot remove access from the last administrator")
	ErrSelfAction             = errors.New("this action cannot be performed on your own account")
	ErrWrongPassword          = errors.New("current password is incorrect")
	ErrSamePassword           = errors.New("new password must differ from the current one")
	ErrWeakPassword           = errors.New("password must not match the username")
	ErrInvalidIdleTimeout     = errors.New("idle timeout must be 0 (disabled) or between 1 and 1440 minutes")
)

const (
	// DefaultIdleTimeoutMinutes — автовыход по бездействию, пока администратор не задал своё значение.
	DefaultIdleTimeoutMinutes = 15
	// MaxIdleTimeoutMinutes — верхняя граница настройки (сутки).
	MaxIdleTimeoutMinutes = 24 * 60
	// idleGrace — запас бэкенда сверх настроенного времени. Интерфейс сообщает об активности
	// не чаще раза в ~20 с, поэтому его собственный таймер всегда срабатывает раньше и успевает
	// корректно выйти; бэкенд — страховка на случай, если интерфейс завис или недоступен.
	idleGrace = 30 * time.Second

	freeAttempts = 5
	// По адресу клиента порог выше (один адрес — часто целый офис за NAT) и счётчик забывается.
	sourceFreeAttempts = 30
	sourceWindow       = 15 * time.Minute
	baseLockout        = 15 * time.Second
	maxLockout         = 5 * time.Minute
	// maxTracked — сколько записей о неудачах хранится отдельно для логинов и отдельно для адресов:
	// переполнение одного вида не вытесняет записи другого (см. makeRoomLocked).
	maxTracked = 1024
)

// UserInfo — безопасное представление пользователя для интерфейса (без хеша пароля).
type UserInfo struct {
	ID                 string       `json:"id"`
	Username           string       `json:"username"`
	Role               Role         `json:"role"`
	RoleLabel          string       `json:"roleLabel"`
	Permissions        []Permission `json:"permissions"`
	Disabled           bool         `json:"disabled"`
	MustChangePassword bool         `json:"mustChangePassword"`
	CreatedAt          string       `json:"createdAt"`
	LastLoginAt        string       `json:"lastLoginAt"`
}

// Status — состояние аутентификации при старте интерфейса.
type Status struct {
	NeedsSetup bool `json:"needsSetup"`
	// SetupTokenRequired — первичная настройка по сети требует токен (заполняет серверный слой в сервисном режиме).
	SetupTokenRequired bool      `json:"setupTokenRequired,omitempty"`
	User               *UserInfo `json:"user"`
	// IdleTimeoutMinutes — через сколько минут без действий пользователя сессия завершается (0 — не завершается).
	IdleTimeoutMinutes int `json:"idleTimeoutMinutes"`
}

// shared — состояние, общее для всех сессий: хранилище пользователей, счётчики неудачных входов,
// журнал аудита и реестр сессий. Всё защищено одним мьютексом mu.
type shared struct {
	mu       sync.Mutex
	st       *store
	failures map[string]*failure // счётчики по адресу (и по логину, если нет общего хранилища fail)
	fail     FailureBackend      // не nil: блокировки по логину общие для всех реплик
	now      func() time.Time
	rec      Recorder
	access   Access                // nil — прежняя модель: глобальная роль пользователя
	sessions map[*Service]struct{} // живые сессии, включая неявную сессию десктопа

	// Счётчики для метрик (только числа, без имён пользователей и адресов).
	loginBlocked    atomic.Int64 // попытки входа, отклонённые из-за действующей блокировки
	lockoutsStarted atomic.Int64 // сколько раз серия неудач привела к блокировке

	missAt atomic.Int64 // когда последний раз перечитывали пользователей из-за неизвестного логина (UnixNano)

	dummyOnce sync.Once
	dummyHash string
}

// Service — аутентификация, сессия и управление пользователями.
//
// Service — это ВИД на общее состояние (shared) со своей сессией: у каждого окна браузера свой
// Service (NewSession), у десктопа — один неявный. Методы работают от имени пользователя именно
// этой сессии; вход в одном виде не влияет на другие. Права пользователя перечитываются из
// хранилища при каждой проверке, так что смена роли действует немедленно, а отключение, удаление
// и смена пароля пользователя завершают его сессии (вызывая их обработчики очистки).
type Service struct {
	*shared
	sessionID  string    // id пользователя этой сессии ("" — не вошёл)
	lastActive time.Time // последнее действие пользователя в интерфейсе (не вызов API)
	onLogout   func()    // очистка ресурсов этой сессии (терминалы, port-forward)
	source     string    // откуда пришёл вход (IP) — для блокировки перебора паролей
}

// NewService открывает (или создаёт при первом запуске) хранилище пользователей в каталоге dir.
func NewService(dir string) (*Service, error) {
	return NewServiceWithBackend(NewFileBackend(dir))
}

// NewServiceWithBackend открывает сервис поверх произвольного хранилища (файл, PostgreSQL и т. д.).
func NewServiceWithBackend(b Backend) (*Service, error) {
	st, err := loadStore(b)
	if err != nil {
		return nil, err
	}
	sh := &shared{st: st, failures: map[string]*failure{}, now: time.Now, sessions: map[*Service]struct{}{}}
	sh.fail, _ = b.(FailureBackend)
	svc := &Service{shared: sh}
	sh.sessions[svc] = struct{}{}
	return svc, nil
}

func normalizeUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand is unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func toInfo(u *UserRecord) *UserInfo {
	info := &UserInfo{
		ID:                 u.ID,
		Username:           u.Username,
		Role:               u.Role,
		RoleLabel:          u.Role.Label(),
		Permissions:        u.Role.Permissions(),
		Disabled:           u.Disabled,
		MustChangePassword: u.MustChangePassword,
		CreatedAt:          u.CreatedAt.UTC().Format(time.RFC3339),
	}
	if u.LastLoginAt != nil {
		info.LastLoginAt = u.LastLoginAt.UTC().Format(time.RFC3339)
	}
	return info
}

// Status возвращает состояние для стартового экрана.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{NeedsSetup: len(s.st.users) == 0, IdleTimeoutMinutes: s.idleMinutesLocked()}
	if u := s.sessionUserLocked(); u != nil {
		st.User = toInfo(u)
	}
	return st
}

// Current возвращает пользователя сессии (даже если ему нужно сменить пароль).
func (s *Service) Current() (*UserInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.sessionUserLocked()
	if u == nil {
		return nil, ErrNotAuthenticated
	}
	return toInfo(u), nil
}

// missRefreshEvery — не чаще одного внеочередного чтения при неизвестном логине (защита хранилища от перебора).
const missRefreshEvery = time.Second

// Stats — счётчики защиты входа для метрик: сколько попыток отклонено блокировкой и сколько блокировок
// началось с запуска процесса. Только числа: ни логинов, ни адресов.
type Stats struct{ LoginBlocked, LockoutsStarted int64 }

// Stats возвращает счётчики общего состояния; безопасно вызывать из любого вида.
func (s *Service) Stats() Stats {
	if s == nil || s.shared == nil {
		return Stats{}
	}
	return Stats{LoginBlocked: s.loginBlocked.Load(), LockoutsStarted: s.lockoutsStarted.Load()}
}
