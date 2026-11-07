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

	"github.com/24DSO-lab/kubron/internal/clusterhealth"
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
	key             string // ключ в общем пуле; пусто — равен Name
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
	activeCtx        string       // id активного кластера
	activeKey        string       // ключ его подключения в пуле: (кластер, субъект)
	authSession      AuthSession
	rawConfig        *api.Config
	terminalSessions *TerminalSessionManager
	termObserver     TerminalObserver // защищён viewMu

	notifyMu sync.RWMutex
	notifier func(event string, data ...any)
	logs     logStreams
	limits   Limits // лимиты ресурсов вида; наследуются видами сессий

	// scopes — чьи учётные данные у кластера (credential.Scope), узнанные при подключении и в фоновой проверке;
	// authLost — личные входы, у которых брокер сообщил «нужен вход». Общие для всех видов.
	scopes   *sync.Map
	authLost *sync.Map
	// build строит подключение по плану; в тестах подменяется.
	build func(ConnPlan) (*ClusterConnection, error)

	helmActor atomic.Pointer[string] // пользователь вида: created_by репозиториев Helm (сервис)

	// base — контекст жизни вида: запросы к API кластера, начатые методами вида, обрываются при Close (конец сессии).
	// nil — без отмены (десктоп, тесты). Задаётся только при создании вида.
	base      context.Context
	closeBase context.CancelFunc
}

// baseCtx — родитель контекстов запросов этого вида. Запрос ограничивается ещё и своим таймаутом.
func (m *Manager) baseCtx() context.Context {
	if m.base != nil {
		return m.base
	}
	return context.Background()
}

// Close обрывает незавершённые запросы этого вида к API кластеров (при освобождении сессии). Подключения, кэши и
// informers общие для всех видов и не затрагиваются; повторный вызов безопасен.
func (m *Manager) Close() {
	if m.closeBase != nil {
		m.closeBase()
	}
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
func (m *Manager) emitCluster(event string, conn *ClusterConnection) {
	key := conn.key
	if key == "" {
		key = conn.Name
	}
	for _, h := range m.pool.holdersOf(key) {
		h.emit(event, conn.Name)
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
func (m *Manager) activateLocked(clusterID, key string) error {
	prev, prevKey := m.activeCtx, m.activeKey
	if prevKey == key {
		m.activeCtx = clusterID
		return nil
	}
	if err := m.pool.acquire(key, m); err != nil {
		return err
	}
	m.activeCtx, m.activeKey = clusterID, key
	if prev != "" {
		m.pool.release(prevKey, m)
	}
	return nil
}

func NewManager() *Manager {
	return &Manager{
		pool:             newConnPool(),
		health:           clusterhealth.NewBoard(),
		terminalSessions: NewTerminalSessionManager(),
		limits:           Limits{LogStreams: logMaxStreams}, // десктоп: прежний предел логов
		scopes:           &sync.Map{},
		authLost:         &sync.Map{},
	}
}

// ForSession возвращает новый вид на тот же пул подключений: свой активный кластер,
// свои терминалы, логи и notifier. Методы Manager работают с видом без изменений.
func (m *Manager) ForSession() *Manager {
	base, cancel := context.WithCancel(m.baseCtx())
	return &Manager{
		base:             base,
		closeBase:        cancel,
		pool:             m.pool,
		health:           m.health,
		terminalSessions: NewTerminalSessionManager(),
		limits:           m.Limits(),
		authSession:      m.AuthSessionOf(),
		scopes:           m.scopes,
		authLost:         m.authLost,
		build:            m.build,
	}
}

// Detach снимает отметку активного кластера этого вида (при закрытии сессии), не трогая
// чужие виды. Терминалы и логи вида должны быть закрыты вызывающим.
func (m *Manager) Detach() {
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	if m.activeCtx != "" {
		m.pool.release(m.activeKey, m)
		m.activeCtx, m.activeKey = "", ""
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
	m.pool.removeCluster(clusterID) // подключения всех субъектов этого кластера
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	if m.activeCtx == clusterID {
		m.activeCtx, m.activeKey = "", ""
		return true
	}
	return false
}

func (m *Manager) GetActiveConnection() (*ClusterConnection, error) {
	m.viewMu.RLock()
	id := m.activeKey
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
	ctx, cancel := context.WithTimeout(m.baseCtx(), 5*time.Second)
	defer cancel()

	_, err = conn.Client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	return err
}

// SetAuthSession задаёт, кто подключается этим видом (брокер входа, пользователь, сессия). Вызывается до
// ConnectToCluster; без брокера действует прежний путь.
func (m *Manager) SetAuthSession(s AuthSession) {
	m.viewMu.Lock()
	m.authSession = s
	m.viewMu.Unlock()
}

// AuthSessionOf возвращает текущие данные подключающегося.
func (m *Manager) AuthSessionOf() AuthSession {
	m.viewMu.RLock()
	defer m.viewMu.RUnlock()
	return m.authSession
}

func (m *Manager) ConnectToCluster(clusterID string) error {
	sess := m.AuthSessionOf()
	orig := sess.OnAuthRequired
	var key string
	sess.OnAuthRequired = func(id string) { // брокер сказал «нужен вход»: пометить и передать дальше
		m.authLost.Store(key, true)
		if orig != nil {
			orig(id)
		}
	}
	plan, err := PlanFor(sess, clusterID)
	if err != nil {
		return err
	}
	key = plan.Key
	m.noteScope(plan)
	build := m.build
	if build == nil {
		build = buildPlanConnection
	}
	if _, err := m.pool.getOrCreate(plan.Key, func() (*ClusterConnection, error) { return build(plan) }); err != nil {
		return err
	}
	m.authLost.Delete(plan.Key) // вход действует: пометка «нужен вход» снимается до первого отказа брокера
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	return m.activateLocked(clusterID, plan.Key)
}

// noteScope запоминает, чьи учётные данные у кластера, и ставит пометку «нужен вход» при отказе брокера.
func (m *Manager) noteScope(plan ConnPlan) {
	if plan.Subject.Scope != "" {
		m.scopes.Store(plan.ClusterID, plan.Subject.Scope)
	}
}

// buildPlanConnection создаёт подключение по плану: конфигурацию даёт только ConfigFor.
func buildPlanConnection(p ConnPlan) (*ClusterConnection, error) {
	config, err := ConfigFor(p)
	if err != nil {
		return nil, err
	}
	conn, err := buildConnection(p.ClusterID, config)
	if err != nil {
		return nil, err
	}
	conn.key = p.Key
	return conn, nil
}

// buildClusterConnection — подключение без брокера (фоновая проверка и прежний путь).
func buildClusterConnection(clusterID string) (*ClusterConnection, error) {
	p, err := PlanFor(AuthSession{}, clusterID)
	if err != nil {
		return nil, err
	}
	return buildPlanConnection(p)
}

// DropPersonal закрывает личное подключение пользователя (informers) к кластеру; пустой clusterID — ко всем кластерам.
// Вызывается при отзыве входа. Виды, державшие подключение, при следующем обращении получат «нет активного подключения».
func (m *Manager) DropPersonal(clusterID, owner string) {
	if owner == "" {
		return
	}
	m.pool.removeIf(func(c *ClusterConnection) bool {
		return (clusterID == "" || c.Name == clusterID) && c.key == userPoolKey(c.Name, owner)
	})
}

// lookupConn — подключение кластера для этого вида: личное, затем общее.
func (m *Manager) lookupConn(clusterID string) (*ClusterConnection, bool) {
	if owner := m.AuthSessionOf().UserID; owner != "" {
		if c, ok := m.pool.get(userPoolKey(clusterID, owner)); ok {
			return c, true
		}
	}
	return m.pool.get(clusterID)
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

		_, connected := m.lookupConn(cluster.ID)
		status.Connected = connected
		status.AuthRequired = m.authRequired(cluster.ID, connected)
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
