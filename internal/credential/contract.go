// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package credential: общий контракт входа в кластер (ADR-0018), версия 1.
package credential

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type Scope string

const (
	ScopeUser Scope = "user" // личные данные пользователя
	ScopeTeam Scope = "team" // общие данные команды
)

type Kind string

const (
	KindStatic Kind = "static"    // токен, сертификат, basic: kubeconfig как есть
	KindOIDC   Kind = "oidc"      // Params["grant"]: password | authcode | authcode-keyboard | device-code | legacy-refresh
	KindEKS    Kind = "cloud-eks" // aws eks get-token, aws-iam-authenticator
	KindGKE    Kind = "cloud-gke" // gke-gcloud-auth-plugin
	KindAKS    Kind = "cloud-aks" // kubelogin для Azure
	KindExec   Kind = "exec"      // любой другой exec-плагин
)

// ExecSpec: блок exec из kubeconfig. Только данные; в вебе не выполняется никогда.
type ExecSpec struct {
	APIVersion      string            `json:"apiVersion"`
	Command         string            `json:"command"`
	Args            []string          `json:"args"`
	Env             map[string]string `json:"env,omitempty"`
	InteractiveMode string            `json:"interactiveMode,omitempty"`
	InstallHint     string            `json:"installHint,omitempty"`
}

// Descriptor: описание способа входа, только в памяти. Params может содержать client-secret: в API и
// логи не попадает. Detect кладёт в Params сведения о кластере: cluster.server,
// cluster.certificate-authority-data, cluster.tls-server-name, cluster.insecure-skip-tls-verify.
type Descriptor struct {
	Kind   Kind
	Params map[string]string
	Exec   *ExecSpec // сохраняется и для известных плагинов (нужно десктопу)
}

type Subject struct {
	ClusterID string
	Scope     Scope
	OwnerID   string // id пользователя (ScopeUser) или команды (ScopeTeam)
	SessionID string // веб-сессия; пусто на десктопе
}

type ChallengeKind string

const (
	ChallengeCredentials     ChallengeKind = "credentials"      // поля username, password
	ChallengeDeviceCode      ChallengeKind = "device-code"      // URL + UserCode, Poll
	ChallengeBrowserLoopback ChallengeKind = "browser-loopback" // десктоп: браузер открыт, ждём, Poll
	ChallengeBrowserCallback ChallengeKind = "browser-callback" // веб: возврат на сервер (вторая волна)
	ChallengePasteRedirect   ChallengeKind = "paste-redirect"   // URL входа + поле redirectUrl
	ChallengeSecretFields    ChallengeKind = "secret-fields"    // произвольные поля провайдера
	ChallengeManualToken     ChallengeKind = "manual-token"     // Command для показа + поле token
	ChallengeConfirmExec     ChallengeKind = "confirm-exec"     // Exec; ответ decision=once|always
)

type Field struct {
	Name   string `json:"name"` // подпись: ключ i18n credential.field.<name>
	Secret bool   `json:"secret"`
}

// Challenge: ID и ExpiresAt проставляет Broker, провайдер оставляет их пустыми.
type Challenge struct {
	ID          string        `json:"id"`
	Kind        ChallengeKind `json:"kind"`
	Fields      []Field       `json:"fields,omitempty"`
	URL         string        `json:"url,omitempty"`
	UserCode    string        `json:"userCode,omitempty"`
	Command     []string      `json:"command,omitempty"` // manual-token: только показать
	Exec        *ExecSpec     `json:"exec,omitempty"`    // confirm-exec; значения Env обнулены
	Message     string        `json:"message,omitempty"` // вывод плагина, только для показа
	Poll        bool          `json:"poll,omitempty"`
	IntervalSec int           `json:"intervalSec,omitempty"`
	ExpiresAt   time.Time     `json:"expiresAt"`
}

// Answer: ответ пользователя. Значения секретные, String скрывает их.
type Answer map[string]string

func (Answer) String() string   { return "[redacted]" }
func (Answer) GoString() string { return "[redacted]" }

type Token struct {
	Value     string
	Expiry    time.Time
	Principal string // sub или логин, для аудита
}

func (Token) String() string   { return "[redacted token]" }
func (Token) GoString() string { return "[redacted token]" }

// Request: вход Provider. Saved и Pending непрозрачны для Broker, шифрует их Broker.
type Request struct {
	Descriptor Descriptor
	Subject    Subject
	Saved      []byte    // сохранённые данные входа (refresh-токен и т. п.); nil, если нет
	Pending    []byte    // приватное состояние текущего вызова (PKCE verifier, device_code)
	Challenge  Challenge // при Continue
	Answer     Answer    // при Continue
}

// Result: ровно одно из Token или Challenge. Оба nil означает «ничего не нужно» (KindStatic).
type Result struct {
	Token     *Token
	Challenge *Challenge
	Save      []byte // nil: без изменений; пустой не-nil: стереть
	Pending   []byte // новое приватное состояние вызова
}

type Env struct {
	HTTP    *http.Client // единственный клиент для обращений к IdP; в вебе с SSRF-политикой
	Now     func() time.Time
	Desktop bool
	OpenURL func(url string) error // только десктоп
	DataDir string
}

// Provider: чистая функция от Request и Env. Без хранилища, без глобального состояния,
// без секретов в ошибках. Если Saved непригоден, Begin возвращает Challenge и Save: []byte{}.
type Provider interface {
	Kind() Kind
	Begin(ctx context.Context, env Env, req Request) (Result, error)
	Continue(ctx context.Context, env Env, req Request) (Result, error)
}

// WebCallbackBroker completes an OIDC browser redirect using the challenge ID carried in OAuth state.
// Both methods bind the operation to the authenticated application user and web session.
type WebCallbackBroker interface {
	CallbackSubject(ctx context.Context, sessionID, challengeID string) (Subject, error)
	AnswerCallback(ctx context.Context, sub Subject, challengeID, redirectURL string) (Result, error)
}

type RevokeFilter struct {
	ClusterID string
	OwnerID   string
	Scope     Scope
} // пустые поля означают «любой»

// Broker: оркестратор. Реализует разработчик 1, использует разработчик 2.
type Broker interface {
	Describe(kubeconfig []byte, contextName string) (Descriptor, error) // чистая функция
	Acquire(ctx context.Context, s Subject, d Descriptor) (Result, error)
	Answer(ctx context.Context, s Subject, challengeID string, a Answer) (Result, error)
	Poll(ctx context.Context, s Subject, challengeID string) (Result, error)
	Cancel(ctx context.Context, s Subject, challengeID string) error
	Token(ctx context.Context, s Subject, d Descriptor) (Token, error) // ErrChallengeRequired
	Revoke(ctx context.Context, f RevokeFilter) error
	List(ctx context.Context, owner string) ([]ConnectionInfo, error)
}

var (
	ErrChallengeRequired = errors.New("sign-in is required")
	ErrChallengeExpired  = errors.New("the sign-in request has expired")
	ErrAnswerRejected    = errors.New("the credentials were rejected")
	// ErrInvalidAnswer: the answer cannot be used at all (an empty field, a value that is not a token, a document
	// that is not an ExecCredential). Unlike ErrAnswerRejected it is not a wrong guess: it does not use up an
	// attempt, and the dialog stays open with the entered value.
	ErrInvalidAnswer = errors.New("the value could not be used")
	ErrInvalidGrant  = errors.New("the saved sign-in is no longer valid")
	ErrUnsupported   = errors.New("this sign-in method is not supported")
)

// Провод RPC (JSON, camelCase). Методы: ConnectCluster(clusterId), AnswerChallenge(challengeId, values),
// PollChallenge(challengeId), CancelChallenge(challengeId), ListMyConnections(), DisconnectCluster(clusterId).
// События, адресно сессии: cluster:auth-completed {clusterId, challengeId}, cluster:auth-required {clusterId}.
type ConnectResult struct {
	Status    string     `json:"status"` // "connected" | "challenge"
	Challenge *Challenge `json:"challenge,omitempty"`
}

type ConnectionInfo struct {
	ClusterID string     `json:"clusterId"`
	Kind      Kind       `json:"kind"`
	Scope     Scope      `json:"scope"`
	Principal string     `json:"principal,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}
