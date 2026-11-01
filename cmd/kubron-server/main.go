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
	"io/fs"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/24DSO-labs/kubron/frontend"
	"github.com/24DSO-labs/kubron/internal/app"
	"github.com/24DSO-labs/kubron/internal/jobs"
	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/internal/pgstore"
	webplatform "github.com/24DSO-labs/kubron/internal/platform/web"
	"github.com/24DSO-labs/kubron/internal/replicaevents"
	"github.com/24DSO-labs/kubron/internal/secretbox"
	"github.com/24DSO-labs/kubron/internal/server"
	"github.com/24DSO-labs/kubron/internal/store"
	"github.com/24DSO-labs/kubron/internal/websession"
)

// userRefreshEvery — как часто реплика перечитывает пользователей из PostgreSQL (изменения других реплик).
const userRefreshEvery = 2 * time.Second

func main() {
	if len(os.Args) > 1 && adminCommands[os.Args[1]] {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err := runAdmin(ctx, os.Args[1], os.Args[2:], os.Stdout)
		stop()
		if err != nil {
			log.Fatalf("%s: %v", os.Args[1], err)
		}
		return
	}
	cfg, err := server.LoadConfig(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatalf("configuration: %v", err)
	}

	hostname, _ := os.Hostname()
	if h := os.Getenv("HOSTNAME"); h != "" {
		hostname = h // в поде совпадает с его именем
	}
	replicaID, err := server.ResolveReplicaID(cfg.ReplicaID, hostname)
	if err != nil {
		log.Fatalf("configuration: %v", err)
	}
	lifecycle := server.NewLifecycle(replicaID)

	log.Printf("kubron-server %s, replica %s", version, replicaID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dist, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		log.Fatalf("frontend: %v", err)
	}
	if err := server.CheckWebBuild(dist); err != nil {
		log.Fatalf("frontend: %v", err)
	}

	// Каталог данных задаётся до запуска ядра: хранилища открываются в OnStartup
	k8s.SetKubronDir(cfg.DataDir)
	// Веб-сервер не запускает команды из kubeconfig и не читает по ним файлы сервера (задача 3.7).
	k8s.SetConnectionPolicy(k8s.ConnectionPolicy{AllowExec: false, AllowLocalFiles: false})
	// Запросы Helm к адресам, которые задаёт пользователь (репозитории, OCI), проходят через защиту от SSRF (ADR-0015).
	if err := setupHelmNetPolicy(os.Getenv); err != nil {
		log.Fatalf("network policy: %v", err)
	}
	// Адрес API-сервера кластера из kubeconfig пользователя; по умолчанию выключено (см. helmnet.go).
	if err := setupClusterAddrPolicy(os.Getenv); err != nil {
		log.Fatalf("network policy: %v", err)
	}

	hub := server.NewHub()
	platform := &webplatform.Platform{}
	platform.SetEmitter(hub.Emit)
	platform.SetSessionEmitter(hub.EmitTo)
	core := app.NewApp(platform)
	app.SetReplicaID(core, replicaID) // потоки терминала и логов несут реплику-владельца в идентификаторе
	// Общее хранилище веб-сессий: с PostgreSQL сессия, выданная одной репликой, действует на всех (ADR-0016).
	// Файловое хранилище его не имеет, сессии остаются в памяти процесса.
	var sessionBackend websession.Backend
	var pgStore *pgstore.Store
	var notifier *pgstore.Notifier
	if cfg.DatabaseURL != "" {
		pg, err := openPostgres(ctx, cfg)
		if err != nil {
			log.Fatalf("database: %v", err)
		}
		defer pg.Close()
		pgStore = pg
		// События между репликами (LISTEN/NOTIFY, ADR-0016): прямой адрес БД, не pgbouncer в режиме transaction.
		listenURL := cfg.DBListenURL
		if listenURL == "" {
			listenURL = cfg.DatabaseURL
		}
		notifier = pg.NewNotifier(listenURL, log.Printf)
		st := store.FromPostgres(pg)
		app.SetStore(core, st)
		if p, ok := st.(websession.Provider); ok {
			sessionBackend = p.Sessions()
		}
	}
	app.OnStartup(core)(ctx)
	app.StartUserRefresh(ctx, core, userRefreshEvery) // другие реплики меняют пользователей и роли
	defer app.OnShutdown(core)(context.Background())

	dispatcher, err := server.NewAppDispatcher(core)
	if err != nil {
		log.Fatalf("dispatcher: %v", err)
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
	app.SetResourceLimits(core, k8s.Limits{Terminals: cfg.MaxTerminals, LogStreams: cfg.MaxLogStreams, PortForwards: cfg.MaxPortForwards})
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
	pf := setupPortForward(cfg, core, sessions)
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

	handler := server.NewHandler(dispatcher, dist, guard, server.WithCSPReportOnly(cfg.CSPReportOnly))
	if pf != nil {
		go pf.Run(ctx)
		// Закрытая веб-сессия закрывает и свои потоки port-forward (ReleaseView → StopAll); дополнительно
		// владелец проверяется на каждом запросе.
		handler = server.WithPortForwardHosts(handler, pf.Handler(), pf.IsPortForwardHost)
	}
	// SIGTERM: draining (новые соединения не принимаются, /readyz смотрит на lifecycle.Draining()),
	// пауза для балансировщика, затем закрытие WebSocket кадром 1012 и остановка HTTP-сервера.
	plan := server.ShutdownPlan{Drain: guard.BeginDrain, DrainDelay: cfg.DrainDelay, CloseConns: guard.CloseConnections}
	if err := server.RunWith(ctx, cfg, handler, plan); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// openPostgres подключается к БД, применяет миграции и готовит организацию по умолчанию.
func openPostgres(ctx context.Context, cfg server.Config) (*pgstore.Store, error) {
	keys, err := secretbox.ParseKeyring(cfg.SecretKey, cfg.PreviousSecretKeys...)
	if err != nil {
		return nil, err
	}
	pg, err := pgstore.Open(ctx, cfg.DatabaseURL, keys)
	if err != nil {
		return nil, err
	}
	res, err := pg.Migrate(ctx)
	if err != nil {
		pg.Close()
		return nil, err
	}
	if len(res.Applied) > 0 {
		log.Printf("database: applied migrations %v", res.Applied)
	}
	if len(res.Ahead) > 0 {
		log.Printf("database: the schema is newer than this build (versions %v); running in compatibility mode", res.Ahead)
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
			log.Printf("replica events: %v", err)
			return
		}
		n.Publish(payload)
	}
	sessions.Publish = publish
	app.SetReplicaPublisher(core, publish)
	pg.SetAccessNotifier(func() { publish(replicaevents.Message{Kind: replicaevents.AccessChanged}) })

	ev := replicaevents.NewDispatcher(replicaID, log.Printf)
	ev.Handle(replicaevents.SessionEnded, func(m replicaevents.Message) { sessions.EndRemote(m.Session) })
	ev.Handle(replicaevents.UserSessionsEnded, func(m replicaevents.Message) { sessions.EndUserRemote(m.User, m.Session) })
	accessChanged := func(replicaevents.Message) {
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
