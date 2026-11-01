// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import "time"

// UserRecord — запись пользователя в хранилище. Наружу (в интерфейс) не отдаётся: см. UserInfo.
type UserRecord struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	Role               Role       `json:"role"`
	PasswordHash       string     `json:"passwordHash"`
	Disabled           bool       `json:"disabled"`
	MustChangePassword bool       `json:"mustChangePassword"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	LastLoginAt        *time.Time `json:"lastLoginAt,omitempty"`
}

// SecuritySettings — настройки безопасности, общие для всех пользователей.
// Указатель различает «не задано» (берётся значение по умолчанию) и явный 0 («выключено»).
type SecuritySettings struct {
	IdleTimeoutMinutes *int `json:"idleTimeoutMinutes,omitempty"`
}

// Data — всё, что хранится постоянно: пользователи и настройки безопасности.
type Data struct {
	Users    []UserRecord     `json:"users"`
	Settings SecuritySettings `json:"settings"`
}

// Backend — постоянное хранилище пользователей. Реализации: файл users.json (десктоп) и,
// позже, PostgreSQL (сервер, ADR-0007). Вызывается под блокировкой Service.
type Backend interface {
	// Load читает данные. Отсутствие данных — пустой Data (первый запуск). Повреждённые или
	// созданные более новой версией данные — ошибка: молча считать их пустыми нельзя, иначе
	// любой смог бы пройти первичную настройку и создать администратора.
	Load() (*Data, error)
	// Save атомарно сохраняет данные целиком.
	Save(*Data) error
}

// DeltaBackend — необязательное расширение Backend для хранилища, общего для нескольких процессов
// (PostgreSQL, реплики сервиса). Save заменяет набор целиком и затёр бы изменения других реплик, поэтому Service
// пишет только то, что изменил: одного пользователя, один вход, удаление, настройки. Файловое хранилище
// (один процесс) расширения не реализует.
type DeltaBackend interface {
	Backend
	// UpsertUser создаёт или обновляет пользователя целиком (имя занято другим — ошибка).
	UpsertUser(u UserRecord) error
	// TouchLogin записывает время входа и, если passwordHash не пуст, новый хэш (обновление параметров хэша).
	// Остальные поля пользователя не затрагиваются: их могли изменить на другой реплике.
	TouchLogin(id string, at time.Time, passwordHash string) error
	// DeleteUser удаляет пользователя (отсутствие — не ошибка).
	DeleteUser(id string) error
	// SaveSettings записывает настройки безопасности.
	SaveSettings(SecuritySettings) error
}

// store — пользователи и настройки в памяти поверх Backend. Не потокобезопасно: синхронизацию делает Service.
type store struct {
	backend  Backend
	delta    DeltaBackend // не nil, если хранилище общее для нескольких процессов
	users    []UserRecord
	settings SecuritySettings
	gen      uint64 // растёт при каждом локальном изменении: устаревшее чтение не затирает новое
}

func loadStore(b Backend) (*store, error) {
	d, err := b.Load()
	if err != nil {
		return nil, err
	}
	st := &store{backend: b, users: d.Users, settings: d.Settings}
	st.delta, _ = b.(DeltaBackend)
	return st, nil
}

func (s *store) save() error {
	s.gen++
	return s.backend.Save(&Data{Users: s.users, Settings: s.settings})
}

// reload заменяет пользователей и настройки данными из хранилища.
func (s *store) reload() error {
	d, err := s.backend.Load()
	if err != nil {
		return err
	}
	s.users, s.settings = d.Users, d.Settings
	s.gen++
	return nil
}

// saveUser сохраняет одного изменённого пользователя (общее хранилище) или всё целиком (файл).
func (s *store) saveUser(u *UserRecord) error {
	if s.delta == nil {
		return s.save()
	}
	s.gen++
	return s.delta.UpsertUser(*u)
}

// saveLogin сохраняет вход: время и, если нужно, обновлённый хэш пароля.
func (s *store) saveLogin(u *UserRecord, newHash string) error {
	if s.delta == nil {
		return s.save()
	}
	s.gen++
	var at time.Time
	if u.LastLoginAt != nil {
		at = *u.LastLoginAt
	}
	return s.delta.TouchLogin(u.ID, at, newHash)
}

// saveRemoved сохраняет удаление пользователя.
func (s *store) saveRemoved(id string) error {
	if s.delta == nil {
		return s.save()
	}
	s.gen++
	return s.delta.DeleteUser(id)
}

// saveSettings сохраняет настройки безопасности.
func (s *store) saveSettings() error {
	if s.delta == nil {
		return s.save()
	}
	s.gen++
	return s.delta.SaveSettings(s.settings)
}

func (s *store) findByID(id string) *UserRecord {
	for i := range s.users {
		if s.users[i].ID == id {
			return &s.users[i]
		}
	}
	return nil
}

func (s *store) findByName(normalized string) *UserRecord {
	for i := range s.users {
		if normalizeUsername(s.users[i].Username) == normalized {
			return &s.users[i]
		}
	}
	return nil
}

func (s *store) remove(id string) {
	for i := range s.users {
		if s.users[i].ID == id {
			s.users = append(s.users[:i], s.users[i+1:]...)
			return
		}
	}
}

// enabledAdmins — число активных администраторов.
func (s *store) enabledAdmins() int {
	n := 0
	for _, u := range s.users {
		if u.Role == RoleAdmin && !u.Disabled {
			n++
		}
	}
	return n
}
