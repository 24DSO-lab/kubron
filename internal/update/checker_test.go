// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	mu    sync.Mutex
	calls int
	rel   *Release
	err   error
}

func (f *fakeSource) Latest(context.Context) (*Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.rel, f.err
}

func (f *fakeSource) set(rel *Release, err error) {
	f.mu.Lock()
	f.rel, f.err = rel, err
	f.mu.Unlock()
}

func (f *fakeSource) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestChecker(current string, src ReleaseSource, mutate func(*Config)) (*Checker, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	cfg := Config{
		Current:   current,
		Install:   Install{Target: TargetWeb, Method: MethodHelm, OS: "linux", Arch: "amd64"},
		Source:    src,
		AutoCheck: true,
		Now:       clk.Now,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewChecker(cfg), clk
}

func TestInfoBeforeAnyCheckHasNoNetwork(t *testing.T) {
	src := &fakeSource{}
	c, _ := newTestChecker("1.2.0", src, nil)
	in := c.Info()
	if in.Current != "1.2.0" || in.Available || in.Latest != "" || in.CheckedAt != "" {
		t.Errorf("info = %+v", in)
	}
	if !in.AutoCheck || in.Locked {
		t.Errorf("autoCheck=%v locked=%v", in.AutoCheck, in.Locked)
	}
	if src.count() != 0 {
		t.Error("Info must not use the network")
	}
}

func TestCheckNowReportsNewerVersionWithInstructions(t *testing.T) {
	src := &fakeSource{rel: testRelease("1.3.0")}
	c, _ := newTestChecker("1.2.0", src, nil)

	in := c.CheckNow(context.Background())
	if !in.Available || in.Latest != "1.3.0" {
		t.Fatalf("info = %+v", in)
	}
	if in.ReleaseURL == "" || in.CheckedAt != "2026-11-08T12:00:00Z" || in.Error != "" {
		t.Errorf("info = %+v", in)
	}
	if len(in.Commands) == 0 || in.Commands[0].ID != CmdHelmUpgrade {
		t.Errorf("a Helm install must come with the helm command: %+v", in.Commands)
	}
}

func TestSameOrOlderVersionIsNotAnUpdate(t *testing.T) {
	for _, current := range []string{"1.3.0", "1.4.0", "2.0.0-rc.1"} {
		src := &fakeSource{rel: testRelease("1.3.0")}
		c, _ := newTestChecker(current, src, nil)
		in := c.CheckNow(context.Background())
		if in.Available || len(in.Commands) != 0 || in.Download != nil {
			t.Errorf("current %s: info = %+v", current, in)
		}
		if in.Latest != "1.3.0" {
			t.Errorf("current %s: the latest known version should still be reported, got %q", current, in.Latest)
		}
	}
}

func TestCheckNowRespectsMinForceGap(t *testing.T) {
	src := &fakeSource{rel: testRelease("1.3.0")}
	c, clk := newTestChecker("1.2.0", src, nil)

	c.CheckNow(context.Background())
	c.CheckNow(context.Background())
	clk.Advance(DefaultMinForceGap - time.Second)
	c.CheckNow(context.Background())
	if src.count() != 1 {
		t.Fatalf("calls = %d: repeated clicks inside the gap must serve the cache", src.count())
	}
	clk.Advance(2 * time.Second)
	c.CheckNow(context.Background())
	if src.count() != 2 {
		t.Errorf("calls = %d, want a new request after the gap", src.count())
	}
}

func TestFailureKeepsLastKnownReleaseAndReportsCode(t *testing.T) {
	src := &fakeSource{rel: testRelease("1.3.0")}
	c, clk := newTestChecker("1.2.0", src, nil)
	c.CheckNow(context.Background())

	clk.Advance(time.Hour)
	src.set(nil, ErrRateLimited)
	in := c.CheckNow(context.Background())
	if in.Error != string(ErrRateLimited) {
		t.Errorf("error = %q", in.Error)
	}
	if !in.Available || in.Latest != "1.3.0" {
		t.Errorf("a failed check must not forget the last known release: %+v", in)
	}

	clk.Advance(time.Hour)
	src.set(testRelease("1.3.0"), nil)
	if in := c.CheckNow(context.Background()); in.Error != "" {
		t.Errorf("a successful check clears the error: %+v", in)
	}
}

func TestUnknownSourceErrorBecomesUnexpected(t *testing.T) {
	src := &fakeSource{err: context.DeadlineExceeded}
	c, _ := newTestChecker("1.2.0", src, nil)
	if in := c.CheckNow(context.Background()); in.Error != string(ErrUnexpected) {
		t.Errorf("error = %q: only known codes may reach the interface", in.Error)
	}
}

func TestLockedNeverUsesTheNetwork(t *testing.T) {
	src := &fakeSource{rel: testRelease("1.3.0")}
	c, _ := newTestChecker("1.2.0", src, func(cfg *Config) { cfg.Locked = true })

	in := c.CheckNow(context.Background())
	c.Run(context.Background()) // при Locked возвращается сразу
	if src.count() != 0 {
		t.Errorf("calls = %d: KUBRON_UPDATE_CHECK=off forbids outgoing requests", src.count())
	}
	if !in.Locked || in.AutoCheck || in.Available {
		t.Errorf("info = %+v", in)
	}
	c.SetAutoCheck(true)
	if c.AutoCheck() {
		t.Error("SetAutoCheck must not lift the administrator's lock")
	}
}

func TestDevBuildNeverChecks(t *testing.T) {
	for _, current := range []string{DevVersion, "", "nightly"} {
		src := &fakeSource{rel: testRelease("1.3.0")}
		c, _ := newTestChecker(current, src, nil)
		in := c.CheckNow(context.Background())
		if src.count() != 0 || in.Available || in.AutoCheck {
			t.Errorf("current %q: calls=%d info=%+v", current, src.count(), in)
		}
		if in.Current != current {
			t.Errorf("current = %q", in.Current)
		}
	}
}

func TestSetAutoCheckDoesNotBlockManualCheck(t *testing.T) {
	src := &fakeSource{rel: testRelease("1.3.0")}
	c, _ := newTestChecker("1.2.0", src, nil)
	c.SetAutoCheck(false)
	if c.Info().AutoCheck {
		t.Error("auto check should be off")
	}
	if in := c.CheckNow(context.Background()); !in.Available || in.AutoCheck {
		t.Errorf("a manual check works with the background one off: %+v", in)
	}
}

func TestVersion(t *testing.T) {
	c, _ := newTestChecker("1.2.0", &fakeSource{}, nil)
	if v := c.Version(); v.Version != "1.2.0" || v.Target != TargetWeb {
		t.Errorf("version = %+v", v)
	}
}

func TestRunChecksInBackgroundAndStopsOnCancel(t *testing.T) {
	src := &fakeSource{rel: testRelease("1.3.0")}
	c, _ := newTestChecker("1.2.0", src, func(cfg *Config) {
		cfg.StartDelay = 5 * time.Millisecond
		cfg.Interval = 20 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()

	waitFor(t, func() bool { return c.Info().Available })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after the context was cancelled")
	}
}

func TestRunSkipsWhileAutoCheckIsOff(t *testing.T) {
	src := &fakeSource{rel: testRelease("1.3.0")}
	c, _ := newTestChecker("1.2.0", src, func(cfg *Config) {
		cfg.AutoCheck = false
		cfg.StartDelay = 5 * time.Millisecond
		cfg.Interval = 10 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	time.Sleep(80 * time.Millisecond)
	if src.count() != 0 {
		t.Fatalf("calls = %d with the background check off", src.count())
	}
	c.SetAutoCheck(true) // цикл жив и подхватывает включение
	waitFor(t, func() bool { return src.count() > 0 })
}

func TestNextDelay(t *testing.T) {
	src := &fakeSource{rel: testRelease("1.3.0")}
	c, _ := newTestChecker("1.2.0", src, nil)

	lo, hi := time.Duration(float64(DefaultInterval)*0.9), time.Duration(float64(DefaultInterval)*1.1)
	for range 200 {
		if d := c.nextDelay(); d < lo || d > hi {
			t.Fatalf("delay %v is outside ±10%% of %v", d, DefaultInterval)
		}
	}
	src.set(nil, ErrNetwork)
	c.CheckNow(context.Background())
	if d := c.nextDelay(); d != DefaultRetryInterval {
		t.Errorf("after a failure the retry interval applies, got %v", d)
	}
}

func TestConcurrentChecksMakeOneRequest(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	src := sourceFunc(func(context.Context) (*Release, error) {
		calls.Add(1)
		<-release
		return testRelease("1.3.0"), nil
	})
	c, _ := newTestChecker("1.2.0", src, nil)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); c.CheckNow(context.Background()) }()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("calls = %d: simultaneous clicks must not multiply requests to GitHub", calls.Load())
	}
}

type sourceFunc func(context.Context) (*Release, error)

func (f sourceFunc) Latest(ctx context.Context) (*Release, error) { return f(ctx) }

func TestPolicyFromEnv(t *testing.T) {
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == EnvUpdateCheck {
				return v
			}
			return ""
		}
	}
	for v, locked := range map[string]bool{"": false, "on": false, "TRUE": false, "1": false, " yes ": false,
		"off": true, "Off": true, "false": true, "0": true, "no": true} {
		got, err := PolicyFromEnv(get(v))
		if err != nil || got != locked {
			t.Errorf("%q: locked=%v err=%v, want locked=%v", v, got, err, locked)
		}
	}
	for _, bad := range []string{"of", "disable", "2", "maybe"} {
		if _, err := PolicyFromEnv(get(bad)); err == nil {
			t.Errorf("%q: a typo in the policy must be an error, not a silent default", bad)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not reached in time")
}
