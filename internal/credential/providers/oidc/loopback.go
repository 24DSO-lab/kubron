// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// loopbackTTL is how long the listener of one sign-in waits for the browser.
const loopbackTTL = 10 * time.Minute

// The authorization-code flow of the desktop (RFC 8252 7.3): a listener on 127.0.0.1 receives the redirect of
// the browser. This is the one place where the provider keeps state outside of Pending: the listening socket
// and the answer it received live in the memory of the process, in the registry of the Provider, under a
// random id that is kept in Pending. The state (the PKCE verifier, nonce, state) is still in Pending only.
//
// A listener serves one request: it answers only a GET of the redirect path with the right state, takes the
// first such answer, and is closed when the answer has been collected, cancelled, or after loopbackTTL.

type loopbackRegistry struct {
	mu sync.Mutex
	m  map[string]*loopback
}

type loopback struct {
	reg   *loopbackRegistry
	id    string
	state string
	hosts []string // accepted Host header names
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

// release removes and closes a listener; it is safe to call twice.
func (r *loopbackRegistry) release(id string) {
	r.mu.Lock()
	l := r.m[id]
	delete(r.m, id)
	r.mu.Unlock()
	if l != nil {
		l.timer.Stop()
		// The socket is closed here, synchronously: http.Server.Close only closes listeners that Serve has already
		// registered, and the goroutine that calls Serve may not have run yet. The port must refuse connections as soon as
		// the request is cancelled.
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

func (l *loopback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only the names the redirect URL uses: a page that tricks the browser into talking to this port under
	// another name (DNS rebinding) gets nothing.
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	okHost := false
	for _, h := range l.hosts {
		if h == host {
			okHost = true
		}
	}
	switch {
	case !okHost:
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

// startLoopback listens on the first free address of the kubeconfig (127.0.0.1:8000 and :18000 by default),
// opens the browser and returns the challenge to poll.
func (s *session) startLoopback(ctx context.Context) (credential.Result, error) {
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	var ln net.Listener
	for _, addr := range s.cfg.listen {
		if ln, err = net.Listen("tcp", addr); err == nil {
			break
		}
	}
	if ln == nil {
		return credential.Result{}, errListen
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	p, err := s.newAuthRequest(flowLoopback, s.redirectURI(port))
	if err == nil {
		p.LoopID, err = randString(s.rand, 16)
	}
	var authURL string
	if err == nil {
		authURL, err = s.authURL(d, p)
	}
	if err != nil {
		_ = ln.Close()
		return credential.Result{}, err
	}
	reg := &s.p.loops
	l := &loopback{reg: reg, id: p.LoopID, state: p.State, ln: ln, hosts: []string{s.cfg.redirectHost, "localhost", "127.0.0.1", "::1"}}
	l.srv = &http.Server{Handler: l, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	l.timer = time.AfterFunc(loopbackTTL, func() { reg.release(l.id) })
	reg.add(l)
	go func() { _ = l.srv.Serve(ln) }()

	if !s.cfg.skipBrowser && s.env.OpenURL != nil {
		// Failing to open a browser is not fatal: the challenge carries the address for the user.
		_ = s.env.OpenURL(authURL)
	}
	return credential.Result{
		Challenge: &credential.Challenge{Kind: credential.ChallengeBrowserLoopback, URL: authURL, Poll: true, IntervalSec: 1},
		Pending:   p.encode(),
	}, nil
}

// continueLoopback is a poll: nothing new while the browser has not come back, the exchange of the code when
// it has. A process that has forgotten the listener (it was restarted) cannot finish the request.
func (s *session) continueLoopback(ctx context.Context, req credential.Request, sv *saved) (credential.Result, error) {
	p, err := decodePending(req.Pending, flowLoopback)
	if err != nil {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	l := s.p.loops.get(p.LoopID)
	if l == nil {
		return credential.Result{}, credential.ErrChallengeExpired
	}
	q := l.result()
	if q == nil {
		return waiting(req.Challenge, req.Pending), nil
	}
	s.p.loops.release(p.LoopID) // the answer is taken exactly once
	d, err := s.discover(ctx)
	if err != nil {
		return credential.Result{}, err
	}
	code, err := s.checkResponse(d, q, p)
	if err != nil {
		return credential.Result{}, err
	}
	return s.exchangeCode(ctx, d, req, sv, code, p)
}

// Cancel releases the listener of a sign-in that is abandoned. The Broker may call it with the Pending of the
// request it removes; it is optional, the listener also closes itself after loopbackTTL.
func (p *Provider) Cancel(pend []byte) {
	if lp, err := decodePending(pend, flowLoopback); err == nil {
		p.loops.release(lp.LoopID)
	}
}

// Close releases every open listener.
func (p *Provider) Close() { p.loops.closeAll() }
