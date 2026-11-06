// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// The authorization-code flow of the desktop (RFC 8252 7.3): a listener on 127.0.0.1 receives the redirect of the
// browser. Like the oidc provider, this is the one place where the provider keeps something outside of Pending:
// the listening socket and the answer it received live in the memory of this process, in the registry of the
// Provider, under a random id that is kept in Pending. The secrets of the request (PKCE verifier, nonce, state)
// are still in Pending only. A listener serves one request: it answers only a GET of "/" for the literal host
// 127.0.0.1 with the right state, takes the first such answer, and is closed when the answer has been collected,
// when the request is cancelled, after loopbackTTL, or when the Broker stops.

const (
	flowLoopback = "loopback"

	// loopbackTTL is how long the listener of one sign-in waits for the browser.
	loopbackTTL = 10 * time.Minute
)

type loopbackRegistry struct {
	mu sync.Mutex
	m  map[string]*loopback
}

type loopback struct {
	reg   *loopbackRegistry
	id    string
	state string
	srv   *http.Server
	ln    net.Listener // closed by release itself: Server.Close alone leaves a listener that Serve has not tracked yet open
	timer *time.Timer

	mu  sync.Mutex
	got url.Values // the query of the redirect, nil until it arrives
}

func (r *loopbackRegistry) add(l *loopback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]*loopback{}
	}
	r.m[l.id] = l
}

func (r *loopbackRegistry) get(id string) *loopback {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[id]
}

func (r *loopbackRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m)
}

// release removes and closes a listener; it is safe to call twice. The socket is closed here, synchronously, so
// that the port refuses connections as soon as the request is gone.
func (r *loopbackRegistry) release(id string) {
	r.mu.Lock()
	l := r.m[id]
	delete(r.m, id)
	r.mu.Unlock()
	if l != nil {
		l.timer.Stop()
		_ = l.ln.Close()
		_ = l.srv.Close()
	}
}

func (r *loopbackRegistry) closeAll() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.m))
	for id := range r.m {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.release(id)
	}
}

func (l *loopback) result() url.Values {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.got
}

// ServeHTTP receives the redirect. Only the literal name 127.0.0.1 is accepted: a page that tricks the browser
// into talking to this port under another name (DNS rebinding) gets nothing. The answers never echo the request.
func (l *loopback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	switch {
	case host != "127.0.0.1":
		http.Error(w, "unexpected host", http.StatusMisdirectedRequest)
		return
	case r.Method != http.MethodGet || (r.URL.Path != "/" && r.URL.Path != ""):
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(l.state)) != 1 {
		http.Error(w, "the sign-in response does not match the request", http.StatusBadRequest)
		return
	}
	l.mu.Lock()
	if l.got == nil {
		l.got = q
	}
	l.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write([]byte("Sign-in complete. You can close this window.\n"))
}

func (p *Provider) listenFunc() func(network, address string) (net.Listener, error) {
	if p.listen != nil {
		return p.listen
	}
	return net.Listen
}

func (p *Provider) loopTTL() time.Duration {
	if p.ttl > 0 {
		return p.ttl
	}
	return loopbackTTL
}

// startLoopback listens on a free port of 127.0.0.1 (a client of the "Desktop app" type accepts any port),
// opens the browser and returns the challenge to poll. If the port cannot be opened it returns ok == false and
// the caller falls back to the paste variant.
func (p *Provider) startLoopback(env credential.Env) (res credential.Result, ok bool, err error) {
	ln, lerr := p.listenFunc()("tcp", "127.0.0.1:0")
	if lerr != nil {
		return credential.Result{}, false, nil
	}
	pd, err := p.newAuthRequest(flowLoopback, "http://"+ln.Addr().String())
	if err == nil {
		pd.LoopID, err = p.randString(16)
	}
	if err != nil {
		_ = ln.Close()
		return credential.Result{}, true, err
	}
	authURL := p.authURL(pd)
	reg := &p.loops
	l := &loopback{reg: reg, id: pd.LoopID, state: pd.State, ln: ln}
	l.srv = &http.Server{Handler: l, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	l.timer = time.AfterFunc(p.loopTTL(), func() { reg.release(l.id) })
	reg.add(l)
	go func() { _ = l.srv.Serve(ln) }()
	if env.OpenURL != nil {
		_ = env.OpenURL(authURL) // not fatal: the challenge carries the address for the user
	}
	return credential.Result{
		Challenge: &credential.Challenge{Kind: credential.ChallengeBrowserLoopback, URL: authURL, Poll: true, IntervalSec: 1},
		Pending:   pd.encode(),
	}, true, nil
}

// continueLoopback is a poll: nothing new while the browser has not come back, the exchange of the code when it
// has. A process that has forgotten the listener (it was restarted) cannot finish the request.
func (p *Provider) continueLoopback(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	pd, err := decodePending(req.Pending, flowLoopback)
	if err != nil {
		return credential.Result{}, err
	}
	l := p.loops.get(pd.LoopID)
	if l == nil {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	q := l.result()
	if q == nil {
		c := req.Challenge
		c.ID, c.ExpiresAt = "", time.Time{}
		return credential.Result{Challenge: &c, Pending: req.Pending}, nil
	}
	p.loops.release(pd.LoopID) // the answer is taken exactly once
	return p.handleResponse(ctx, env, pd, q)
}

// Cancel releases the listener of a sign-in that is abandoned (credential.Canceller). It is idempotent.
func (p *Provider) Cancel(pend []byte) {
	if pd, err := decodePending(pend, flowLoopback); err == nil {
		p.loops.release(pd.LoopID)
	}
}

// Close releases every open listener (credential.Closer).
func (p *Provider) Close() { p.loops.closeAll() }

var (
	_ credential.Canceller = (*Provider)(nil)
	_ credential.Closer    = (*Provider)(nil)
)
