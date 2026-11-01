// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pfproxy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// Виды подписанных токенов. Вид входит в подписываемые данные: cookie нельзя выдать за ticket и наоборот.
const (
	kindTicket = "t"
	kindCookie = "c"
)

// Ошибки проверки токена. Наружу (в ответ браузеру) причина не раскрывается.
var (
	ErrBadToken     = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
	ErrUsedTicket   = errors.New("ticket already used")
)

type tokenPayload struct {
	Kind    string `json:"k"`
	ID      string `json:"i"`
	Owner   string `json:"o"`
	Expires int64  `json:"e"`
	JTI     string `json:"j,omitempty"`
}

// Signer выдаёт и проверяет ticket'ы и cookie потоков (HMAC-SHA256). Ticket одноразовый: использованные
// идентификаторы помнятся до истечения срока.
type Signer struct {
	key []byte
	now func() time.Time

	mu   sync.Mutex
	used map[string]time.Time // jti → срок ticket'а
}

// NewSigner создаёт подписывающий объект. Из secret выводится отдельный ключ (метка pf-ticket), поэтому
// общий ключ сервера не используется напрямую.
func NewSigner(secret []byte) *Signer {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("kubron/pf-ticket/v1"))
	return &Signer{key: mac.Sum(nil), now: time.Now, used: map[string]time.Time{}}
}

// RandomSecret возвращает случайный ключ для режима без общего ключа (один процесс): ticket'ы живут секунды,
// после перезапуска они и так недействительны.
func RandomSecret() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("pfproxy: crypto/rand is unavailable: " + err.Error())
	}
	return b
}

func (s *Signer) sign(kind string, p tokenPayload) string {
	p.Kind = kind
	raw, _ := json.Marshal(p) // структура из строк и чисел: ошибки быть не может
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + s.mac(body)
}

func (s *Signer) mac(body string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Signer) open(kind, tok string) (tokenPayload, error) {
	var p tokenPayload
	body, sig, ok := strings.Cut(tok, ".")
	if !ok || body == "" || len(tok) > 1024 {
		return p, ErrBadToken
	}
	if !hmac.Equal([]byte(sig), []byte(s.mac(body))) {
		return p, ErrBadToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Kind != kind || !ValidID(p.ID) || p.Owner == "" {
		return tokenPayload{}, ErrBadToken
	}
	if !s.now().Before(time.Unix(p.Expires, 0)) {
		return tokenPayload{}, ErrExpiredToken
	}
	return p, nil
}

// IssueTicket выдаёт одноразовый ticket на вход в поток id для веб-сессии owner.
func (s *Signer) IssueTicket(id, owner string, ttl time.Duration) string {
	var j [12]byte
	if _, err := rand.Read(j[:]); err != nil {
		panic("pfproxy: crypto/rand is unavailable: " + err.Error())
	}
	return s.sign(kindTicket, tokenPayload{
		ID: id, Owner: owner, Expires: s.now().Add(ttl).Unix(), JTI: base64.RawURLEncoding.EncodeToString(j[:]),
	})
}

// RedeemTicket проверяет ticket и помечает его использованным.
func (s *Signer) RedeemTicket(tok string) (id, owner string, err error) {
	p, err := s.open(kindTicket, tok)
	if err != nil {
		return "", "", err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for j, exp := range s.used { // таблица мала: ticket'ы живут секунды
		if !now.Before(exp) {
			delete(s.used, j)
		}
	}
	if _, dup := s.used[p.JTI]; dup {
		return "", "", ErrUsedTicket
	}
	s.used[p.JTI] = time.Unix(p.Expires, 0)
	return p.ID, p.Owner, nil
}

// IssueCookie выдаёт значение cookie потока.
func (s *Signer) IssueCookie(id, owner string, exp time.Time) string {
	return s.sign(kindCookie, tokenPayload{ID: id, Owner: owner, Expires: exp.Unix()})
}

// VerifyCookie проверяет значение cookie потока.
func (s *Signer) VerifyCookie(tok string) (id, owner string, err error) {
	p, err := s.open(kindCookie, tok)
	if err != nil {
		return "", "", err
	}
	return p.ID, p.Owner, nil
}
