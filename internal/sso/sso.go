// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package sso implements the storage-independent OpenID Connect sign-in core.
package sso

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/idpnet"
	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/oauth2"
)

const (
	flowLifetime = 10 * time.Minute
	flowKeyInfo  = "kubron/oidc/sign-in-flow/v1"
)

var (
	ErrDisabled        = errors.New("OIDC sign-in is disabled")
	ErrInvalidConfig   = errors.New("OIDC configuration is invalid")
	ErrStateMismatch   = errors.New("OIDC state does not match")
	ErrIssuerMismatch  = errors.New("OIDC issuer does not match")
	ErrIDToken         = errors.New("OIDC ID token is invalid")
	ErrNoUsername      = errors.New("OIDC identity has no valid username")
	ErrFlowExpired     = errors.New("OIDC sign-in flow has expired")
	ErrFlowInvalid     = errors.New("OIDC sign-in flow is invalid")
	ErrProvider        = errors.New("OIDC provider is unavailable")
	ErrAuthorization   = errors.New("OIDC authorization was rejected")
	ErrMissingCode     = errors.New("OIDC authorization code is missing")
	ErrInvalidRedirect = errors.New("OIDC redirect URL is invalid")
)

type Config struct {
	Issuer            string
	ClientID          string
	ClientSecret      string
	Scopes            []string
	UsernameClaim     string
	GroupsClaim       string
	AdminGroups       []string
	OperatorGroups    []string
	DefaultRole       string
	AutoCreate        bool
	LinkExisting      bool
	DisableLocalLogin bool
	ButtonLabel       string
	CAFile            string
	Desktop           bool
}

func LoadConfig(getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	linkExisting, err := parseEnvBool(getenv("KUBRON_OIDC_LINK_EXISTING"))
	if err != nil {
		return Config{}, ErrInvalidConfig
	}
	disableLocalLogin, err := parseEnvBool(getenv("KUBRON_OIDC_DISABLE_LOCAL_LOGIN"))
	if err != nil {
		return Config{}, ErrInvalidConfig
	}
	desktop, err := parseEnvBool(getenv("KUBRON_DESKTOP_MODE"))
	if err != nil {
		return Config{}, ErrInvalidConfig
	}
	cfg := Config{
		Issuer:            strings.TrimSpace(getenv("KUBRON_OIDC_ISSUER")),
		ClientID:          strings.TrimSpace(getenv("KUBRON_OIDC_CLIENT_ID")),
		ClientSecret:      getenv("KUBRON_OIDC_CLIENT_SECRET"),
		Scopes:            strings.Fields(getenv("KUBRON_OIDC_SCOPES")),
		UsernameClaim:     strings.TrimSpace(getenv("KUBRON_OIDC_USERNAME_CLAIM")),
		GroupsClaim:       strings.TrimSpace(getenv("KUBRON_OIDC_GROUPS_CLAIM")),
		AdminGroups:       splitList(getenv("KUBRON_OIDC_ADMIN_GROUPS")),
		OperatorGroups:    splitList(getenv("KUBRON_OIDC_OPERATOR_GROUPS")),
		DefaultRole:       strings.TrimSpace(getenv("KUBRON_OIDC_DEFAULT_ROLE")),
		AutoCreate:        true,
		LinkExisting:      linkExisting,
		DisableLocalLogin: disableLocalLogin,
		ButtonLabel:       strings.TrimSpace(getenv("KUBRON_OIDC_BUTTON_LABEL")),
		CAFile:            strings.TrimSpace(getenv("KUBRON_OIDC_CA_FILE")),
		// Desktop builds set this process-mode flag before loading configuration.
		Desktop: desktop,
	}
	if raw := strings.TrimSpace(getenv("KUBRON_OIDC_AUTO_CREATE")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, ErrInvalidConfig
		}
		cfg.AutoCreate = v
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "preferred_username"
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = "user"
	}
	if cfg.Issuer == "" {
		if cfg.ClientID != "" || cfg.ClientSecret != "" {
			return Config{}, ErrInvalidConfig
		}
		return cfg, nil
	}
	if cfg.ClientID == "" {
		return Config{}, ErrInvalidConfig
	}
	if err := validateIssuer(cfg.Issuer, cfg.Desktop); err != nil {
		return Config{}, ErrInvalidConfig
	}
	if !validRole(cfg.DefaultRole) {
		return Config{}, ErrInvalidConfig
	}
	for _, scope := range cfg.Scopes {
		if strings.ContainsAny(scope, " \t\r\n") {
			return Config{}, ErrInvalidConfig
		}
	}
	return cfg, nil
}

func validRole(role string) bool { return role == "admin" || role == "operator" || role == "user" }

func validateIssuer(raw string, desktop bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return ErrInvalidConfig
	}
	if strings.EqualFold(u.Scheme, "https") {
		return nil
	}
	if !desktop || !strings.EqualFold(u.Scheme, "http") {
		return ErrInvalidConfig
	}
	host := strings.Trim(u.Hostname(), "[]")
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return ErrInvalidConfig
	}
	return nil
}

func splitList(raw string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func parseEnvBool(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}

type Identity struct {
	Issuer        string
	Subject       string
	Username      string
	Email         string
	EmailVerified bool
	Name          string
	Groups        []string
}

type FlowState struct {
	State       string    `json:"s"`
	Nonce       string    `json:"n"`
	Verifier    string    `json:"v"`
	RedirectURL string    `json:"r"`
	CreatedAt   time.Time `json:"t"`
}

type flowPayload struct {
	Version int       `json:"v"`
	Flow    FlowState `json:"f"`
}

var processKeyOnce sync.Once
var processKey []byte
var processKeyErr error

func flowEncryptionKey(key []byte) ([]byte, error) {
	if len(key) == 0 {
		processKeyOnce.Do(func() {
			processKey = make([]byte, 32)
			_, processKeyErr = rand.Read(processKey)
		})
		if processKeyErr != nil {
			return nil, ErrFlowInvalid
		}
		key = processKey
	}
	r := hkdf.New(sha256.New, key, []byte("kubron-oidc-flow-key"), []byte(flowKeyInfo))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, ErrFlowInvalid
	}
	return out, nil
}

func (f FlowState) Seal(key []byte) (string, error) {
	if f.State == "" || f.Nonce == "" || f.Verifier == "" || f.RedirectURL == "" || f.CreatedAt.IsZero() {
		return "", ErrFlowInvalid
	}
	k, err := flowEncryptionKey(key)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", ErrFlowInvalid
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrFlowInvalid
	}
	plain, err := json.Marshal(flowPayload{Version: 1, Flow: f})
	if err != nil {
		return "", ErrFlowInvalid
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", ErrFlowInvalid
	}
	sealed := aead.Seal(nonce, nonce, plain, []byte(flowKeyInfo))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func Open(key []byte, value string) (FlowState, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return FlowState{}, ErrFlowInvalid
	}
	k, err := flowEncryptionKey(key)
	if err != nil {
		return FlowState{}, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return FlowState{}, ErrFlowInvalid
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return FlowState{}, ErrFlowInvalid
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return FlowState{}, ErrFlowInvalid
	}
	nonce, ciphertext := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, []byte(flowKeyInfo))
	if err != nil {
		return FlowState{}, ErrFlowInvalid
	}
	var payload flowPayload
	if json.Unmarshal(plain, &payload) != nil || payload.Version != 1 {
		return FlowState{}, ErrFlowInvalid
	}
	f := payload.Flow
	if f.State == "" || f.Nonce == "" || f.Verifier == "" || f.RedirectURL == "" || f.CreatedAt.IsZero() {
		return FlowState{}, ErrFlowInvalid
	}
	age := time.Since(f.CreatedAt)
	if age < -time.Minute || age > flowLifetime {
		return FlowState{}, ErrFlowExpired
	}
	return f, nil
}

type Provider struct {
	cfg        Config
	httpClient *http.Client
	mu         sync.Mutex
	discovered *gooidc.Provider
}

func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Issuer == "" {
		return nil, ErrDisabled
	}
	if err := validateIssuer(cfg.Issuer, cfg.Desktop); err != nil || cfg.ClientID == "" || !validRole(defaultRole(cfg.DefaultRole)) {
		return nil, ErrInvalidConfig
	}
	var ca []byte
	if cfg.CAFile != "" {
		b, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, ErrInvalidConfig
		}
		ca = b
	}
	hc, err := idpnet.NewClient(idpnet.Options{Desktop: cfg.Desktop, CAData: ca})
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "preferred_username"
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = "user"
	}
	return &Provider{cfg: cfg, httpClient: hc}, nil
}

func defaultRole(role string) string {
	if role == "" {
		return "user"
	}
	return role
}

func (p *Provider) provider(_ context.Context) (*gooidc.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.discovered != nil {
		return p.discovered, nil
	}
	ctx := gooidc.ClientContext(context.Background(), p.httpClient)
	provider, err := gooidc.NewProvider(ctx, p.cfg.Issuer)
	if err != nil {
		return nil, ErrProvider
	}
	p.discovered = provider
	return provider, nil
}

func validRedirect(u *url.URL, desktop bool) bool {
	if u == nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if strings.EqualFold(u.Scheme, "https") {
		return true
	}
	if !desktop || !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	host := strings.Trim(u.Hostname(), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", ErrFlowInvalid
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (p *Provider) Begin(redirectURL string) (string, FlowState, error) {
	if p == nil || p.cfg.Issuer == "" {
		return "", FlowState{}, ErrDisabled
	}
	redirect, err := url.Parse(redirectURL)
	if err != nil || !validRedirect(redirect, p.cfg.Desktop) {
		return "", FlowState{}, ErrInvalidRedirect
	}
	ctx, cancel := context.WithTimeout(context.Background(), idpnet.DefaultTimeout)
	defer cancel()
	provider, err := p.provider(ctx)
	if err != nil {
		return "", FlowState{}, err
	}
	state, err := randomToken(32)
	if err != nil {
		return "", FlowState{}, err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return "", FlowState{}, err
	}
	verifier := oauth2.GenerateVerifier()
	flow := FlowState{State: state, Nonce: nonce, Verifier: verifier, RedirectURL: redirectURL, CreatedAt: time.Now().UTC()}
	oauth := oauth2.Config{ClientID: p.cfg.ClientID, ClientSecret: p.cfg.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: redirectURL, Scopes: p.cfg.Scopes}
	authURL := oauth.AuthCodeURL(state, gooidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	return authURL, flow, nil
}

func (p *Provider) Complete(ctx context.Context, flow FlowState, query url.Values) (Identity, error) {
	if p == nil || p.cfg.Issuer == "" {
		return Identity{}, ErrDisabled
	}
	if flow.State == "" || flow.Nonce == "" || flow.Verifier == "" || flow.RedirectURL == "" ||
		subtle.ConstantTimeCompare([]byte(flow.State), []byte(query.Get("state"))) != 1 {
		return Identity{}, ErrStateMismatch
	}
	age := time.Since(flow.CreatedAt)
	if flow.CreatedAt.IsZero() || age < -time.Minute || age > flowLifetime {
		return Identity{}, ErrFlowExpired
	}
	if responseIssuer := query.Get("iss"); responseIssuer != "" && responseIssuer != p.cfg.Issuer {
		return Identity{}, ErrIssuerMismatch
	}
	if query.Get("error") != "" {
		return Identity{}, ErrAuthorization
	}
	code := query.Get("code")
	if code == "" {
		return Identity{}, ErrMissingCode
	}
	provider, err := p.provider(ctx)
	if err != nil {
		return Identity{}, err
	}
	ctx = gooidc.ClientContext(ctx, p.httpClient)
	oauth := oauth2.Config{ClientID: p.cfg.ClientID, ClientSecret: p.cfg.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: flow.RedirectURL, Scopes: p.cfg.Scopes}
	token, err := oauth.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		return Identity{}, ErrAuthorization
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return Identity{}, ErrIDToken
	}
	idToken, err := provider.Verifier(&gooidc.Config{ClientID: p.cfg.ClientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return Identity{}, ErrIDToken
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.Nonce)) != 1 {
		return Identity{}, ErrIDToken
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, ErrIDToken
	}
	identity := Identity{Issuer: idToken.Issuer, Subject: idToken.Subject}
	if identity.Issuer == "" || identity.Subject == "" {
		return Identity{}, ErrIDToken
	}
	identity.Email = claimString(claims, "email")
	identity.Username = normalizeUsername(claimString(claims, p.cfg.UsernameClaim), identity.Email)
	if identity.Username == "" {
		return Identity{}, ErrNoUsername
	}
	identity.EmailVerified, _ = claims["email_verified"].(bool)
	identity.Name = claimString(claims, "name")
	identity.Groups = claimStrings(claims[p.cfg.GroupsClaim])
	return identity, nil
}

func normalizeUsername(candidate, email string) string {
	candidate = strings.TrimSpace(candidate)
	if auth.ValidateUsername(candidate) == nil {
		return candidate
	}
	email = strings.TrimSpace(email)
	if local, _, ok := strings.Cut(email, "@"); ok && auth.ValidateUsername(local) == nil {
		return local
	}
	return ""
}

func claimString(claims map[string]any, key string) string {
	v, _ := claims[key].(string)
	return strings.TrimSpace(v)
}

func claimStrings(v any) []string {
	switch values := v.(type) {
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return append([]string(nil), values...)
	default:
		return nil
	}
}

func (p *Provider) RoleFor(groups []string) (string, bool) {
	if p == nil {
		return "user", false
	}
	for _, group := range groups {
		if contains(p.cfg.AdminGroups, group) {
			return "admin", true
		}
	}
	for _, group := range groups {
		if contains(p.cfg.OperatorGroups, group) {
			return "operator", true
		}
	}
	return defaultRole(p.cfg.DefaultRole), false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
