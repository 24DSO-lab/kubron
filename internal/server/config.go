// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config — параметры сервера. Источники: флаги и переменные окружения (флаг важнее).
type Config struct {
	// Listen — адрес прослушивания, по умолчанию 127.0.0.1:8080 (KUBRON_LISTEN).
	Listen string
	// AllowRemote разрешает слушать не на loopback (KUBRON_ALLOW_REMOTE=1).
	// Без TLS cookie уходит открытым текстом: для сети нужен обратный прокси с TLS.
	AllowRemote bool
	// PublicURL — публичный origin сервера для browser-callback (KUBRON_PUBLIC_URL). Пусто — browser-callback выключен.
	PublicURL string
	// GKEOAuthClientID и GKEOAuthClientSecret — OAuth-клиент Google для входа пользователя в GKE
	// (KUBRON_GKE_OAUTH_CLIENT_ID, KUBRON_GKE_OAUTH_CLIENT_SECRET). Без идентификатора вход в GKE идёт только
	// по ключу сервисного аккаунта. Секрет не логируется и не отдаётся в API.
	GKEOAuthClientID     string
	GKEOAuthClientSecret string
	// AllowedHosts — допустимые значения заголовка Host (KUBRON_ALLOWED_HOSTS, через запятую; allowedhosts.go). Пусто —
	// проверка выключена. Потоки port-forward (*.pf.<домен>) разрешаются автоматически.
	AllowedHosts []string
	// SetupToken — токен первичной настройки в сервисном режиме (KUBRON_SETUP_TOKEN, только окружение: аргументы
	// видны в списке процессов). Без него сетевой SetupAdmin отклоняется; первого администратора создаёт
	// «kubron-server create-admin». Токен не пишется в журнал и не уходит в API.
	SetupToken string
	// DataDir — каталог данных (пользователи, кластеры, журнал); пусто — ~/.kubron
	// (--data-dir или KUBRON_DATA_DIR). Читается только сервером: десктоп переменную игнорирует.
	DataDir string
	// DatabaseURL — строка подключения к PostgreSQL (KUBRON_DATABASE_URL или --database-url). Пусто — файловое
	// хранилище в каталоге данных. Пароль лучше передавать через переменную окружения: аргументы видны в списке процессов.
	DatabaseURL string
	// DBListenURL — прямой адрес PostgreSQL для LISTEN/NOTIFY между репликами (только KUBRON_DB_LISTEN_URL: в адресе
	// может быть пароль). LISTEN не работает через pgbouncer в режиме transaction; пусто — используется DatabaseURL.
	DBListenURL string
	// SecretKey — ключ шифрования секретов в БД «id:base64» (только KUBRON_SECRET_KEY; флага нет).
	// PreviousSecretKeys — прежние ключи для чтения после ротации (KUBRON_SECRET_KEY_PREVIOUS, через запятую).
	SecretKey          string
	PreviousSecretKeys []string
	// TrustProxy — доверять X-Forwarded-Proto от обратного прокси: тогда cookie помечается
	// Secure (KUBRON_TRUST_PROXY=1). Включайте, только если сервер доступен лишь через прокси.
	TrustProxy bool
	// TrustedProxyHops — сколько доверенных прокси стоит перед сервером (KUBRON_TRUSTED_PROXY_HOPS, по умолчанию 1; 0 равносилен 1).
	// Адрес клиента берётся из X-Forwarded-For справа: клиент = элемент len-hops. Нужен, когда перед
	// сервером цепочка (балансировщик → ingress). Требует TrustProxy.
	TrustedProxyHops int
	// TrustedProxies — адреса доверенных прокси, CIDR или адреса через запятую (KUBRON_TRUSTED_PROXIES).
	// Если задан, вместо счётчика клиентом считается первый справа адрес вне списка; X-Forwarded-For
	// от недоверенного собеседника игнорируется. Требует TrustProxy.
	TrustedProxies []netip.Prefix
	// CSPReportOnly — отдавать Content-Security-Policy в режиме Report-Only (KUBRON_CSP_REPORT_ONLY=1):
	// нарушения видны в консоли браузера, но ничего не блокируется. Для обкатки; по умолчанию политика применяется.
	CSPReportOnly bool
	// SessionTTL — абсолютный срок жизни веб-сессии (KUBRON_SESSION_TTL, по умолчанию 12h);
	// простой отслеживает ядро (SetIdleTimeout).
	SessionTTL time.Duration
	// ClusterCheckInterval — как часто сервер проверяет соединение с кластерами в фоне
	// (KUBRON_CLUSTER_CHECK_INTERVAL, по умолчанию 30s; 0 — не проверять).
	ClusterCheckInterval time.Duration
	// MetricsListen — адрес отдельного листенера /metrics (KUBRON_METRICS_LISTEN, --metrics-listen). Пусто —
	// метрики выключены (по умолчанию). Это не публичный порт: Ingress на него не смотрит, доступ к нему
	// ограничивается сетью (NetworkPolicy), аутентификации у эндпоинта нет.
	MetricsListen string
	// ReplicaID — идентификатор реплики (KUBRON_REPLICA_ID): буквы, цифры, «-», «_», до 63 символов, без точки.
	// Пусто — «<имя узла>-<случайный суффикс процесса>». Заданный вручную должен быть уникален среди реплик.
	ReplicaID string
	// DrainDelay — пауза между переходом в draining (SIGTERM) и закрытием WebSocket кадром 1012: за это время
	// балансировщик снимает реплику с обслуживания (KUBRON_DRAIN_DELAY, по умолчанию 0; для сервиса за
	// балансировщиком рекомендуется 5s). Меньше предела ожидания остановки пода (terminationGracePeriodSeconds).
	DrainDelay time.Duration
	// AuditRetention — как долго хранятся записи журнала аудита в PostgreSQL (KUBRON_AUDIT_RETENTION, например
	// 2160h). 0 — бессрочно (по умолчанию). Просроченные записи удаляет одна реплика (ADR-0016, раздел 6).
	AuditRetention time.Duration
	// Лимиты ресурсов на одного пользователя (0 — без ограничения):
	// KUBRON_MAX_TERMINALS, KUBRON_MAX_LOG_STREAMS, KUBRON_MAX_PORT_FORWARDS.
	MaxTerminals    int
	MaxLogStreams   int
	MaxPortForwards int
	// PFBaseDomain — домен потоков port-forward: браузер открывает {id}.pf.<домен> (KUBRON_PF_BASE_DOMAIN, ADR-0017).
	// Нужны DNS и сертификат на *.pf.<домен>. Пусто: на сервере с БД или не-loopback адресом port-forward выключен,
	// в режиме одного пользователя на loopback остаётся localhost-порт.
	PFBaseDomain string
	// PFScheme — схема адреса потока: https (по умолчанию) или http (KUBRON_PF_SCHEME; только для localhost-домена).
	PFScheme string
	// PFIdleTimeout и PFMaxDuration — простой и абсолютный срок потока (KUBRON_PF_IDLE_TIMEOUT, KUBRON_PF_MAX_DURATION);
	// 0 — значения по умолчанию (15m и 8h).
	PFIdleTimeout time.Duration
	PFMaxDuration time.Duration
	// Отключение возможностей для всех пользователей: KUBRON_DISABLE_PORT_FORWARD, KUBRON_DISABLE_NODE_SHELL,
	// KUBRON_DISABLE_EXEC (терминалы подов; оболочка узла тоже, она работает через exec).
	DisablePortForward bool
	DisableNodeShell   bool
	DisableExec        bool
	// TerminalMaxDuration и TerminalIdleTimeout — абсолютный срок терминала и срок без ввода
	// (KUBRON_TERMINAL_MAX_DURATION, по умолчанию 8h; KUBRON_TERMINAL_IDLE_TIMEOUT, по умолчанию 30m). 0 — без ограничения.
	TerminalMaxDuration time.Duration
	TerminalIdleTimeout time.Duration
}

// ServiceMode — сервер для нескольких пользователей: база данных или адрес не на loopback.
// Port-forward на localhost хоста сервера в нём недопустим.
func (c Config) ServiceMode() bool {
	if c.DatabaseURL != "" || c.AllowRemote {
		return true
	}
	host, _, err := net.SplitHostPort(c.Listen)
	return err != nil || !isLoopbackHost(host)
}

// Фоновая проверка кластеров: интервал по умолчанию и нижняя граница (чаще — лишняя нагрузка на API-серверы).
const (
	DefaultClusterCheckInterval = 30 * time.Second
	MinClusterCheckInterval     = 5 * time.Second
)

// MaxDrainDelay — верхняя граница KUBRON_DRAIN_DELAY (пауза обязана укладываться в время остановки пода).
const MaxDrainDelay = 60 * time.Second

// MinAuditRetention — наименьший ненулевой срок хранения аудита: опечатка в единицах не должна стереть журнал.
const MinAuditRetention = 24 * time.Hour

// Лимиты ресурсов на пользователя по умолчанию (ADR-0005).
const (
	DefaultMaxTerminals    = 8
	DefaultMaxLogStreams   = 20
	DefaultMaxPortForwards = 10

	DefaultTerminalMaxDuration = 8 * time.Hour
	DefaultTerminalIdleTimeout = 30 * time.Minute
)

// LoadConfig разбирает флаги args (без имени программы) и окружение через getenv.
func LoadConfig(args []string, getenv func(string) string, out io.Writer) (Config, error) {
	cfg := Config{Listen: "127.0.0.1:8080", SessionTTL: 12 * time.Hour, ClusterCheckInterval: DefaultClusterCheckInterval, TrustedProxyHops: 1,
		MaxTerminals: DefaultMaxTerminals, MaxLogStreams: DefaultMaxLogStreams, MaxPortForwards: DefaultMaxPortForwards,
		TerminalMaxDuration: DefaultTerminalMaxDuration, TerminalIdleTimeout: DefaultTerminalIdleTimeout}
	if v := getenv("KUBRON_LISTEN"); v != "" {
		cfg.Listen = v
	}
	cfg.AllowRemote = truthy(getenv("KUBRON_ALLOW_REMOTE"))
	cfg.PublicURL = strings.TrimSpace(getenv("KUBRON_PUBLIC_URL"))
	cfg.GKEOAuthClientID = strings.TrimSpace(getenv("KUBRON_GKE_OAUTH_CLIENT_ID"))
	cfg.GKEOAuthClientSecret = strings.TrimSpace(getenv("KUBRON_GKE_OAUTH_CLIENT_SECRET"))
	cfg.ReplicaID = strings.TrimSpace(getenv("KUBRON_REPLICA_ID"))
	cfg.MetricsListen = strings.TrimSpace(getenv("KUBRON_METRICS_LISTEN"))
	cfg.DataDir = getenv("KUBRON_DATA_DIR")
	cfg.SetupToken = strings.TrimSpace(getenv("KUBRON_SETUP_TOKEN"))
	cfg.TrustProxy = truthy(getenv("KUBRON_TRUST_PROXY"))
	cfg.CSPReportOnly = truthy(getenv("KUBRON_CSP_REPORT_ONLY"))
	cfg.DisablePortForward = truthy(getenv("KUBRON_DISABLE_PORT_FORWARD"))
	cfg.DisableNodeShell = truthy(getenv("KUBRON_DISABLE_NODE_SHELL"))
	cfg.DisableExec = truthy(getenv("KUBRON_DISABLE_EXEC"))
	cfg.PFBaseDomain = strings.TrimSpace(getenv("KUBRON_PF_BASE_DOMAIN"))
	cfg.PFScheme = strings.ToLower(strings.TrimSpace(getenv("KUBRON_PF_SCHEME")))
	for _, d := range []struct {
		env string
		dst *time.Duration
	}{{"KUBRON_PF_IDLE_TIMEOUT", &cfg.PFIdleTimeout}, {"KUBRON_PF_MAX_DURATION", &cfg.PFMaxDuration},
		{"KUBRON_TERMINAL_MAX_DURATION", &cfg.TerminalMaxDuration}, {"KUBRON_TERMINAL_IDLE_TIMEOUT", &cfg.TerminalIdleTimeout}} {
		if v := getenv(d.env); v != "" {
			dur, err := time.ParseDuration(v)
			if err != nil {
				return Config{}, fmt.Errorf("%s %q: %w", d.env, v, err)
			}
			*d.dst = dur
		}
	}
	if v := getenv("KUBRON_TRUSTED_PROXY_HOPS"); v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return Config{}, fmt.Errorf("KUBRON_TRUSTED_PROXY_HOPS %q: %w", v, err)
		}
		cfg.TrustedProxyHops = n
	}
	trusted := getenv("KUBRON_TRUSTED_PROXIES")
	cfg.DatabaseURL = getenv("KUBRON_DATABASE_URL")
	cfg.DBListenURL = strings.TrimSpace(getenv("KUBRON_DB_LISTEN_URL"))
	cfg.SecretKey = strings.TrimSpace(getenv("KUBRON_SECRET_KEY"))
	for _, k := range strings.Split(getenv("KUBRON_SECRET_KEY_PREVIOUS"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			cfg.PreviousSecretKeys = append(cfg.PreviousSecretKeys, k)
		}
	}
	if v := getenv("KUBRON_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUBRON_SESSION_TTL %q: %w", v, err)
		}
		cfg.SessionTTL = d
	}
	if v := getenv("KUBRON_DRAIN_DELAY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUBRON_DRAIN_DELAY %q: %w", v, err)
		}
		cfg.DrainDelay = d
	}
	if v := getenv("KUBRON_AUDIT_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUBRON_AUDIT_RETENTION %q: %w", v, err)
		}
		cfg.AuditRetention = d
	}
	if v := getenv("KUBRON_CLUSTER_CHECK_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("KUBRON_CLUSTER_CHECK_INTERVAL %q: %w", v, err)
		}
		cfg.ClusterCheckInterval = d
	}

	for _, l := range []struct {
		env string
		dst *int
	}{
		{"KUBRON_MAX_TERMINALS", &cfg.MaxTerminals},
		{"KUBRON_MAX_LOG_STREAMS", &cfg.MaxLogStreams},
		{"KUBRON_MAX_PORT_FORWARDS", &cfg.MaxPortForwards},
	} {
		if v := getenv(l.env); v != "" {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return Config{}, fmt.Errorf("%s %q: %w", l.env, v, err)
			}
			*l.dst = n
		}
	}

	fs := flag.NewFlagSet("kubron-server", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "listen address (env KUBRON_LISTEN)")
	fs.BoolVar(&cfg.AllowRemote, "allow-remote", cfg.AllowRemote,
		"allow a non-loopback address; unsafe without a TLS proxy (env KUBRON_ALLOW_REMOTE)")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory, defaults to ~/.kubron (env KUBRON_DATA_DIR)")
	fs.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL,
		"PostgreSQL URL; empty means file storage in the data directory (env KUBRON_DATABASE_URL, preferred: arguments are visible in the process list)")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", cfg.TrustProxy,
		"trust X-Forwarded-Proto from a reverse proxy, set Secure on cookies (env KUBRON_TRUST_PROXY)")
	fs.IntVar(&cfg.TrustedProxyHops, "trusted-proxy-hops", cfg.TrustedProxyHops,
		"number of trusted reverse proxies in front of the server, the client address is taken from the right of X-Forwarded-For (env KUBRON_TRUSTED_PROXY_HOPS)")
	fs.StringVar(&trusted, "trusted-proxies", trusted,
		"comma-separated CIDRs or addresses of trusted reverse proxies, replaces trusted-proxy-hops (env KUBRON_TRUSTED_PROXIES)")
	fs.BoolVar(&cfg.CSPReportOnly, "csp-report-only", cfg.CSPReportOnly,
		"send Content-Security-Policy as Report-Only: violations are logged by the browser, nothing is blocked (env KUBRON_CSP_REPORT_ONLY)")
	fs.DurationVar(&cfg.SessionTTL, "session-ttl", cfg.SessionTTL, "web session lifetime (env KUBRON_SESSION_TTL)")
	fs.StringVar(&cfg.MetricsListen, "metrics-listen", cfg.MetricsListen,
		"address of the separate Prometheus /metrics listener, empty disables it; not exposed through the Ingress (env KUBRON_METRICS_LISTEN)")
	fs.StringVar(&cfg.ReplicaID, "replica-id", cfg.ReplicaID,
		"replica identifier: letters, digits, '-' and '_', up to 63 characters; empty means hostname plus a random suffix (env KUBRON_REPLICA_ID)")
	fs.DurationVar(&cfg.DrainDelay, "drain-delay", cfg.DrainDelay,
		"pause between SIGTERM and closing WebSocket connections, so a load balancer can stop routing to the replica (env KUBRON_DRAIN_DELAY)")
	fs.DurationVar(&cfg.AuditRetention, "audit-retention", cfg.AuditRetention,
		"how long audit log entries are kept in PostgreSQL, 0 keeps them forever (env KUBRON_AUDIT_RETENTION)")
	fs.DurationVar(&cfg.ClusterCheckInterval, "cluster-check-interval", cfg.ClusterCheckInterval,
		"how often cluster connectivity is checked in the background, 0 disables (env KUBRON_CLUSTER_CHECK_INTERVAL)")
	fs.IntVar(&cfg.MaxTerminals, "max-terminals", cfg.MaxTerminals, "terminals per user, 0 for unlimited (env KUBRON_MAX_TERMINALS)")
	fs.IntVar(&cfg.MaxLogStreams, "max-log-streams", cfg.MaxLogStreams, "log streams per user, 0 for unlimited (env KUBRON_MAX_LOG_STREAMS)")
	fs.IntVar(&cfg.MaxPortForwards, "max-port-forwards", cfg.MaxPortForwards, "port-forwards per user, 0 for unlimited (env KUBRON_MAX_PORT_FORWARDS)")
	fs.BoolVar(&cfg.DisablePortForward, "disable-port-forward", cfg.DisablePortForward, "disable port-forward for everyone (env KUBRON_DISABLE_PORT_FORWARD)")
	fs.BoolVar(&cfg.DisableNodeShell, "disable-node-shell", cfg.DisableNodeShell, "disable the shell on nodes for everyone (env KUBRON_DISABLE_NODE_SHELL)")
	fs.BoolVar(&cfg.DisableExec, "disable-exec", cfg.DisableExec, "disable pod terminals and the node shell for everyone (env KUBRON_DISABLE_EXEC)")
	fs.DurationVar(&cfg.TerminalMaxDuration, "terminal-max-duration", cfg.TerminalMaxDuration, "maximum lifetime of a terminal, 0 for unlimited (env KUBRON_TERMINAL_MAX_DURATION)")
	fs.DurationVar(&cfg.TerminalIdleTimeout, "terminal-idle-timeout", cfg.TerminalIdleTimeout, "close a terminal after this time without input, 0 for unlimited (env KUBRON_TERMINAL_IDLE_TIMEOUT)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	proxies, err := ParseTrustedProxies(trusted)
	if err != nil {
		return Config{}, fmt.Errorf("KUBRON_TRUSTED_PROXIES %q: %w", trusted, err)
	}
	cfg.TrustedProxies = proxies
	allowedHosts, err := ParseAllowedHosts(getenv("KUBRON_ALLOWED_HOSTS"))
	if err != nil {
		return Config{}, fmt.Errorf("KUBRON_ALLOWED_HOSTS: %w", err)
	}
	cfg.AllowedHosts = allowedHosts
	return cfg, cfg.Validate()
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Validate проверяет конфигурацию. Не-loopback адрес без явного разрешения — ошибка.
func (c Config) Validate() error {
	if err := validatePublicURL(c.PublicURL); err != nil {
		return fmt.Errorf("KUBRON_PUBLIC_URL: %w", err)
	}
	if c.GKEOAuthClientSecret != "" && c.GKEOAuthClientID == "" {
		return errors.New("KUBRON_GKE_OAUTH_CLIENT_SECRET is set without KUBRON_GKE_OAUTH_CLIENT_ID")
	}
	if c.SessionTTL <= 0 {
		return errors.New("session lifetime must be positive")
	}
	if c.TrustedProxyHops < 0 {
		return errors.New("trusted proxy hops cannot be negative")
	}
	// Без доверия к прокси X-Forwarded-For игнорируется: настройка молча не работала бы
	if !c.TrustProxy && (len(c.TrustedProxies) > 0 || c.TrustedProxyHops > 1) {
		return errors.New("trusted proxies and hops are used only together with trust-proxy (KUBRON_TRUST_PROXY=1)")
	}
	if c.SetupToken != "" && len(c.SetupToken) < MinSetupTokenLen {
		return fmt.Errorf("the setup token is too short: use at least %d characters (KUBRON_SETUP_TOKEN)", MinSetupTokenLen)
	}
	if c.ReplicaID != "" {
		if err := ValidateReplicaID(c.ReplicaID); err != nil {
			return err
		}
	}
	if c.DrainDelay < 0 || c.DrainDelay > MaxDrainDelay {
		return errors.New("drain delay must be between 0 and 60s")
	}
	if c.AuditRetention != 0 && c.AuditRetention < MinAuditRetention {
		return errors.New("audit retention must be 0 (forever) or at least 24h")
	}
	if c.ClusterCheckInterval != 0 && c.ClusterCheckInterval < MinClusterCheckInterval {
		return errors.New("cluster check interval must be 0 (off) or at least 5s")
	}
	if c.MaxTerminals < 0 || c.MaxLogStreams < 0 || c.MaxPortForwards < 0 {
		return errors.New("resource limits cannot be negative (0 means unlimited)")
	}
	if c.TerminalMaxDuration < 0 || c.TerminalIdleTimeout < 0 {
		return errors.New("terminal timeouts cannot be negative")
	}
	if c.PFScheme != "" && c.PFScheme != "https" && c.PFScheme != "http" {
		return errors.New("KUBRON_PF_SCHEME must be https or http")
	}
	if c.PFIdleTimeout < 0 || c.PFMaxDuration < 0 {
		return errors.New("port-forward timeouts cannot be negative")
	}
	if c.PFScheme == "http" {
		h := strings.ToLower(c.PFBaseDomain)
		if hh, _, err := net.SplitHostPort(h); err == nil {
			h = hh
		}
		if h != "localhost" && !strings.HasSuffix(h, ".localhost") {
			return errors.New("KUBRON_PF_SCHEME=http is allowed only for a localhost base domain")
		}
	}
	if c.MetricsListen != "" {
		if _, _, err := net.SplitHostPort(c.MetricsListen); err != nil {
			return fmt.Errorf("metrics listen %q: %w", c.MetricsListen, err)
		}
		if c.MetricsListen == c.Listen {
			return errors.New("metrics listen address must differ from the main listen address: /metrics must not be served on the public port")
		}
	}
	if c.DatabaseURL != "" && c.SecretKey == "" {
		return errors.New("KUBRON_SECRET_KEY is required when a database is used")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if !c.AllowRemote && !isLoopbackHost(host) {
		return errors.New("address " + c.Listen + " is not loopback: the server does not terminate TLS, so the session cookie would be sent in plain text; " +
			"to allow network access, put a TLS proxy in front and pass --allow-remote")
	}
	return nil
}

// validatePublicURL проверяет origin, на который IdP будет возвращать browser-callback.
// В production требуется HTTPS; HTTP допустим только для loopback-разработки.
func validatePublicURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("must be an absolute URL")
	}
	if !u.IsAbs() || u.Opaque != "" || u.Host == "" || u.Hostname() == "" {
		return errors.New("must be an absolute URL with a host")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return errors.New("must not contain user information, query, or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("must be an origin without a path")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
	}
	if !strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http") {
		return errors.New("scheme must be https (http is allowed only for localhost)")
	}
	if strings.EqualFold(u.Scheme, "http") {
		host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		ip := net.ParseIP(host)
		local := host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && ip.IsLoopback())
		if !local {
			return errors.New("http is allowed only for localhost or a loopback IP address")
		}
	}
	return nil
}

// isLoopbackHost: пустой host (":8080") означает все интерфейсы, это не loopback.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
