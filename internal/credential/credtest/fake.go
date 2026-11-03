// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credtest

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// Grant is what the fake identity provider hands out.
type Grant struct {
	Access  string
	Expiry  time.Time
	Refresh string
	User    string
}

type refreshState struct {
	user string
	used bool
}

// FakeIdP simulates an identity provider with passwords, device codes and refresh tokens. With
// StrictRotation (the default) a refresh token works once; presenting it again is invalid_grant, which is how
// providers with reuse detection behave and what makes races between replicas visible.
type FakeIdP struct {
	Now            func() time.Time
	AccessTTL      time.Duration
	StrictRotation bool

	mu       sync.Mutex
	users    map[string]string
	refresh  map[string]*refreshState
	devices  map[string]string // device code -> approved user ("" while pending)
	seq      int
	logins   int
	refreshs int
}

// NewFakeIdP returns an IdP that reads time from now (time.Now if nil).
func NewFakeIdP(now func() time.Time) *FakeIdP {
	if now == nil {
		now = time.Now
	}
	return &FakeIdP{Now: now, AccessTTL: 5 * time.Minute, StrictRotation: true,
		users: map[string]string{}, refresh: map[string]*refreshState{}, devices: map[string]string{}}
}

// AddUser registers a user.
func (i *FakeIdP) AddUser(user, password string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.users[user] = password
}

func (i *FakeIdP) grant(user string) Grant {
	i.seq++
	n := strconv.Itoa(i.seq)
	rt := "rt-" + n
	i.refresh[rt] = &refreshState{user: user}
	return Grant{Access: "at-" + n, Expiry: i.Now().Add(i.AccessTTL), Refresh: rt, User: user}
}

// Login checks a password. A wrong one is credential.ErrAnswerRejected.
func (i *FakeIdP) Login(user, password string) (Grant, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.logins++
	if p, ok := i.users[user]; !ok || p != password {
		return Grant{}, credential.ErrAnswerRejected
	}
	return i.grant(user), nil
}

// Refresh exchanges a refresh token for a new grant. Unknown, revoked and (when strict) already used tokens are
// credential.ErrInvalidGrant.
func (i *FakeIdP) Refresh(rt string) (Grant, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.refreshs++
	st, ok := i.refresh[rt]
	if !ok || (st.used && i.StrictRotation) {
		return Grant{}, credential.ErrInvalidGrant
	}
	st.used = true
	return i.grant(st.user), nil
}

// RevokeRefresh invalidates a refresh token.
func (i *FakeIdP) RevokeRefresh(rt string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.refresh, rt)
}

// NewDeviceCode starts a device authorization.
func (i *FakeIdP) NewDeviceCode() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.seq++
	code := "dc-" + strconv.Itoa(i.seq)
	i.devices[code] = ""
	return code
}

// ApproveDevice approves a device code for a user (the person typed the code in a browser).
func (i *FakeIdP) ApproveDevice(code, user string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.devices[code] = user
}

// PollDevice returns the grant once the device code is approved; ok is false while pending.
func (i *FakeIdP) PollDevice(code string) (g Grant, ok bool, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	user, found := i.devices[code]
	if !found {
		return Grant{}, false, credential.ErrChallengeExpired
	}
	if user == "" {
		return Grant{}, false, nil
	}
	delete(i.devices, code)
	return i.grant(user), true, nil
}

// Calls returns how many logins and refreshes the IdP has seen.
func (i *FakeIdP) Calls() (logins, refreshes int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.logins, i.refreshs
}

// FakeProvider is a Provider whose behavior is programmed by the test. The canned constructors
// (NewPasswordProvider, NewDeviceCodeProvider) cover the usual flows.
type FakeProvider struct {
	KindValue  credential.Kind
	OnBegin    func(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error)
	OnContinue func(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error)

	mu        sync.Mutex
	begins    int
	conts     int
	lastReq   credential.Request
	inFlight  int
	maxFlight int
}

var _ credential.Provider = (*FakeProvider)(nil)

// Kind implements credential.Provider.
func (f *FakeProvider) Kind() credential.Kind { return f.KindValue }

// Calls returns the number of Begin and Continue calls so far.
func (f *FakeProvider) Calls() (begins, continues int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.begins, f.conts
}

// LastRequest returns the request of the latest call.
func (f *FakeProvider) LastRequest() credential.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

// MaxConcurrent returns the highest number of simultaneous calls seen.
func (f *FakeProvider) MaxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxFlight
}

func (f *FakeProvider) enter(req credential.Request, begin bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if begin {
		f.begins++
	} else {
		f.conts++
	}
	f.lastReq = req
	f.inFlight++
	f.maxFlight = max(f.maxFlight, f.inFlight)
}

func (f *FakeProvider) leave() {
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
}

// Begin implements credential.Provider.
func (f *FakeProvider) Begin(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	f.enter(req, true)
	defer f.leave()
	if f.OnBegin == nil {
		return credential.Result{}, credential.ErrUnsupported
	}
	return f.OnBegin(ctx, env, req)
}

// Continue implements credential.Provider.
func (f *FakeProvider) Continue(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	f.enter(req, false)
	defer f.leave()
	if f.OnContinue == nil {
		return credential.Result{}, credential.ErrUnsupported
	}
	return f.OnContinue(ctx, env, req)
}

func tokenResult(g Grant) credential.Result {
	return credential.Result{
		Token: &credential.Token{Value: g.Access, Expiry: g.Expiry, Principal: g.User},
		Save:  []byte(g.Refresh),
	}
}

// NewPasswordProvider returns a provider that signs in with username and password against idp and keeps the
// refresh token (never the password). With a saved refresh token Begin refreshes silently; if the IdP refuses
// it, Begin returns a credentials challenge and erases the saved data, as the contract asks.
func NewPasswordProvider(kind credential.Kind, idp *FakeIdP) *FakeProvider {
	askCreds := func() credential.Result {
		return credential.Result{
			Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials,
				Fields: []credential.Field{{Name: "username"}, {Name: "password", Secret: true}}},
		}
	}
	return &FakeProvider{
		KindValue: kind,
		OnBegin: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			if len(req.Saved) == 0 {
				return askCreds(), nil
			}
			g, err := idp.Refresh(string(req.Saved))
			if err != nil {
				r := askCreds()
				r.Save = []byte{}
				return r, nil
			}
			return tokenResult(g), nil
		},
		OnContinue: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			g, err := idp.Login(req.Answer["username"], req.Answer["password"])
			if err != nil {
				return credential.Result{}, err
			}
			return tokenResult(g), nil
		},
	}
}

// NewDeviceCodeProvider returns a provider with the device-code flow: Begin hands out a code, Continue
// (Poll) reports "still waiting" until the test approves the code with idp.ApproveDevice.
func NewDeviceCodeProvider(kind credential.Kind, idp *FakeIdP) *FakeProvider {
	return &FakeProvider{
		KindValue: kind,
		OnBegin: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			if len(req.Saved) > 0 {
				if g, err := idp.Refresh(string(req.Saved)); err == nil {
					return tokenResult(g), nil
				}
			}
			code := idp.NewDeviceCode()
			return credential.Result{
				Challenge: &credential.Challenge{Kind: credential.ChallengeDeviceCode, URL: "https://idp.example/device",
					UserCode: "ABCD-" + code, Poll: true, IntervalSec: 5},
				Pending: []byte(code),
				Save:    []byte{},
			}, nil
		},
		OnContinue: func(_ context.Context, _ credential.Env, req credential.Request) (credential.Result, error) {
			g, ok, err := idp.PollDevice(string(req.Pending))
			if err != nil {
				return credential.Result{}, err
			}
			if !ok {
				c := req.Challenge
				c.ID, c.ExpiresAt = "", time.Time{}
				return credential.Result{Challenge: &c, Pending: req.Pending}, nil
			}
			return tokenResult(g), nil
		},
	}
}

// LeakyError is an error whose text contains a secret, to test that the Service scrubs provider errors.
func LeakyError(secret string) error {
	return &leakyError{"upstream said: invalid request for " + secret}
}

type leakyError struct{ msg string }

func (e *leakyError) Error() string { return e.msg }
