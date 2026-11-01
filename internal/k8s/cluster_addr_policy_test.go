// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/24DSO-labs/kubron/internal/netpolicy"
	"github.com/24DSO-labs/kubron/internal/oidcpassword"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func useClusterAddrPolicy(t *testing.T, profile netpolicy.Profile, allow string) {
	t.Helper()
	prefixes, err := netpolicy.ParseAllowList(allow)
	if err != nil {
		t.Fatal(err)
	}
	netpolicy.Set(netpolicy.ScopeClusterAPI, netpolicy.New("cluster-api", profile, prefixes))
	t.Cleanup(func() { netpolicy.Set(netpolicy.ScopeClusterAPI, nil) })
}

func cfgWithServer(server, proxy string) *clientcmdapi.Config {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["c"] = &clientcmdapi.Cluster{Server: server, ProxyURL: proxy}
	cfg.AuthInfos["u"] = &clientcmdapi.AuthInfo{Token: "t"}
	cfg.Contexts["ctx"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u"}
	cfg.CurrentContext = "ctx"
	return cfg
}

// Адрес API-сервера в kubeconfig: IP-литералы в любой записи и схемы проверяются до соединения.
func TestCheckKubeconfigPolicyChecksServerAddress(t *testing.T) {
	cases := []struct {
		server     string
		permissive bool // true — разрешён в профиле permissive
		strict     bool
	}{
		{"https://203.0.113.10:6443", true, true},
		{"https://api.cluster.example:6443", true, true}, // имя: решит проверка на соединении
		{"https://10.0.0.5:6443", true, false},
		{"10.0.0.5:6443", true, false}, // без схемы, clientcmd добавит https
		{"https://192.168.1.10", true, false},
		{"https://127.0.0.1:6443", false, false},
		{"https://[::1]:6443", false, false},
		{"https://2130706433:6443", false, false},
		{"https://0177.0.0.1:6443", false, false},
		{"https://169.254.169.254", false, false},
		{"https://[fd00:ec2::254]", false, false},
		{"https://[::ffff:169.254.169.254]", false, false},
		{"file:///etc/passwd", false, false},
		{"gopher://203.0.113.10:70", false, false},
	}
	for _, c := range cases {
		for _, p := range []struct {
			profile netpolicy.Profile
			want    bool
		}{{netpolicy.ProfilePermissive, c.permissive}, {netpolicy.ProfileStrict, c.strict}} {
			useClusterAddrPolicy(t, p.profile, "")
			err := CheckKubeconfigPolicy(cfgWithServer(c.server, ""), "ctx", false)
			if (err == nil) != p.want {
				t.Errorf("%s in %s: err=%v, allowed want %v", c.server, p.profile, err, p.want)
			}
		}
	}
	// Выключенная политика ничего не проверяет.
	netpolicy.Set(netpolicy.ScopeClusterAPI, nil)
	if err := CheckKubeconfigPolicy(cfgWithServer("https://127.0.0.1:6443", ""), "ctx", false); err != nil {
		t.Errorf("without a policy any address passes: %v", err)
	}
}

func TestCheckKubeconfigPolicyRejectsProxyURL(t *testing.T) {
	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "")
	err := CheckKubeconfigPolicy(cfgWithServer("https://203.0.113.10:6443", "http://proxy.internal:3128"), "ctx", false)
	if err != ErrClusterProxyForbidden {
		t.Errorf("proxy-url must be rejected: %v", err)
	}
	netpolicy.Set(netpolicy.ScopeClusterAPI, nil)
	if err := CheckKubeconfigPolicy(cfgWithServer("https://203.0.113.10:6443", "http://proxy.internal:3128"), "ctx", false); err != nil {
		t.Errorf("without a policy proxy-url stays allowed: %v", err)
	}
}

func clusterWithFakeAPI(t *testing.T) (*fakeKubeAPI, string) {
	t.Helper()
	api := newFakeKubeAPI(t)
	store := newMemClusterStore()
	if err := store.Save(&ClusterMeta{ID: "c1", DisplayName: "c1", ContextName: "ctx", Mode: ModeKubeconfig},
		kubeconfigFor(t, api, &clientcmdapi.AuthInfo{Token: "kc-token"})); err != nil {
		t.Fatal(err)
	}
	useClusterStore(t, store)
	return api, "c1"
}

// Соединения подключения (REST, discovery, Helm) идут через политику: loopback не открывается даже по верному
// kubeconfig, а исключение оператора и прокси из окружения ведут себя предсказуемо.
func TestClusterConnectionHonorsAddressPolicy(t *testing.T) {
	api, id := clusterWithFakeAPI(t)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1") // прокси из окружения при включённой политике не используется

	// Без политики подключение строится как обычно.
	if _, err := buildClusterConnection(id); err != nil {
		t.Fatal(err)
	}

	api.mu.Lock()
	api.auth = nil
	api.mu.Unlock()

	// permissive без исключений: loopback закрыт, на сервер ничего не доходит, а Helm наследует то же.
	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "")
	if _, err := buildClusterConnection(id); err == nil {
		t.Fatal("the stored kubeconfig must be checked against the policy when connecting")
	}

	// Исключение оператора для адреса тестового API: работает, прокси из окружения игнорируется.
	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "127.0.0.0/8")
	conn, err := buildClusterConnection(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.DiscoveryClient.ServerVersion(); err != nil {
		t.Fatalf("an allowed address must work, and the environment proxy must be ignored: %v", err)
	}
	if len(api.authHeaders()) == 0 {
		t.Error("the request must reach the API server")
	}
}

// Адрес, прошедший статическую проверку по имени, но резолвящийся во внутренний, закрывается на соединении.
func TestClusterConnectionBlocksAtDialTime(t *testing.T) {
	api, _ := clusterWithFakeAPI(t)
	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "")

	rc := newRESTConfigGetter(mustRESTConfig(t, api), "ns")
	applyClusterAddrPolicy(rc.cfg)
	d, err := rc.ToDiscoveryClient()
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.ServerVersion()
	if err == nil || !netpolicy.IsBlocked(err) {
		t.Fatalf("the dial to loopback must be blocked: %v", err)
	}
	if len(api.authHeaders()) != 0 {
		t.Error("the API server must not receive the request")
	}
	// Helm получает копию конфига вместе с дайлером политики.
	conn := &ClusterConnection{Name: "c1", Config: rc.cfg}
	d2, _ := newRESTConfigGetter(conn.helmRESTConfig(), "ns").ToDiscoveryClient()
	if _, err := d2.ServerVersion(); !netpolicy.IsBlocked(err) {
		t.Errorf("Helm must inherit the policy: %v", err)
	}
}

func TestCheckKubeconfigRespectsAddressPolicy(t *testing.T) {
	api := newFakeKubeAPI(t)
	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "")
	res, err := CheckKubeconfig(KubeconfigSource{Text: string(kubeconfigFor(t, api, &clientcmdapi.AuthInfo{Token: "t"}))}, "ctx")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Error == "" {
		t.Errorf("the test connection must refuse a loopback address: %+v", res)
	}
	if len(api.authHeaders()) != 0 {
		t.Error("the API server must not receive the request")
	}
}

// Терминал и port-forward открывают соединение через политику (стандартный SPDY-дайлер её не знает).
func TestSPDYTransportUsesAddressPolicy(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	rc := mustRESTConfigFor(srv.URL)

	if _, up, err := spdyRoundTripperFor(rc); err != nil || up == nil {
		t.Fatalf("without a policy the standard transport is used: %v", err)
	}

	roundTrip := func() error {
		_, upgrader, err := spdyRoundTripperFor(rc)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "SPDY/3.1")
		_, err = upgrader.(http.RoundTripper).RoundTrip(req)
		return err
	}

	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "")
	if err := roundTrip(); err == nil || !netpolicy.IsBlocked(err) {
		t.Errorf("the SPDY dial to loopback must be blocked: %v", err)
	}
	if hits.Load() != 0 {
		t.Error("the server must not be reached")
	}
	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "127.0.0.0/8")
	_ = roundTrip() // сервер не говорит по SPDY, ошибка апгрейда ожидаема; важно, что соединение открыто
	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() == 0 {
		t.Error("an allowed address must be reachable through the SPDY transport")
	}
}

func mustRESTConfig(t *testing.T, api *fakeKubeAPI) *rest.Config {
	t.Helper()
	return &rest.Config{Host: api.srv.URL, BearerToken: "t", TLSClientConfig: rest.TLSClientConfig{CAData: api.caPEM()}}
}

func mustRESTConfigFor(host string) *rest.Config { return &rest.Config{Host: host, BearerToken: "t"} }

// Любое соединение с API-сервером кластера должно идти через политику адреса. Стандартные SPDY- и WebSocket-дайлеры
// client-go rest.Config.Dial не учитывают, поэтому их прямые вызовы разрешены только в cluster_addr_policy.go
// (spdyRoundTripperFor и spdyExecutorFor). Тест не даёт новому коду (терминал, port-forward, прокси) обойти защиту.
func TestNoDirectSPDYOrWebSocketDialers(t *testing.T) {
	forbidden := []string{
		"spdy.RoundTripperFor(", "remotecommand.NewSPDYExecutor(", "remotecommand.NewSPDYExecutorForTransports(",
		"remotecommand.NewWebSocketExecutor", "remotecommand.NewFallbackExecutor(", "websocket.RoundTripperFor(",
		"portforward.NewSPDYOverWebsocketDialer(", "portforward.NewFallbackDialer(",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "cluster_addr_policy.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(data), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") {
				continue
			}
			for _, f := range forbidden {
				if strings.Contains(code, f) {
					t.Errorf("%s:%d calls %s directly: use spdyRoundTripperFor/spdyExecutorFor so that the cluster address policy applies", name, n+1, f)
				}
			}
		}
	}
}

// Адрес IdP (issuer из kubeconfig пользователя) и token_endpoint из ответа самого IdP — тот же SSRF-вектор, что и
// адрес API-сервера: запросы к IdP идут через политику.
func TestIdPRequestsHonorAddressPolicy(t *testing.T) {
	idp := newFakeIdP(t)
	sec := &oidcSecret{IssuerURL: idp.srv.URL, ClientID: "kubernetes", Username: "alice", Password: "s3cret"}
	token := func() error { _, _, err := sec.source().Token(t.Context()); return err }

	if err := token(); err != nil {
		t.Fatalf("without a policy the IdP works: %v", err)
	}

	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "")
	idp.passwordN.Store(0)
	if err := token(); err == nil || !netpolicy.IsBlocked(err) {
		t.Errorf("an IdP on loopback must be blocked: %v", err)
	}
	if idp.passwordN.Load() != 0 {
		t.Error("the IdP must not receive the request")
	}

	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "127.0.0.0/8")
	if err := token(); err != nil {
		t.Errorf("an allowed IdP must work: %v", err)
	}

	// Недоверенный IdP подсовывает в discovery внутренний token_endpoint.
	var metaHits atomic.Int32
	var evil *httptest.Server
	evil = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metaHits.Add(1)
		writeJSON(w, 200, map[string]string{"issuer": evil.URL, "token_endpoint": "http://169.254.169.254/latest/api/token"})
	}))
	defer evil.Close()
	bad := &oidcSecret{IssuerURL: evil.URL, ClientID: "k", Username: "u", Password: "p"}
	if _, _, err := bad.source().Token(t.Context()); err == nil || !netpolicy.IsBlocked(err) || netpolicy.BlockReason(err) != netpolicy.ReasonMetadata {
		t.Errorf("a token_endpoint on the metadata address must be blocked: %v", err)
	}
	if metaHits.Load() != 1 {
		t.Errorf("only the discovery request must reach the IdP: %d", metaHits.Load())
	}

	// При сохранении литерал IP проверяется сразу.
	useClusterAddrPolicy(t, netpolicy.ProfilePermissive, "")
	if _, err := newOIDCSecret(oidcpassword.ExecParams{IssuerURL: "https://169.254.169.254/", ClientID: "k"}, OIDCCredentials{Username: "u", Password: "p"}); err == nil {
		t.Error("an issuer on the metadata address must be rejected when saving")
	}
}
