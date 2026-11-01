// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	argoServerSelector = "app.kubernetes.io/name=argocd-server"
	argoDefaultNS      = "argocd"
	argoParamsConfig   = "argocd-cmd-params-cm"
)

// ArgoUIInfo — куда открыть Argo CD UI в браузере.
type ArgoUIInfo struct {
	// URL — адрес для браузера. В режиме proxy он одноразовый и живёт секунды: открывать сразу.
	URL       string `json:"url"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	LocalPort int    `json:"localPort"` // 0 в режиме proxy
	Reused    bool   `json:"reused"`    // использована уже запущенная сессия проброса
	// PortForwardID — сессия проброса (режим proxy); адрес для повторного открытия даёт PortForwardURL.
	PortForwardID string `json:"portForwardId,omitempty"`
	Mode          string `json:"mode,omitempty"`
}

// argoTarget — найденный argocd-server: сервис, выбранный порт и режим TLS.
type argoTarget struct {
	svc      *corev1.Service
	port     corev1.ServicePort
	insecure bool
}

func (pfm *PortForwardManager) findArgoTarget(ctx context.Context, conn *ClusterConnection) (*argoTarget, error) {
	svc, err := findArgoServerService(ctx, conn.Client)
	if err != nil {
		return nil, err
	}
	insecure := argoServerInsecure(ctx, conn.Client, svc.Namespace)
	port, ok := pickArgoPort(svc.Spec.Ports, insecure)
	if !ok {
		return nil, fmt.Errorf("service %s/%s has no ports", svc.Namespace, svc.Name)
	}
	return &argoTarget{svc: svc, port: port, insecure: insecure}, nil
}

// OpenArgoUI открывает доступ к argocd-server и возвращает адрес для браузера. Повторный вызов переиспользует
// активную сессию, а не плодит туннели. В режиме local — localhost-порт, в режиме proxy — адрес прокси на
// отдельном origin (ADR-0017), порты на хосте не открываются.
func (pfm *PortForwardManager) OpenArgoUI() (*ArgoUIInfo, error) {
	switch pfm.Mode() {
	case PortForwardModeOff:
		return nil, ErrPortForwardDisabled
	case PortForwardModeProxy:
		return pfm.openArgoProxy()
	}
	return pfm.openArgoLocal()
}

func (pfm *PortForwardManager) openArgoProxy() (*ArgoUIInfo, error) {
	conn, err := pfm.k8sMgr.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t, err := pfm.findArgoTarget(ctx, conn)
	if err != nil {
		return nil, err
	}
	sess, err := pfm.startService(t.svc.Namespace, t.svc.Name, 0, int(t.port.Port), true, argoScheme(t.insecure))
	if err != nil {
		return nil, err
	}
	url, err := pfm.PortForwardURL(sess.ID)
	if err != nil {
		return nil, err
	}
	return &ArgoUIInfo{URL: url, Namespace: t.svc.Namespace, Service: t.svc.Name, PortForwardID: sess.ID, Mode: PortForwardModeProxy}, nil
}

// openArgoLocal пробрасывает порт на argocd-server на 127.0.0.1 (десктоп, режим одного пользователя).
func (pfm *PortForwardManager) openArgoLocal() (*ArgoUIInfo, error) {
	conn, err := pfm.k8sMgr.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t, err := pfm.findArgoTarget(ctx, conn)
	if err != nil {
		return nil, err
	}
	svc, port := t.svc, t.port
	scheme := argoScheme(t.insecure)
	info := func(local int, reused bool) *ArgoUIInfo {
		return &ArgoUIInfo{
			URL: fmt.Sprintf("%s://localhost:%d", scheme, local), Namespace: svc.Namespace,
			Service: svc.Name, LocalPort: local, Reused: reused, Mode: PortForwardModeLocal,
		}
	}

	if s := findArgoForward(pfm.ListPortForwards(), conn.Name, svc.Namespace, svc.Name, int(port.Port)); s != nil {
		return info(s.LocalPort, true), nil
	}
	local, err := freeLocalPort()
	if err != nil {
		return nil, err
	}
	sess, err := pfm.startService(svc.Namespace, svc.Name, local, int(port.Port), false, "")
	if err != nil {
		return nil, err
	}
	return info(sess.LocalPort, false), nil
}

// argoClient возвращает клиент Argo CD API активного кластера для запросов самого сервера (сравнение состояний,
// выпуск токена) и функцию освобождения. В режимах proxy и off запросы идут через PodDialer: ни порта на хосте,
// ни сессии в списке пользователя не появляется. В режиме local сохранён прежний путь через localhost-туннель.
func (pfm *PortForwardManager) argoClient(token string) (*argoAPIClient, func(), error) {
	if pfm.Mode() == PortForwardModeLocal {
		info, err := pfm.openArgoLocal()
		if err != nil {
			return nil, nil, err
		}
		c, err := newArgoAPIClient(info.URL, token)
		return c, func() {}, err
	}
	conn, err := pfm.k8sMgr.GetActiveConnection()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t, err := pfm.findArgoTarget(ctx, conn)
	if err != nil {
		return nil, nil, err
	}
	st, err := resolveServiceTarget(ctx, conn, t.svc.Namespace, t.svc.Name, int(t.port.Port))
	if err != nil {
		return nil, nil, err
	}
	d, err := NewPodDialer(conn, t.svc.Namespace, st.pod, st.podPort)
	if err != nil {
		return nil, nil, err
	}
	c, err := newArgoAPIClientVia(argoScheme(t.insecure)+"://argocd-server.invalid", token, func(ctx context.Context, _, _ string) (net.Conn, error) {
		return d.DialContext(ctx)
	})
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	return c, d.Close, nil
}

// findArgoForward возвращает активную сессию проброса на argocd-server именно этого кластера.
// У разных кластеров совпадают namespace, имя сервиса и порт (argocd/argocd-server:443), поэтому
// без сравнения clusterID после переключения кластера вернулся бы туннель в предыдущий.
func findArgoForward(forwards []PortForwardSession, clusterID, namespace, service string, servicePort int) *PortForwardSession {
	for i := range forwards {
		s := &forwards[i]
		if s.Status == PortForwardActive && s.Mode != PortForwardModeProxy && s.ClusterID == clusterID &&
			s.Namespace == namespace && s.Service == service && s.ServicePort == servicePort {
			return s
		}
	}
	return nil
}

// findArgoServerService ищет Service argocd-server по лейблу; при нескольких предпочитает namespace argocd.
func findArgoServerService(ctx context.Context, client kubernetes.Interface) (*corev1.Service, error) {
	list, err := client.CoreV1().Services("").List(ctx, metav1.ListOptions{LabelSelector: argoServerSelector})
	if err != nil {
		// нет прав на все namespace: пробуем стандартный
		list, err = client.CoreV1().Services(argoDefaultNS).List(ctx, metav1.ListOptions{LabelSelector: argoServerSelector})
		if err != nil {
			return nil, fmt.Errorf("failed to look up argocd-server service: %w", err)
		}
	}
	if len(list.Items) == 0 {
		return nil, errors.New("argocd-server service not found (label app.kubernetes.io/name=argocd-server)")
	}
	items := list.Items
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i].Namespace == argoDefaultNS, items[j].Namespace == argoDefaultNS
		if a != b {
			return a
		}
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Name < items[j].Name
	})
	return &items[0], nil
}

// argoServerInsecure: server.insecure=true в argocd-cmd-params-cm (сервер отдаёт plain HTTP). Ошибки = false.
func argoServerInsecure(ctx context.Context, client kubernetes.Interface, ns string) bool {
	cm, err := client.CoreV1().ConfigMaps(ns).Get(ctx, argoParamsConfig, metav1.GetOptions{})
	return err == nil && cm.Data["server.insecure"] == "true"
}

func argoScheme(insecure bool) string {
	if insecure {
		return "http"
	}
	return "https"
}

// pickArgoPort выбирает порт сервиса: https (имя или 443), а при insecure — http (имя или 80).
// Порты обычно ведут на один и тот же 8080 пода, поэтому схема определяется режимом сервера, а не портом.
func pickArgoPort(ports []corev1.ServicePort, insecure bool) (corev1.ServicePort, bool) {
	name, num := "https", int32(443)
	if insecure {
		name, num = "http", 80
	}
	for _, p := range ports {
		if p.Name == name {
			return p, true
		}
	}
	for _, p := range ports {
		if p.Port == num {
			return p, true
		}
	}
	if len(ports) > 0 {
		return ports[0], true
	}
	return corev1.ServicePort{}, false
}

func freeLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("no free local port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
