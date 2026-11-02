// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Маскирование секретов kubeconfig в сервисном режиме.
//
// Сервер не отдаёт браузеру значения секретов: в тексте, который открывается для правки, их место занимает плейсхолдер
// MaskPlaceholder. При сохранении, пробном подключении и разборе сервер подставляет сохранённое значение того же
// пользователя на место плейсхолдера сам; значение, пришедшее от клиента, ему не источник. Секретами считаются
// users[].user.token, password, client-key-data, client-key, секретные поля auth-provider.config, значения
// exec.env и значения секретных флагов exec.args (--oidc-client-secret=…). Сертификаты (client-certificate-data,
// certificate-authority-data) остаются: это не секреты.

// MaskPlaceholder — значение секрета, скрытое от клиента. «Оставить как есть» в сохранённом кластере.
const MaskPlaceholder = "<kubron:unchanged>"

var (
	// ErrSecretPlaceholderNoCluster — в тексте есть плейсхолдер, но не указан сохранённый кластер, откуда взять значение.
	ErrSecretPlaceholderNoCluster = errors.New("the kubeconfig contains hidden secrets but no saved cluster to take them from")
	// ErrSecretPlaceholderRebound — адрес кластера или пользователь контекста изменились, значение секрета оставить нельзя.
	ErrSecretPlaceholderRebound = errors.New("the cluster address or the user of a context changed, so hidden secrets cannot be kept: enter them again")
	// ErrKubeconfigNotMasked — секрет не удалось скрыть (нестандартная разметка YAML): текст не отдаётся.
	ErrKubeconfigNotMasked = errors.New("the kubeconfig cannot be shown safely: a secret could not be hidden")
)

// HasSecretPlaceholder сообщает, что текст содержит плейсхолдер скрытого секрета.
func HasSecretPlaceholder(text string) bool { return strings.Contains(text, MaskPlaceholder) }

// ClusterSecretsMasked сообщает, что хранилище кластеров серверное (PostgreSQL): оно хранит секреты шифрованными и
// работает в сервисном режиме, где значения секретов не покидают сервер. У файлового хранилища десктопа и режима одного
// пользователя этой возможности нет, текст возвращается как раньше. Решает хранилище, а не признаки интерфейса.
func ClusterSecretsMasked() bool {
	_, ok := clusterStore.(modeClusterStore)
	return ok
}

// secretFlagRe — имена флагов exec.args, значение которых секретно (--oidc-client-secret, --password, --token…).
var secretFlagRe = regexp.MustCompile(`(?i)^--?[a-z0-9._-]*(secret|password|passwd|token|api-?key|private-?key)[a-z0-9._-]*$`)

// secretConfigKey — ключи auth-provider.config, значения которых секретны (access-token, refresh-token, client-secret…).
func secretConfigKey(k string) bool {
	k = strings.ToLower(k)
	if strings.HasSuffix(k, "-certificate-authority-data") || strings.HasSuffix(k, "-certificate-authority") {
		return false
	}
	for _, w := range []string{"token", "secret", "password", "passwd", "key"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// secretRef — одно секретное значение в YAML: пользователь, путь поля и узел, который хранит значение. prefix — часть
// значения до секрета (у «--flag=значение» это «--flag=»).
type secretRef struct {
	user, path string
	node       *yaml.Node
	prefix     string
}

func (r secretRef) value() string { return strings.TrimPrefix(r.node.Value, r.prefix) }

func (r secretRef) set(v string) {
	r.node.Value = r.prefix + v
	r.node.Tag = "!!str"
	r.node.Style = 0
}

func (r secretRef) key() string { return r.user + "\x00" + r.path }

// loadSentinel заменяет плейсхолдер перед разбором clientcmd: поле client-key-data — base64, а «<kubron:unchanged>» в
// base64 не разбирается. Строка допустимого base64 из 16 символов (без дополнения) разбирается и в строковых полях.
const loadSentinel = "KubronUnchanged0"

func forLoad(text string) string { return strings.ReplaceAll(text, MaskPlaceholder, loadSentinel) }

func loadConfig(text string) (*clientcmdapi.Config, error) {
	return clientcmd.Load([]byte(forLoad(text)))
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// pairs перечисляет пары «ключ, значение» отображения, раскрывая merge-ключи (<<: *base) и алиасы.
func pairs(m *yaml.Node, fn func(key string, val *yaml.Node)) {
	m = resolveAlias(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Value == "<<" && k.Tag == "!!merge" {
			if v = resolveAlias(v); v != nil && v.Kind == yaml.SequenceNode {
				for _, item := range v.Content {
					pairs(item, fn)
				}
			} else {
				pairs(v, fn)
			}
			continue
		}
		fn(k.Value, v)
	}
}

func lookup(m *yaml.Node, key string) *yaml.Node {
	var found *yaml.Node
	pairs(m, func(k string, v *yaml.Node) {
		if k == key && found == nil {
			found = resolveAlias(v)
		}
	})
	return found
}

func items(seq *yaml.Node) []*yaml.Node {
	seq = resolveAlias(seq)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]*yaml.Node, 0, len(seq.Content))
	for _, n := range seq.Content {
		out = append(out, resolveAlias(n))
	}
	return out
}

func nonEmptyScalar(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Value != "" && n.Tag != "!!null"
}

// walkSecrets вызывает fn для каждого непустого секрета каждого пользователя.
func walkSecrets(doc *yaml.Node, fn func(secretRef)) {
	root := resolveAlias(doc)
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = resolveAlias(root.Content[0])
	}
	for _, u := range items(lookup(root, "users")) {
		name := ""
		if n := lookup(u, "name"); n != nil {
			name = n.Value
		}
		user := lookup(u, "user")
		if user == nil {
			continue
		}
		for _, key := range []string{"token", "password", "client-key-data", "client-key"} {
			if n := lookup(user, key); nonEmptyScalar(n) {
				fn(secretRef{user: name, path: key, node: n})
			}
		}
		if ap := lookup(user, "auth-provider"); ap != nil {
			pairs(lookup(ap, "config"), func(k string, v *yaml.Node) {
				if v = resolveAlias(v); secretConfigKey(k) && nonEmptyScalar(v) {
					fn(secretRef{user: name, path: "auth-provider.config." + k, node: v})
				}
			})
		}
		if ex := lookup(user, "exec"); ex != nil {
			for _, e := range items(lookup(ex, "env")) {
				nameNode, valNode := lookup(e, "name"), lookup(e, "value")
				if nameNode != nil && nonEmptyScalar(valNode) {
					fn(secretRef{user: name, path: "exec.env." + nameNode.Value, node: valNode})
				}
			}
			args := items(lookup(ex, "args"))
			for i, a := range args {
				if !nonEmptyScalar(a) {
					continue
				}
				if flag, _, ok := strings.Cut(a.Value, "="); ok && secretFlagRe.MatchString(flag) {
					if a.Value != flag+"=" {
						fn(secretRef{user: name, path: "exec.args." + flag, node: a, prefix: flag + "="})
					}
				} else if secretFlagRe.MatchString(a.Value) && i+1 < len(args) && nonEmptyScalar(args[i+1]) {
					fn(secretRef{user: name, path: "exec.args." + a.Value, node: args[i+1]})
				}
			}
		}
	}
}

func parseNode(text string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	return &doc, nil
}

func encodeNode(doc *yaml.Node) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// MaskKubeconfigSecrets заменяет значения секретов плейсхолдером, сохраняя разметку и комментарии. Результат
// проверяется независимым разбором (clientcmd): если какой-то секрет остался (нестандартная разметка), текст не
// отдаётся вовсе — ошибка вместо утечки.
func MaskKubeconfigSecrets(text string) (string, error) {
	doc, err := parseNode(text)
	if err != nil {
		return "", err
	}
	if doc.Kind == 0 {
		return text, nil
	}
	walkSecrets(doc, func(r secretRef) { r.set(MaskPlaceholder) })
	out, err := encodeNode(doc)
	if err != nil {
		return "", err
	}
	if leaked := LeakedSecrets(out); len(leaked) > 0 {
		return "", ErrKubeconfigNotMasked
	}
	return out, nil
}

// secretValues собирает секреты разобранного kubeconfig (clientcmd) по тем же путям, что и обход YAML.
func secretValues(cfg *clientcmdapi.Config) map[string]string {
	out := map[string]string{}
	put := func(user, path, v string) {
		if v != "" {
			out[user+"\x00"+path] = v
		}
	}
	for name, ai := range cfg.AuthInfos {
		if ai == nil {
			continue
		}
		put(name, "token", ai.Token)
		put(name, "password", ai.Password)
		if len(ai.ClientKeyData) > 0 { // в тексте это base64, как в YAML
			put(name, "client-key-data", base64.StdEncoding.EncodeToString(ai.ClientKeyData))
		}
		put(name, "client-key", ai.ClientKey)
		if ai.AuthProvider != nil {
			for k, v := range ai.AuthProvider.Config {
				if secretConfigKey(k) {
					put(name, "auth-provider.config."+k, v)
				}
			}
		}
		if ai.Exec != nil {
			for _, e := range ai.Exec.Env {
				put(name, "exec.env."+e.Name, e.Value)
			}
			for i, a := range ai.Exec.Args {
				if flag, val, ok := strings.Cut(a, "="); ok && secretFlagRe.MatchString(flag) {
					put(name, "exec.args."+flag, val)
				} else if secretFlagRe.MatchString(a) && i+1 < len(ai.Exec.Args) {
					put(name, "exec.args."+a, ai.Exec.Args[i+1])
				}
			}
		}
	}
	return out
}

// LeakedSecrets возвращает пути секретов (без значений), которые в тексте kubeconfig не заменены плейсхолдером.
// Пустой список — текст не содержит секретных значений. Используется как независимая проверка маскирования.
func LeakedSecrets(text string) []string {
	cfg, err := loadConfig(text)
	if err != nil {
		return []string{"unparsable"}
	}
	var leaked []string
	for k, v := range secretValues(cfg) {
		if v != loadSentinel {
			leaked = append(leaked, strings.Replace(k, "\x00", ".", 1))
		}
	}
	return leaked
}

// serverOf возвращает адрес API-сервера и proxy-url кластера контекста для проверки «тот же ли кластер».
func serverOf(cfg *clientcmdapi.Config, ctx *clientcmdapi.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	cl := cfg.Clusters[ctx.Cluster]
	if cl == nil {
		return "", false
	}
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(cl.Server), "/")) + "\x00" + cl.ProxyURL, true
}

// checkSecretBinding не даёт сохранённому секрету уйти на другой адрес: каждый контекст нового kubeconfig, использующий
// пользователя user, обязан существовать в сохранённом с тем же пользователем и тем же адресом сервера. Иначе
// правка адреса кластера на чужой сервер заставила бы сервер отправить туда сохранённый токен.
func checkSecretBinding(user string, newCfg, oldCfg *clientcmdapi.Config) error {
	for name, ctx := range newCfg.Contexts {
		if ctx == nil || ctx.AuthInfo != user {
			continue
		}
		oldCtx := oldCfg.Contexts[name]
		if oldCtx == nil || oldCtx.AuthInfo != user {
			return ErrSecretPlaceholderRebound
		}
		newServer, ok1 := serverOf(newCfg, ctx)
		oldServer, ok2 := serverOf(oldCfg, oldCtx)
		if !ok1 || !ok2 || newServer != oldServer {
			return ErrSecretPlaceholderRebound
		}
	}
	return nil
}

// ExpandKubeconfigSecrets подставляет сохранённые значения на место плейсхолдеров в тексте newText. stored — сохранённый
// kubeconfig того же кластера (его читает сервер, не клиент). Значение берётся для того же пользователя и того же
// поля; плейсхолдер без сохранённого аналога — ошибка, как и смена адреса сервера или пользователя у контекста.
// Значение вместо плейсхолдера остаётся как есть и заменяет секрет. Текст без плейсхолдеров возвращается без изменений.
func ExpandKubeconfigSecrets(newText, stored string) (string, error) {
	if !HasSecretPlaceholder(newText) {
		return newText, nil
	}
	doc, err := parseNode(newText)
	if err != nil {
		return "", err
	}
	oldCfg, err := loadConfig(stored)
	if err != nil {
		return "", fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	newCfg, err := loadConfig(newText)
	if err != nil {
		return "", fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	saved := secretValues(oldCfg)

	var firstErr error
	checked := map[string]bool{}
	walkSecrets(doc, func(r secretRef) {
		if firstErr != nil || r.value() != MaskPlaceholder {
			return
		}
		value, ok := saved[r.key()]
		if !ok {
			firstErr = fmt.Errorf("hidden secret %s of user %q has no saved counterpart: enter its value", r.path, r.user)
			return
		}
		if !checked[r.user] {
			checked[r.user] = true
			if err := checkSecretBinding(r.user, newCfg, oldCfg); err != nil {
				firstErr = err
				return
			}
		}
		r.set(value)
	})
	if firstErr != nil {
		return "", firstErr
	}
	out, err := encodeNode(doc)
	if err != nil {
		return "", err
	}
	if HasSecretPlaceholder(out) {
		// плейсхолдер вне секретного поля (в имени, адресе): честная ошибка, а не отправка его серверу
		return "", ErrSecretPlaceholderMisplaced
	}
	return out, nil
}

// ErrSecretPlaceholderMisplaced — плейсхолдер стоит не в поле секрета.
var ErrSecretPlaceholderMisplaced = errors.New("the hidden-secret placeholder is used outside a secret field")

// KubeconfigSecretsChanged сообщает, отличаются ли секреты нового kubeconfig от сохранённого (после подстановки): добавлен,
// удалён или изменён хотя бы один. Значения не раскрываются.
func KubeconfigSecretsChanged(newText, stored string) bool {
	newCfg, err1 := loadConfig(newText)
	oldCfg, err2 := loadConfig(stored)
	if err1 != nil || err2 != nil {
		return true
	}
	a, b := secretValues(newCfg), secretValues(oldCfg)
	if len(a) != len(b) {
		return true
	}
	for k, v := range a {
		if b[k] != v {
			return true
		}
	}
	return false
}
