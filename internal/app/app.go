// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/clusterhealth"
	"github.com/24DSO-lab/kubron/internal/credential"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/pfproxy"
	"github.com/24DSO-lab/kubron/internal/replicaevents"
	"github.com/24DSO-lab/kubron/internal/signin"
	"github.com/24DSO-lab/kubron/internal/store"
	"github.com/24DSO-lab/kubron/internal/update"
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
	// updates — проверка новых версий (ADR-0028). Один на процесс: виды веб-сессий копируют указатель с корневого
	// экземпляра. nil — оболочка проверку не подключала (тесты).
	updates *update.Checker
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
	// gkeOAuth — хранилище настройки OAuth-клиента Google (только десктоп); gkeOAuthActive — клиент, с которым
	// собран запущенный broker.
	gkeOAuth       *GKEOAuthStore
	gkeOAuthActive string
	conn           *signin.State
	bugReport      *bugReportService // письма «Сообщить об ошибке» (десктоп)
	rev            *revocations      // отозванные личные входы реплики; общий для всех видов
	// limitUser и limitIDP ограничивают попытки AnswerChallenge на пользователя и на провайдера входа; nil — без лимита.
	limitUser, limitIDP AttemptLimiter
	// finish подменяется в тестах: подключение и проверка кластера после успешного входа.
	finish func(clusterID string) error
	// base — контекст жизни вида: у корневого вида это контекст startup (до остановки ядра), у веб-сессии —
	// дочерний контекст, который отменяет ReleaseView. nil — без отмены (тесты). См. opCtx.
	base   context.Context
	cancel context.CancelFunc
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
		platform:  p,
		k8sMgr:    mgr,
		pfMgr:     k8s.NewPortForwardManager(mgr),
		conn:      signin.NewState(),
		rev:       &revocations{},
		bugReport: newBugReportService(),
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
	a.base = ctx
	// Бэкенд сообщает фронтенду об изменениях кластера (informers) вместо постоянного опроса
	a.k8sMgr.SetNotifier(a.platform.Emit)
	if err := k8s.InitKubronDir(); err != nil {
		slog.Error("app: не удалось подготовить каталог Kubron", "error", err)
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
		// Отладочный контейнер без терминала бесполезен: подключение к нему идёт через attach.
		c.Exec, c.NodeShell, c.Debug = false, false, false
	}
	if a.interactive.DisableNodeShell {
		c.NodeShell = false
	}
	if a.interactive.DisableDebug {
		c.Debug = false
	}
	c.BugReport = a.bugReportCapability(c.BugReport)
	c.Teams = a.dir != nil // команды есть там, где хранилище умеет их хранить, а не в оболочке
	c.InCluster = c.Teams && k8s.InClusterAvailable()
	return c
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
