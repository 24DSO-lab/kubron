// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pfproxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestNewIDIsValidAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := NewID()
		if !ValidID(id) {
			t.Fatalf("invalid id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
	for _, bad := range []string{"", "ABC", strings.Repeat("a", 25), strings.Repeat("a", 27), strings.Repeat("1", 26)} {
		if ValidID(bad) {
			t.Errorf("%q must not be a valid id", bad)
		}
	}
}

func TestTicketLifecycle(t *testing.T) {
	s := NewSigner([]byte("secret"))
	c := &clock{t: time.Unix(1_000_000, 0)}
	s.now = c.now
	id := NewID()

	tok := s.IssueTicket(id, "sess-1", 30*time.Second)
	gid, owner, err := s.RedeemTicket(tok)
	if err != nil || gid != id || owner != "sess-1" {
		t.Fatalf("redeem: %v %v %v", gid, owner, err)
	}
	if _, _, err := s.RedeemTicket(tok); err != ErrUsedTicket {
		t.Fatalf("second redeem must fail with ErrUsedTicket, got %v", err)
	}

	tok = s.IssueTicket(id, "sess-1", 30*time.Second)
	c.add(31 * time.Second)
	if _, _, err := s.RedeemTicket(tok); err != ErrExpiredToken {
		t.Fatalf("expired ticket: %v", err)
	}
}

func TestTokensRejectTamperingAndKindConfusion(t *testing.T) {
	s := NewSigner([]byte("secret"))
	other := NewSigner([]byte("another"))
	id := NewID()

	tok := s.IssueTicket(id, "sess", time.Minute)
	if _, _, err := other.RedeemTicket(tok); err != ErrBadToken {
		t.Errorf("foreign key: %v", err)
	}
	if _, _, err := s.RedeemTicket(tok + "x"); err != ErrBadToken {
		t.Errorf("tampered signature: %v", err)
	}
	for _, bad := range []string{"", ".", "abc", "a.b", strings.Repeat("a", 2000)} {
		if _, _, err := s.RedeemTicket(bad); err != ErrBadToken {
			t.Errorf("%q: %v", bad, err)
		}
	}
	cookie := s.IssueCookie(id, "sess", time.Now().Add(time.Hour))
	if _, _, err := s.RedeemTicket(cookie); err != ErrBadToken {
		t.Errorf("a cookie must not work as a ticket: %v", err)
	}
	if _, _, err := s.VerifyCookie(tok); err != ErrBadToken {
		t.Errorf("a ticket must not work as a cookie: %v", err)
	}
	if gid, owner, err := s.VerifyCookie(cookie); err != nil || gid != id || owner != "sess" {
		t.Errorf("cookie: %v %v %v", gid, owner, err)
	}
}

func dialer(addr string) DialFunc {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

func TestRegistryOwnershipAndExpiry(t *testing.T) {
	r := NewRegistry(10*time.Minute, time.Hour)
	c := &clock{t: time.Unix(1_000_000, 0)}
	r.now = c.now

	var mu sync.Mutex
	reasons := map[string][]string{}
	add := func(owner string) string {
		id := NewID()
		if err := r.Add(Target{ID: id, Owner: owner, Dial: dialer("127.0.0.1:1")}, func(reason string) {
			mu.Lock()
			reasons[id] = append(reasons[id], reason)
			mu.Unlock()
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}

	a := add("alice")
	if _, ok := r.Resolve("bob", a); ok {
		t.Fatal("a foreign stream must look like a missing one")
	}
	if _, ok := r.Resolve("alice", a); !ok {
		t.Fatal("owner must resolve own stream")
	}
	if err := r.Add(Target{ID: a, Owner: "alice", Dial: dialer("x")}, nil); err != ErrDuplicate {
		t.Fatalf("duplicate: %v", err)
	}
	if err := r.Add(Target{ID: "short", Owner: "alice", Dial: dialer("x")}, nil); err == nil {
		t.Fatal("invalid id must be rejected")
	}

	// простой: активность продлевает жизнь
	c.add(9 * time.Minute)
	r.Touch(a)
	c.add(9 * time.Minute)
	if _, ok := r.Resolve("alice", a); !ok {
		t.Fatal("touched stream must stay alive")
	}
	c.add(11 * time.Minute)
	if n := r.Sweep(); n != 1 {
		t.Fatalf("sweep removed %d", n)
	}
	if got := reasons[a]; len(got) != 1 || got[0] != ReasonIdle {
		t.Fatalf("reasons: %v", got)
	}

	// абсолютный срок не продлевается активностью
	b := add("alice")
	for i := 0; i < 8; i++ {
		c.add(9 * time.Minute)
		r.Touch(b)
	}
	if _, ok := r.Resolve("alice", b); ok {
		t.Fatal("stream past max duration must be gone")
	}
	if got := reasons[b]; len(got) != 1 || got[0] != ReasonMaxDuration {
		t.Fatalf("reasons: %v", got)
	}

	// закрытие владельца и однократность stop
	x, y, z := add("carol"), add("carol"), add("dave")
	if n := r.CloseOwner("carol", ReasonSessionClosed); n != 2 {
		t.Fatalf("CloseOwner: %d", n)
	}
	if r.Remove(x, ReasonUser) {
		t.Fatal("repeated remove must report false")
	}
	if len(reasons[x]) != 1 || len(reasons[y]) != 1 || len(reasons[z]) != 0 {
		t.Fatalf("stop must run exactly once per removed stream: %v", reasons)
	}
	if r.Len() != 1 {
		t.Fatalf("len=%d", r.Len())
	}
}

// upstream поднимает приложение «в поде» и возвращает сервис с зарегистрированным потоком.
func setup(t *testing.T, h http.Handler, tune func(*Config)) (svc *Service, id string, base string) {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	cfg := Config{BaseDomain: "kubron.test", Scheme: "https"}
	if tune != nil {
		tune(&cfg)
	}
	svc, err := NewService(cfg, []byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	id = NewID()
	if err := svc.Registry.Add(Target{
		ID: id, Owner: "alice", Namespace: "ns", Pod: "web-0", Service: "web", Port: 80, Dial: dialer(up.Listener.Addr().String()),
	}, nil); err != nil {
		t.Fatal(err)
	}
	return svc, id, id + ".pf.kubron.test"
}

// do отправляет запрос в обработчик с нужным Host, минуя сеть.
func do(svc *Service, host, target string, hdr http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Host = host
	for k, v := range hdr {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	svc.Handler().ServeHTTP(w, r)
	return w
}

// login проходит ticket → cookie и возвращает заголовок Cookie.
func login(t *testing.T, svc *Service, id, host string) http.Header {
	t.Helper()
	entry, err := svc.EntryURL("alice", id)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(entry)
	if u.Host != host || u.Scheme != "https" || u.Path != EnterPath {
		t.Fatalf("entry url: %s", entry)
	}
	w := do(svc, u.Host, u.RequestURI(), nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("enter: %d %v", w.Code, w.Header())
	}
	var kpf *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			kpf = c
		}
	}
	if kpf == nil || !kpf.HttpOnly || !kpf.Secure || kpf.Domain != "" || kpf.Path != "/" {
		t.Fatalf("stream cookie: %+v", kpf)
	}
	return http.Header{"Cookie": {kpf.Name + "=" + kpf.Value}}
}

func TestHandlerRequiresTicketThenCookie(t *testing.T) {
	var gotCookie, gotHost, gotFwdHost, gotFwdProto string
	svc, id, host := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie, gotHost = r.Header.Get("Cookie"), r.Host
		gotFwdHost, gotFwdProto = r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto")
		_, _ = io.WriteString(w, "hello "+r.URL.Path)
	}), nil)

	if w := do(svc, host, "/", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", w.Code)
	}
	if w := do(svc, host, EnterPath+"?ticket=garbage", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad ticket: %d", w.Code)
	}
	if w := do(svc, "example.com", "/", nil); w.Code != http.StatusNotFound {
		t.Fatalf("foreign host: %d", w.Code)
	}

	hdr := login(t, svc, id, host)
	hdr.Set("Cookie", hdr.Get("Cookie")+"; app=1")
	w := do(svc, host, "/x/y", hdr)
	if w.Code != 200 || w.Body.String() != "hello /x/y" {
		t.Fatalf("proxied: %d %q", w.Code, w.Body.String())
	}
	if gotCookie != "app=1" {
		t.Errorf("the access cookie must not reach the app, got %q", gotCookie)
	}
	if gotHost != host || gotFwdHost != host || gotFwdProto != "https" {
		t.Errorf("host headers: %q %q %q", gotHost, gotFwdHost, gotFwdProto)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("hardening headers missing: %v", w.Header())
	}
}

func TestTicketIsSingleUseAndBoundToTheStreamHost(t *testing.T) {
	svc, id, host := setup(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), nil)
	entry, _ := svc.EntryURL("alice", id)
	u, _ := url.Parse(entry)
	if w := do(svc, host, u.RequestURI(), nil); w.Code != http.StatusFound {
		t.Fatalf("first use: %d", w.Code)
	}
	if w := do(svc, host, u.RequestURI(), nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("replay must fail: %d", w.Code)
	}
	// ticket одного потока не работает на хосте другого
	other := NewID()
	_ = svc.Registry.Add(Target{ID: other, Owner: "alice", Dial: dialer("127.0.0.1:1")}, nil)
	entry, _ = svc.EntryURL("alice", id)
	u, _ = url.Parse(entry)
	if w := do(svc, other+".pf.kubron.test", u.RequestURI(), nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("ticket for another stream: %d", w.Code)
	}
}

func TestForeignOwnerCannotUseStream(t *testing.T) {
	svc, id, host := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret") }), nil)

	if _, err := svc.EntryURL("mallory", id); err == nil {
		t.Fatal("EntryURL for a foreign session must fail")
	}
	// cookie, подписанная верным ключом, но выданная другой сессии: поток для неё «не найден»
	forged := svc.signer.IssueCookie(id, "mallory", time.Now().Add(time.Hour))
	w := do(svc, host, "/", http.Header{"Cookie": {CookieName + "=" + forged}})
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("foreign owner: %d %q", w.Code, w.Body.String())
	}
	// cookie другого потока на этом хосте
	other := svc.signer.IssueCookie(NewID(), "alice", time.Now().Add(time.Hour))
	if w := do(svc, host, "/", http.Header{"Cookie": {CookieName + "=" + other}}); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie of another stream: %d", w.Code)
	}
}

func TestStoppedStreamAndClosedSessionAreUnavailable(t *testing.T) {
	alive := true
	svc, id, host := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }),
		func(c *Config) { c.SessionAlive = func(string) bool { return alive } })
	hdr := login(t, svc, id, host)
	if w := do(svc, host, "/", hdr); w.Code != 200 {
		t.Fatalf("before: %d", w.Code)
	}
	alive = false
	if w := do(svc, host, "/", hdr); w.Code != http.StatusNotFound {
		t.Fatalf("closed session: %d", w.Code)
	}
	if svc.Registry.Len() != 0 {
		t.Fatal("the stream must be removed when its session is gone")
	}
}

func TestRedirectAndCookieRewriting(t *testing.T) {
	svc, id, host := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal":
			http.Redirect(w, r, "http://localhost:8080/login?next=%2Fa", http.StatusFound)
		case "/svc":
			http.Redirect(w, r, "https://web.ns.svc.cluster.local/app#frag", http.StatusFound)
		case "/self":
			http.Redirect(w, r, "https://"+r.Host+"/home", http.StatusFound)
		case "/external":
			http.Redirect(w, r, "https://idp.example.com/auth", http.StatusFound)
		case "/evil":
			w.Header().Set("Location", "http://localhost//evil.example/x")
			w.WriteHeader(http.StatusFound)
		case "/cookies":
			http.SetCookie(w, &http.Cookie{Name: "argocd.token", Value: "t", Domain: "web.ns.svc", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "evil", Path: "/"})
			w.WriteHeader(200)
		}
	}), nil)
	hdr := login(t, svc, id, host)

	loc := func(path string) string { return do(svc, host, path, hdr).Header().Get("Location") }
	if got := loc("/internal"); got != "/login?next=%2Fa" {
		t.Errorf("internal: %q", got)
	}
	if got := loc("/svc"); got != "/app#frag" {
		t.Errorf("svc: %q", got)
	}
	if got := loc("/self"); got != "/home" {
		t.Errorf("self: %q", got)
	}
	if got := loc("/external"); got != "https://idp.example.com/auth" {
		t.Errorf("external redirects must stay untouched: %q", got)
	}
	if got := loc("/evil"); got != "/evil.example/x" {
		t.Errorf("protocol-relative path must not become another host: %q", got)
	}

	w := do(svc, host, "/cookies", hdr)
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "argocd.token" {
		t.Fatalf("cookies: %+v", cs)
	}
	if cs[0].Domain != "" || !cs[0].Secure || cs[0].SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie must be host-only, Secure and SameSite: %+v", cs[0])
	}
}

func TestSSEIsStreamedWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	svc, id, host := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: two\n\n")
	}), nil)
	hdr := login(t, svc, id, host)

	front := httptest.NewServer(svc.Handler())
	defer front.Close()
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/events", nil)
	req.Host = host
	req.Header = hdr.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	if err != nil || line != "data: one\n" {
		t.Fatalf("first event must arrive before the response ends: %q %v", line, err)
	}
	close(release)
	_, _ = br.ReadString('\n')
	line, _ = br.ReadString('\n')
	if line != "data: two\n" {
		t.Fatalf("second event: %q", line)
	}
}

// echoUpgrade — приложение с Upgrade (как WebSocket): после 101 отвечает эхом на каждую строку.
func echoUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") != "echo" {
		http.Error(w, "no", 400)
		return
	}
	conn, brw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")
	_ = brw.Flush()
	for {
		line, err := brw.ReadString('\n')
		if err != nil {
			return
		}
		_, _ = brw.WriteString("echo:" + line)
		_ = brw.Flush()
	}
}

func upgradeClient(t *testing.T, front *httptest.Server, host string, hdr http.Header) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	req := "GET /ws HTTP/1.1\r\nHost: " + host + "\r\nConnection: Upgrade\r\nUpgrade: echo\r\nCookie: " + hdr.Get("Cookie") + "\r\n\r\n"
	_, _ = io.WriteString(conn, req)
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(status, "HTTP/1.1 101") {
		t.Fatalf("status: %q", status)
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if l == "\r\n" {
			break
		}
	}
	return conn, br
}

func TestUpgradeWorksAndStopClosesActiveConnections(t *testing.T) {
	svc, id, host := setup(t, http.HandlerFunc(echoUpgrade), nil)
	hdr := login(t, svc, id, host)
	front := httptest.NewServer(svc.Handler())
	defer front.Close()

	conn, br := upgradeClient(t, front, host, hdr)
	_, _ = io.WriteString(conn, "ping\n")
	if got, err := br.ReadString('\n'); err != nil || got != "echo:ping\n" {
		t.Fatalf("echo: %q %v", got, err)
	}

	// остановка потока обрывает уже открытый Upgrade-канал
	svc.Registry.Remove(id, ReasonUser)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadString('\n'); err == nil {
		t.Fatal("the connection must be closed after the stream is stopped")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the connection stayed open after the stream was stopped")
	}
}

func TestUpgradeTrafficCountsAsActivity(t *testing.T) {
	svc, id, host := setup(t, http.HandlerFunc(echoUpgrade), nil)
	c := &clock{t: time.Now()}
	svc.Registry.now = c.now
	svc.Registry.idle = time.Minute
	hdr := login(t, svc, id, host)
	front := httptest.NewServer(svc.Handler())
	defer front.Close()

	conn, br := upgradeClient(t, front, host, hdr)
	c.add(50 * time.Second)
	_, _ = io.WriteString(conn, "x\n")
	if _, err := br.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	c.add(50 * time.Second) // с момента последнего трафика прошло 50 с, а не 100
	if n := svc.Registry.Sweep(); n != 0 {
		t.Fatal("a stream with live traffic must not be swept as idle")
	}
	c.add(20 * time.Second)
	if n := svc.Registry.Sweep(); n != 1 {
		t.Fatal("an idle stream must be swept")
	}
}

func TestConcurrencyAndBodyLimits(t *testing.T) {
	block := make(chan struct{})

	started := make(chan struct{}, 1)
	svc, id, host := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			started <- struct{}{}

			<-block
		}
		b, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, strings.Repeat("x", len(b)))
	}), func(c *Config) { c.MaxConcurrent = 1; c.MaxBody = 8 })
	hdr := login(t, svc, id, host)
	front := httptest.NewServer(svc.Handler())
	defer front.Close()

	get := func(path, body string) (*http.Response, error) {
		req, _ := http.NewRequest(http.MethodPost, front.URL+path, strings.NewReader(body))
		req.Host = host
		req.Header = hdr.Clone()
		return http.DefaultClient.Do(req)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := get("/slow", "")
		if err != nil {
			t.Errorf("slow request: %v", err)
			return
		}
		resp.Body.Close()
	}()

	select {
	case <-started: // медленный запрос держит единственный слот
	case <-time.After(5 * time.Second):
		t.Fatal("the slow request never reached the application")
	}
	resp, err := get("/fast", "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the concurrency limit was not applied: status %d", resp.StatusCode)

	}
	close(block)
	<-done

	resp, err = get("/fast", strings.Repeat("a", 100))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("a body over the limit must not be proxied successfully")
	}
}

func TestUnreachablePodGivesBadGateway(t *testing.T) {
	svc, id, host := setup(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), nil)
	hdr := login(t, svc, id, host)
	// подменяем поток на недоступный адрес
	svc.Registry.Remove(id, ReasonUser)
	_ = svc.Registry.Add(Target{ID: id, Owner: "alice", Dial: func(context.Context) (net.Conn, error) {
		return nil, io.ErrClosedPipe
	}}, nil)
	if w := do(svc, host, "/", hdr); w.Code != http.StatusBadGateway {
		t.Fatalf("status %d", w.Code)
	}
}

func TestLocationRewriteTable(t *testing.T) {
	tg := Target{Service: "argocd-server", Pod: "argocd-server-abc"}
	cases := []struct {
		in, host, want string
		changed        bool
	}{
		{"/relative", "h.pf.x", "/relative", false},
		{"mailto:a@b.c", "h.pf.x", "mailto:a@b.c", false},
		{"http://127.0.0.1:8080/a", "h.pf.x", "/a", true},
		{"http://[::1]:8080/a", "h.pf.x", "/a", true},
		{"https://10.1.2.3/a?b=c", "h.pf.x", "/a?b=c", true},
		{"https://argocd-server.argocd/a", "h.pf.x", "/a", true},
		{"https://argocd-server-abc/a", "h.pf.x", "/a", true},
		{"https://h.pf.x/a", "h.pf.x", "/a", true},
		{"https://H.PF.X", "h.pf.x", "/", true},
		{"https://8.8.8.8/a", "h.pf.x", "https://8.8.8.8/a", false},
		{"https://login.example.com/a", "h.pf.x", "https://login.example.com/a", false},
		{"javascript://localhost/%0aalert(1)", "h.pf.x", "javascript://localhost/%0aalert(1)", false},
	}
	for _, c := range cases {
		got, changed := rewriteLocation(c.in, c.host, tg)
		if got != c.want || changed != c.changed {
			t.Errorf("%q: got (%q,%v), want (%q,%v)", c.in, got, changed, c.want, c.changed)
		}
	}
}

func TestMatchHost(t *testing.T) {
	svc, _ := NewService(Config{BaseDomain: "Kubron.Example.com:8443"}, nil)
	id := NewID()
	if got, ok := svc.MatchHost(strings.ToUpper(id) + ".pf.kubron.example.com:8443"); !ok || got != id {
		t.Errorf("got %q %v", got, ok)
	}
	for _, h := range []string{"kubron.example.com:8443", id + ".pf.kubron.example.com", "x.pf.kubron.example.com:8443",
		id + ".evil.kubron.example.com:8443", "a." + id + ".pf.kubron.example.com:8443"} {
		if _, ok := svc.MatchHost(h); ok {
			t.Errorf("%q must not match", h)
		}
	}
	if !svc.IsPortForwardHost("x.pf.kubron.example.com:8443") || svc.IsPortForwardHost("kubron.example.com:8443") {
		t.Error("IsPortForwardHost")
	}
}

func TestNewServiceValidation(t *testing.T) {
	for _, d := range []string{"", " ", ".example.com", "a/b", "a b", "user@host"} {
		if _, err := NewService(Config{BaseDomain: d}, nil); err == nil {
			t.Errorf("base domain %q must be rejected", d)
		}
	}
	if _, err := NewService(Config{BaseDomain: "x.test", Scheme: "ftp"}, nil); err == nil {
		t.Error("unknown scheme must be rejected")
	}
}
