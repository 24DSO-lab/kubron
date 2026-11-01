// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"sync"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// restConfigGetter — genericclioptions.RESTClientGetter поверх готового *rest.Config. Helm ждёт файл kubeconfig,
// а у кластеров из PostgreSQL файла нет: kubeconfig лежит зашифрованным в БД, а у in-cluster и oidc-password его
// вообще нет. Мы НЕ выписываем токены и пароли в статичный kubeconfig или временный файл: у oidc-password
// токен выдаёт и обновляет WrapTransport, он живёт только внутри rest.Config подключения. Поэтому берём копию самого
// конфига (rest.CopyConfig сохраняет WrapTransport, источник токена остаётся общим с подключением).
type restConfigGetter struct {
	cfg       *rest.Config // исходный конфиг подключения; наружу отдаются только копии
	namespace string       // namespace релиза: его Helm берёт из ToRawKubeConfigLoader().Namespace()

	mu        sync.Mutex
	discovery discovery.CachedDiscoveryInterface
	mapper    apimeta.RESTMapper
}

var _ genericclioptions.RESTClientGetter = (*restConfigGetter)(nil)

func newRESTConfigGetter(cfg *rest.Config, namespace string) *restConfigGetter {
	return &restConfigGetter{cfg: cfg, namespace: namespace}
}

// helmRESTConfig — копия rest.Config подключения для Helm. Копия нужна, потому что Helm и kubectl-фабрика
// меняют полученный конфиг (лимиты запросов, UserAgent), а подключение приложения должно остаться прежним.
func (c *ClusterConnection) helmRESTConfig() *rest.Config {
	return rest.CopyConfig(c.Config)
}

func (g *restConfigGetter) ToRESTConfig() (*rest.Config, error) {
	return rest.CopyConfig(g.cfg), nil
}

// ToDiscoveryClient — клиент обнаружения API с кэшем в памяти на время одной операции Helm: каталог
// ~/.kube/cache в сервисе не нужен и общим для команд быть не должен.
func (g *restConfigGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.discoveryLocked()
}

func (g *restConfigGetter) discoveryLocked() (discovery.CachedDiscoveryInterface, error) {
	if g.discovery != nil {
		return g.discovery, nil
	}
	dc, err := discovery.NewDiscoveryClientForConfig(rest.CopyConfig(g.cfg))
	if err != nil {
		return nil, err
	}
	g.discovery = memory.NewMemCacheClient(dc)
	return g.discovery, nil
}

func (g *restConfigGetter) ToRESTMapper() (apimeta.RESTMapper, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.mapper != nil {
		return g.mapper, nil
	}
	dc, err := g.discoveryLocked()
	if err != nil {
		return nil, err
	}
	// Те же короткие имена ресурсов (deploy, svc…), что и в kubectl; предупреждения о них не нужны.
	g.mapper = restmapper.NewShortcutExpander(restmapper.NewDeferredDiscoveryRESTMapper(dc), dc, func(string) {})
	return g.mapper, nil
}

// ToRawKubeConfigLoader отдаёт загрузчик, из которого Helm берёт только namespace по умолчанию. Если кто-то всё же
// запросит из него клиентский конфиг, он получит тот же rest.Config подключения, а не пустой.
func (g *restConfigGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return &connectionClientConfig{g: g}
}

// connectionClientConfig — clientcmd.ClientConfig без файлов: namespace релиза и rest.Config подключения.
type connectionClientConfig struct{ g *restConfigGetter }

func (c *connectionClientConfig) Namespace() (string, bool, error) {
	if c.g.namespace == "" {
		return "default", false, nil
	}
	return c.g.namespace, true, nil
}

func (c *connectionClientConfig) ClientConfig() (*rest.Config, error) {
	return rest.CopyConfig(c.g.cfg), nil
}

// RawConfig описывает кластер без учётных данных: токены и пароли в «сырой» kubeconfig не попадают никогда.
func (c *connectionClientConfig) RawConfig() (clientcmdapi.Config, error) {
	ns, _, _ := c.Namespace()
	raw := clientcmdapi.NewConfig()
	raw.Clusters["cluster"] = &clientcmdapi.Cluster{Server: c.g.cfg.Host}
	raw.AuthInfos["user"] = &clientcmdapi.AuthInfo{}
	raw.Contexts["default"] = &clientcmdapi.Context{Cluster: "cluster", AuthInfo: "user", Namespace: ns}
	raw.CurrentContext = "default"
	return *raw, nil
}

func (c *connectionClientConfig) ConfigAccess() clientcmd.ConfigAccess { return noFileConfigAccess{} }

// noFileConfigAccess — доступ к конфигурации без файлов: Helm и kubectl ничего не должны записывать на диск.
type noFileConfigAccess struct{}

func (noFileConfigAccess) GetLoadingPrecedence() []string { return nil }
func (noFileConfigAccess) GetStartingConfig() (*clientcmdapi.Config, error) {
	return clientcmdapi.NewConfig(), nil
}
func (noFileConfigAccess) GetDefaultFilename() string { return "" }
func (noFileConfigAccess) IsExplicitFile() bool       { return false }
func (noFileConfigAccess) GetExplicitFile() string    { return "" }
