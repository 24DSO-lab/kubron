// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	maxClusterNameLen  = 60
	maxKubeconfigBytes = 5 << 20 // настоящие kubeconfig — единицы килобайт; больше — явно не он
	checkTimeout       = 8 * time.Second
	defaultColor       = "#89b4fa"
)

var (
	clusterIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
	colorRe     = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	iconKeyRe   = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)
	// namespaceRe — DNS-1123 label, как требует Kubernetes.
	namespaceRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

	environments = map[string]bool{"": true, "dev": true, "test": true, "stage": true, "prod": true}

	errKubeconfigTooLarge = fmt.Errorf("kubeconfig is larger than %d MB", maxKubeconfigBytes>>20)
)

// ClusterSettings — свойства кластера, которые пользователь задаёт при добавлении и правит позже.
type ClusterSettings struct {
	DisplayName string `json:"displayName"`
	// Icon — ключ значка из набора интерфейса; пустая строка означает монограмму из названия.
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	Environment string `json:"environment"`
	// DefaultNamespace — namespace, который выбирается при подключении (пусто — все).
	DefaultNamespace string `json:"defaultNamespace"`
	// ContextName — контекст kubeconfig; пусто — current-context.
	ContextName string `json:"contextName"`
	// ArgoURL — опубликованный адрес Argo CD (Ingress). Если задан, OpenArgoUI открывает его и не создаёт
	// port-forward; пусто — открытие через port-forward или прокси потоков (ADR-0017).
	ArgoURL string `json:"argoUrl"`
	// SharedAccount — общая учётная запись команды: все участники входят в кластер под одной записью, которую вводит
	// администратор кластера (ModeTeamAccount). Имеет смысл только для kubeconfig с входом через провайдера
	// (oidc-login, облачный плагин) и только у кластера команды.
	SharedAccount bool `json:"sharedAccount"`
}

var (
	errSharedNeedsTeam   = errors.New("a shared account is available only for clusters of a team")
	errSharedNeedsSignIn = errors.New("a shared account is meaningful only for a kubeconfig that signs in through an identity provider or a plugin")
)

// contextSignsIn — контекст получает учётные данные входом (блок exec или auth-provider), а не статически.
func contextSignsIn(cfg *clientcmdapi.Config, ctxName string) bool {
	c := cfg.Contexts[ctxName]
	if c == nil {
		return false
	}
	ai := cfg.AuthInfos[c.AuthInfo]
	return ai != nil && (ai.Exec != nil || ai.AuthProvider != nil)
}

// authKindOf — вид входа контекста по описанию брокера (чистая функция credential.Describe, без сети). Если описать
// не удалось, но вход нужен, это общий exec.
func authKindOf(cfg *clientcmdapi.Config, data []byte, ctxName string) credential.Kind {
	if d, err := credential.Describe(data, ctxName); err == nil {
		return d.Kind
	}
	if contextSignsIn(cfg, ctxName) {
		return credential.KindExec
	}
	return credential.KindStatic
}

// modeForSettings решает режим кластера по настройке «общая учётная запись». Режим in-cluster не меняется. Уже
// включённая настройка не проверяется заново: kubeconfig мог повредиться, а название поправить нужно.
func modeForSettings(current string, s ClusterSettings, teamID string, signsIn bool) (string, error) {
	switch {
	case current == ModeInCluster:
		return current, nil
	case !s.SharedAccount:
		return "", nil
	case current == ModeTeamAccount:
		return current, nil
	case teamID == "":
		return "", errSharedNeedsTeam
	case !signsIn:
		return "", errSharedNeedsSignIn
	}
	return ModeTeamAccount, nil
}

const maxArgoURLLen = 512

// normalizeArgoURL проверяет адрес Argo CD: только http(s) с хостом, без учётных данных, query и fragment.
// Адрес открывается в браузере пользователя, поэтому другие схемы (javascript:, data:, file:) недопустимы.
func normalizeArgoURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxArgoURLLen {
		return "", fmt.Errorf("the Argo CD address is longer than %d characters", maxArgoURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("the Argo CD address must be an http or https URL without credentials, query or fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// normalize проверяет настройки и приводит их к каноничному виду.
// legacyIcon — значок, уже сохранённый у кластера: старые записи содержат эмодзи, и их нельзя
// отвергать, пока пользователь не выбрал другой значок.
func (s ClusterSettings) normalize(legacyIcon string) (ClusterSettings, error) {
	s.DisplayName = strings.TrimSpace(s.DisplayName)
	if s.DisplayName == "" {
		return s, errors.New("cluster name cannot be empty")
	}
	if utf8.RuneCountInString(s.DisplayName) > maxClusterNameLen {
		return s, fmt.Errorf("name is longer than %d characters", maxClusterNameLen)
	}

	s.Environment = strings.ToLower(strings.TrimSpace(s.Environment))
	if !environments[s.Environment] {
		return s, fmt.Errorf("unknown environment %q", s.Environment)
	}

	s.Color = strings.ToLower(strings.TrimSpace(s.Color))
	if s.Color == "" {
		s.Color = defaultColor
	} else if !colorRe.MatchString(s.Color) {
		return s, fmt.Errorf("invalid color %q: expected #rrggbb", s.Color)
	}

	if s.Icon != "" && s.Icon != legacyIcon && !iconKeyRe.MatchString(s.Icon) {
		return s, fmt.Errorf("invalid icon %q", s.Icon)
	}

	s.DefaultNamespace = strings.TrimSpace(s.DefaultNamespace)
	if s.DefaultNamespace != "" && (len(s.DefaultNamespace) > 63 || !namespaceRe.MatchString(s.DefaultNamespace)) {
		return s, fmt.Errorf("invalid namespace name %q", s.DefaultNamespace)
	}

	s.ContextName = strings.TrimSpace(s.ContextName)

	var err error
	if s.ArgoURL, err = normalizeArgoURL(s.ArgoURL); err != nil {
		return s, err
	}
	return s, nil
}

// KubeconfigSource — откуда взять kubeconfig: файл на диске, вставленный текст или уже
// сохранённый кластер. Должен быть задан ровно один источник.
type KubeconfigSource struct {
	Path      string `json:"path"`
	Text      string `json:"text"`
	ClusterID string `json:"clusterId"`
	// BaseClusterID — сохранённый кластер, из которого сервер подставляет скрытые секреты (MaskPlaceholder) в Text.
	// Источником не считается: задаёт только место, откуда берутся значения, и проверяется на право управления кластером.
	BaseClusterID string `json:"baseClusterId,omitempty"`
}

func (s KubeconfigSource) isEmpty() bool {
	return s.Path == "" && strings.TrimSpace(s.Text) == "" && s.ClusterID == ""
}

func (s KubeconfigSource) read() ([]byte, error) {
	set := 0
	for _, v := range []string{s.Path, strings.TrimSpace(s.Text), s.ClusterID} {
		if v != "" {
			set++
		}
	}
	switch {
	case set == 0:
		return nil, errors.New("no kubeconfig source specified")
	case set > 1:
		return nil, errors.New("specify exactly one kubeconfig source")
	case s.Path != "":
		return readKubeconfigFile(s.Path)
	case s.ClusterID != "":
		data, err := readStoredKubeconfig(s.ClusterID)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	if len(s.Text) > maxKubeconfigBytes {
		return nil, errKubeconfigTooLarge
	}
	if HasSecretPlaceholder(s.Text) {
		return expandPlaceholders(s.Text, s.BaseClusterID)
	}
	return []byte(s.Text), nil
}

// expandPlaceholders подставляет в текст скрытые секреты из сохранённого kubeconfig кластера base.
func expandPlaceholders(text, base string) ([]byte, error) {
	if base == "" {
		return nil, ErrSecretPlaceholderNoCluster
	}
	stored, err := readStoredKubeconfig(base)
	if err != nil {
		return nil, err
	}
	out, err := ExpandKubeconfigSecrets(text, string(stored))
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

func readKubeconfigFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("a directory was given instead of a kubeconfig file")
	}
	if info.Size() > maxKubeconfigBytes {
		return nil, errKubeconfigTooLarge
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

func readStoredKubeconfig(id string) ([]byte, error) {
	return clusterStore.Kubeconfig(id)
}

// ReadClusterKubeconfig возвращает текст сохранённого kubeconfig кластера (для правки в приложении).
//
// В сервисном режиме значения секретов заменены плейсхолдером MaskPlaceholder и до клиента не доходят; если скрыть секрет
// не удалось, ошибка вместо текста.
func ReadClusterKubeconfig(id string) (string, error) {
	data, err := readStoredKubeconfig(id)
	if err != nil {
		return "", err
	}
	if ClusterSecretsMasked() {
		return MaskKubeconfigSecrets(string(data))
	}
	return string(data), nil
}

func parseKubeconfig(data []byte) (*clientcmdapi.Config, error) {
	cfg, err := clientcmd.Load(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	if len(cfg.Contexts) == 0 {
		return nil, errors.New("kubeconfig has no contexts")
	}
	return cfg, nil
}

func sortedContextNames(cfg *clientcmdapi.Config) []string {
	names := make([]string, 0, len(cfg.Contexts))
	for name := range cfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// pickContext выбирает контекст: запрошенный (он обязан существовать), иначе current-context,
// иначе первый по алфавиту — результат не зависит от порядка обхода map.
func pickContext(cfg *clientcmdapi.Config, requested string) (string, error) {
	if requested != "" {
		if _, ok := cfg.Contexts[requested]; !ok {
			return "", fmt.Errorf("context %q not found in kubeconfig", requested)
		}
		return requested, nil
	}
	if _, ok := cfg.Contexts[cfg.CurrentContext]; ok {
		return cfg.CurrentContext, nil
	}
	return sortedContextNames(cfg)[0], nil
}

// resolveContext выбирает контекст при правке кластера: явный выбор пользователя обязан существовать,
// ранее сохранённый контекст используется, пока он есть в файле, иначе — выбор по умолчанию.
func resolveContext(cfg *clientcmdapi.Config, requested, stored string) (string, error) {
	if requested == "" {
		if _, ok := cfg.Contexts[stored]; ok {
			requested = stored
		}
	}
	return pickContext(cfg, requested)
}

// KubeconfigContextInfo описывает контекст kubeconfig без секретов (токенов и сертификатов).
type KubeconfigContextInfo struct {
	Name      string `json:"name"`
	Cluster   string `json:"cluster"`
	User      string `json:"user"`
	Namespace string `json:"namespace"`
	Server    string `json:"server"`
	// Auth — как контекст получает учётные данные (credential.Kind): static, oidc, cloud-eks, cloud-gke, cloud-aks
	// или exec. По нему диалог предлагает общую учётную запись команды и объясняет, как будет выполнен вход.
	Auth string `json:"auth"`
}

// KubeconfigInfo — краткое содержимое kubeconfig для интерфейса.
type KubeconfigInfo struct {
	Contexts       []KubeconfigContextInfo `json:"contexts"`
	CurrentContext string                  `json:"currentContext"`
}

// InspectKubeconfig разбирает kubeconfig и возвращает список контекстов (без секретов).
func InspectKubeconfig(src KubeconfigSource) (*KubeconfigInfo, error) {
	data, err := src.read()
	if err != nil {
		return nil, err
	}
	cfg, err := parseKubeconfig(data)
	if err != nil {
		return nil, err
	}
	current, err := pickContext(cfg, "")
	if err != nil {
		return nil, err
	}
	info := &KubeconfigInfo{CurrentContext: current}
	for _, name := range sortedContextNames(cfg) {
		c := cfg.Contexts[name]
		item := KubeconfigContextInfo{Name: name, Cluster: c.Cluster, User: c.AuthInfo, Namespace: c.Namespace,
			Auth: string(authKindOf(cfg, data, name))}
		if cl := cfg.Clusters[c.Cluster]; cl != nil {
			item.Server = cl.Server
		}
		info.Contexts = append(info.Contexts, item)
	}
	return info, nil
}

// ConnectionCheck — результат пробного подключения. Недоступный кластер — не ошибка вызова,
// а результат проверки (OK=false и текст в Error).
type ConnectionCheck struct {
	OK            bool   `json:"ok"`
	ServerVersion string `json:"serverVersion"`
	LatencyMs     int64  `json:"latencyMs"`
	// Warning — кластер доступен, но с оговоркой (например, нет права на чтение namespaces).
	Warning string `json:"warning"`
	Error   string `json:"error"`
}

// CheckKubeconfig пробует подключиться к кластеру по kubeconfig и контексту, не сохраняя ничего.
func CheckKubeconfig(src KubeconfigSource, contextName string) (*ConnectionCheck, error) {
	data, err := src.read()
	if err != nil {
		return nil, err
	}
	cfg, err := parseKubeconfig(data)
	if err != nil {
		return nil, err
	}
	ctxName, err := pickContext(cfg, strings.TrimSpace(contextName))
	if err != nil {
		return nil, err
	}

	// Пробное подключение не должно запускать exec-плагин и читать файлы сервера там, где это запрещено.
	if err := CheckKubeconfigPolicy(cfg, ctxName); err != nil {
		return &ConnectionCheck{Error: err.Error()}, nil
	}
	// Блок exec и auth-provider не выполняется никогда (ADR-0018): пробное подключение входит только по
	// статическим данным, а вход через провайдера проверяется при подключении, через брокер.
	signs := contextSignsIn(cfg, ctxName)
	if signs {
		cfg = cfg.DeepCopy()
		if c := cfg.Contexts[ctxName]; c != nil {
			if ai := cfg.AuthInfos[c.AuthInfo]; ai != nil {
				ai.Exec, ai.AuthProvider = nil, nil
			}
		}
	}

	restCfg, err := clientcmd.NewNonInteractiveClientConfig(*cfg, ctxName, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return &ConnectionCheck{Error: err.Error()}, nil
	}
	restCfg.Timeout = checkTimeout
	applyClusterAddrPolicy(restCfg) // пробное подключение подчиняется той же политике адресов

	start := time.Now()
	disc, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return &ConnectionCheck{Error: err.Error()}, nil
	}
	ver, err := disc.ServerVersion()
	if err != nil {
		return &ConnectionCheck{Error: err.Error()}, nil
	}
	res := &ConnectionCheck{OK: true, ServerVersion: ver.GitVersion}
	if signs { // address and TLS are checked; the sign-in itself is the broker's business and happens on connect
		res.Warning = "the kubeconfig signs in through a plugin or an identity provider: the address is reachable, the sign-in is checked when you connect"
		res.LatencyMs = time.Since(start).Milliseconds()
		return res, nil
	}

	// /version нередко открыт без авторизации, поэтому учётные данные проверяем отдельным запросом.
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return &ConnectionCheck{Error: err.Error()}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	if _, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		if apierrors.IsForbidden(err) {
			res.Warning = "no permission to read namespaces: some data may be unavailable"
		} else {
			res.OK = false
			res.Error = err.Error()
		}
	}
	res.LatencyMs = time.Since(start).Milliseconds()
	return res, nil
}

// ClusterUpdateResult — итог сохранения настроек кластера.
type ClusterUpdateResult struct {
	Cluster ClusterMeta `json:"cluster"`
	// ConnectionChanged — изменился kubeconfig или контекст: существующее подключение устарело.
	ConnectionChanged bool `json:"connectionChanged"`
	// CredentialsChanged — в новом kubeconfig изменились секреты (токен, пароль, ключ): для журнала аудита.
	CredentialsChanged bool `json:"credentialsChanged"`
	// AccountModeChanged — включили или выключили общую учётную запись команды: сохранённые входы прежнего вида
	// недействительны (другой владелец), вызывающий их отзывает.
	AccountModeChanged bool `json:"accountModeChanged"`
}

// UpdateClusterSettings сохраняет настройки кластера и, если replace непуст, заменяет его kubeconfig.
// Любая проверка выполняется до записи: при ошибке на диске остаётся прежнее состояние.
func UpdateClusterSettings(id string, settings ClusterSettings, replace KubeconfigSource) (*ClusterUpdateResult, error) {
	meta, err := LoadClusterMeta(id)
	if err != nil {
		return nil, fmt.Errorf("cluster not found: %w", err)
	}
	settings, err = settings.normalize(meta.Icon)
	if err != nil {
		return nil, err
	}

	oldData, readErr := clusterStore.Kubeconfig(id)

	var newData []byte
	if !replace.isEmpty() {
		if replace.ClusterID != "" {
			return nil, errors.New("kubeconfig can only be replaced with a file or text")
		}
		replace.BaseClusterID = id // скрытые секреты берутся только у редактируемого кластера
		if newData, err = replace.read(); err != nil {
			return nil, err
		}
	}

	// Контекст проверяем по тому kubeconfig, который будет сохранён.
	contextName := meta.ContextName
	signsIn := false
	switch {
	case newData != nil:
		cfg, err := parseKubeconfig(newData)
		if err != nil {
			return nil, err
		}
		if contextName, err = resolveContext(cfg, settings.ContextName, meta.ContextName); err != nil {
			return nil, err
		}
		if err := CheckKubeconfigPolicy(cfg, contextName); err != nil {
			return nil, err
		}
		signsIn = contextSignsIn(cfg, contextName)
	case readErr == nil:
		// Если сохранённый файл повреждён, не мешаем поправить название и оформление.
		if cfg, perr := parseKubeconfig(oldData); perr == nil {
			var cerr error
			if contextName, cerr = resolveContext(cfg, settings.ContextName, meta.ContextName); cerr != nil {
				return nil, cerr
			}
			signsIn = contextSignsIn(cfg, contextName)
		}
	}

	mode, err := modeForSettings(meta.Mode, settings, meta.TeamID, signsIn)
	if err != nil {
		return nil, err
	}
	changed := newData != nil || contextName != meta.ContextName
	credsChanged := newData != nil && (readErr != nil || KubeconfigSecretsChanged(string(newData), string(oldData)))
	meta.DisplayName = settings.DisplayName
	meta.Icon = settings.Icon
	meta.Color = settings.Color
	meta.Environment = settings.Environment
	meta.DefaultNamespace = settings.DefaultNamespace
	meta.ArgoURL = settings.ArgoURL
	meta.ContextName = contextName
	modeChanged := mode != meta.Mode
	meta.Mode = mode

	// Хранилище заменяет kubeconfig и метаданные вместе и при сбое возвращает прежний kubeconfig.
	if err := clusterStore.Save(meta, newData); err != nil {
		return nil, err
	}
	return &ClusterUpdateResult{Cluster: *meta, ConnectionChanged: changed || modeChanged, CredentialsChanged: credsChanged,
		AccountModeChanged: modeChanged}, nil
}

// writeFileAtomic пишет файл через временный и переименование: при сбое остаётся прежнее содержимое,
// а не наполовину записанный kubeconfig.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
