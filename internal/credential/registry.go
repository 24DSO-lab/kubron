// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"errors"
	"os/exec"
	"sync"
)

// ErrProviderRegistered is returned when a second provider is registered for the same kind.
var ErrProviderRegistered = errors.New("a provider for this sign-in kind is already registered")

// Registry holds the providers and chooses one for a Descriptor.
//
// Routing (ADR-0018): on the desktop, a descriptor with an exec block whose binary is found in PATH goes to
// the KindExec provider (the plugin runs as the user's kubectl would run it); otherwise the provider of
// the descriptor's Kind is used; if there is no native provider but an exec block exists, the KindExec
// provider is used (the plugin on the desktop, the manual token in the web). The KindExec provider is
// registered per mode by the application: execplugin on the desktop, manual on the web.
type Registry struct {
	desktop  bool
	lookPath func(string) (string, error)

	mu        sync.RWMutex
	providers map[Kind]Provider
}

// NewRegistry returns an empty registry for the desktop or the web mode.
func NewRegistry(desktop bool) *Registry {
	return &Registry{desktop: desktop, lookPath: exec.LookPath, providers: map[Kind]Provider{}}
}

// SetLookPath replaces the PATH lookup (tests).
func (r *Registry) SetLookPath(fn func(string) (string, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookPath = fn
}

// Register adds a provider. A kind can have only one provider.
func (r *Registry) Register(p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[p.Kind()]; ok {
		return ErrProviderRegistered
	}
	r.providers[p.Kind()] = p
	return nil
}

// Resolve returns the provider for d, or ErrUnsupported. KindStatic never has a provider.
func (r *Registry) Resolve(d Descriptor) (Provider, error) {
	if d.Kind == KindStatic {
		return nil, ErrUnsupported
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	execP := r.providers[KindExec]
	if r.desktop && d.Exec != nil && execP != nil && d.Exec.Command != "" {
		if _, err := r.lookPath(d.Exec.Command); err == nil {
			return execP, nil
		}
	}
	if p := r.providers[d.Kind]; p != nil && d.Kind != KindExec {
		return p, nil
	}
	if d.Exec != nil && execP != nil {
		return execP, nil
	}
	return nil, ErrUnsupported
}

// all returns every registered provider once.
func (r *Registry) all() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	return out
}
