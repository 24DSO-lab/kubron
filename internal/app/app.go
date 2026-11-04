// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/clusterhealth"
	"github.com/24DSO-labs/kubron/internal/credential"
	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/internal/pfproxy"
	"github.com/24DSO-labs/kubron/internal/replicaevents"
	"github.com/24DSO-labs/kubron/internal/store"
	"github.com/24DSO-labs/kubron/internal/streamid"
	"github.com/24DSO-labs/kubron/internal/termbuf"
)

type App struct {
	platform           Platform
	k8sMgr             *k8s.Manager
	pfMgr              *k8s.PortForwardManager
	audit              *audit.Logger
	auth               *auth.Service // nil, если хранилище пользователей не открылось (см. authErr)
	authErr            error
	dir                auth.Directory // команды и участники; nil вне сервисного режима
	store              store.Store    // общее хранилище настроек и данных
	webOIDCRedirectURL string         // публичный callback URL; задаётся серверной оболочкой до запуска
	// sessionID — веб-сессия владельца вида (пусто у корневого экземпляра и в десктопе).
	sessionID string
	// Port-forward в вебе (ADR-0017): режим и прокси; задаются до создания видов (SetPortForwardMode/Proxy).
	pfMode    string
	pfService *pfproxy.Service
	// Сообщения между репликами (ADR-0016): publish отправляет их другим репликам (nil — реплика одна),
	// health — монитор проверок кластеров (для немедленного чтения результатов лидера).
	publish func(replicaevents.Message)
	health  *clusterhealth.Monitor
	// replicaID — идентификатор этой реплики (ADR-0016): потоки терминала и логов несут его в идентификаторе, чужие
	// отклоняются. Пусто — реплика одна (десктоп, режим без PostgreSQL): идентификаторы принимаются любые.
	replicaID string
	// interactive — отключённые администратором терминалы и оболочка узла (KUBRON_DISABLE_*).
	interactive InteractivePolicy
	// broker — вход в кластеры (ADR-0018); nil — старый путь без вызовов. conn — состояние входов этого вида.
	broker credential.Broker
	conn   *connectState
	rev    *revocations // отозванные личные входы реплики; общий для всех видов
	// limitUser и limitIDP ограничивают попытки AnswerChallenge на пользователя и на провайдера входа; nil — без лимита.
	limitUser, limitIDP AttemptLimiter
	// finish подменяется в тестах: подключение и проверка кластера после успешного входа.
	finish func(clusterID string) error
}

// SetStore задаёт хранилище вместо файлового; вызывается до OnStartup. Это функция пакета, а не метод App:
// она не должна попадать в привязки Wails и RPC.
func SetStore(a *App, st store.Store) { a.store = st }

// NewApp создаёт ядро приложения. p — возможности оболочки (Wails или HTTP-сервер);
// nil допустим (все операции платформы тогда не делают ничего).
func NewApp(p Platform) *App {
	if p == nil {
		p = nopPlatform{}
	}
	mgr := k8s.NewManager()
	a := &App{
		platform: p,
		k8sMgr:   mgr,
		pfMgr:    k8s.NewPortForwardManager(mgr),
		conn:     newConnectState(),
		rev:      &revocations{},
	}
	a.pfMgr.SetObserver(a.portForwardObserver())
	a.configureTerminals()
	return a
}

// shutdown вызывается Wails при закрытии приложения: гасим все port-forward туннели.
func (a *App) shutdown(_ context.Context) {
	a.pfMgr.StopAll()
	// Release what the sign-in providers hold (the loopback listener of a browser sign-in on the desktop).
	if c, ok := a.broker.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

func (a *App) startup(ctx context.Context) {
	// Бэкенд сообщает фронтенду об изменениях кластера (informers) вместо постоянного опроса
	a.k8sMgr.SetNotifier(a.platform.Emit)
	if err := k8s.InitKubronDir(); err != nil {
		println("Error:", err.Error())
	}
	if a.store != nil {
		a.initAuthWith(a.store)
	} else {
		a.initAuth(k8s.GetKubronDir())
	}
	if a.auth != nil {
		go a.auth.RunIdleWatcher(ctx, 10*time.Second)
	}
}

// === Возможности оболочки ===

// GetCapabilities — что доступно в текущей оболочке. Вызывается до входа в систему
// (экран входа зависит от флагов), поэтому authz не требует: см. publicMethods в policy_test.
func (a *App) GetCapabilities() Capabilities {
	c := a.platform.Capabilities()
	if c.PortForward {
		c.PortForwardMode = a.portForwardMode()
		if c.PortForwardMode == k8s.PortForwardModeOff {
			c.PortForward = false
		}
	}
	if a.interactive.DisableExec {
		c.Exec, c.NodeShell = false, false
	}
	if a.interactive.DisableNodeShell {
		c.NodeShell = false
	}
	c.Teams = a.dir != nil // команды есть там, где хранилище умеет их хранить, а не в оболочке
	c.InCluster = c.Teams && k8s.InClusterAvailable()
	return c
}

// === Kubron Cluster Management ===

func (a *App) GetClusterStatuses() []k8s.ClusterStatus {
	if a.authz(auth.PermRead) != nil {
		return nil
	}
	// В сервисном режиме пользователь видит только кластеры своих команд.
	all := a.k8sMgr.GetClusterStatuses()
	out := make([]k8s.ClusterStatus, 0, len(all))
	for _, st := range all {
		if a.auth.CanSeeCluster(st.ID) {
			out = append(out, st)
		}
	}
	return out
}

// PickKubeconfigFile opens a native file dialog and returns the chosen path
// (empty string when the dialog was cancelled). The dialog title is supplied
// by the frontend (already translated); an empty title falls back to English.
func (a *App) PickKubeconfigFile(title string) (string, error) {
	if err := a.authz(auth.PermClusterManage); err != nil {
		return "", err
	}
	if title == "" {
		title = "Select kubeconfig file"
	}
	return a.platform.PickFile(title)
}

// AddClusterFromFile adds a cluster from a kubeconfig file previously chosen
// with PickKubeconfigFile.
func (a *App) AddClusterFromFile(path string, settings k8s.ClusterSettings) (*k8s.ClusterMeta, error) {
	if err := a.authzTeam(auth.PermClusterManage, a.defaultTeam(), "displayName", settings.DisplayName); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("kubeconfig file is not selected")
	}
	return k8s.AddClusterFromFile(path, settings)
}

func (a *App) AddClusterFromText(kubeconfigText string, settings k8s.ClusterSettings) (*k8s.ClusterMeta, error) {
	// Без указания команды кластер попадает в команду по умолчанию: добавлять в неё может только её администратор.
	if err := a.authzTeam(auth.PermClusterManage, a.defaultTeam(), "displayName", settings.DisplayName); err != nil {
		return nil, err
	}
	return k8s.AddClusterFromText(kubeconfigText, settings)
}

// InspectKubeconfig разбирает kubeconfig (файл, текст или сохранённый кластер) и возвращает
// контексты без секретов — для выбора контекста в диалогах кластера.
func (a *App) InspectKubeconfig(src k8s.KubeconfigSource) (*k8s.KubeconfigInfo, error) {
	if err := a.authz(auth.PermClusterManage); err != nil {
		return nil, err
	}
	if err := a.checkKubeconfigSource(src); err != nil {
		return nil, err
	}
	return k8s.InspectKubeconfig(src)
}

// TestKubeconfig пробно подключается к кластеру по kubeconfig, ничего не сохраняя.
func (a *App) TestKubeconfig(src k8s.KubeconfigSource, contextName string) (*k8s.ConnectionCheck, error) {
	if err := a.authz(auth.PermClusterManage, "context", contextName); err != nil {
		return nil, err
	}
	if err := a.checkKubeconfigSource(src); err != nil {
		return nil, err
	}
	return k8s.CheckKubeconfig(src, contextName)
}

// GetClusterKubeconfig возвращает текст сохранённого kubeconfig (с секретами) для правки в диалоге.
func (a *App) GetClusterKubeconfig(id string) (string, error) {
	if err := a.authzCluster(auth.PermClusterManage, id, "id", id); err != nil {
		return "", err
	}
	return k8s.ReadClusterKubeconfig(id)
}

func (a *App) DeleteCluster(id string) error {
	if err := a.authzCluster(auth.PermClusterManage, id, "id", id); err != nil {
		return err
	}
	if err := k8s.DeleteCluster(id); err != nil {
		return err
	}
	// Подключение и проброшенные порты удалённого активного кластера больше не нужны.
	if a.k8sMgr.RemoveConnection(id) {
		a.pfMgr.StopAll()
	}
	return nil
}

// UpdateCluster сохраняет настройки кластера; непустой kubeconfig заменяет сохранённый.
// Содержимое kubeconfig в журнал не попадает — только способ замены.
func (a *App) UpdateCluster(id string, settings k8s.ClusterSettings, kubeconfig k8s.KubeconfigSource) (*k8s.ClusterUpdateResult, error) {
	if err := a.authzCluster(auth.PermClusterManage, id, "id", id, "displayName", settings.DisplayName, "kubeconfig", kubeconfigChange(kubeconfig)); err != nil {
		return nil, err
	}
	kubeconfig.BaseClusterID = id // скрытые секреты берутся только у редактируемого кластера
	if err := a.checkKubeconfigSource(kubeconfig); err != nil {
		return nil, err
	}
	res, err := k8s.UpdateClusterSettings(id, settings, kubeconfig)
	if err != nil {
		return nil, err
	}
	if res.CredentialsChanged {
		a.recordCredentialsUpdate(id)
	}
	// Подключение собрано по прежнему kubeconfig/контексту: забываем его, следующее
	// подключение построит новое. Проброшенные порты старого активного кластера теряют смысл.
	if res.ConnectionChanged && a.k8sMgr.RemoveConnection(id) {
		a.pfMgr.StopAll()
	}
	return res, nil
}

// kubeconfigChange описывает способ замены kubeconfig для журнала аудита (без содержимого).
func kubeconfigChange(src k8s.KubeconfigSource) string {
	switch {
	case src.Path != "":
		return "file"
	case strings.TrimSpace(src.Text) != "":
		return "text"
	}
	return "unchanged"
}

// ConnectToCluster is the legacy call, kept for old clients: a thin wrapper over the new flow. If the cluster
// needs the user to answer a challenge, it returns *ChallengeRequiredError instead of connecting.
func (a *App) ConnectToCluster(clusterID string) error {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return err
	}
	res, err := a.connect(context.Background(), clusterID)
	if err != nil {
		return err
	}
	if res.Status == "challenge" && res.Challenge != nil {
		if a.broker != nil { // an old client will never answer: do not leave the request pending
			if pc, ok := a.conn.cluster(res.Challenge.ID); ok {
				_ = a.broker.Cancel(context.Background(), pc.sub, res.Challenge.ID)
			}
			a.conn.forget(res.Challenge.ID)
		}
		return &ChallengeRequiredError{ClusterID: clusterID, Kind: res.Challenge.Kind}
	}
	return nil
}

func (a *App) GetLastActiveCluster() string {
	if a.authz(auth.PermRead) != nil {
		return ""
	}
	// В веб-режиме не читаем общую настройку: она раскрывает выбор другого пользователя
	// и заставляет новую сессию автоматически подключаться к его кластеру.
	if a.sessionID != "" {
		return a.k8sMgr.GetActiveContext()
	}
	config, err := k8s.LoadKubronConfig()
	if err != nil || !a.auth.CanSeeCluster(config.LastActiveCluster) {
		return "" // настройка общая: чужой кластер не должен выдавать даже своё имя
	}
	return config.LastActiveCluster
}

func (a *App) GetAppSettings() (*k8s.KubronConfig, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return k8s.LoadKubronConfig()
}

func (a *App) SaveAppSettings(settings *k8s.KubronConfig) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	return k8s.SaveKubronConfig(settings)
}

// === Контексты ===

func (a *App) GetActiveContext() string {
	if a.authz(auth.PermRead) != nil {
		return ""
	}
	return a.k8sMgr.GetActiveContext()
}

// === Overview ===

func (a *App) GetClusterOverview() (*k8s.ClusterOverview, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetClusterOverview()
}

func (a *App) GetOverviewHotspots(withMetrics bool) (*k8s.OverviewHotspots, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetOverviewHotspots(withMetrics)
}

func (a *App) GetOverviewRisks() (*k8s.OverviewRisks, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetOverviewRisks()
}

func (a *App) GetRecentChanges(windowMinutes int) ([]k8s.ChangeEvent, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetRecentChanges(windowMinutes)
}

// Только имя, субъект и дата окончания; содержимое секретов наружу не уходит.
func (a *App) GetCertificateExpiry() (*k8s.CertScan, error) {
	if err := a.authz(auth.PermSecretsRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCertificateExpiry()
}

// === Node actions ===

func (a *App) CordonNode(name string, cordon bool) error {
	if err := a.authz(auth.PermOperate, "node", name, "cordon", cordon); err != nil {
		return err
	}
	return a.k8sMgr.CordonNode(name, cordon)
}

func (a *App) DrainNode(name string) error {
	if err := a.authz(auth.PermWrite, "node", name); err != nil {
		return err
	}
	return a.k8sMgr.DrainNode(name)
}

func (a *App) StartNodeShell(name string) (*k8s.NodeShellSession, error) {
	if err := a.authz(auth.PermNodeShell, "node", name); err != nil {
		return nil, err
	}
	if a.interactive.DisableExec {
		return nil, errExecDisabled
	}
	if a.interactive.DisableNodeShell {
		return nil, errNodeShellDisabled
	}
	return a.k8sMgr.StartNodeShell(name)
}

// === Argo CD ===

func (a *App) SuspendArgoAutoSync(apps []k8s.ArgoAppRef) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermWrite, "apps", argoAuditApps(apps)); err != nil {
		return nil, err
	}
	return a.k8sMgr.SuspendArgoAutoSync(apps)
}

func (a *App) ResumeArgoAutoSync(apps []k8s.ArgoAppRef) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermWrite, "apps", argoAuditApps(apps)); err != nil {
		return nil, err
	}
	return a.k8sMgr.ResumeArgoAutoSync(apps)
}

func (a *App) DeleteArgoApplication(namespace, name string, cascade bool) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name, "cascade", cascade); err != nil {
		return err
	}
	return a.k8sMgr.DeleteArgoApplication(namespace, name, cascade)
}

// ArgoInstalled сообщает, установлен ли Argo CD (CRD Application) в активном кластере.
func (a *App) ArgoInstalled() (bool, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return false, err
	}
	return a.k8sMgr.ArgoInstalled()
}

// GetArgoApplications возвращает Argo CD Application; пустой namespace — из всех namespace.
func (a *App) GetArgoApplications(namespaces []string) ([]k8s.ArgoApplicationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoApplications(namespaces)
}

// SyncArgoApplication запускает синхронизацию одного или нескольких Application.
// Результат — по каждому приложению отдельно (одна ошибка не отменяет остальные).
func (a *App) SyncArgoApplication(apps []k8s.ArgoAppRef, opts k8s.ArgoSyncOptions) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermOperate, "apps", argoAuditApps(apps), "prune", opts.Prune, "force", opts.Force, "dryRun", opts.DryRun, "revision", opts.Revision); err != nil {
		return nil, err
	}
	return a.k8sMgr.SyncArgoApplication(apps, opts)
}

func (a *App) RefreshArgoApplication(apps []k8s.ArgoAppRef, hard bool) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermOperate, "apps", argoAuditApps(apps), "hard", hard); err != nil {
		return nil, err
	}
	return a.k8sMgr.RefreshArgoApplication(apps, hard)
}

func (a *App) TerminateArgoOperation(apps []k8s.ArgoAppRef) ([]k8s.ArgoActionResult, error) {
	if err := a.authz(auth.PermOperate, "apps", argoAuditApps(apps)); err != nil {
		return nil, err
	}
	return a.k8sMgr.TerminateArgoOperation(apps)
}

func (a *App) RollbackArgoApplication(namespace, name string, id int64, prune, dryRun bool) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "id", id, "prune", prune, "dryRun", dryRun); err != nil {
		return err
	}
	return a.k8sMgr.RollbackArgoApplication(namespace, name, id, prune, dryRun)
}

// GetArgoApplicationSets возвращает Argo CD ApplicationSet; пустой namespace — из всех namespace.
func (a *App) GetArgoApplicationSets(namespaces []string) ([]k8s.ArgoApplicationSetInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoApplicationSets(namespaces)
}

// GetArgoProjects возвращает Argo CD AppProject; пустой namespace — из всех namespace.
func (a *App) GetArgoProjects(namespaces []string) ([]k8s.ArgoProjectInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoProjects(namespaces)
}

// GetArgoRepositories возвращает репозитории Argo CD (санитизировано: без паролей, токенов и ключей).
func (a *App) GetArgoRepositories(namespaces []string) ([]k8s.ArgoRepositoryInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoRepositories(namespaces)
}

// GetArgoClusters возвращает кластеры Argo CD (санитизировано: без config с токенами и сертификатами).
func (a *App) GetArgoClusters(namespaces []string) ([]k8s.ArgoClusterInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoClusters(namespaces)
}

// GetArgoApplicationGraph возвращает граф ресурсов Application (владение, Service → Pod, Ingress → Service).
func (a *App) GetArgoApplicationGraph(namespace, name string) (*k8s.ArgoGraph, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoApplicationGraph(namespace, name)
}

// GetArgoAPIStatus сообщает, задан ли токен Argo CD API для активного кластера (сам токен наружу не отдаётся).
func (a *App) GetArgoAPIStatus() (*k8s.ArgoAPIStatus, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetArgoAPIStatus()
}

// SetArgoAPIToken сохраняет токен Argo CD API для активного кластера.
// Токен нельзя передавать в authz: аргументы попадают в журнал аудита.
func (a *App) SetArgoAPIToken(token string) error {
	// Токен относится к активному кластеру: право проверяется по роли в его команде, а не «где-нибудь админ».
	if err := a.authzCluster(auth.PermClusterManage, a.k8sMgr.GetActiveContext()); err != nil {
		return err
	}
	return a.k8sMgr.SetArgoAPIToken(token)
}

// GenerateArgoAPIToken получает токен Argo CD API для аккаунта и сохраняет его для активного кластера.
// Логин и пароль Argo CD нужны только для этого вызова: они не сохраняются и не передаются в authz
// (аргументы authz попадают в журнал аудита); токен фронтенду не возвращается.
func (a *App) GenerateArgoAPIToken(account, username, password string, days int) error {
	if err := a.authzCluster(auth.PermClusterManage, a.k8sMgr.GetActiveContext(), "account", account); err != nil {
		return err
	}
	return a.pfMgr.GenerateArgoAPIToken(account, username, password, days)
}

// DeleteArgoAPIToken удаляет токен Argo CD API активного кластера.
func (a *App) DeleteArgoAPIToken() error {
	if err := a.authz(auth.PermWrite); err != nil {
		return err
	}
	return a.k8sMgr.DeleteArgoAPIToken()
}

// GetArgoApplicationDiff сравнивает желаемое (git) и текущее состояние ресурсов Application через Argo CD API.
// Поднимает туннель к argocd-server, поэтому требует права на проброс портов.
func (a *App) GetArgoApplicationDiff(namespace, name string) (*k8s.ArgoDiff, error) {
	if err := a.authz(auth.PermPortForward, "namespace", namespace, "name", name); err != nil {
		return nil, err
	}
	return a.pfMgr.GetArgoApplicationDiff(namespace, name)
}

// OpenArgoUI пробрасывает порт на argocd-server и возвращает URL для браузера.
func (a *App) OpenArgoUI() (*k8s.ArgoUIInfo, error) {
	if err := a.authz(auth.PermPortForward); err != nil {
		return nil, err
	}
	return a.pfMgr.OpenArgoUI()
}

// argoAuditApps — компактная цель действия для журнала: первые приложения и счётчик остальных.
func argoAuditApps(apps []k8s.ArgoAppRef) string {
	const show = 5
	names := make([]string, 0, show)
	for i, ref := range apps {
		if i == show {
			break
		}
		names = append(names, ref.Namespace+"/"+ref.Name)
	}
	s := strings.Join(names, ",")
	if len(apps) > show {
		s += fmt.Sprintf(" +%d", len(apps)-show)
	}
	return s
}

// === Неймспейсы ===

func (a *App) GetNamespaces() ([]k8s.NamespaceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNamespaces()
}

// === Поды ===

func (a *App) GetPods(namespaces []string) ([]k8s.PodInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPods(namespaces)
}

func (a *App) GetPodDetail(namespace, name string) (*k8s.PodDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPodDetail(namespace, name)
}

func (a *App) GetPodLogs(namespace, name, container string, lines int64) (string, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return "", err
	}
	return a.k8sMgr.GetPodLogs(namespace, name, container, lines)
}

func (a *App) EvictPod(namespace, name string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.EvictPod(namespace, name)
}

// === Deployments ===

func (a *App) GetDeployments(namespaces []string) ([]k8s.DeploymentInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetDeployments(namespaces)
}

func (a *App) ScaleDeployment(namespace, name string, replicas int32) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "replicas", replicas); err != nil {
		return err
	}
	return a.k8sMgr.ScaleDeployment(namespace, name, replicas)
}

func (a *App) RestartDeployment(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RestartDeployment(namespace, name)
}

func (a *App) GetDeploymentRevisions(namespace, name string) ([]k8s.RevisionInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetDeploymentRevisions(namespace, name)
}

func (a *App) RollbackDeployment(namespace, name string, revision int64) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "revision", revision); err != nil {
		return err
	}
	return a.k8sMgr.RollbackDeployment(namespace, name, revision)
}

// === StatefulSets ===

func (a *App) GetStatefulSets(namespaces []string) ([]k8s.StatefulSetInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetStatefulSets(namespaces)
}

func (a *App) ScaleStatefulSet(namespace, name string, replicas int32) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "replicas", replicas); err != nil {
		return err
	}
	return a.k8sMgr.ScaleStatefulSet(namespace, name, replicas)
}

// === ReplicaSets ===

func (a *App) GetReplicaSets(namespaces []string) ([]k8s.ReplicaSetInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetReplicaSets(namespaces)
}

func (a *App) ScaleReplicaSet(namespace, name string, replicas int32) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "replicas", replicas); err != nil {
		return err
	}
	return a.k8sMgr.ScaleReplicaSet(namespace, name, replicas)
}

// === DaemonSets ===

func (a *App) GetDaemonSets(namespaces []string) ([]k8s.DaemonSetInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetDaemonSets(namespaces)
}

// === Jobs ===

func (a *App) GetJobs(namespaces []string) ([]k8s.JobInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetJobs(namespaces)
}

func (a *App) RestartDaemonSet(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RestartDaemonSet(namespace, name)
}

func (a *App) RetryJob(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RetryJob(namespace, name)
}

// === CronJobs ===

func (a *App) GetCronJobs(namespaces []string) ([]k8s.CronJobInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCronJobs(namespaces)
}

func (a *App) GetCronJobJobs(namespace, cronJobName string) ([]k8s.JobInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCronJobJobs(namespace, cronJobName)
}

func (a *App) GetCronJobDetail(namespace, name string) (*k8s.CronJobDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCronJobDetail(namespace, name)
}

func (a *App) TriggerCronJob(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.TriggerCronJob(namespace, name)
}

func (a *App) ToggleCronJobSuspend(namespace, name string) (bool, error) {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return false, err
	}
	return a.k8sMgr.ToggleCronJobSuspend(namespace, name)
}

// === Services ===

func (a *App) GetServices(namespaces []string) ([]k8s.ServiceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetServices(namespaces)
}

func (a *App) GetServiceDetail(namespace, name string) (*k8s.ServiceDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetServiceDetail(namespace, name)
}

// === Ingresses ===

func (a *App) GetIngresses(namespaces []string) ([]k8s.IngressInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetIngresses(namespaces)
}

// === Nodes ===

func (a *App) GetNodes() ([]k8s.NodeInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNodes()
}

func (a *App) GetNodeDetail(name string) (*k8s.NodeDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNodeDetail(name)
}

// === Applications ===

func (a *App) GetApplications(namespaces []string) ([]k8s.ApplicationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetApplications(namespaces)
}

// === Helm ===

// PreviewHelmUpgrade — dry-run апгрейда values; в ответе манифесты (Secret скрыты), нужен secrets.read.
func (a *App) PreviewHelmUpgrade(namespace, name, valuesYAML string) (*k8s.HelmUpgradePreview, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name); err != nil {
		return nil, err
	}
	return a.k8sMgr.PreviewHelmUpgrade(namespace, name, valuesYAML)
}

// UpgradeHelmRelease применяет новые values; возвращает номер новой ревизии.
func (a *App) UpgradeHelmRelease(namespace, name, valuesYAML, opID string) (int, error) {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name); err != nil {
		return 0, err
	}
	return a.k8sMgr.UpgradeHelmRelease(namespace, name, valuesYAML, opID)
}

// GetHelmUpgradeSources — репозитории, из которых можно обновить чарт релиза, и их версии (локальные индексы).
func (a *App) GetHelmUpgradeSources(namespace, name string) (*k8s.HelmUpgradeSources, error) {
	if err := a.authz(auth.PermRead, "namespace", namespace, "name", name); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmUpgradeSources(namespace, name)
}

// PreviewHelmChartUpgrade — dry-run апгрейда релиза на другую версию чарта; в ответе манифесты
// (Secret скрыты) и values, нужен secrets.read.
func (a *App) PreviewHelmChartUpgrade(req k8s.HelmChartUpgradeRequest) (*k8s.HelmChartUpgradePreview, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", req.Namespace, "name", req.Name, "repo", req.Repo, "version", req.Version); err != nil {
		return nil, err
	}
	return a.k8sMgr.PreviewHelmChartUpgrade(req)
}

// UpgradeHelmChart переводит релиз на другую версию чарта; возвращает номер новой ревизии.
// В authz не передаём Values: там могут быть пароли, а аргументы попадают в журнал аудита.
func (a *App) UpgradeHelmChart(req k8s.HelmChartUpgradeRequest) (int, error) {
	if err := a.authz(auth.PermWrite, "namespace", req.Namespace, "name", req.Name, "repo", req.Repo, "version", req.Version); err != nil {
		return 0, err
	}
	return a.k8sMgr.UpgradeHelmChart(req)
}

// ВАЖНО: в authz не передаём URL и пароль. Адрес, введённый пользователем, может содержать
// логин:пароль, а аргументы попадают в журнал аудита. Только имя репозитория.

// ListHelmRepos — репозитории чартов (локальный файл Kubron; пароли наружу не отдаются).
func (a *App) ListHelmRepos() ([]k8s.HelmRepoInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.ListHelmRepos()
}

// AddHelmRepo добавляет репозиторий и проверяет его, скачав index.yaml.
func (a *App) AddHelmRepo(name, url, username, password string, insecureTLS bool) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "name", name); err != nil {
		return err
	}
	a.rememberHelmActor()
	return a.k8sMgr.AddHelmRepo(name, url, username, password, insecureTLS)
}

func (a *App) RemoveHelmRepo(name string) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RemoveHelmRepo(name)
}

// UpdateHelmRepos обновляет индекс одного репозитория; пустое имя — всех.
func (a *App) UpdateHelmRepos(name string) ([]k8s.HelmRepoUpdateResult, error) {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "name", name); err != nil {
		return nil, err
	}
	return a.k8sMgr.UpdateHelmRepos(name)
}

func (a *App) SearchHelmCharts(query, repo string) (*k8s.HelmCatalogResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.SearchHelmCharts(query, repo)
}

func (a *App) GetHelmChartVersions(repo, chart string) ([]k8s.HelmChartVersionInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmChartVersions(repo, chart)
}

// GetHelmChartPackage скачивает архив чарта из репозитория и отдаёт README и values.yaml.
func (a *App) GetHelmChartPackage(repo, chart, version string) (*k8s.HelmChartPackage, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmChartPackage(repo, chart, version)
}

func (a *App) GetHelmReleases() ([]k8s.HelmReleaseInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmReleases()
}

func (a *App) GetHelmCharts() ([]k8s.HelmChartInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmCharts()
}
func (a *App) GetHelmReleaseDetail(namespace, name string) (*k8s.HelmReleaseDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmReleaseDetail(namespace, name)
}

// GetHelmReleaseContent отдаёт values, notes и манифест ревизии. Содержимое не передаётся в authz:
// аргументы попадают в журнал аудита.
func (a *App) GetHelmReleaseContent(namespace, name string, revision int) (*k8s.HelmReleaseContent, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name, "revision", revision); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmReleaseContent(namespace, name, revision)
}

func (a *App) RollbackHelmRelease(namespace, name string, revision int, opID string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "revision", revision); err != nil {
		return err
	}
	return a.k8sMgr.RollbackHelmRelease(namespace, name, revision, opID)
}

func (a *App) UninstallHelmRelease(namespace, name string, keepHistory bool, opID string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name, "keepHistory", keepHistory); err != nil {
		return err
	}
	return a.k8sMgr.UninstallHelmRelease(namespace, name, keepHistory, opID)
}

// GetHelmRevisionDiff сравнивает ревизии релиза (values и манифест); как и GetHelmReleaseContent, требует secrets.read.
func (a *App) GetHelmRevisionDiff(namespace, name string, from, to int) (*k8s.HelmRevisionDiff, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name, "from", from, "to", to); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmRevisionDiff(namespace, name, from, to)
}

// Права: установка создаёт произвольные ресурсы, поэтому PermWrite (как апгрейд). Preview тоже за Write:
// он нужен только тому, кто может установить. Values в authz НЕ передаются (могут содержать секреты).

// PreviewHelmInstall — dry-run установки чарта из каталога (кластер не затрагивается).
func (a *App) PreviewHelmInstall(req k8s.HelmInstallRequest) (*k8s.HelmInstallResult, error) {
	if err := a.authz(auth.PermWrite, "namespace", req.Namespace, "release", req.ReleaseName, "chart", req.Chart); err != nil {
		return nil, err
	}
	return a.k8sMgr.PreviewHelmInstall(req)
}

// InstallHelmChart устанавливает чарт из репозитория каталога.
func (a *App) InstallHelmChart(req k8s.HelmInstallRequest) (*k8s.HelmInstallResult, error) {
	if err := a.authz(auth.PermWrite, "namespace", req.Namespace, "release", req.ReleaseName, "chart", req.Chart); err != nil {
		return nil, err
	}
	return a.k8sMgr.InstallHelmChart(req)
}

// CancelHelmOperation прерывает идущую операцию (установка и апгрейд); откат и удаление отменить нельзя.
func (a *App) CancelHelmOperation(opID string) error {
	if err := a.authz(auth.PermWrite, "opID", opID); err != nil {
		return err
	}
	return a.k8sMgr.CancelHelmOperation(opID)
}

// AddHelmOCIRepo добавляет OCI-реестр с набором чартов. В authz — только имя: пароль и адрес не логируются.
func (a *App) AddHelmOCIRepo(name, url string, charts []string, username, password string) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "name", name); err != nil {
		return err
	}
	a.rememberHelmActor()
	return a.k8sMgr.AddHelmOCIRepo(name, url, charts, username, password)
}

func (a *App) AddHelmOCIChart(repo, chart string) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "repo", repo, "chart", chart); err != nil {
		return err
	}
	return a.k8sMgr.AddHelmOCIChart(repo, chart)
}

func (a *App) RemoveHelmOCIChart(repo, chart string) error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID(), "repo", repo, "chart", chart); err != nil {
		return err
	}
	return a.k8sMgr.RemoveHelmOCIChart(repo, chart)
}

// ListHelmOperations — история Helm-операций (без журнала Helm).
func (a *App) ListHelmOperations(q k8s.HelmOperationQuery) ([]k8s.HelmOperationRecord, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.ListHelmOperations(q)
}

// GetHelmOperation — запись истории вместе с журналом Helm.
func (a *App) GetHelmOperation(id string) (*k8s.HelmOperationRecord, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHelmOperation(id)
}

// ClearHelmOperations стирает историю Helm-операций.
func (a *App) ClearHelmOperations() error {
	if err := a.authzCluster(auth.PermClusterManage, a.activeClusterID()); err != nil {
		return err
	}
	return a.k8sMgr.ClearHelmOperations()
}

// === Events ===

func (a *App) GetEvents(namespaces []string) ([]k8s.EventInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetEvents(namespaces)
}

// === ConfigMaps ===

func (a *App) GetConfigMaps(namespaces []string) ([]k8s.ConfigMapInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetConfigMaps(namespaces)
}

func (a *App) GetConfigMapDetail(namespace, name string) (*k8s.ConfigMapInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetConfigMapDetail(namespace, name)
}

func (a *App) UpdateConfigMap(namespace, name string, data map[string]string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.UpdateConfigMap(namespace, name, data)
}

// === Secrets ===

func (a *App) GetSecrets(namespaces []string) ([]k8s.SecretInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetSecrets(namespaces)
}

func (a *App) GetSecretValue(namespace, name, key string) (string, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name, "key", key); err != nil {
		return "", err
	}
	return a.k8sMgr.GetSecretValue(namespace, name, key)
}

func (a *App) GetSecretBase64Value(namespace, name, key string) (string, error) {
	if err := a.authz(auth.PermSecretsRead, "namespace", namespace, "name", name, "key", key); err != nil {
		return "", err
	}
	return a.k8sMgr.GetSecretBase64Value(namespace, name, key)
}

func (a *App) UpdateSecret(namespace, name string, data map[string]string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.UpdateSecret(namespace, name, data)
}

// === Storage ===

func (a *App) GetPersistentVolumes() ([]k8s.PVInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPersistentVolumes()
}

func (a *App) GetPersistentVolumeClaims(namespaces []string) ([]k8s.PVCInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPersistentVolumeClaims(namespaces)
}

func (a *App) GetStorageClasses() ([]k8s.StorageClassInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetStorageClasses()
}

// === Config (continued) ===

func (a *App) GetLimitRanges(namespaces []string) ([]k8s.LimitRangeInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetLimitRanges(namespaces)
}

func (a *App) GetHPA(namespaces []string) ([]k8s.HPAInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetHPA(namespaces)
}

func (a *App) GetPodDisruptionBudgets(namespaces []string) ([]k8s.PDBInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPodDisruptionBudgets(namespaces)
}

func (a *App) GetPriorityClasses() ([]k8s.PriorityClassInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPriorityClasses()
}

func (a *App) GetRuntimeClasses() ([]k8s.RuntimeClassInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetRuntimeClasses()
}

func (a *App) GetLeases(namespaces []string) ([]k8s.LeaseInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetLeases(namespaces)
}

func (a *App) GetMutatingWebhookConfigurations() ([]k8s.WebhookConfigurationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetMutatingWebhookConfigurations()
}

func (a *App) GetValidatingWebhookConfigurations() ([]k8s.WebhookConfigurationInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetValidatingWebhookConfigurations()
}

// === Endpoints ===

func (a *App) GetEndpoints(namespaces []string) ([]k8s.EndpointListInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetEndpoints(namespaces)
}

func (a *App) GetEndpointSlices(namespaces []string) ([]k8s.EndpointSliceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetEndpointSlices(namespaces)
}

// === Network Policies ===

func (a *App) GetNetworkPolicies(namespaces []string) ([]k8s.NetworkPolicyInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNetworkPolicies(namespaces)
}

// === Ingress Classes ===

func (a *App) GetIngressClasses() ([]k8s.IngressClassInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetIngressClasses()
}

func (a *App) SetDefaultIngressClass(name string, makeDefault bool) error {
	if err := a.authz(auth.PermWrite, "name", name, "default", makeDefault); err != nil {
		return err
	}
	return a.k8sMgr.SetDefaultIngressClass(name, makeDefault)
}

// === Access Control ===

func (a *App) GetServiceAccounts(namespaces []string) (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetServiceAccounts(namespaces)
}

func (a *App) GetRoles(namespaces []string) (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetRoles(namespaces)
}

func (a *App) GetRoleBindings(namespaces []string) (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetRoleBindings(namespaces)
}

func (a *App) GetClusterRoles() (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetClusterRoles()
}

func (a *App) GetClusterRoleBindings() (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetClusterRoleBindings()
}

func (a *App) GetServiceAccountDetail(namespace, name string) (interface{}, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetServiceAccountDetail(namespace, name)
}

func (a *App) RestartStatefulSet(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RestartStatefulSet(namespace, name)
}

func (a *App) GetResourceQuotas(namespaces []string) ([]k8s.ResourceQuotaInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetResourceQuotas(namespaces)
}

// === YAML ===

func (a *App) GetResourceYAML(namespace, kind, name string, clean bool) (string, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return "", err
	}
	// YAML секрета содержит его данные, поэтому требует того же права, что и GetSecretValue.
	if strings.EqualFold(strings.TrimSpace(kind), "secret") {
		if err := a.authz(auth.PermSecretsRead); err != nil {
			return "", err
		}
	}
	return a.k8sMgr.GetResourceYAML(namespace, kind, name, clean)
}

func (a *App) ApplyYAML(namespace, kind, name, jsonData string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "kind", kind, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.ApplyYAML(namespace, kind, name, jsonData)
}

// CreateResourceYAML создаёт новый объект из манифеста (кнопка «+» в списках) и возвращает его имя.
// kind — ключ типа (id определения ресурса на фронтенде). namespace — запасное значение,
// если в манифесте metadata.namespace не указан.
func (a *App) CreateResourceYAML(namespace, kind, yamlData string) (string, error) {
	// authz — первый оператор (его проверяет policy_test). namespace и name в журнал берём из самого
	// манифеста: именно их увидит кластер.
	if err := a.authz(auth.PermWrite, "namespace", k8s.ManifestNamespace(yamlData, namespace),
		"kind", kind, "name", k8s.ManifestName(yamlData)); err != nil {
		return "", err
	}
	return a.k8sMgr.CreateResourceYAML(namespace, kind, yamlData)
}

// === Exec ===

// === Metrics ===

func (a *App) GetPodMetrics(namespaces []string) ([]k8s.PodMetrics, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPodMetrics(namespaces)
}

func (a *App) GetNodeMetrics() ([]k8s.NodeMetrics, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNodeMetrics()
}

func (a *App) GetMetricsStatus() (*k8s.MetricsStackStatus, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetMetricsStatus()
}

func (a *App) InstallMetricsStack() error {
	if err := a.authz(auth.PermWrite); err != nil {
		return err
	}
	return a.k8sMgr.InstallMetricsStack()
}

func (a *App) UninstallMetricsStack() error {
	if err := a.authz(auth.PermWrite); err != nil {
		return err
	}
	return a.k8sMgr.UninstallMetricsStack()
}

func (a *App) GetClusterMetricsSnapshot() (*k8s.ClusterMetricsSnapshot, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetClusterMetricsSnapshot()
}

func (a *App) QueryPrometheus(query string) (*k8s.PromQueryResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.QueryPrometheus(query)
}

func (a *App) QueryPrometheusRange(query string, startUnix, endUnix int64, step string) (*k8s.PromQueryResult, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	start := time.Unix(startUnix, 0)
	end := time.Unix(endUnix, 0)
	return a.k8sMgr.QueryPrometheusRange(query, start, end, step)
}

// === Port Forward ===

func (a *App) StartPortForward(namespace, pod string, localPort, podPort int) (*k8s.PortForwardSession, error) {
	if err := a.authz(auth.PermPortForward, "namespace", namespace, "pod", pod, "localPort", localPort, "podPort", podPort); err != nil {
		return nil, err
	}
	return a.pfMgr.StartPortForward(namespace, pod, localPort, podPort)
}

func (a *App) StartServicePortForward(namespace, service string, localPort, servicePort int) (*k8s.PortForwardSession, error) {
	if err := a.authz(auth.PermPortForward, "namespace", namespace, "service", service, "localPort", localPort, "servicePort", servicePort); err != nil {
		return nil, err
	}
	return a.pfMgr.StartServicePortForward(namespace, service, localPort, servicePort)
}

func (a *App) StopPortForward(id string) error {
	if err := a.authz(auth.PermPortForward, "id", id); err != nil {
		return err
	}
	if !pfproxy.OwnedBy(a.replicaID, id) { // поток открыт на другой реплике: клиент повторит запрос
		return streamid.ErrElsewhere
	}
	return a.pfMgr.StopPortForward(id)
}

func (a *App) ListPortForwards() []k8s.PortForwardSession {
	if a.authz(auth.PermRead) != nil {
		return nil
	}
	return a.pfMgr.ListPortForwards()
}

func (a *App) GetFreePort() (int, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return 0, err
	}
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (a *App) OpenBrowserURL(url string) {
	if a.authz(auth.PermRead) != nil {
		return
	}
	a.platform.OpenURL(url)
}

// === Terminal ===

func (a *App) DetectShell(namespace, pod, container string) (string, error) {
	if err := a.authz(auth.PermExec, "namespace", namespace, "pod", pod, "container", container); err != nil {
		return "", err
	}
	if err := a.checkTerminalTarget(namespace, pod); err != nil {
		return "", err
	}
	return a.k8sMgr.DetectShell(namespace, pod, container)
}

func (a *App) StartTerminalSession(sessionID, namespace, pod, container, shell string, cols, rows uint16) error {
	if err := a.authz(auth.PermExec, "namespace", namespace, "pod", pod, "container", container, "shell", shell); err != nil {
		return err
	}
	if err := a.checkTerminalTarget(namespace, pod); err != nil {
		return err
	}
	return a.k8sMgr.StartTerminalSession(sessionID, namespace, pod, container, shell, cols, rows)
}

// terminalHost — оболочка, которая держит веб-терминалы (HTTP-оболочка). У десктопа её нет.
type terminalHost interface{ Terminals() *termbuf.Registry }

// OpenTerminal открывает терминал для веб-оболочки: идентификатор выдаёт сервер (неугадываемый),
// вывод копится, пока браузер не подключится к WebSocket /api/terminal/{id}; ввод и размер идут
// по этому же сокету. Десктоп использует StartTerminalSession.
func (a *App) OpenTerminal(namespace, pod, container, shell string, cols, rows uint16) (string, error) {
	if err := a.authz(auth.PermExec, "namespace", namespace, "pod", pod, "container", container, "shell", shell); err != nil {
		return "", err
	}
	if err := a.checkTerminalTarget(namespace, pod); err != nil {
		return "", err
	}
	host, ok := a.platform.(terminalHost)
	if !ok {
		return "", errors.New("web terminal is unavailable in this shell")
	}
	reg := host.Terminals()
	id := streamid.New(a.replicaID)
	buf, err := reg.Register(id, termbuf.Hooks{
		Input:  func(p []byte) error { return a.k8sMgr.WriteTerminal(id, p) },
		Resize: func(c, r uint16) error { return a.k8sMgr.ResizeTerminal(id, c, r) },
		Close:  func() { _ = a.k8sMgr.CloseTerminal(id) },
		Owner:  a.sessionID,
	})
	if err != nil {
		return "", err
	}
	if err := a.k8sMgr.StartTerminalWithOutput(id, namespace, pod, container, shell, cols, rows, buf); err != nil {
		reg.Remove(id)
		return "", err
	}
	return id, nil
}

func (a *App) WriteTerminal(sessionID string, data []byte) error {
	if err := a.authz(auth.PermExec); err != nil {
		return err
	}
	return a.k8sMgr.WriteTerminal(sessionID, data)
}

func (a *App) ResizeTerminal(sessionID string, cols, rows uint16) error {
	if err := a.authz(auth.PermExec); err != nil {
		return err
	}
	return a.k8sMgr.ResizeTerminal(sessionID, cols, rows)
}

func (a *App) CloseTerminal(sessionID string) error {
	if err := a.authz(auth.PermExec); err != nil {
		return err
	}
	return a.k8sMgr.CloseTerminal(sessionID)
}

// DeleteResource удаляет ресурс любого поддерживаемого типа. kind — ключ типа
// (совпадает с id определения ресурса на фронтенде, например "deployment").
func (a *App) DeleteResource(kind, namespace, name string) error {
	if err := a.authz(auth.PermWrite, "kind", kind, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.DeleteResource(kind, namespace, name)
}

// === Custom Resources ===

func (a *App) GetCRDs() ([]k8s.CRDInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCRDs()
}

func (a *App) GetCustomResources(group, version, plural string, namespaces []string) ([]k8s.CustomResourceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCustomResources(group, version, plural, namespaces)
}

// === Streaming ===

func (a *App) StartLogStream(streamID, namespace, pod, container string, tail int64) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if err := streamid.Check(a.replicaID, streamID); err != nil { // поток должен открыться там, где браузер держит WebSocket событий
		return err
	}
	return a.k8sMgr.StartLogStream(streamID, namespace, pod, container, tail)
}

func (a *App) StopLogStream(streamID string) error {
	if err := a.authz(auth.PermRead); err != nil {
		return err
	}
	if err := streamid.Check(a.replicaID, streamID); err != nil {
		return err
	}
	a.k8sMgr.StopLogStream(streamID)
	return nil
}

// rememberHelmActor сообщает ядру, кто добавляет репозиторий (created_by в сервисе). Вызывается после authz.
func (a *App) rememberHelmActor() {
	if a.auth == nil {
		return
	}
	if u, err := a.auth.Current(); err == nil && u != nil {
		a.k8sMgr.SetHelmActor(u.ID)
	}
}

// activeClusterID — активный кластер вида (пусто, если его нет). Состояние Helm принадлежит команде этого кластера,
// поэтому право управлять репозиториями проверяется по ней, а не по «админу хотя бы одной команды»: иначе админ
// команды Blue мог бы менять репозитории Red, пока у него в Red только чтение.
func (a *App) activeClusterID() string {
	if a.k8sMgr == nil {
		return ""
	}
	return a.k8sMgr.GetActiveContext()
}
