// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/24DSO-labs/kubron/internal/pfproxy"
)

// Статусы сессии проброса портов.
const (
	PortForwardStarting = "starting"
	PortForwardActive   = "active"
	PortForwardError    = "error"
)

// Режимы port-forward (capability portForwardMode, ADR-0017).
const (
	// PortForwardModeLocal — порт слушается на 127.0.0.1 той же машины (десктоп, режим одного пользователя).
	PortForwardModeLocal = "local"
	// PortForwardModeProxy — порты не открываются: браузер ходит через pfproxy на отдельном origin.
	PortForwardModeProxy = "proxy"
	// PortForwardModeOff — port-forward недоступен.
	PortForwardModeOff = "off"
)

// Причины остановки сессии (попадают в аудит). Совпадают с pfproxy.Reason*.
const (
	PortForwardStopUser          = pfproxy.ReasonUser
	PortForwardStopIdle          = pfproxy.ReasonIdle
	PortForwardStopMaxDuration   = pfproxy.ReasonMaxDuration
	PortForwardStopSessionClosed = pfproxy.ReasonSessionClosed
	PortForwardStopError         = pfproxy.ReasonError
)

// ErrPortForwardDisabled — port-forward отключён (режим off).
var ErrPortForwardDisabled = errors.New("port-forward is disabled")

// portForwardReadyTimeout — сколько ждём установления туннеля при старте.
const portForwardReadyTimeout = 15 * time.Second

// PortForwardSession — сессия проброса порта в под (напрямую или через сервис).
type PortForwardSession struct {
	ID string `json:"id"`
	// ClusterID — кластер, в котором открыт туннель. Туннель построен на подключении
	// этого кластера и после переключения активного кластера продолжает вести в него.
	ClusterID string `json:"clusterId,omitempty"`
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Service   string `json:"service,omitempty"` // заполнено, если проброс запущен через сервис
	// ServicePort — порт сервиса, выбранный пользователем (podPort — разрешённый targetPort).
	ServicePort int    `json:"servicePort,omitempty"`
	LocalPort   int    `json:"localPort"`
	PodPort     int    `json:"podPort"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	Name        string `json:"name"`   // = ID, нужен списку ресурсов для ключа и открытия панели
	Target      string `json:"target"` // "pod/nginx" или "svc/nginx:80"
	// Mode — "local" (порт на 127.0.0.1) или "proxy" (через pfproxy; LocalPort равен 0). Адрес для браузера
	// всегда запрашивается методом PortForwardURL: в режиме proxy он одноразовый и живёт секунды.
	Mode   string `json:"mode"`
	Scheme string `json:"scheme,omitempty"` // схема приложения в поде в режиме proxy: http или https

	stopChan chan struct{}
	stopOnce sync.Once

	key    string     // ключ дедупликации в режиме proxy
	dialer *PodDialer // режим proxy

	obsMu    sync.Mutex
	obsStop  func(reason string)
	obsDone  bool
	obsCause string
}

// attachObserver запоминает функцию, которую нужно вызвать при остановке сессии. Если сессия уже остановлена,
// функция вызывается сразу.
func (s *PortForwardSession) attachObserver(f func(reason string)) {
	if f == nil {
		return
	}
	s.obsMu.Lock()
	if s.obsDone {
		reason := s.obsCause
		s.obsMu.Unlock()
		f(reason)
		return
	}
	s.obsStop = f
	s.obsMu.Unlock()
}

// markStopped фиксирует причину остановки и вызывает наблюдателя; повторные вызовы ничего не делают.
func (s *PortForwardSession) markStopped(reason string) {
	s.obsMu.Lock()
	if s.obsDone {
		s.obsMu.Unlock()
		return
	}
	s.obsDone, s.obsCause = true, reason
	f := s.obsStop
	s.obsStop = nil
	s.obsMu.Unlock()
	if f != nil {
		f(reason)
	}
}

func (s *PortForwardSession) stop() {
	s.stopOnce.Do(func() { close(s.stopChan) })
}

// snapshot возвращает копию без внутреннего состояния (вызывать под pfm.mu).
func (s *PortForwardSession) snapshot() PortForwardSession {
	return PortForwardSession{
		ID:          s.ID,
		ClusterID:   s.ClusterID,
		Namespace:   s.Namespace,
		Pod:         s.Pod,
		Service:     s.Service,
		ServicePort: s.ServicePort,
		LocalPort:   s.LocalPort,
		PodPort:     s.PodPort,
		Status:      s.Status,
		Error:       s.Error,
		Name:        s.Name,
		Target:      s.Target,
		Mode:        s.Mode,
		Scheme:      s.Scheme,
	}
}

// PortForwardManager управляет сессиями проброса портов. Сессии живут только
// пока работает приложение: туннель нельзя восстановить после перезапуска.
type PortForwardManager struct {
	mu       sync.Mutex
	sessions map[string]*PortForwardSession
	k8sMgr   *Manager

	mode     string           // PortForwardMode*; пусто — local
	proxy    *pfproxy.Service // режим proxy
	owner    string           // веб-сессия владельца (режим proxy)
	observer PortForwardObserver
}

// PortForwardObserver вызывается, когда сессия стала активной (запись аудита «start»), и возвращает функцию,
// которую менеджер вызовет один раз при остановке с причиной (запись «stop»). Пользователя и кластер
// наблюдатель определяет сам в момент старта: остановка может произойти после выхода пользователя.
type PortForwardObserver func(s *PortForwardSession) (stopped func(reason string))

// SetObserver задаёт наблюдателя сессий; вызывать до первого старта.
func (pfm *PortForwardManager) SetObserver(o PortForwardObserver) {
	pfm.mu.Lock()
	pfm.observer = o
	pfm.mu.Unlock()
}

// SetMode задаёт режим: local, proxy или off. Режим proxy задаётся методом SetProxy.
func (pfm *PortForwardManager) SetMode(mode string) {
	pfm.mu.Lock()
	pfm.mode, pfm.proxy, pfm.owner = mode, nil, ""
	pfm.mu.Unlock()
}

// SetProxy включает режим proxy: потоки владельца owner регистрируются в svc, порты на хосте не открываются.
func (pfm *PortForwardManager) SetProxy(svc *pfproxy.Service, owner string) {
	pfm.mu.Lock()
	pfm.mode, pfm.proxy, pfm.owner = PortForwardModeProxy, svc, owner
	pfm.mu.Unlock()
}

// Mode возвращает действующий режим.
func (pfm *PortForwardManager) Mode() string {
	pfm.mu.Lock()
	defer pfm.mu.Unlock()
	return pfm.modeLocked()
}

func (pfm *PortForwardManager) modeLocked() string {
	if pfm.mode == "" {
		return PortForwardModeLocal
	}
	return pfm.mode
}

// settings возвращает режим, прокси, владельца и наблюдателя одним снимком.
func (pfm *PortForwardManager) settings() (mode string, svc *pfproxy.Service, owner string, obs PortForwardObserver) {
	pfm.mu.Lock()
	defer pfm.mu.Unlock()
	return pfm.modeLocked(), pfm.proxy, pfm.owner, pfm.observer
}

// NewSessionPortForwardManager — менеджер port-forward вида сессии (без очистки каталога данных).
func NewSessionPortForwardManager(k8sMgr *Manager) *PortForwardManager {
	return &PortForwardManager{sessions: make(map[string]*PortForwardSession), k8sMgr: k8sMgr}
}

func NewPortForwardManager(k8sMgr *Manager) *PortForwardManager {
	// Ранние версии сохраняли сессии на диск и после перезапуска показывали
	// «мёртвые» записи. Убираем оставшийся файл.
	_ = os.RemoveAll(filepath.Join(GetKubronDir(), "portforwards"))
	return &PortForwardManager{
		sessions: make(map[string]*PortForwardSession),
		k8sMgr:   k8sMgr,
	}
}

// StartPortForward пробрасывает localPort на podPort указанного пода. В режиме proxy localPort игнорируется:
// порт на хосте не открывается, а сессия получает идентификатор для адреса прокси.
func (pfm *PortForwardManager) StartPortForward(namespace, pod string, localPort, podPort int) (*PortForwardSession, error) {
	mode, _, _, _ := pfm.settings()
	switch mode {
	case PortForwardModeOff:
		return nil, ErrPortForwardDisabled
	case PortForwardModeProxy:
		return pfm.startProxy(namespace, pod, "", 0, podPort, guessScheme("", podPort, nil))
	}
	id := fmt.Sprintf("%s-%s-%d-%d", namespace, pod, localPort, podPort)
	return pfm.start(id, namespace, pod, "", 0, localPort, podPort)
}

// StartServicePortForward пробрасывает localPort на порт сервиса: выбирается готовый
// под сервиса, а targetPort (в том числе именованный) разрешается в номер порта пода.
func (pfm *PortForwardManager) StartServicePortForward(namespace, serviceName string, localPort, servicePort int) (*PortForwardSession, error) {
	mode, _, _, _ := pfm.settings()
	if mode == PortForwardModeOff {
		return nil, ErrPortForwardDisabled
	}
	return pfm.startService(namespace, serviceName, localPort, servicePort, mode == PortForwardModeProxy, "")
}

// startService разрешает сервис в под и запускает проброс в нужном режиме.
func (pfm *PortForwardManager) startService(namespace, serviceName string, localPort, servicePort int, proxy bool, scheme string) (*PortForwardSession, error) {
	conn, err := pfm.k8sMgr.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t, err := resolveServiceTarget(ctx, conn, namespace, serviceName, servicePort)
	if err != nil {
		return nil, err
	}
	if proxy {
		if scheme == "" {
			scheme = t.scheme
		}
		return pfm.startProxy(namespace, t.pod, serviceName, servicePort, t.podPort, scheme)
	}
	id := fmt.Sprintf("%s-svc-%s-%d-%d", namespace, serviceName, localPort, servicePort)
	return pfm.start(id, namespace, t.pod, serviceName, servicePort, localPort, t.podPort)
}

// serviceTarget — куда ведёт порт сервиса: готовый под, порт контейнера и схема приложения.
type serviceTarget struct {
	pod     string
	podPort int
	scheme  string
}

func resolveServiceTarget(ctx context.Context, conn *ClusterConnection, namespace, serviceName string, servicePort int) (*serviceTarget, error) {
	svc, err := conn.Client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("service not found: %w", err)
	}
	if len(svc.Spec.Selector) == 0 {
		return nil, fmt.Errorf("service %s has no selector", serviceName)
	}

	var svcPort *corev1.ServicePort
	for i := range svc.Spec.Ports {
		if int(svc.Spec.Ports[i].Port) == servicePort {
			svcPort = &svc.Spec.Ports[i]
			break
		}
	}
	if svcPort == nil {
		return nil, fmt.Errorf("service %s has no port %d", serviceName, servicePort)
	}

	pods, err := conn.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to find pods: %w", err)
	}

	pod := pickReadyPod(pods.Items)
	if pod == nil {
		return nil, fmt.Errorf("no ready pods for service %s", serviceName)
	}

	podPort, err := resolveTargetPort(svcPort, pod)
	if err != nil {
		return nil, err
	}
	return &serviceTarget{pod: pod.Name, podPort: podPort, scheme: guessScheme(svcPort.Name, int(svcPort.Port), svcPort.AppProtocol)}, nil
}

// guessScheme выбирает схему приложения в поде: https для явного appProtocol, имени порта https и стандартных
// TLS-портов, иначе http. Сертификат пода прокси не проверяет.
func guessScheme(portName string, port int, appProtocol *string) string {
	if appProtocol != nil && strings.EqualFold(*appProtocol, "https") {
		return "https"
	}
	if strings.HasPrefix(strings.ToLower(portName), "https") || port == 443 || port == 8443 {
		return "https"
	}
	return "http"
}

// pickReadyPod возвращает первый под в фазе Running с условием Ready.
func pickReadyPod(pods []corev1.Pod) *corev1.Pod {
	for i := range pods {
		p := &pods[i]
		if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
			continue
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return p
			}
		}
	}
	return nil
}

// resolveTargetPort переводит targetPort сервиса в номер порта контейнера пода.
func resolveTargetPort(sp *corev1.ServicePort, pod *corev1.Pod) (int, error) {
	tp := sp.TargetPort
	if tp.String() == "" || tp.String() == "0" {
		return int(sp.Port), nil // targetPort не задан — совпадает с port
	}
	if tp.IntVal != 0 && tp.StrVal == "" {
		return int(tp.IntVal), nil
	}
	for _, c := range pod.Spec.Containers {
		for _, cp := range c.Ports {
			if cp.Name == tp.StrVal {
				return int(cp.ContainerPort), nil
			}
		}
	}
	return 0, fmt.Errorf("port %q not found in containers of pod %s", tp.StrVal, pod.Name)
}

func (pfm *PortForwardManager) start(id, namespace, pod, service string, servicePort, localPort, podPort int) (*PortForwardSession, error) {
	conn, err := pfm.k8sMgr.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	target := "pod/" + pod
	if service != "" {
		target = fmt.Sprintf("svc/%s:%d", service, servicePort)
	}

	session := &PortForwardSession{
		ID:          id,
		ClusterID:   conn.Name,
		Namespace:   namespace,
		Pod:         pod,
		Service:     service,
		ServicePort: servicePort,
		LocalPort:   localPort,
		PodPort:     podPort,
		Status:      PortForwardStarting,
		stopChan:    make(chan struct{}),
		Name:        id,
		Target:      target,
		Mode:        PortForwardModeLocal,
	}

	// Проверка и вставка выполняются под одним локом, чтобы два одновременных
	// вызова не создали два туннеля на один порт.
	pfm.mu.Lock()
	if existing, ok := pfm.sessions[id]; ok {
		if existing.Status != PortForwardError {
			snap := existing.snapshot()
			pfm.mu.Unlock()
			return &snap, nil
		}
		existing.stop()
		delete(pfm.sessions, id)
	}
	if max := pfm.k8sMgr.Limits().PortForwards; max > 0 && len(pfm.sessions) >= max {
		pfm.mu.Unlock()
		return nil, fmt.Errorf("too many port-forwards (maximum %d)", max)
	}
	pfm.sessions[id] = session
	pfm.mu.Unlock()

	fail := func(err error) (*PortForwardSession, error) {
		pfm.mu.Lock()
		if pfm.sessions[id] == session {
			delete(pfm.sessions, id)
		}
		pfm.mu.Unlock()
		session.stop()
		return nil, err
	}

	reqURL := conn.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(pod).
		SubResource("portforward").
		URL()

	transport, upgrader, err := spdyRoundTripperFor(conn.Config)
	if err != nil {
		return fail(fmt.Errorf("failed to create transport: %w", err))
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, &url.URL{
		Scheme: reqURL.Scheme,
		Host:   reqURL.Host,
		Path:   reqURL.Path,
	})

	readyChan := make(chan struct{})
	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:%d", localPort, podPort)}, session.stopChan, readyChan, nil, nil)
	if err != nil {
		return fail(fmt.Errorf("failed to create port-forward: %w", err))
	}

	done := make(chan error, 1)
	go func() {
		err := fw.ForwardPorts()
		done <- err

		// Туннель закрылся: если сессию не остановили вручную, отмечаем ошибку,
		// чтобы интерфейс не показывал «живую» сессию.
		pfm.mu.Lock()
		defer pfm.mu.Unlock()
		if pfm.sessions[id] != session {
			return
		}
		msg := "connection closed"
		if err != nil {
			msg = err.Error()
		}
		session.Status = PortForwardError
		session.Error = msg
		go session.markStopped(PortForwardStopError)
	}()

	select {
	case <-readyChan:
		pfm.mu.Lock()
		if pfm.sessions[id] == session && session.Status == PortForwardStarting {
			session.Status = PortForwardActive
		}
		snap := session.snapshot()
		obs := pfm.observer
		pfm.mu.Unlock()
		if obs != nil {
			session.attachObserver(obs(&snap))
		}
		return &snap, nil
	case err := <-done:
		if err == nil {
			err = fmt.Errorf("connection closed before becoming ready")
		}
		return fail(fmt.Errorf("failed to start port-forward: %w", err))
	case <-time.After(portForwardReadyTimeout):
		return fail(fmt.Errorf("timed out establishing port-forward (%s)", portForwardReadyTimeout))
	}
}

// StopPortForward останавливает сессию и удаляет её из списка.
func (pfm *PortForwardManager) StopPortForward(id string) error {
	pfm.mu.Lock()
	session, ok := pfm.sessions[id]
	if ok {
		delete(pfm.sessions, id)
	}
	proxy := pfm.proxy
	pfm.mu.Unlock()

	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	pfm.finish(session, proxy, PortForwardStopUser)
	return nil
}

// finish останавливает уже изъятую из списка сессию.
func (pfm *PortForwardManager) finish(s *PortForwardSession, proxy *pfproxy.Service, reason string) {
	if s.Mode == PortForwardModeProxy && proxy != nil {
		proxy.Registry.Remove(s.ID, reason) // вызовет onProxyStopped: закроет dialer и запишет остановку
		s.markStopped(reason)               // на случай, если реестр уже удалил поток
		s.closeDialer()
		return
	}
	s.stop()
	s.markStopped(reason)
}

// ListPortForwards возвращает копии всех сессий, отсортированные по ID.
func (pfm *PortForwardManager) ListPortForwards() []PortForwardSession {
	pfm.mu.Lock()
	defer pfm.mu.Unlock()

	result := make([]PortForwardSession, 0, len(pfm.sessions))
	for _, s := range pfm.sessions {
		result = append(result, s.snapshot())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// StopAll останавливает все сессии (выход из учётной записи, закрытие приложения).
func (pfm *PortForwardManager) StopAll() {
	pfm.mu.Lock()
	sessions := make([]*PortForwardSession, 0, len(pfm.sessions))
	for id, s := range pfm.sessions {
		sessions = append(sessions, s)
		delete(pfm.sessions, id)
	}
	proxy := pfm.proxy
	pfm.mu.Unlock()

	for _, s := range sessions {
		pfm.finish(s, proxy, PortForwardStopSessionClosed)
	}
}

func (s *PortForwardSession) closeDialer() {
	if s.dialer != nil {
		s.dialer.Close()
	}
}

// startProxy регистрирует поток в прокси: порт на хосте не открывается, под достижим только через SPDY-соединение
// с kube-apiserver, которое прокси открывает на каждое соединение браузера.
func (pfm *PortForwardManager) startProxy(namespace, pod, service string, servicePort, podPort int, scheme string) (*PortForwardSession, error) {
	_, proxy, owner, obs := pfm.settings()
	if proxy == nil || owner == "" {
		return nil, ErrPortForwardDisabled
	}
	conn, err := pfm.k8sMgr.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	target := "pod/" + pod
	key := fmt.Sprintf("%s|%s|pod|%s|%d", conn.Name, namespace, pod, podPort)
	if service != "" {
		target = fmt.Sprintf("svc/%s:%d", service, servicePort)
		key = fmt.Sprintf("%s|%s|svc|%s|%d", conn.Name, namespace, service, servicePort)
	}

	// Повторный запуск того же проброса возвращает действующую сессию (как в режиме local).
	pfm.mu.Lock()
	for _, existing := range pfm.sessions {
		if existing.key == key && existing.Status != PortForwardError {
			snap := existing.snapshot()
			pfm.mu.Unlock()
			return &snap, nil
		}
	}
	if max := pfm.k8sMgr.Limits().PortForwards; max > 0 && len(pfm.sessions) >= max {
		pfm.mu.Unlock()
		return nil, fmt.Errorf("too many port-forwards (maximum %d)", max)
	}
	pfm.mu.Unlock()

	dialer, err := NewPodDialer(conn, namespace, pod, podPort)
	if err != nil {
		return nil, err
	}
	// Соединение с apiserver открываем сразу: ошибки доступа и отсутствие пода видны при запуске, а не при первом запросе.
	ctx, cancel := context.WithTimeout(context.Background(), portForwardReadyTimeout)
	defer cancel()
	if _, err := dialer.connection(ctx); err != nil {
		return nil, fmt.Errorf("failed to start port-forward: %w", err)
	}

	id := pfproxy.NewID()
	session := &PortForwardSession{
		ID: id, ClusterID: conn.Name, Namespace: namespace, Pod: pod, Service: service, ServicePort: servicePort,
		PodPort: podPort, Status: PortForwardActive, Name: id, Target: target, Mode: PortForwardModeProxy, Scheme: scheme,
		stopChan: make(chan struct{}), key: key, dialer: dialer,
	}

	pfm.mu.Lock()
	// Другой вызов мог успеть запустить тот же проброс или занять последний слот, пока мы открывали соединение.
	for _, existing := range pfm.sessions {
		if existing.key == key && existing.Status != PortForwardError {
			snap := existing.snapshot()
			pfm.mu.Unlock()
			dialer.Close()
			return &snap, nil
		}
	}
	if max := pfm.k8sMgr.Limits().PortForwards; max > 0 && len(pfm.sessions) >= max {
		pfm.mu.Unlock()
		dialer.Close()
		return nil, fmt.Errorf("too many port-forwards (maximum %d)", max)
	}
	pfm.sessions[id] = session
	snap := session.snapshot()
	pfm.mu.Unlock()

	if obs != nil {
		session.attachObserver(obs(&snap))
	}
	err = proxy.Registry.Add(pfproxy.Target{
		ID: id, Owner: owner, ClusterID: conn.Name, Namespace: namespace, Pod: pod, Service: service,
		Port: podPort, Scheme: scheme, Dial: dialer.DialContext,
	}, func(reason string) { pfm.onProxyStopped(session, reason) })
	if err != nil {
		pfm.mu.Lock()
		delete(pfm.sessions, id)
		pfm.mu.Unlock()
		session.markStopped(PortForwardStopError)
		dialer.Close()
		return nil, err
	}
	return &snap, nil
}

// onProxyStopped вызывается реестром, когда поток удалён (остановка пользователем, простой, срок, закрытие сессии).
func (pfm *PortForwardManager) onProxyStopped(s *PortForwardSession, reason string) {
	pfm.mu.Lock()
	if pfm.sessions[s.ID] == s {
		delete(pfm.sessions, s.ID)
	}
	pfm.mu.Unlock()
	s.stop()
	s.closeDialer()
	s.markStopped(reason)
}

// PortForwardURL возвращает адрес для браузера. В режиме proxy он одноразовый (ticket живёт секунды), поэтому
// запрашивается перед каждым открытием, а не хранится в сессии.
func (pfm *PortForwardManager) PortForwardURL(id string) (string, error) {
	pfm.mu.Lock()
	s, ok := pfm.sessions[id]
	var snap PortForwardSession
	if ok {
		snap = s.snapshot()
	}
	proxy, owner := pfm.proxy, pfm.owner
	pfm.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("session not found: %s", id)
	}
	if snap.Status != PortForwardActive {
		return "", errors.New("port-forward is not active")
	}
	if snap.Mode == PortForwardModeProxy {
		if proxy == nil {
			return "", ErrPortForwardDisabled
		}
		return proxy.EntryURL(owner, id)
	}
	return fmt.Sprintf("http://localhost:%d", snap.LocalPort), nil
}
