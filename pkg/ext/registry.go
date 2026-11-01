// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"errors"
	"fmt"
	"sync"
)

// ErrNotFound — «нет такого значения» для хранилищ (токена нет и т. п.).
var ErrNotFound = errors.New("ext: not found")

// Module — подключаемый модуль (например, SSO). Собирается в отдельном Go-модуле
// и подключается при сборке приложения: registry.Install(mymodule.New()).
type Module interface {
	// Name — имя модуля для сообщений об ошибках и журнала.
	Name() string
	// APIVersion — версия контракта ext, под которую собран модуль (возвращайте ext.APIVersion).
	APIVersion() int
	// Register добавляет в реестр реализации модуля.
	Register(r *Registry) error
}

// Registry — реестр реализаций, добавленных модулями. Потокобезопасен.
type Registry struct {
	mu      sync.RWMutex
	auths   map[string]Authenticator
	order   []string
	modules []string
}

func NewRegistry() *Registry {
	return &Registry{auths: map[string]Authenticator{}}
}

// Install подключает модуль: проверяет версию контракта и вызывает его Register.
func (r *Registry) Install(m Module) error {
	if m == nil {
		return errors.New("ext: empty module")
	}
	if v := m.APIVersion(); v != APIVersion {
		return fmt.Errorf("ext: module %q was built for API version %d, the core supports %d", m.Name(), v, APIVersion)
	}
	if err := m.Register(r); err != nil {
		return fmt.Errorf("ext: module %q: %w", m.Name(), err)
	}
	r.mu.Lock()
	r.modules = append(r.modules, m.Name())
	r.mu.Unlock()
	return nil
}

// Modules возвращает имена установленных модулей в порядке установки.
func (r *Registry) Modules() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.modules...)
}

// RegisterAuthenticator добавляет способ входа. ID должен быть уникальным, а реализация —
// соответствовать Kind(): PasswordAuthenticator для KindPassword, RedirectAuthenticator для KindRedirect.
func (r *Registry) RegisterAuthenticator(a Authenticator) error {
	if a == nil || a.ID() == "" {
		return errors.New("ext: authenticator has no ID")
	}
	switch a.Kind() {
	case KindPassword:
		if _, ok := a.(PasswordAuthenticator); !ok {
			return fmt.Errorf("ext: authenticator %q declares kind=password but does not implement PasswordAuthenticator", a.ID())
		}
	case KindRedirect:
		if _, ok := a.(RedirectAuthenticator); !ok {
			return fmt.Errorf("ext: authenticator %q declares kind=redirect but does not implement RedirectAuthenticator", a.ID())
		}
	default:
		return fmt.Errorf("ext: authenticator %q: unknown kind %q", a.ID(), a.Kind())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.auths[a.ID()]; dup {
		return fmt.Errorf("ext: authenticator %q is already registered", a.ID())
	}
	r.auths[a.ID()] = a
	r.order = append(r.order, a.ID())
	return nil
}

// Authenticator возвращает способ входа по ID.
func (r *Registry) Authenticator(id string) (Authenticator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.auths[id]
	return a, ok
}

// Authenticators возвращает все способы входа в порядке регистрации.
func (r *Registry) Authenticators() []Authenticator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Authenticator, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.auths[id])
	}
	return out
}
