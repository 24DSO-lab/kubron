// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"sync"
	"time"
)

// Canceller is an optional interface of a Provider. A provider that keeps something outside of Pending (the
// loopback listener of the desktop) implements it; the Broker calls Cancel with the last Pending of a request when
// the request is cancelled, expires, is swept, completes or is revoked. Cancel must be idempotent and quick.
type Canceller interface {
	Cancel(pending []byte)
}

// Closer is an optional interface of a Provider: Close releases everything the provider holds, and the Broker calls
// it when it stops (Service.Close).
type Closer interface {
	Close()
}

// liveSet remembers, in the memory of this process, the pending requests of providers that implement Canceller. It
// exists because what a Canceller holds (a listening socket) lives in this process only: a replica that does not
// know the request cannot release it, and one that does know it is this one.
type liveSet struct {
	mu sync.Mutex
	m  map[string]liveRequest
}

type liveRequest struct {
	p       Canceller
	pending []byte
	key     Key
	expires time.Time
}

func (l *liveSet) track(id string, r liveRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]liveRequest{}
	}
	r.pending = append([]byte(nil), r.pending...)
	if old, ok := l.m[id]; ok {
		clear(old.pending)
	}
	l.m[id] = r
}

// take removes and returns the request.
func (l *liveSet) take(id string) (liveRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.m[id]
	delete(l.m, id)
	return r, ok
}

// takeIf removes and returns the requests for which match is true.
func (l *liveSet) takeIf(match func(liveRequest) bool) []liveRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []liveRequest
	for id, r := range l.m {
		if match(r) {
			out = append(out, r)
			delete(l.m, id)
		}
	}
	return out
}

func (r liveRequest) cancel() {
	r.p.Cancel(r.pending)
	clear(r.pending)
}

// track remembers a pending request whose provider can be cancelled.
func (s *Service) track(id string, sub Subject, d Descriptor, pending []byte, exp time.Time) {
	p, err := s.reg.Resolve(d)
	if err != nil {
		return
	}
	if c, ok := p.(Canceller); ok {
		s.live.track(id, liveRequest{p: c, pending: pending, key: sub.key(), expires: exp})
	}
}

// release tells the provider that a request is gone (cancelled, expired, completed, replaced).
func (s *Service) dropLive(id string) {
	if r, ok := s.live.take(id); ok {
		r.cancel()
	}
}

// Close implements io.Closer for the Service: it cancels every pending request this process holds and closes the
// providers that have something to release (the loopback listener of the desktop). The Service must not be used
// afterwards. Close is safe to call twice.
func (s *Service) Close() error {
	for _, r := range s.live.takeIf(func(liveRequest) bool { return true }) {
		r.cancel()
	}
	for _, p := range s.reg.all() {
		if c, ok := p.(Closer); ok {
			c.Close()
		}
	}
	return nil
}
