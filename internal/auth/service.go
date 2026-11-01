// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
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

// LockedError — вход временно заблокирован после серии неудачных попыток.
type LockedError struct{ RetryAfter time.Duration }

func (e *LockedError) Error() string {
	secs := int((e.RetryAfter + time.Second - 1) / time.Second)
	return fmt.Sprintf("too many failed login attempts, try again in %d s", secs)
}

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
	NeedsSetup bool      `json:"needsSetup"`
	User       *UserInfo `json:"user"`
	// IdleTimeoutMinutes — через сколько минут без действий пользователя сессия завершается (0 — не завершается).
	IdleTimeoutMinutes int `json:"idleTimeoutMinutes"`
}

type failure struct {
	count       int
	lockedUntil time.Time
	last        time.Time // последняя неудача (для забывания счётчика по адресу)
}

// Recorder принимает события журнала аудита. Реализация (audit.Logger) не должна
// вызывать методы Service: события пишутся под его блокировкой.
type Recorder interface{ Record(audit.Event) }

// Actor — кто выполняет действие (для журнала аудита).
type Actor struct {
	Username string
	Role     Role
	Source   string // адрес клиента сессии (веб); пусто в десктопе
	// Контекст проверки в сервисном режиме: кластер и команда, к которым относится действие (для журнала).
	ClusterID string
	TeamID    string
}

// shared — состояние, общее для всех сессий: хранилище пользователей, счётчики неудачных входов,
// журнал аудита и реестр сессий. Всё защищено одним мьютексом mu.
type shared struct {
	mu       sync.Mutex
	st       *store
	failures map[string]*failure
	now      func() time.Time
	rec      Recorder
	access   Access                // nil — прежняя модель: глобальная роль пользователя
	sessions map[*Service]struct{} // живые сессии, включая неявную сессию десктопа

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
	svc := &Service{shared: sh}
	sh.sessions[svc] = struct{}{}
	return svc, nil
}

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

// SetAuditor подключает журнал аудита.
func (s *Service) SetAuditor(r Recorder) {
	s.mu.Lock()
	s.rec = r
	s.mu.Unlock()
}

// record пишет событие в журнал. Вызывается под s.mu; actor может быть nil.
func (s *Service) record(actor *UserRecord, action, result, detail string) {
	s.recordFrom(s.source, actor, action, result, detail)
}

// recordFrom — то же, что record, но с адресом клиента той сессии, о которой событие.
func (s *Service) recordFrom(source string, actor *UserRecord, action, result, detail string) {
	if s.rec == nil {
		return
	}
	e := audit.Event{Time: s.now(), Action: action, Result: result, Detail: detail, Source: source}
	if actor != nil {
		e.User, e.Role = actor.Username, string(actor.Role)
	}
	s.rec.Record(e)
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

// ─── Сессия ────────────────────────────────────────────────

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

// ─── Автовыход по бездействию ──────────────────────────────

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

// Active — у этого вида есть действующая сессия пользователя (не вышел, не завершена простоем
// или администратором). Нужен серверу, чтобы закрыть веб-сессию, у которой ядро уже сессию закончило.
func (s *Service) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID != ""
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

// ─── Вход / выход ──────────────────────────────────────────

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
	if err := s.st.saveUser(&rec); err != nil {
		s.st.users = nil
		return nil, fmt.Errorf("failed to save user: %w", err)
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

// Login проверяет логин и пароль и открывает сессию.
func (s *Service) Login(username, password string) (*UserInfo, error) {
	key := normalizeUsername(username)

	s.mu.Lock()
	if len(s.st.users) == 0 {
		s.mu.Unlock()
		return nil, ErrSetupRequired
	}

	ipKey := s.ipKey()
	if remaining := max(s.lockRemainingLocked(key), s.lockRemainingLocked(ipKey)); remaining > 0 {
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

	s.mu.Lock()
	if !ok || snapshot == nil {
		s.recordFailureLocked(key, freeAttempts)
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

	if replaced && hook != nil {
		hook()
	}
	return info, nil
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

// missRefreshEvery — не чаще одного внеочередного чтения при неизвестном логине (защита хранилища от перебора).
const missRefreshEvery = time.Second

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

func (s *Service) lockRemainingLocked(key string) time.Duration {
	f := s.failures[key]
	if f == nil {
		return 0
	}
	if d := f.lockedUntil.Sub(s.now()); d > 0 {
		return d
	}
	return 0
}

// ipKey — ключ блокировки по адресу клиента ("" — адрес неизвестен, блокировки по адресу нет).
func (s *Service) ipKey() string {
	if s.source == "" {
		return ""
	}
	return "ip:" + NormalizeSource(s.source)
}

// recordSourceFailureLocked считает неудачу по адресу клиента: порог выше, чем по логину
// (за одним адресом может быть целый офис), а счётчик забывается через sourceWindow.
func (s *Service) recordSourceFailureLocked(key string) {
	if key == "" {
		return
	}
	if f := s.failures[key]; f != nil && s.now().Sub(f.last) > sourceWindow && f.lockedUntil.Before(s.now()) {
		delete(s.failures, key)
	}
	s.recordFailureLocked(key, sourceFreeAttempts)
}

func (s *Service) recordFailureLocked(key string, free int) {
	if s.failures[key] == nil && !s.makeRoomLocked(key) {
		return // все места заняты действующими блокировками: новый ключ не отслеживается, чужие блокировки целы
	}
	f := s.failures[key]
	if f == nil {
		f = &failure{}
		s.failures[key] = f
	}
	f.count++
	f.last = s.now()
	if f.count >= free {
		exp := f.count - free
		if exp > 6 {
			exp = 6
		}
		d := baseLockout << uint(exp)
		if d > maxLockout {
			d = maxLockout
		}
		f.lockedUntil = s.now().Add(d)
	}
}

// makeRoomLocked освобождает место под новый ключ, не трогая действующие блокировки. Лимит считается
// отдельно для адресов («ip:») и для логинов, поэтому подбор множества логинов не стирает записи об
// адресах и наоборот. Порядок вытеснения: сначала записи без блокировки, давно не обновлявшиеся
// (sourceWindow), затем самая старая из остальных незаблокированных. Если все записи этого вида —
// действующие блокировки, место не освобождается (false): новый ключ не отслеживается, зато ни одна
// блокировка не сбрасывается. Обход всей карты выполняется только при заполнении.
func (s *Service) makeRoomLocked(key string) bool {
	isIP := strings.HasPrefix(key, "ip:")
	now := s.now()
	n := 0
	var oldestKey string
	var oldest time.Time
	for k, f := range s.failures {
		if strings.HasPrefix(k, "ip:") != isIP {
			continue
		}
		n++
		if f.lockedUntil.After(now) {
			continue
		}
		if now.Sub(f.last) > sourceWindow {
			delete(s.failures, k) // истёкшая запись
			n--
			continue
		}
		if oldestKey == "" || f.last.Before(oldest) {
			oldestKey, oldest = k, f.last
		}
	}
	if n < maxTracked {
		return true
	}
	if oldestKey == "" {
		return false
	}
	delete(s.failures, oldestKey)
	return true
}

// ChangePassword меняет пароль текущего пользователя.
func (s *Service) ChangePassword(oldPassword, newPassword string) error {
	s.mu.Lock()
	u := s.sessionUserLocked()
	if u == nil {
		s.mu.Unlock()
		return ErrNotAuthenticated
	}
	id, username, oldHash := u.ID, u.Username, u.PasswordHash
	// Подбор старого пароля (украденная сессия) ограничивается так же, как вход: по логину и по адресу
	pwKey, ipKey := "pw:"+normalizeUsername(username), s.ipKey()
	if remaining := max(s.lockRemainingLocked(pwKey), s.lockRemainingLocked(ipKey)); remaining > 0 {
		s.record(u, "ChangePassword", audit.ResultDenied, "password change temporarily blocked after failed attempts")
		s.mu.Unlock()
		return &LockedError{RetryAfter: remaining}
	}
	s.mu.Unlock()

	if err := validateCredentialsPassword(username, newPassword); err != nil {
		return err
	}
	if ok, _ := verifyPassword(oldPassword, oldHash); !ok {
		s.mu.Lock()
		s.recordFailureLocked(pwKey, freeAttempts)
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
	s.touchLocked()
	s.record(rec, "ChangePassword", audit.ResultOK, "")
	// Остальные сессии этого пользователя (другие браузеры) завершаются: пароль мог быть скомпрометирован
	hooks = s.endUserSessionsLocked(id, s, "ChangePassword")
	return nil
}

// ─── Управление пользователями (право users.manage) ────────

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

// ─── Проверки ввода ────────────────────────────────────────

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
