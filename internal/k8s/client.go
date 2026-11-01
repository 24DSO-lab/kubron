// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/24DSO-labs/kubron/internal/clusterhealth"
	"github.com/24DSO-labs/kubron/internal/netpolicy"
)

type ContextInfo struct {
	Name      string `json:"name"`
	Cluster   string `json:"cluster"`
	User      string `json:"user"`
	Namespace string `json:"namespace"`
	IsCurrent bool   `json:"isCurrent"`
}

type ClusterConnection struct {
	Name            string
	Client          *kubernetes.Clientset
	Config          *rest.Config
	DynamicClient   dynamic.Interface
	DiscoveryClient discovery.DiscoveryInterface

	cacheMu  sync.Mutex
	overview *overviewCache

	argoMu      sync.Mutex
	argo        *argoCache
	argoRetryAt time.Time

	helmMu      sync.Mutex
	helm        *helmCache
	helmRetryAt time.Time

	// listAll кеширует проверку права на cluster-wide list (выбор нескольких namespace).
	listAll listAllProbe
}

// overviewCache лениво запускает informers для главного экрана (только у активного кластера).
func (c *ClusterConnection) overviewCache(onChange func()) *overviewCache {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.overview == nil {
		c.overview = newOverviewCache(c.Client, onChange)
	}
	return c.overview
}

// Close останавливает фоновые informers подключения.
func (c *ClusterConnection) Close() {
	c.closeArgoCache()
	c.closeHelmCache()
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.overview != nil {
		c.overview.stop()
		c.overview = nil
	}
}

// Manager — «вид» на общий пул подключений: собственный активный кластер, терминалы,
// логи и уведомления. Десктоп использует один вид (NewManager); веб-сервер создаёт
// по виду на сессию через ForSession, все они делят один connPool.
type Manager struct {
	pool   *connPool            // общий для всех видов
	health *clusterhealth.Board // состояние соединения кластеров; общее для всех видов

	viewMu           sync.RWMutex // защищает activeCtx и rawConfig этого вида
	activeCtx        string
	rawConfig        *api.Config
	terminalSessions *TerminalSessionManager
	termObserver     TerminalObserver // защищён viewMu

	notifyMu sync.RWMutex
	notifier func(event string, data ...any)
	logs     logStreams
	limits   Limits // лимиты ресурсов вида; наследуются видами сессий

	helmActor atomic.Pointer[string] // пользователь вида: created_by репозиториев Helm (сервис)
}

// Limits — ограничения на ресурсы одного вида (пользователя). 0 — без ограничения.
type Limits struct {
	Terminals    int
	LogStreams   int
	PortForwards int
	// TerminalMaxDuration — абсолютный срок терминала, TerminalIdleTimeout — срок без ввода пользователя
	// (вывод процесса простоем не считается). 0 — без ограничения.
	TerminalMaxDuration time.Duration
	TerminalIdleTimeout time.Duration
}

// DefaultLimits — значения для сервера: 8 терминалов, 20 потоков логов, 10 port-forward на пользователя.
func DefaultLimits() Limits {
	return Limits{Terminals: 8, LogStreams: 20, PortForwards: 10, TerminalMaxDuration: 8 * time.Hour, TerminalIdleTimeout: 30 * time.Minute}
}

// SetLimits задаёт лимиты; виды, созданные ForSession после вызова, их наследуют.
func (m *Manager) SetLimits(l Limits) {
	m.viewMu.Lock()
	m.limits = l
	m.viewMu.Unlock()
}

// SetIdleTTL задаёт, через сколько после ухода последнего вида останавливаются informers кластера
// (0 — сразу). Относится к общему пулу.
func (m *Manager) SetIdleTTL(d time.Duration) {
	m.pool.mu.Lock()
	m.pool.idleTTL = d
	m.pool.mu.Unlock()
}

// Limits возвращает действующие лимиты вида.
func (m *Manager) Limits() Limits {
	m.viewMu.RLock()
	defer m.viewMu.RUnlock()
	return m.limits
}

// SetNotifier задаёт функцию, которой бэкенд сообщает фронтенду о событиях (Wails EventsEmit).
func (m *Manager) SetNotifier(f func(event string, data ...any)) {
	m.notifyMu.Lock()
	m.notifier = f
	m.notifyMu.Unlock()
}

// emitCluster сообщает об изменении данных кластера всем видам, у которых он активен:
// кеши informers общие, а получатели — свои у каждой сессии.
func (m *Manager) emitCluster(event, clusterName string) {
	for _, h := range m.pool.holdersOf(clusterName) {
		h.emit(event, clusterName)
	}
}

// Notify отправляет событие владельцу вида (веб-сессии или окну десктопа).
func (m *Manager) Notify(event string, data ...any) { m.emit(event, data...) }

func (m *Manager) emit(event string, data ...any) {
	m.notifyMu.RLock()
	f := m.notifier
	m.notifyMu.RUnlock()
	if f != nil {
		f(event, data...)
	}
}

// activateLocked делает кластер активным для этого вида и снимает отметку с прежнего:
// informers прежнего кластера останавливаются, только если его больше не держит ни один
// другой вид (вызывать под m.viewMu).
func (m *Manager) activateLocked(clusterID string) error {
	prev := m.activeCtx
	if prev == clusterID {
		return nil
	}
	if err := m.pool.acquire(clusterID, m); err != nil {
		return err
	}
	m.activeCtx = clusterID
	if prev != "" {
		m.pool.release(prev, m)
	}
	return nil
}

func NewManager() *Manager {
	return &Manager{
		pool:             newConnPool(),
		health:           clusterhealth.NewBoard(),
		terminalSessions: NewTerminalSessionManager(),
		limits:           Limits{LogStreams: logMaxStreams}, // десктоп: прежний предел логов
	}
}

// ForSession возвращает новый вид на тот же пул подключений: свой активный кластер,
// свои терминалы, логи и notifier. Методы Manager работают с видом без изменений.
func (m *Manager) ForSession() *Manager {
	return &Manager{
		pool:             m.pool,
		health:           m.health,
		terminalSessions: NewTerminalSessionManager(),
		limits:           m.Limits(),
	}
}

// Detach снимает отметку активного кластера этого вида (при закрытии сессии), не трогая
// чужие виды. Терминалы и логи вида должны быть закрыты вызывающим.
func (m *Manager) Detach() {
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	if m.activeCtx != "" {
		m.pool.release(m.activeCtx, m)
		m.activeCtx = ""
	}
}

func GetKubeconfigPath() string {
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	kubeDir := filepath.Join(home, ".kube")
	configPath := filepath.Join(kubeDir, "config")

	info, err := os.Stat(configPath)
	if err == nil {
		if !info.IsDir() {
			return configPath
		}

		entries, err := os.ReadDir(configPath)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					name := entry.Name()
					ext := filepath.Ext(name)
					if ext == ".conf" || ext == ".yaml" || ext == ".yml" {
						return filepath.Join(configPath, name)
					}
				}
			}
		}
	}

	return configPath
}

func (m *Manager) LoadContexts() ([]ContextInfo, error) {
	path := GetKubeconfigPath()
	config, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	m.viewMu.Lock()
	m.rawConfig = config
	m.viewMu.Unlock()

	var contexts []ContextInfo
	for name, ctx := range config.Contexts {
		contexts = append(contexts, ContextInfo{
			Name:      name,
			Cluster:   ctx.Cluster,
			User:      ctx.AuthInfo,
			Namespace: ctx.Namespace,
			IsCurrent: name == config.CurrentContext,
		})
	}

	return contexts, nil
}

func buildConnection(name string, config *rest.Config) (*ClusterConnection, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	return &ClusterConnection{
		Name:            name,
		Client:          clientset,
		Config:          config,
		DynamicClient:   dynClient,
		DiscoveryClient: discClient,
	}, nil
}

// RemoveConnection забывает подключение к кластеру (при удалении кластера из приложения).
// Если кластер был активным, активное подключение сбрасывается.
func (m *Manager) RemoveConnection(clusterID string) (wasActive bool) {
	m.pool.remove(clusterID)
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	if m.activeCtx == clusterID {
		m.activeCtx = ""
		return true
	}
	return false
}

func (m *Manager) GetActiveConnection() (*ClusterConnection, error) {
	m.viewMu.RLock()
	id := m.activeCtx
	m.viewMu.RUnlock()
	conn, exists := m.pool.get(id)
	if !exists {
		return nil, fmt.Errorf("no active connection")
	}
	return conn, nil
}

func (m *Manager) GetActiveContext() string {
	m.viewMu.RLock()
	defer m.viewMu.RUnlock()
	return m.activeCtx
}

func (m *Manager) TestConnection() error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = conn.Client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	return err
}

func (m *Manager) ConnectToCluster(clusterID string) error {
	if _, err := m.pool.getOrCreate(clusterID, func() (*ClusterConnection, error) {
		return buildClusterConnection(clusterID)
	}); err != nil {
		return err
	}
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	return m.activateLocked(clusterID)
}

// buildClusterConnection создаёт подключение по сохранённому kubeconfig кластера.
// Файловое хранилище отдаёт путь: client-go читает kubeconfig по пути, и относительные пути
// внутри него резолвятся от каталога файла. Хранилища без файлов отдают байты — конфиг
// строится в памяти (clientcmd.Load), файл на диске не создаётся.
func buildClusterConnection(clusterID string) (*ClusterConnection, error) {
	meta, err := LoadClusterMeta(clusterID)
	if err != nil {
		return nil, fmt.Errorf("cluster metadata not found: %w", err)
	}

	// in-cluster и oidc-password строят подключение сами (без exec-плагина и без файлов сервера).
	if config, ok, err := buildModeConfig(clusterID, meta); ok {
		if err != nil {
			return nil, err
		}
		config.QPS = 50
		config.Burst = 100
		if meta.Mode != ModeInCluster { // адрес API-сервера in-cluster задаёт сам кластер
			applyClusterAddrPolicy(config)
		}
		return buildConnection(clusterID, config)
	}

	overrides := &clientcmd.ConfigOverrides{}
	if meta.ContextName != "" {
		overrides.CurrentContext = meta.ContextName
	}

	// Политика соединений действует и при подключении, не только при добавлении: так не пройдут ни кластеры,
	// импортированные из файлов десктопа, ни kubeconfig, сохранённый до включения запретов.
	if p := currentPolicy(); !p.AllowExec || !p.AllowLocalFiles || netpolicy.For(netpolicy.ScopeClusterAPI) != nil {
		if err := checkStoredPolicy(clusterID, meta); err != nil {
			return nil, err
		}
	}

	var config *rest.Config
	if kcPath := GetClusterKubeconfigPath(clusterID); kcPath != "" {
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kcPath},
			overrides,
		).ClientConfig()
	} else {
		var data []byte
		if data, err = clusterStore.Kubeconfig(clusterID); err != nil {
			return nil, fmt.Errorf("kubeconfig not found for cluster %s", clusterID)
		}
		var cfg *api.Config
		if cfg, err = clientcmd.Load(data); err != nil {
			return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
		}
		config, err = clientcmd.NewNonInteractiveClientConfig(*cfg, meta.ContextName, overrides, nil).ClientConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("configuration error: %w", err)
	}

	config.QPS = 50
	config.Burst = 100
	applyClusterAddrPolicy(config)

	return buildConnection(clusterID, config)
}

func (m *Manager) GetClusterStatuses() []ClusterStatus {
	clusters, err := ListClusters()
	if err != nil {
		return nil
	}
	m.viewMu.RLock()
	active := m.activeCtx
	m.viewMu.RUnlock()

	var statuses []ClusterStatus
	for _, cluster := range clusters {
		status := ClusterStatus{
			ClusterMeta: cluster,
			Connected:   false,
			Active:      false,
		}

		if m.pool.has(cluster.ID) {
			status.Connected = true
		}
		if active == cluster.ID {
			status.Active = true
		}
		if m.health != nil {
			if h, ok := m.health.Get(cluster.ID); ok {
				hc := h
				status.Health = &hc
				if h.State != clusterhealth.OK && h.State != clusterhealth.Unknown {
					status.Error = h.Message
				}
			}
		}

		statuses = append(statuses, status)
	}

	return statuses
}
