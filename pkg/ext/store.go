// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package ext

import "time"

// Store — всё, что приложение хранит постоянно (ADR-0007). Реализации: файлы (десктоп),
// PostgreSQL с шифрованием секретов (сервер, ADR-0008).
type Store interface {
	Users() UserBackend
	Audit() AuditBackend
	ArgoTokens() TokenStore
}

// UserRecord — пользователь в хранилище. Role — идентификатор роли ядра ("admin", "viewer", …).
type UserRecord struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	Role               string     `json:"role"`
	PasswordHash       string     `json:"passwordHash"`
	Disabled           bool       `json:"disabled"`
	MustChangePassword bool       `json:"mustChangePassword"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	LastLoginAt        *time.Time `json:"lastLoginAt,omitempty"`
	ExternalIssuer     string     `json:"externalIssuer,omitempty"`
	ExternalSubject    string     `json:"externalSubject,omitempty"`
}

// SecuritySettings — настройки безопасности, общие для всех пользователей.
// Указатель различает «не задано» (значение по умолчанию) и явный 0 («выключено»).
type SecuritySettings struct {
	IdleTimeoutMinutes *int `json:"idleTimeoutMinutes,omitempty"`
}

// UserData — пользователи и настройки целиком.
type UserData struct {
	Users    []UserRecord     `json:"users"`
	Settings SecuritySettings `json:"settings"`
}

// UserBackend — постоянное хранилище пользователей.
type UserBackend interface {
	// Load читает данные. Отсутствие данных — пустой UserData (первый запуск). Повреждённые данные
	// или созданные более новой версией — ошибка: молча считать их пустыми нельзя, иначе любой смог бы
	// пройти первичную настройку и создать администратора.
	Load() (*UserData, error)
	// Save атомарно сохраняет данные целиком.
	Save(*UserData) error
}

// AuditBackend — хранилище записей журнала аудита.
type AuditBackend interface {
	// Append добавляет запись: одна JSON-строка без перевода строки в конце.
	Append(line []byte) error
	// ScanNewestFirst отдаёт записи (JSON-строки) от новых к старым, пока fn возвращает true.
	// Отсутствие журнала — не ошибка (пустой обход).
	ScanNewestFirst(fn func(line []byte) bool) error
}

// TokenStore — хранилище токенов Argo CD API по идентификатору кластера. Токен приходит проверенным.
type TokenStore interface {
	// Put сохраняет токен, заменяя прежний.
	Put(clusterID, token string) error
	// Get возвращает токен; если его нет — ошибку, для которой ядро проверяет errors.Is(err, ErrNotFound).
	Get(clusterID string) (string, error)
	// Delete удаляет токен; отсутствие токена — не ошибка.
	Delete(clusterID string) error
}
