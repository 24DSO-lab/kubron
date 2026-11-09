// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// kubron-server — HTTP-оболочка Kubron: то же ядро и тот же интерфейс, что в десктопе,
// но в браузере. Сейчас — режим одного пользователя на loopback (этап 1 плана).
//
//	kubron-server --listen 127.0.0.1:8080
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/24DSO-lab/kubron/frontend"
	"github.com/24DSO-lab/kubron/internal/app"
	"github.com/24DSO-lab/kubron/internal/jobs"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/pgstore"
	webplatform "github.com/24DSO-lab/kubron/internal/platform/web"
	"github.com/24DSO-lab/kubron/internal/replicaevents"
	"github.com/24DSO-lab/kubron/internal/secrets"
	"github.com/24DSO-lab/kubron/internal/server"
	"github.com/24DSO-lab/kubron/internal/store"
	"github.com/24DSO-lab/kubron/internal/websession"
)

// userRefreshEvery — как часто реплика перечитывает пользователей из PostgreSQL (изменения других реплик).
const userRefreshEvery = 2 * time.Second

func main() {
	if err := setupLogging(os.Getenv, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "configuration:", err)
		os.Exit(2)
	}
	if len(os.Args) > 1 && adminCommands[os.Args[1]] {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err := runAdmin(ctx, os.Args[1], os.Args[2:], os.Stdout)
		stop()
		if err != nil {
			fatal(os.Args[1], err)
		}
		return
	}
	cfg, err := server.LoadConfig(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fatal("configuration", err)
	}

	registerSecrets(cfg)
	if enableAuditStdout(os.Getenv, os.Stdout) {
		slog.Info("audit: records are also written to stdout as JSON lines (type=audit)")
	}

	hostname, _ := os.Hostname()
	if h := os.Getenv("HOSTNAME"); h != "" {
		hostname = h // в поде совпадает с его именем
	}
	replicaID, err := server.ResolveReplicaID(cfg.ReplicaID, hostname)
	if err != nil {
		fatal("configuration", err)
	}
	lifecycle := server.NewLifecycle(replicaID)

	slog.Info("kubron-server starting", "version", version, "replica", replicaID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dist, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		fatal("frontend", err)
	}
	if err := server.CheckWebBuild(dist); err != nil {
		fatal("frontend", err)
	}

	// Каталог данных задаётся до запуска ядра: хранилища открываются в OnStartup
	k8s.SetKubronDir(cfg.DataDir)
	// Блок exec из kubeconfig не выполняется нигде (вход делает брокер); веб-сервер не читает по kubeconfig файлы сервера (задача 3.7).
	k8s.SetConnectionPolicy(k8s.ConnectionPolicy{AllowLocalFiles: false})
	// Запросы Helm к адресам, которые задаёт пользователь (репозитории, OCI), проходят через защиту от SSRF (ADR-0015).
	if err := setupHelmNetPolicy(os.Getenv); err != nil {
		fatal("network policy", err)
	}
	// Адрес API-сервера кластера и IdP из kubeconfig пользователя; в сервисном режиме по умолчанию permissive (см. helmnet.go).
	if err := setupClusterAddrPolicy(os.Getenv, cfg.ServiceMode()); err != nil {
		fatal("network policy", err)
	}

	hub := server.NewHub()
	platform := &webplatform.Platform{}
	platform.SetEmitter(hub.Emit)
	platform.SetSessionEmitter(hub.EmitTo)
	core := app.NewApp(platform)
	app.SetReplicaID(core, replicaID) // потоки терминала и логов несут реплику-владельца в идентификаторе
	if err := setupUpdateCheck(ctx, core, os.Getenv); err != nil {
		fatal("configuration", err)
	}
	// Общее хранилище веб-сессий: с PostgreSQL сессия, выданная одной репликой, действует на всех (ADR-0016).
	// Файловое хранилище его не имеет, сессии остаются в памяти процесса.
	var sessionBackend websession.Backend
	var pgStore *pgstore.Store
	var notifier *pgstore.Notifier
	if cfg.DatabaseURL != "" {
		pg, err := openPostgres(ctx, cfg)
		if err != nil {
			fatal("database", err)
		}
		defer pg.Close()
		if err := checkSecretKey(ctx, pg); err != nil {
			fatal("database", err)
		}
		migrateLegacyCredentials(ctx, pg, slog.Default())
		pgStore = pg
		// События между репликами (LISTEN/NOTIFY, ADR-0016): прямой адрес БД, не pgbouncer в режиме transaction.
		listenURL := cfg.DBListenURL
		if listenURL == "" {
			listenURL = cfg.DatabaseURL
		}
		notifier = pg.NewNotifier(listenURL, logf)
		st := store.FromPostgres(pg)
		app.SetStore(core, st)
		if p, ok := st.(websession.Provider); ok {
			sessionBackend = p.Sessions()
		}
	}
	if err := setupBroker(ctx, cfg, pgStore, core); err != nil {
		fatal("sign-in", err)
	}
	app.OnStartup(core)(ctx)
	app.StartUserRefresh(ctx, core, userRefreshEvery) // другие реплики меняют пользователей и роли
	defer app.OnShutdown(core)(context.Background())

	dispatcher, err := server.NewAppDispatcher(core)
	if err != nil {
		fatal("dispatcher", err)
	}
	// У каждой веб-сессии свой вид ядра (пользователь, права, кластер, терминалы, логи, события):
	// одновременно могут работать несколько пользователей.
	sessions := server.NewSessions(cfg.SessionTTL, false)
	sessions.Backend, sessions.ReplicaID = sessionBackend, replicaID
	// Сессия открыта на другой реплике: вид строится заново (пользователь без пароля, кластер — при первом запросе).
	sessions.Restore = func(rec websession.Record) (any, error) {
		v := app.NewSessionView(core, rec.Source)
		if err := app.ResumeView(v, rec.UserID, rec.LastActive); err != nil {
			app.ReleaseView(v)
			return nil, err
		}
		app.BindView(v, rec.ID)
		return v, nil
	}
	sessions.Sync = func(view any, rec websession.Record) { app.SyncViewActivity(view.(*app.App), rec.LastActive) }
	terminals := platform.Terminals()
	guard := &server.Guard{Sessions: sessions, TrustProxy: cfg.TrustProxy, Hub: hub, Terminals: terminals,
		TrustedProxyHops: cfg.TrustedProxyHops, TrustedProxies: cfg.TrustedProxies,
		AnonLimiter: server.NewRateLimiter(server.DefaultAnonRate, server.DefaultAnonBurst), Lifecycle: lifecycle}
	guard.Setup = setupGate(cfg, core)
	app.SetResourceLimits(core, k8s.Limits{Terminals: cfg.MaxTerminals, LogStreams: cfg.MaxLogStreams, PortForwards: cfg.MaxPortForwards,
		TerminalMaxDuration: cfg.TerminalMaxDuration, TerminalIdleTimeout: cfg.TerminalIdleTimeout})
	app.SetInteractivePolicy(core, app.InteractivePolicy{DisableExec: cfg.DisableExec, DisableNodeShell: cfg.DisableNodeShell})
	app.SetClusterIdleTTL(core, 2*time.Minute)
	enforceAccess := func() { // права могли измениться: закрыть то, что осталось открытым у потерявших доступ
		for _, v := range sessions.Views() {
			app.EnforceAccess(v.(*app.App))
		}
	}
	startEvents := func() {}
	if notifier != nil {
		startEvents = setupReplicaEvents(ctx, notifier, pgStore, core, sessions, replicaID, enforceAccess)
	}
	pf := setupPortForward(cfg, core, sessions, replicaID)
	guard.NewView = func(src string) any { return app.NewSessionView(core, src) }
	guard.Bind = func(v any, id string) { app.BindView(v.(*app.App), id) }
	guard.ActiveCluster = func(v any) string { return app.ViewActiveCluster(v.(*app.App)) }
	guard.RestoreCluster = func(v any, id string) error { return app.RestoreActiveCluster(v.(*app.App), id) }
	sessions.Release = func(v any) { app.ReleaseView(v.(*app.App)) }
	sessions.OnEnd = guard.CloseSession // закрытая сессия закрывает и свои WebSocket
	go terminals.Run(ctx.Done(), 5*time.Second)
	app.StartHealthMonitor(ctx, core, cfg.ClusterCheckInterval)
	startEvents() // слушатель стартует после монитора: первая сверка читает результаты проверок
	if pgStore != nil {
		// Очистка просроченных строк и хранение аудита: исполняет одна реплика (pg_try_advisory_lock).
		go jobs.Run(ctx, maintenanceJobs(pgStore, sessionBackend, cfg.AuditRetention)...)
	}
	// Фоновая уборка веб-сессий: закрывает те, у которых ядро уже завершило пользовательскую сессию
	// (простой, отключение пользователя) или истёк срок, и освобождает их ресурсы и ссылки пула.
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sessions.Revalidate()
				sessions.Reap(func(v any) bool { return app.ViewActive(v.(*app.App)) })
				// Права могли измениться (перенос кластера, исключение из команды): закрываем то, что
				// осталось открытым у потерявших доступ. Вне блокировки реестра сессий. Это нижняя граница
				// задержки: сообщения между репликами (access.changed) ускоряют, но не заменяют сверку.
				enforceAccess()
			}
		}
	}()
	defer terminals.CloseAll()

	handler := server.NewHandler(dispatcher, dist, guard,
		server.WithCSPReportOnly(cfg.CSPReportOnly), server.WithContractBuild(version), readyzOption(pgStore, lifecycle),
		server.WithWebOIDCCallback(func(ctx context.Context, view any, state, redirectURL string) error {
			v, ok := view.(*app.App)
			if !ok {
				return errors.New("sign-in session is unavailable")
			}
			return app.CompleteWebOIDCCallback(ctx, v, state, redirectURL)
		}))
	handler = setupMetrics(ctx, cfg, metricsDeps{Core: core, Sessions: sessions, Hub: hub, Terminals: terminals,
		PG: pgStore, Lifecycle: lifecycle}, handler, dispatcher)
	if pf != nil {
		go pf.Run(ctx)
		// Закрытая веб-сессия закрывает и свои потоки port-forward (ReleaseView → StopAll); дополнительно
		// владелец проверяется на каждом запросе.
		handler = server.WithPortForwardHosts(handler, pf.Handler(), pf.IsPortForwardHost)
	}
	// Host вне KUBRON_ALLOWED_HOSTS отклоняется до маршрутизации; потоки *.pf.<домен> разрешены, когда port-forward включён.
	var pfHost func(string) bool
	if pf != nil {
		pfHost = pf.IsPortForwardHost
	}
	handler = server.WithAllowedHosts(handler, cfg.AllowedHosts, pfHost)
	handler = server.WithRequestID(handler) // снаружи всех: каждая запись журнала запроса несёт request_id
	// SIGTERM: draining (новые соединения не принимаются, /readyz смотрит на lifecycle.Draining()),
	// пауза для балансировщика, затем закрытие WebSocket кадром 1012 и остановка HTTP-сервера.
	plan := server.ShutdownPlan{Drain: guard.BeginDrain, DrainDelay: cfg.DrainDelay, CloseConns: guard.CloseConnections}
	if err := server.RunWith(ctx, cfg, handler, plan); err != nil {
		fatal("server", err)
	}
}

// openPostgres подключается к БД, применяет миграции и готовит организацию по умолчанию.
func openPostgres(ctx context.Context, cfg server.Config) (*pgstore.Store, error) {
	// С PostgreSQL мастер-ключ обязателен и приходит только из KUBRON_SECRET_KEY (источники-предупреждения
	// key-file-* относятся к режимам без БД).
	sel, err := secrets.Resolve(ctx, secrets.Options{
		Mode: secrets.ModeServerPG, EnvKey: cfg.SecretKey, EnvPrevious: cfg.PreviousSecretKeys, DataDir: cfg.DataDir,
	})
	if err != nil {
		return nil, err
	}
	for _, w := range sel.Warnings {
		slog.Warn("secrets: master key warning", "code", w)
	}
	pg, err := pgstore.Open(ctx, cfg.DatabaseURL, sel.Keyring)
	if err != nil {
		return nil, err
	}
	res, err := pg.Migrate(ctx)
	if err != nil {
		pg.Close()
		return nil, err
	}
	if len(res.Applied) > 0 {
		slog.Info("database: migrations applied", "versions", res.Applied)
	}
	if len(res.Ahead) > 0 {
		slog.Warn("database: the schema is newer than this build; running in compatibility mode", "versions", res.Ahead)
	}
	if err := pg.Init(ctx); err != nil {
		pg.Close()
		return nil, err
	}
	return pg, nil
}

// setupReplicaEvents подключает сообщения между репликами (ADR-0016, раздел 2): публикацию локальных изменений и
// приём чужих. Сообщения — ускорение; корректность держится на периодической сверке с БД, поэтому недоступный
// слушатель не мешает работе сервиса. Публикация настраивается сразу, а слушатель запускает возвращённая функция.
func setupReplicaEvents(ctx context.Context, n *pgstore.Notifier, pg *pgstore.Store, core *app.App,
	sessions *server.Sessions, replicaID string, enforceAccess func()) (start func()) {
	publish := func(m replicaevents.Message) {
		payload, err := m.Encode(replicaID)
		if err != nil {
			slog.Warn("replica events: the message was not encoded", "error", err)
			return
		}
		n.Publish(payload)
	}
	sessions.Publish = publish
	app.SetReplicaPublisher(core, publish)
	pg.SetAccessNotifier(func() { publish(replicaevents.Message{Kind: replicaevents.AccessChanged}) })

	ev := replicaevents.NewDispatcher(replicaID, logf)
	ev.Handle(replicaevents.SessionEnded, func(m replicaevents.Message) { sessions.EndRemote(m.Session) })
	ev.Handle(replicaevents.UserSessionsEnded, func(m replicaevents.Message) { sessions.EndUserRemote(m.User, m.Session) })
	accessChanged := func(replicaevents.Message) {
		app.RefreshUsers(core) // пользователи и роли — до проверки прав открытых сессий
		pg.ApplyRemoteAccessChange()
		enforceAccess()
		app.NotifyClustersChanged(core)
	}
	ev.Handle(replicaevents.AccessChanged, accessChanged)
	ev.Handle(replicaevents.DirectoryChanged, accessChanged)
	ev.Handle(replicaevents.HealthChanged, func(replicaevents.Message) { app.RefreshHealth(ctx, core) })

	// После каждого (пере)подключения слушателя сообщения могли потеряться: полная сверка с БД.
	resync := func() {
		sessions.Revalidate()
		accessChanged(replicaevents.Message{})
		app.RefreshHealth(ctx, core)
	}
	return func() { go n.Run(ctx, func(payload string) { ev.Dispatch(payload) }, resync) }
}
