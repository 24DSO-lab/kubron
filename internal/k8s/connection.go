// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/24DSO-labs/kubron/internal/netpolicy"
	"github.com/24DSO-labs/kubron/internal/oidcpassword"
)

// Способы подключения к кластеру (ClusterMeta.Mode). Пусто означает kubeconfig.
const (
	// ModeKubeconfig — токен, сертификат или иные статические учётные данные из kubeconfig.
	ModeKubeconfig = "kubeconfig"
	// ModeInCluster — сервер сам работает в этом кластере и ходит под своим ServiceAccount.
	ModeInCluster = "in-cluster"
	// ModeOIDCPassword — kubeconfig с `kubectl oidc-login` (grant password): сервер сам получает и обновляет токен,
	// а логин и пароль хранит зашифрованными (ADR-0008).
	ModeOIDCPassword = "oidc-password"
)

// ConnectionPolicy — что разрешено в kubeconfig там, где его присылает пользователь, а не читает владелец машины.
// Десктоп разрешает всё (как kubectl); веб-сервер запрещает запуск команд и чтение файлов сервера.
type ConnectionPolicy struct {
	// AllowExec — учётные данные от внешней команды (exec-плагин): это выполнение чужого кода на сервере.
	AllowExec bool
	// AllowLocalFiles — ссылки на файлы машины с ядром (certificate-authority, client-key, tokenFile): иначе
	// kubeconfig мог бы отправить файл сервера на чужой адрес.
	AllowLocalFiles bool
}

var connPolicy atomic.Pointer[ConnectionPolicy]

// SetConnectionPolicy задаёт политику; вызывается один раз при старте сервера, до открытия хранилищ.
// Десктоп её не вызывает: без неё разрешено всё.
func SetConnectionPolicy(p ConnectionPolicy) { connPolicy.Store(&p) }

func currentPolicy() ConnectionPolicy {
	if p := connPolicy.Load(); p != nil {
		return *p
	}
	return ConnectionPolicy{AllowExec: true, AllowLocalFiles: true}
}

// AuthKind — как kubeconfig получает учётные данные для выбранного контекста.
type AuthKind string

const (
	// AuthStatic — токен, сертификат, логин и пароль или ничего.
	AuthStatic AuthKind = "static"
	// AuthOIDCPassword — oidc-login с grant password: в вебе нужны логин и пароль, Kubron берёт токен сам.
	AuthOIDCPassword AuthKind = "oidc-password"
	// AuthOIDCAuthCode — oidc-login с входом через браузер (authcode и др.): на сервере невозможен.
	AuthOIDCAuthCode AuthKind = "oidc-authcode"
	// AuthExec — любой другой exec-плагин (aws, gcloud, kubelogin для Azure…).
	AuthExec AuthKind = "exec"
)

// Ошибки политики подключений; тексты сопоставлены переводам в backend.json.
var (
	// ErrExecForbidden — exec-плагины в веб-версии запрещены.
	ErrExecForbidden = errors.New("credential plugins (exec) are not allowed in web mode: use a token or a client certificate")
	// ErrLocalFilesForbidden — kubeconfig ссылается на файлы машины с сервером.
	ErrLocalFilesForbidden = errors.New("the kubeconfig refers to files on the server: embed certificates and tokens in it (the -data fields)")
)

// ClassifyAuth определяет способ получения учётных данных в контексте ctxName. Для oidc-login с паролем
// возвращает параметры плагина (издатель, client id, scopes).
func ClassifyAuth(cfg *clientcmdapi.Config, ctxName string) (AuthKind, *oidcpassword.ExecParams) {
	ctx := cfg.Contexts[ctxName]
	if ctx == nil {
		return AuthStatic, nil
	}
	ai := cfg.AuthInfos[ctx.AuthInfo]
	if ai == nil || ai.Exec == nil {
		return AuthStatic, nil
	}
	p, isLogin, err := oidcpassword.ParseOIDCLoginExec(ai.Exec.Command, ai.Exec.Args)
	switch {
	case !isLogin:
		return AuthExec, nil
	case errors.Is(err, oidcpassword.ErrUnsupportedGrant):
		return AuthOIDCAuthCode, nil
	case err != nil:
		return AuthExec, nil // oidc-login без издателя или client id: без них сервер не знает, куда идти
	}
	return AuthOIDCPassword, &p
}

// checkLocalFiles отвергает ссылки на файлы машины с ядром в выбранном контексте.
func checkLocalFiles(cfg *clientcmdapi.Config, ctxName string) error {
	ctx := cfg.Contexts[ctxName]
	if ctx == nil {
		return nil
	}
	if cl := cfg.Clusters[ctx.Cluster]; cl != nil && cl.CertificateAuthority != "" {
		return ErrLocalFilesForbidden
	}
	if ai := cfg.AuthInfos[ctx.AuthInfo]; ai != nil && (ai.ClientCertificate != "" || ai.ClientKey != "" || ai.TokenFile != "") {
		return ErrLocalFilesForbidden
	}
	return nil
}

// CheckKubeconfigPolicy применяет к выбранному контексту проверки, не зависящие от способа входа: адрес сервера
// (политика адресов) и ссылки на файлы машины с сервером. Способ входа определяет credential.Broker.Describe
// (ADR-0018): oidc-login и облачные плагины больше не отклоняются при добавлении кластера, их блок exec не
// выполняется (см. checkExecAllowed и ConfigFor).
func CheckKubeconfigPolicy(cfg *clientcmdapi.Config, ctxName string) error {
	// Адрес API-сервера из kubeconfig пользователя проверяется политикой адресов (если она включена).
	if err := checkClusterAddress(cfg, ctxName); err != nil {
		return err
	}
	if !currentPolicy().AllowLocalFiles {
		if err := checkLocalFiles(cfg, ctxName); err != nil {
			return err
		}
	}
	return nil
}

// checkExecAllowed не даёт запустить блок exec там, где политика это запрещает (веб). Вызывается на каждом пути,
// который отдаёт kubeconfig клиенту client-go как есть, то есть мог бы запустить плагин: прежнее подключение без
// брокера и пробное подключение. Путь через брокер блок exec удаляет и сюда не попадает.
func checkExecAllowed(cfg *clientcmdapi.Config, ctxName string) error {
	if currentPolicy().AllowExec {
		return nil
	}
	if c := cfg.Contexts[ctxName]; c != nil {
		if ai := cfg.AuthInfos[c.AuthInfo]; ai != nil && ai.Exec != nil {
			return ErrExecForbidden
		}
	}
	return nil
}

// OIDCCredentials — логин и пароль, которые пользователь вводит для oidc-login с паролем.
type OIDCCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// oidcSecret — всё, что нужно для входа в IdP; хранится целиком зашифрованным. В API и журнал не попадает.
type oidcSecret struct {
	IssuerURL    string   `json:"issuerUrl"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	Username     string   `json:"username"`
	Password     string   `json:"password"`
}

func newOIDCSecret(p oidcpassword.ExecParams, cred OIDCCredentials) (*oidcSecret, error) {
	// Адрес IdP проверяется политикой адресов до сохранения (литерал IP, схема); имена проверяет соединение.
	if err := netpolicy.For(netpolicy.ScopeClusterAPI).CheckURL(p.IssuerURL); err != nil {
		return nil, err
	}
	cred.Username = strings.TrimSpace(cred.Username)
	if cred.Username == "" || cred.Password == "" {
		return nil, errors.New("login and password are required")
	}
	u, err := url.Parse(p.IssuerURL)
	if err != nil || u.Host == "" || u.Scheme != "https" {
		// Сервер сам ходит по этому адресу: только https, чтобы пароль не уходил открытым текстом.
		return nil, errors.New("the identity provider address must be an https URL")
	}
	return &oidcSecret{IssuerURL: p.IssuerURL, ClientID: p.ClientID, ClientSecret: p.ClientSecret,
		Scopes: p.Scopes, Username: cred.Username, Password: cred.Password}, nil
}

func (s *oidcSecret) source() *oidcpassword.Source {
	return oidcpassword.New(oidcpassword.Config{
		IssuerURL: s.IssuerURL, ClientID: s.ClientID, ClientSecret: s.ClientSecret,
		Username: s.Username, Password: s.Password, Scopes: append([]string{"openid"}, s.Scopes...),
		HTTPClient: idpHTTPClient(),
	})
}

// modeClusterStore — возможности хранилища, нужные режимам in-cluster и oidc-password: секрет подключения
// (шифруется хранилищем) и кластер без kubeconfig. Файловое хранилище десктопа их не имеет.
type modeClusterStore interface {
	// SaveWithSecret — Save с секретом: secret != nil заменяет прежний, nil оставляет.
	SaveWithSecret(meta *ClusterMeta, kubeconfig, secret []byte) error
	// Secret возвращает секрет подключения; nil — его нет.
	Secret(id string) ([]byte, error)
}

var errModeUnsupported = errors.New("this storage cannot keep connection secrets: use the PostgreSQL storage")

func modeStore() (modeClusterStore, error) {
	ms, ok := clusterStore.(modeClusterStore)
	if !ok {
		return nil, errModeUnsupported
	}
	return ms, nil
}

// InClusterAvailable — процесс запущен внутри Kubernetes и может подключиться к его API.
func InClusterAvailable() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != "" && os.Getenv("KUBERNETES_SERVICE_PORT") != ""
}

// AddClusterOIDCToTeam добавляет кластер, kubeconfig которого использует oidc-login с паролем: сервер сам получает
// токен у провайдера входа. Вход проверяется до сохранения, чтобы неверный пароль не попал в хранилище.
func AddClusterOIDCToTeam(kubeconfigText string, settings ClusterSettings, teamID string, cred OIDCCredentials) (*ClusterMeta, error) {
	if len(kubeconfigText) > maxKubeconfigBytes {
		return nil, errKubeconfigTooLarge
	}
	return addClusterFromData([]byte(kubeconfigText), settings, teamID, &cred)
}

// AddInClusterToTeam добавляет кластер, в котором работает сам сервер. Подключение идёт под ServiceAccount пода,
// поэтому право на это вызывающий проверяет отдельно (администратор организации).
func AddInClusterToTeam(settings ClusterSettings, teamID string) (*ClusterMeta, error) {
	if !InClusterAvailable() {
		return nil, errors.New("the server is not running inside a Kubernetes cluster")
	}
	ms, err := modeStore()
	if err != nil {
		return nil, err
	}
	settings, err = settings.normalize("")
	if err != nil {
		return nil, err
	}
	id := uniqueClusterID(settings.DisplayName)
	meta := &ClusterMeta{
		ID: id, DisplayName: settings.DisplayName, Icon: settings.Icon, Color: settings.Color,
		Environment: settings.Environment, DefaultNamespace: settings.DefaultNamespace, ArgoURL: settings.ArgoURL,
		AddedAt: time.Now().Format(time.RFC3339), TeamID: teamID, Mode: ModeInCluster,
	}
	if err := ms.SaveWithSecret(meta, nil, nil); err != nil {
		return nil, err
	}
	return meta, nil
}

// SetClusterOIDCCredentials меняет логин и пароль кластера в режиме oidc-password (сменили пароль в IdP).
// Вход проверяется до записи; действующее подключение забывает вызывающий.
func SetClusterOIDCCredentials(id string, cred OIDCCredentials) error {
	meta, err := LoadClusterMeta(id)
	if err != nil {
		return fmt.Errorf("cluster not found: %w", err)
	}
	if meta.Mode != ModeOIDCPassword {
		return errors.New("the cluster does not use oidc-password")
	}
	ms, err := modeStore()
	if err != nil {
		return err
	}
	raw, err := ms.Secret(id)
	if err != nil || raw == nil {
		return errors.New("the cluster has no stored connection settings")
	}
	var old oidcSecret
	if err := json.Unmarshal(raw, &old); err != nil {
		return errors.New("stored connection settings are damaged")
	}
	sec, err := newOIDCSecret(oidcpassword.ExecParams{IssuerURL: old.IssuerURL, ClientID: old.ClientID,
		ClientSecret: old.ClientSecret, Scopes: old.Scopes}, cred)
	if err != nil {
		return err
	}
	if err := verifyOIDCLogin(sec); err != nil {
		return err
	}
	data, err := json.Marshal(sec)
	if err != nil {
		return err
	}
	return ms.SaveWithSecret(meta, nil, data)
}

// verifyOIDCLogin входит в провайдер входа с введёнными данными. Текст ошибки провайдера пароля не содержит.
func verifyOIDCLogin(sec *oidcSecret) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, _, err := sec.source().Token(ctx); err != nil {
		return fmt.Errorf("sign-in to the identity provider failed: %w", err)
	}
	return nil
}

// buildModeConfig строит rest.Config для кластеров не в режиме kubeconfig; ok=false — режим обычный.
func buildModeConfig(clusterID string, meta *ClusterMeta) (cfg *rest.Config, ok bool, err error) {
	switch meta.Mode {
	case "", ModeKubeconfig:
		return nil, false, nil
	case ModeInCluster:
		cfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, true, fmt.Errorf("in-cluster configuration: %w", err)
		}
		return cfg, true, nil
	case ModeOIDCPassword:
		cfg, err = oidcRestConfig(clusterID, meta)
		return cfg, true, err
	}
	return nil, true, fmt.Errorf("unknown connection mode %q", meta.Mode)
}

func oidcRestConfig(clusterID string, meta *ClusterMeta) (*rest.Config, error) {
	ms, err := modeStore()
	if err != nil {
		return nil, err
	}
	raw, err := ms.Secret(clusterID)
	if err != nil || raw == nil {
		return nil, errors.New("the cluster has no stored sign-in settings")
	}
	var sec oidcSecret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return nil, errors.New("stored sign-in settings are damaged")
	}
	data, err := clusterStore.Kubeconfig(clusterID)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig not found for cluster %s", clusterID)
	}
	kc, err := clientcmd.Load(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	ctxName := meta.ContextName
	if ctxName == "" {
		if ctxName, err = pickContext(kc, ""); err != nil {
			return nil, err
		}
	}
	if err := CheckKubeconfigPolicy(kc, ctxName); err != nil {
		return nil, err
	}
	// Блок exec больше не нужен и не должен запускаться: токен выдаёт сам сервер.
	if ctx := kc.Contexts[ctxName]; ctx != nil {
		if ai := kc.AuthInfos[ctx.AuthInfo]; ai != nil {
			ai.Exec = nil
		}
	}
	rc, err := clientcmd.NewNonInteractiveClientConfig(*kc, ctxName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("configuration error: %w", err)
	}
	src := sec.source()
	rc.Wrap(src.RoundTripper)
	return rc, nil
}

// uniqueClusterID выбирает свободный идентификатор по названию.
func uniqueClusterID(displayName string) string {
	id := sanitizeID(displayName)
	if id == "" {
		id = fmt.Sprintf("cluster-%d", time.Now().Unix())
	}
	if existing, _ := LoadClusterMeta(id); existing != nil {
		id = fmt.Sprintf("%s-%d", id, time.Now().Unix())
	}
	return id
}

// checkStoredPolicy применяет политику к сохранённому kubeconfig кластера.
func checkStoredPolicy(clusterID string, meta *ClusterMeta) error {
	var data []byte
	if kcPath := GetClusterKubeconfigPath(clusterID); kcPath != "" {
		var err error
		if data, err = os.ReadFile(kcPath); err != nil {
			return fmt.Errorf("failed to read kubeconfig: %w", err)
		}
	} else {
		var err error
		if data, err = clusterStore.Kubeconfig(clusterID); err != nil {
			return fmt.Errorf("kubeconfig not found for cluster %s", clusterID)
		}
	}
	cfg, err := clientcmd.Load(data)
	if err != nil {
		return fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	ctxName := meta.ContextName
	if ctxName == "" {
		if ctxName, err = pickContext(cfg, ""); err != nil {
			return err
		}
	}
	if err := CheckKubeconfigPolicy(cfg, ctxName); err != nil {
		return err
	}
	return checkExecAllowed(cfg, ctxName)
}
