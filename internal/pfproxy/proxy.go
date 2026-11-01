// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pfproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Значения по умолчанию (ADR-0017, п. 6).
const (
	DefaultIdleTimeout   = 15 * time.Minute
	DefaultMaxDuration   = 8 * time.Hour
	DefaultTicketTTL     = 30 * time.Second
	DefaultMaxConcurrent = 64
	DefaultMaxBody       = 64 << 20
	DefaultDialTimeout   = 15 * time.Second

	// EnterPath — адрес обмена ticket'а на cookie потока.
	EnterPath = "/__kubron/enter"
	// hostLabel — метка зоны потоков: {id}.pf.<base-domain>.
	hostLabel = "pf"
)

// Config настраивает Service.
type Config struct {
	// BaseDomain — домен Kubron без схемы, возможно с портом: потоки живут на {id}.pf.<BaseDomain>.
	BaseDomain string
	// Scheme — схема адреса потока для браузера: "https" (по умолчанию) или "http" (только тесты и закрытые стенды).
	Scheme string
	// IdleTimeout и MaxDuration: 0 — значения по умолчанию, отрицательное — без ограничения.
	IdleTimeout time.Duration
	MaxDuration time.Duration
	TicketTTL   time.Duration
	// MaxConcurrent — одновременных запросов на поток; MaxBody — размер тела запроса (кроме Upgrade).
	MaxConcurrent int
	MaxBody       int64
	DialTimeout   time.Duration
	// SessionAlive (необязателен) уточняет, что веб-сессия-владелец ещё жива. Реестр и так чистится при закрытии
	// сессии; проверка закрывает окно между закрытием и очисткой.
	SessionAlive func(owner string) bool
}

func orDefault(v, def time.Duration) time.Duration {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	}
	return v
}

// Service — прокси потоков: реестр, подписи и HTTP-обработчик хостов {id}.pf.<BaseDomain>.
type Service struct {
	cfg      Config
	Registry *Registry
	signer   *Signer
	secure   bool

	mu      sync.Mutex
	streams map[string]*streamProxy
}

// NewService создаёт сервис. secret — ключ для подписи ticket'ов (nil — случайный, на время жизни процесса).
func NewService(cfg Config, secret []byte) (*Service, error) {
	cfg.BaseDomain = strings.ToLower(strings.TrimSpace(cfg.BaseDomain))
	if cfg.BaseDomain == "" || strings.ContainsAny(cfg.BaseDomain, "/ \t?#@") || strings.HasPrefix(cfg.BaseDomain, ".") {
		return nil, errors.New("invalid port-forward base domain")
	}
	if cfg.Scheme == "" {
		cfg.Scheme = "https"
	}
	if cfg.Scheme != "https" && cfg.Scheme != "http" {
		return nil, errors.New("invalid port-forward scheme")
	}
	if secret == nil {
		secret = RandomSecret()
	}
	if cfg.TicketTTL <= 0 {
		cfg.TicketTTL = DefaultTicketTTL
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = DefaultMaxBody
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}
	s := &Service{
		cfg:      cfg,
		Registry: NewRegistry(orDefault(cfg.IdleTimeout, DefaultIdleTimeout), orDefault(cfg.MaxDuration, DefaultMaxDuration)),
		signer:   NewSigner(secret),
		secure:   cfg.Scheme == "https",
		streams:  map[string]*streamProxy{},
	}
	s.Registry.OnRemove(s.release)
	return s, nil
}

// Run выполняет фоновую очистку истёкших потоков до отмены ctx.
func (s *Service) Run(ctx context.Context) { s.Registry.Run(ctx, 15*time.Second) }

// BaseDomain — настроенный домен.
func (s *Service) BaseDomain() string { return s.cfg.BaseDomain }

// EntryURL возвращает адрес, который браузер открывает для входа в поток id веб-сессии owner. Адрес
// одноразовый и живёт DefaultTicketTTL: его нельзя сохранять, перед каждым открытием запрашивается новый.
func (s *Service) EntryURL(owner, id string) (string, error) {
	if _, ok := s.Registry.Resolve(owner, id); !ok {
		return "", errors.New("port-forward not found")
	}
	q := url.Values{"ticket": {s.signer.IssueTicket(id, owner, s.cfg.TicketTTL)}}
	return fmt.Sprintf("%s://%s.%s.%s%s?%s", s.cfg.Scheme, id, hostLabel, s.cfg.BaseDomain, EnterPath, q.Encode()), nil
}

// MatchHost возвращает идентификатор потока из заголовка Host ({id}.pf.<BaseDomain>).
func (s *Service) MatchHost(host string) (string, bool) {
	suffix := "." + hostLabel + "." + s.cfg.BaseDomain
	h := strings.ToLower(host)
	if !strings.HasSuffix(h, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(h, suffix)
	return id, ValidID(id)
}

// IsPortForwardHost сообщает, что хост относится к зоне потоков (даже с недопустимым идентификатором):
// такие запросы никогда не должны попадать в обработчик основного приложения.
func (s *Service) IsPortForwardHost(host string) bool {
	return strings.HasSuffix(strings.ToLower(host), "."+hostLabel+"."+s.cfg.BaseDomain)
}

// streamProxy — транспорт и reverse proxy одного потока.
type streamProxy struct {
	rp    *httputil.ReverseProxy
	tr    *http.Transport
	sem   chan struct{}
	mu    sync.Mutex
	conns map[net.Conn]struct{}
	done  bool
}

// release закрывает все соединения потока (в том числе активные WebSocket и SSE).
func (s *Service) release(id string) {
	s.mu.Lock()
	sp := s.streams[id]
	delete(s.streams, id)
	s.mu.Unlock()
	if sp == nil {
		return
	}
	sp.mu.Lock()
	sp.done = true
	victims := make([]net.Conn, 0, len(sp.conns))
	for c := range sp.conns {
		victims = append(victims, c)
	}
	sp.mu.Unlock()
	sp.tr.CloseIdleConnections()
	for _, c := range victims {
		_ = c.Close()
	}
}

// trackedConn отмечает активность потока и снимает себя с учёта при закрытии.
type trackedConn struct {
	net.Conn
	touch func()
	drop  func(net.Conn)
	once  sync.Once
}

func (c *trackedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.touch()
	}
	return n, err
}

func (c *trackedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.touch()
	}
	return n, err
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.drop(c) })
	return c.Conn.Close()
}

// proxyFor возвращает (создавая при необходимости) прокси потока.
func (s *Service) proxyFor(t Target) *streamProxy {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sp, ok := s.streams[t.ID]; ok {
		return sp
	}
	sp := &streamProxy{sem: make(chan struct{}, s.cfg.MaxConcurrent), conns: map[net.Conn]struct{}{}}
	id := t.ID
	dial := t.Dial
	sp.tr = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, s.cfg.DialTimeout)
			defer cancel()
			c, err := dial(ctx)
			if err != nil {
				return nil, err
			}
			tc := &trackedConn{Conn: c, touch: func() { s.Registry.Touch(id) }, drop: func(c net.Conn) {
				sp.mu.Lock()
				delete(sp.conns, c)
				sp.mu.Unlock()
			}}
			sp.mu.Lock()
			if sp.done {
				sp.mu.Unlock()
				_ = c.Close()
				return nil, errors.New("port-forward stopped")
			}
			sp.conns[tc] = struct{}{}
			sp.mu.Unlock()
			return tc, nil
		},
		// Сертификат пода не проверяется: канал до пода защищён аутентифицированным туннелем к apiserver (ADR-0017).
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		DisableCompression:    true, // тело отдаётся как есть, без перекодирования
	}
	scheme := t.Scheme
	sp.rp = &httputil.ReverseProxy{
		Transport:     sp.tr,
		FlushInterval: -1, // SSE и потоковые ответы уходят без буферизации
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = scheme
			pr.Out.URL.Host = "pod.invalid" // адрес игнорируется DialContext; нужен только как Host по умолчанию
			pr.Out.Host = pr.In.Host        // приложение видит тот же Host, что и браузер (абсолютные ссылки, проверка Origin)
			stripAccessCookie(pr.Out.Header)
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			pr.Out.Header.Set("X-Forwarded-Proto", s.cfg.Scheme)
		},
		ModifyResponse: func(resp *http.Response) error {
			if loc := resp.Header.Get("Location"); loc != "" {
				if nl, changed := rewriteLocation(loc, resp.Request.Host, t); changed {
					resp.Header.Set("Location", nl)
				}
			}
			rewriteSetCookie(resp.Header, s.secure)
			hardenResponse(resp.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// В журнал — только метод, путь без query и текст ошибки: в query могут быть токены приложения.
			log.Printf("port-forward %s: %s %s: %v", id[:8], r.Method, r.URL.Path, err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}
	s.streams[id] = sp
	return sp
}

// Handler обслуживает запросы к хостам потоков. Другие хосты получают 404.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.MatchHost(r.Host)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == EnterPath {
			s.enter(w, r, id)
			return
		}
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		cid, owner, err := s.signer.VerifyCookie(cookie.Value)
		if err != nil || cid != id {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		t, ok := s.lookup(owner, id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		sp := s.proxyFor(t)
		select {
		case sp.sem <- struct{}{}:
			defer func() { <-sp.sem }()
		default:
			http.Error(w, "too many concurrent requests", http.StatusServiceUnavailable)
			return
		}
		if !isUpgrade(r) {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBody)
		}
		sp.rp.ServeHTTP(w, r)
	})
}

func (s *Service) lookup(owner, id string) (Target, bool) {
	if s.cfg.SessionAlive != nil && !s.cfg.SessionAlive(owner) {
		s.Registry.Remove(id, ReasonSessionClosed)
		return Target{}, false
	}
	return s.Registry.Resolve(owner, id)
}

// enter меняет ticket на cookie потока и отправляет браузер на корень приложения.
func (s *Service) enter(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tid, owner, err := s.signer.RedeemTicket(r.URL.Query().Get("ticket"))
	if err != nil || tid != id {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	t, ok := s.lookup(owner, id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Cookie живёт столько же, сколько может прожить поток; фактический срок задаёт реестр (idle и абсолютный).
	life := s.Registry.maxDur
	if life <= 0 {
		life = 24 * time.Hour
	}
	exp := t.Created.Add(life)
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: s.signer.IssueCookie(id, owner, exp), Path: "/",
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode, Expires: exp,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func isUpgrade(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") && r.Header.Get("Upgrade") != ""
}
