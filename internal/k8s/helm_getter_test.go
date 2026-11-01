// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"helm.sh/helm/v4/pkg/action"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// fakeKubeAPI — минимальный API-сервер по TLS: версия, обнаружение API и ConfigMap (в них драйвер configmap
// хранит релизы Helm). Запоминает заголовки Authorization всех запросов.
type fakeKubeAPI struct {
	srv *httptest.Server

	mu   sync.Mutex
	cms  map[string]*corev1.ConfigMap // "namespace/имя"
	auth []string
}

func newFakeKubeAPI(t *testing.T) *fakeKubeAPI {
	t.Helper()
	f := &fakeKubeAPI{cms: map[string]*corev1.ConfigMap{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeKubeAPI) caPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw})
}

func (f *fakeKubeAPI) authHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

func (f *fakeKubeAPI) releaseRecords() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, cm := range f.cms {
		if cm.Labels["owner"] == "helm" {
			n++
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter, what string) {
	writeJSON(w, http.StatusNotFound, metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure, Reason: metav1.StatusReasonNotFound, Code: http.StatusNotFound, Message: what + " not found",
	})
}

func (f *fakeKubeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.mu.Unlock()

	switch p := r.URL.Path; {
	case p == "/version":
		writeJSON(w, 200, version.Info{Major: "1", Minor: "33", GitVersion: "v1.33.0"})
	case p == "/api":
		writeJSON(w, 200, metav1.APIVersions{TypeMeta: metav1.TypeMeta{Kind: "APIVersions"}, Versions: []string{"v1"}})
	case p == "/apis":
		writeJSON(w, 200, metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"}})
	case p == "/api/v1":
		verbs := metav1.Verbs{"get", "list", "create", "update", "delete"}
		writeJSON(w, 200, metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList"}, GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "configmaps", Namespaced: true, Kind: "ConfigMap", Verbs: verbs},
				{Name: "secrets", Namespaced: true, Kind: "Secret", Verbs: verbs},
				{Name: "namespaces", Namespaced: false, Kind: "Namespace", Verbs: verbs},
			}})
	case strings.HasPrefix(p, "/api/v1/namespaces/") && strings.Contains(p, "/configmaps"):
		f.serveConfigMaps(w, r)
	default:
		notFound(w, p)
	}
}

func (f *fakeKubeAPI) serveConfigMaps(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/")
	ns, tail, _ := strings.Cut(rest, "/configmaps")
	name := strings.TrimPrefix(tail, "/")
	key := ns + "/" + name

	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && name == "":
		list := corev1.ConfigMapList{TypeMeta: metav1.TypeMeta{Kind: "ConfigMapList", APIVersion: "v1"}}
		sel := r.URL.Query().Get("labelSelector")
		for k, cm := range f.cms {
			if strings.HasPrefix(k, ns+"/") && matchSelector(cm.Labels, sel) {
				list.Items = append(list.Items, *cm)
			}
		}
		writeJSON(w, 200, list)
	case r.Method == http.MethodPost:
		cm, err := decodeConfigMap(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		cm.Namespace = ns
		cm.TypeMeta = metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"}
		f.cms[ns+"/"+cm.Name] = cm
		writeJSON(w, 201, cm)
	case r.Method == http.MethodGet:
		if cm, ok := f.cms[key]; ok {
			writeJSON(w, 200, cm)
			return
		}
		notFound(w, key)
	case r.Method == http.MethodPut:
		cm, err := decodeConfigMap(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		cm.Namespace = ns
		cm.TypeMeta = metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"}
		f.cms[key] = cm
		writeJSON(w, 200, cm)
	case r.Method == http.MethodDelete:
		cm, ok := f.cms[key]
		if !ok {
			notFound(w, key)
			return
		}
		delete(f.cms, key)
		writeJSON(w, 200, cm)
	default:
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}
}

// decodeConfigMap читает тело запроса в любом из форматов клиента (JSON или protobuf).
func decodeConfigMap(r *http.Request) (*corev1.ConfigMap, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(body, nil, &corev1.ConfigMap{})
	if err != nil {
		return nil, err
	}
	return obj.(*corev1.ConfigMap), nil
}

// matchSelector понимает только то, что нужно драйверу Helm: «k=v» через запятую (прочие условия пропускает).
func matchSelector(labels map[string]string, sel string) bool {
	for _, term := range strings.Split(sel, ",") {
		if k, v, ok := strings.Cut(term, "="); ok && !strings.ContainsAny(k, " !") && labels[k] != v {
			return false
		}
	}
	return true
}

// fakeIdP — IdP с password и refresh_token. Токены живут меньше refreshSkew (30 с), поэтому каждый запрос к API
// требует обновления: так проверяется, что Helm ходит через общий WrapTransport подключения.
type fakeIdP struct {
	srv       *httptest.Server
	passwordN atomic.Int32
	refreshN  atomic.Int32
	issued    atomic.Int32
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	f := &fakeIdP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"issuer": f.srv.URL, "token_endpoint": f.srv.URL + "/token"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "password":
			f.passwordN.Add(1)
			if r.Form.Get("username") != "alice" || r.Form.Get("password") != "s3cret" {
				writeJSON(w, 401, map[string]string{"error": "invalid_grant"})
				return
			}
		case "refresh_token":
			f.refreshN.Add(1)
		default:
			w.WriteHeader(400)
			return
		}
		n := f.issued.Add(1)
		enc := func(v any) string {
			b, _ := json.Marshal(v)
			return base64.RawURLEncoding.EncodeToString(b)
		}
		idToken := enc(map[string]string{"alg": "none"}) + "." + enc(map[string]any{"exp": time.Now().Add(10 * time.Second).Unix(), "n": n}) + ".sig"
		writeJSON(w, 200, map[string]any{"id_token": idToken, "refresh_token": "refresh-" + string(rune('a'+n)), "expires_in": 10})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// modeTestStore — хранилище в памяти с секретами подключения, как у PostgreSQL (modeClusterStore), и без файлов.
type modeTestStore struct {
	*memClusterStore
	secrets map[string][]byte
}

func newModeTestStore() *modeTestStore {
	return &modeTestStore{memClusterStore: newMemClusterStore(), secrets: map[string][]byte{}}
}

func (s *modeTestStore) SaveWithSecret(meta *ClusterMeta, kubeconfig, secret []byte) error {
	if err := s.Save(meta, kubeconfig); err != nil {
		return err
	}
	if secret != nil {
		s.mu.Lock()
		s.secrets[meta.ID] = append([]byte(nil), secret...)
		s.mu.Unlock()
	}
	return nil
}

func (s *modeTestStore) Secret(id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secrets[id], nil
}

func kubeconfigFor(t *testing.T, api *fakeKubeAPI, ai *clientcmdapi.AuthInfo) []byte {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["c"] = &clientcmdapi.Cluster{Server: api.srv.URL, CertificateAuthorityData: api.caPEM()}
	cfg.AuthInfos["u"] = ai
	cfg.Contexts["ctx"] = &clientcmdapi.Context{Cluster: "c", AuthInfo: "u"}
	cfg.CurrentContext = "ctx"
	data, err := clientcmd.Write(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// managerWithConn подключает кластер тем же путём, что и приложение (buildClusterConnection), и делает его активным.
func managerWithConn(t *testing.T, id string) *Manager {
	t.Helper()
	conn, err := buildClusterConnection(id)
	if err != nil {
		t.Fatal(err)
	}
	return managerWithBuiltConn(id, conn)
}

func managerWithBuiltConn(id string, conn *ClusterConnection) *Manager {
	m := NewManager()
	m.pool.closeFn = func(*ClusterConnection) {}
	m.pool.put(id, conn)
	m.activeCtx = id
	return m
}

// installEmptyChart ставит чарт без ресурсов: в кластере появляется только запись релиза (ConfigMap драйвера).
func installEmptyChart(t *testing.T, cfg *action.Configuration, ns, name string) {
	t.Helper()
	in := action.NewInstall(cfg)
	in.ReleaseName, in.Namespace = name, ns
	in.WaitStrategy = kube.HookOnlyStrategy
	in.Timeout = 30 * time.Second
	chrt := &chartv2.Chart{Metadata: &chartv2.Metadata{APIVersion: "v2", Name: "demo", Version: "0.1.0"}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := in.RunWithContext(ctx, chrt, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
}

func listReleases(t *testing.T, cfg *action.Configuration) int {
	t.Helper()
	l := action.NewList(cfg)
	l.All = true
	rels, err := l.Run()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return len(rels)
}

// Установка, список и удаление работают для кластера из хранилища без файлов в режимах kubeconfig и in-cluster.
func TestHelmConfigForClustersWithoutKubeconfigFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("sa-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		mode      string
		wantToken string
		build     func(t *testing.T, store *modeTestStore, api *fakeKubeAPI) *Manager
	}{
		{"kubeconfig", ModeKubeconfig, "Bearer kc-token", func(t *testing.T, store *modeTestStore, api *fakeKubeAPI) *Manager {
			if err := store.Save(&ClusterMeta{ID: "c1", DisplayName: "c1", ContextName: "ctx", Mode: ModeKubeconfig},
				kubeconfigFor(t, api, &clientcmdapi.AuthInfo{Token: "kc-token"})); err != nil {
				t.Fatal(err)
			}
			return managerWithConn(t, "c1")
		}},
		{"in-cluster", ModeInCluster, "Bearer sa-token", func(t *testing.T, store *modeTestStore, api *fakeKubeAPI) *Manager {
			// rest.InClusterConfig() читает токен и CA из /var/run/secrets, которых в тесте нет: собираем тот же вид
			// конфига — токен из файла (его client-go перечитывает) и CA сервера.
			if err := store.SaveWithSecret(&ClusterMeta{ID: "c1", DisplayName: "c1", Mode: ModeInCluster}, nil, nil); err != nil {
				t.Fatal(err)
			}
			conn := &ClusterConnection{Name: "c1", Config: &rest.Config{
				Host: api.srv.URL, BearerTokenFile: tokenFile, TLSClientConfig: rest.TLSClientConfig{CAData: api.caPEM()},
			}}
			return managerWithBuiltConn("c1", conn)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := newFakeKubeAPI(t)
			store := newModeTestStore()
			useClusterStore(t, store)
			if GetClusterKubeconfigPath("c1") != "" {
				t.Fatal("the test store must not be file-based")
			}
			m := c.build(t, store, api)

			cfg, err := m.helmConfigDriver("apps", nil, "configmap")
			if err != nil {
				t.Fatalf("helmConfigDriver: %v", err)
			}
			installEmptyChart(t, cfg, "apps", "demo")
			if api.releaseRecords() != 1 {
				t.Fatalf("expected one release record in the cluster, got %d", api.releaseRecords())
			}
			if n := listReleases(t, cfg); n != 1 {
				t.Errorf("list: got %d releases, want 1", n)
			}
			// Каждый запрос Helm должен нести учётные данные подключения.
			for _, h := range api.authHeaders() {
				if h != c.wantToken {
					t.Fatalf("a request went out with Authorization %q, want %q", h, c.wantToken)
				}
			}
			un := action.NewUninstall(cfg)
			un.WaitStrategy = kube.HookOnlyStrategy
			if _, err := un.Run("demo"); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if n := listReleases(t, cfg); n != 0 {
				t.Errorf("after uninstall: got %d releases, want 0", n)
			}
		})
	}
}

// oidc-password: токен выдаёт и обновляет сервер (WrapTransport подключения); Helm должен пользоваться тем же
// источником токена, а не копией kubeconfig. Токены в тесте живут 10 с (< 30 с запаса), то есть обновляются на каждом запросе.
func TestHelmOIDCPasswordRefreshesToken(t *testing.T) {
	api := newFakeKubeAPI(t)
	idp := newFakeIdP(t)
	store := newModeTestStore()
	useClusterStore(t, store)

	login := &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "kubectl", APIVersion: "client.authentication.k8s.io/v1",
		Args: []string{"oidc-login", "get-token", "--oidc-issuer-url=" + idp.srv.URL, "--oidc-client-id=kubernetes", "--grant-type=password"}}}
	secret, _ := json.Marshal(oidcSecret{IssuerURL: idp.srv.URL, ClientID: "kubernetes", Username: "alice", Password: "s3cret"})
	if err := store.SaveWithSecret(&ClusterMeta{ID: "c1", DisplayName: "c1", ContextName: "ctx", Mode: ModeOIDCPassword},
		kubeconfigFor(t, api, login), secret); err != nil {
		t.Fatal(err)
	}
	m := managerWithConn(t, "c1")

	cfg, err := m.helmConfigDriver("apps", nil, "configmap")
	if err != nil {
		t.Fatal(err)
	}
	installEmptyChart(t, cfg, "apps", "demo")
	if n := listReleases(t, cfg); n != 1 {
		t.Fatalf("got %d releases, want 1", n)
	}

	distinct := map[string]bool{}
	for _, h := range api.authHeaders() {
		if !strings.HasPrefix(h, "Bearer ") || len(h) < 20 {
			t.Fatalf("a request went out without the id_token: %q", h)
		}
		distinct[h] = true
	}
	if idp.passwordN.Load() != 1 {
		t.Errorf("the password grant must run once, got %d", idp.passwordN.Load())
	}
	if idp.refreshN.Load() == 0 || len(distinct) < 2 {
		t.Errorf("the token must be refreshed while Helm works: refresh grants=%d, distinct tokens=%d", idp.refreshN.Load(), len(distinct))
	}
}

// Личный режим: у кластера есть файл kubeconfig — Helm читает его, как и раньше.
type pathStore struct {
	*memClusterStore
	path string
}

func (s pathStore) KubeconfigPath(id string) string { return s.path }

func TestHelmKeepsFileKubeconfigInPersonalMode(t *testing.T) {
	store := pathStore{memClusterStore: newMemClusterStore(), path: "/tmp/kubron-test.kubeconfig"}
	useClusterStore(t, store)
	meta := &ClusterMeta{ID: "c1", DisplayName: "c1", ContextName: "dev"}
	if err := store.Save(meta, []byte("x")); err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	getter, err := m.helmRESTClientGetter("c1", meta, "apps")
	if err != nil {
		t.Fatal(err)
	}
	flags, ok := getter.(*genericclioptions.ConfigFlags)
	if !ok {
		t.Fatalf("a cluster with a kubeconfig file must use ConfigFlags, got %T", getter)
	}
	if *flags.KubeConfig != store.path || *flags.Namespace != "apps" || *flags.Context != "dev" {
		t.Errorf("flags: kubeconfig=%q namespace=%q context=%q", *flags.KubeConfig, *flags.Namespace, *flags.Context)
	}
}

func TestHelmRESTClientGetterWithoutConnection(t *testing.T) {
	useClusterStore(t, newModeTestStore())
	meta := &ClusterMeta{ID: "c1", Mode: ModeInCluster}
	if _, err := NewManager().helmRESTClientGetter("c1", meta, "apps"); err == nil || !strings.Contains(err.Error(), "no active connection") {
		t.Errorf("expected the 'no active connection' error, got %v", err)
	}
}

func TestRESTConfigGetter(t *testing.T) {
	var wrapped atomic.Int32
	base := &rest.Config{Host: "https://k8s.example:6443", BearerToken: "very-secret-token"}
	base.Wrap(func(rt http.RoundTripper) http.RoundTripper { wrapped.Add(1); return rt })
	g := newRESTConfigGetter(base, "apps")

	c1, _ := g.ToRESTConfig()
	c2, _ := g.ToRESTConfig()
	if c1 == base || c1 == c2 {
		t.Error("callers must receive their own copy of the connection config")
	}
	c1.Host, c1.Burst = "https://changed", 1
	if base.Host != "https://k8s.example:6443" || base.Burst == 1 {
		t.Error("changes to a copy must not reach the connection config")
	}
	if c2.WrapTransport == nil {
		t.Fatal("WrapTransport (the token source of oidc-password) must survive the copy")
	}
	c2.WrapTransport(http.DefaultTransport)
	if wrapped.Load() != 1 {
		t.Error("the copy must call the same transport wrapper as the connection")
	}

	loader := g.ToRawKubeConfigLoader()
	if ns, overridden, err := loader.Namespace(); err != nil || ns != "apps" || !overridden {
		t.Errorf("Namespace() = %q, %v, %v", ns, overridden, err)
	}
	if cc, err := loader.ClientConfig(); err != nil || cc.BearerToken != "very-secret-token" {
		t.Errorf("ClientConfig must return the real connection config: %v %v", cc, err)
	}
	raw, _ := loader.RawConfig()
	text, err := clientcmd.Write(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"very-secret-token"} {
		if strings.Contains(string(text), secret) {
			t.Errorf("the raw kubeconfig leaks %q", secret)
		}
	}
	if raw.Contexts[raw.CurrentContext].Namespace != "apps" {
		t.Error("the raw kubeconfig must carry the release namespace")
	}
	acc := loader.ConfigAccess()
	if acc.GetDefaultFilename() != "" || acc.IsExplicitFile() || len(acc.GetLoadingPrecedence()) != 0 {
		t.Error("nothing may be read or written on disk")
	}

	if n, _, _ := newRESTConfigGetter(base, "").ToRawKubeConfigLoader().Namespace(); n != "default" {
		t.Errorf("empty namespace must become default, got %q", n)
	}
	d1, err := g.ToDiscoveryClient()
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := g.ToDiscoveryClient()
	if d1 != d2 {
		t.Error("the discovery client must be cached within one operation")
	}
	if m, err := g.ToRESTMapper(); err != nil || m == nil {
		t.Errorf("ToRESTMapper: %v %v", m, err)
	}
}
