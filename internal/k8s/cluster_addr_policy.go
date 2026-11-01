// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/netpolicy"
	"k8s.io/apimachinery/pkg/util/httpstream"
	httpstreamspdy "k8s.io/apimachinery/pkg/util/httpstream/spdy"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
)

// Политика адреса API-сервера кластера (ADR-0015). Адрес сервера берётся из kubeconfig, который прислал пользователь,
// поэтому в сервисе его можно использовать как SSRF: направить сервер на metadata-сервис или внутреннюю службу.
// Политика (netpolicy.ScopeClusterAPI, профиль permissive: частные сети разрешены, loopback/link-local/метаданные
// запрещены) выставляется из cmd/kubron-server и по умолчанию выключена; десктоп её не выставляет.
// Подключение in-cluster политике не подлежит: адрес API-сервера задаёт сам кластер, а не пользователь.

// ErrClusterProxyForbidden — proxy-url в kubeconfig: запрос ушёл бы на прокси, а проверялся бы только его адрес.
var ErrClusterProxyForbidden = errors.New("proxy-url in the kubeconfig is not allowed while the cluster address policy is enabled")

// checkClusterAddress проверяет адрес API-сервера выбранного контекста до соединения: схему и IP-литерал в любой записи.
// Имена не резолвятся: DNS меняется между проверкой и соединением, окончательно решает проверка на самом соединении
// (applyClusterAddrPolicy). Политика не выставлена — ничего не проверяется.
func checkClusterAddress(cfg *clientcmdapi.Config, ctxName string) error {
	pol := netpolicy.For(netpolicy.ScopeClusterAPI)
	if pol == nil || cfg == nil {
		return nil
	}
	ctx := cfg.Contexts[ctxName]
	if ctx == nil {
		return nil
	}
	cl := cfg.Clusters[ctx.Cluster]
	if cl == nil {
		return nil
	}
	if cl.ProxyURL != "" {
		return ErrClusterProxyForbidden
	}
	server := strings.TrimSpace(cl.Server)
	if server == "" {
		return nil
	}
	if !strings.Contains(server, "://") { // clientcmd принимает «host:port» и сам добавляет https://
		server = "https://" + server
	}
	return pol.CheckURL(server)
}

// applyClusterAddrPolicy направляет все соединения клиента к API-серверу (REST, discovery, informers, Helm) через
// политику и отключает прокси из окружения. Вызывается при построении подключения пользовательского кластера.
func applyClusterAddrPolicy(cfg *rest.Config) {
	pol := netpolicy.For(netpolicy.ScopeClusterAPI)
	if pol == nil || cfg == nil {
		return
	}
	cfg.Dial = pol.DialContext
	cfg.Proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
}

// spdyRoundTripperFor — как spdy.RoundTripperFor, но терминал и port-forward открывают соединение через политику:
// стандартная версия использует собственный дайлер и прокси из окружения и rest.Config.Dial не учитывает.
func spdyRoundTripperFor(config *rest.Config) (http.RoundTripper, spdy.Upgrader, error) {
	pol := netpolicy.For(netpolicy.ScopeClusterAPI)
	if pol == nil {
		return spdy.RoundTripperFor(config)
	}
	tlsConfig, err := rest.TLSConfigFor(config)
	if err != nil {
		return nil, nil, err
	}
	upgrader, err := httpstreamspdy.NewRoundTripperWithConfig(httpstreamspdy.RoundTripperConfig{
		UpgradeTransport: &http.Transport{TLSClientConfig: tlsConfig, DialContext: pol.DialContext},
		PingPeriod:       5 * time.Second,
	})
	if err != nil {
		return nil, nil, err
	}
	wrapper, err := rest.HTTPWrappersForConfig(config, upgrader)
	if err != nil {
		return nil, nil, err
	}
	return wrapper, upgrader, nil
}

// spdyExecutorFor создаёт исполнитель exec: без политики — прежним remotecommand.NewSPDYExecutor, с политикой — через
// spdyRoundTripperFor.
func spdyExecutorFor(config *rest.Config, method string, u *url.URL) (remotecommand.Executor, error) {
	if netpolicy.For(netpolicy.ScopeClusterAPI) == nil {
		return remotecommand.NewSPDYExecutor(config, method, u)
	}
	transport, upgrader, err := spdyRoundTripperFor(config)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewSPDYExecutorForTransports(transport, upgrader, method, u)
}

var _ httpstream.UpgradeRoundTripper = (*httpstreamspdy.SpdyRoundTripper)(nil)
