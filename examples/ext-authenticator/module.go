// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package example — пример внешнего модуля: регистрирует свой способ входа, не меняя ядро.
// Он же служит проверкой, что публичный контракт pkg/ext достаточен и собирается отдельно.
package example

import (
	"context"
	"errors"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

// Module подключается при сборке приложения: registry.Install(example.New(users)).
type Module struct{ users map[string]string }

// New создаёт модуль со статическим списком «логин → пароль» (только для примера!).
func New(users map[string]string) *Module { return &Module{users: users} }

func (m *Module) Name() string    { return "example-static" }
func (m *Module) APIVersion() int { return ext.APIVersion }

func (m *Module) Register(r *ext.Registry) error {
	return r.RegisterAuthenticator(&static{users: m.users})
}

type static struct{ users map[string]string }

func (s *static) ID() string     { return "static" }
func (s *static) Kind() ext.Kind { return ext.KindPassword }

func (s *static) Authenticate(_ context.Context, username, password string) (*ext.Identity, error) {
	if want, ok := s.users[username]; !ok || want != password {
		return nil, errors.New("invalid username or password")
	}
	return &ext.Identity{Subject: username, Username: username, Groups: []string{"example"}}, nil
}
