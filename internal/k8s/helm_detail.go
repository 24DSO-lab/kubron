// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// HelmRevisionInfo — одна ревизия релиза (запись истории).
type HelmRevisionInfo struct {
	Revision     int    `json:"revision"`
	Status       string `json:"status"`
	Chart        string `json:"chart"`
	ChartVersion string `json:"chartVersion"`
	AppVersion   string `json:"appVersion"`
	Updated      string `json:"updated"`   // age
	UpdatedAt    string `json:"updatedAt"` // RFC3339
	Description  string `json:"description"`
}

// HelmResourceRef — объект, созданный релизом (из манифеста последней ревизии).
type HelmResourceRef struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// HelmReleaseDetail — данные панели релиза, не требующие права на секреты.
// Поля Name/Namespace продублированы, а не встроены: Wails не разворачивает embedded-структуры.
type HelmReleaseDetail struct {
	Name      string             `json:"name"`
	Namespace string             `json:"namespace"`
	History   []HelmRevisionInfo `json:"history"` // новые первыми
	Resources []HelmResourceRef  `json:"resources"`
}

// HelmReleaseContent — содержимое одной ревизии. Values и манифест могут содержать секреты:
// метод, который его отдаёт, требует права secrets.read.
type HelmReleaseContent struct {
	Revision       int    `json:"revision"`
	Notes          string `json:"notes"`
	Values         string `json:"values"`         // только заданные пользователем (helm get values)
	ComputedValues string `json:"computedValues"` // дефолты чарта + заданные (helm get values --all)
	Manifest       string `json:"manifest"`       // значения Secret скрыты
}

// helmFull — release со всеми полями, нужными панели.
type helmFull struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Version   int    `json:"version"`
	Info      struct {
		Status        string    `json:"status"`
		Description   string    `json:"description"`
		FirstDeployed time.Time `json:"first_deployed"`
		LastDeployed  time.Time `json:"last_deployed"`
		Notes         string    `json:"notes"`
	} `json:"info"`
	Chart struct {
		Metadata struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			AppVersion string `json:"appVersion"`
		} `json:"metadata"`
		Values map[string]interface{} `json:"values"`
	} `json:"chart"`
	Config   map[string]interface{} `json:"config"`
	Manifest string                 `json:"manifest"`
}

func validateHelmRef(namespace, name string) error {
	if namespace == "" || len(validation.IsDNS1123Label(namespace)) > 0 {
		return fmt.Errorf("invalid namespace %q", namespace)
	}
	if name == "" || len(validation.IsDNS1123Subdomain(name)) > 0 {
		return fmt.Errorf("invalid release name %q", name)
	}
	return nil
}

func helmSecretName(name string, revision int) string {
	return fmt.Sprintf("sh.helm.release.v1.%s.v%d", name, revision)
}

// GetHelmReleaseDetail: история ревизий и ресурсы релиза.
func (m *Manager) GetHelmReleaseDetail(namespace, name string) (*HelmReleaseDetail, error) {
	if err := validateHelmRef(namespace, name); err != nil {
		return nil, err
	}
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	items, err := listHelmStored(ctx, conn.Client, namespace, helmReleaseSelector(name))
	if err != nil {
		return nil, err
	}
	return buildHelmDetailStored(items, namespace, name)
}

// GetHelmReleaseContent: values / notes / манифест ревизии (revision <= 0 — последняя).
func (m *Manager) GetHelmReleaseContent(namespace, name string, revision int) (*HelmReleaseContent, error) {
	if err := validateHelmRef(namespace, name); err != nil {
		return nil, err
	}
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	if revision <= 0 {
		items, err := listHelmStored(ctx, conn.Client, namespace, helmReleaseSelector(name))
		if err != nil {
			return nil, err
		}
		var latest *helmStored
		best := 0
		for i := range items {
			var p helmFull
			if items[i].decode(&p) != nil {
				continue
			}
			if p.Version > best {
				best, latest = p.Version, &items[i]
			}
		}
		if latest == nil {
			return nil, fmt.Errorf("release %s/%s not found", namespace, name)
		}
		return helmContentFromStored(*latest)
	}

	it, err := getHelmStored(ctx, conn.Client, namespace, name, revision)
	if err != nil {
		return nil, fmt.Errorf("revision %d of release %s/%s: %w", revision, namespace, name, err)
	}

	return helmContentFromStored(it)

}

// buildHelmDetail оставлен для секретов (тесты и прежние вызовы); работает через общую реализацию.
func buildHelmDetail(secrets []corev1.Secret, releaseNS, name string) (*HelmReleaseDetail, error) {
	items := make([]helmStored, 0, len(secrets))
	for i := range secrets {
		if s, ok := helmStoredFromSecret(&secrets[i]); ok {
			items = append(items, s)
		}
	}
	return buildHelmDetailStored(items, releaseNS, name)
}

func buildHelmDetailStored(items []helmStored, releaseNS, name string) (*HelmReleaseDetail, error) {
	var all []helmFull
	for i := range items {
		var p helmFull
		if err := items[i].decode(&p); err != nil {
			continue // битая ревизия не должна ломать панель
		}
		all = append(all, p)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("release %s/%s not found", releaseNS, name)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Version > all[j].Version })

	d := &HelmReleaseDetail{Name: name, Namespace: releaseNS, History: make([]HelmRevisionInfo, 0, len(all))}
	for i := range all {
		p := &all[i]
		at := ""
		if !p.Info.LastDeployed.IsZero() {
			at = p.Info.LastDeployed.UTC().Format(time.RFC3339)
		}
		d.History = append(d.History, HelmRevisionInfo{
			Revision:     p.Version,
			Status:       p.Info.Status,
			Chart:        p.Chart.Metadata.Name,
			ChartVersion: p.Chart.Metadata.Version,
			AppVersion:   p.Chart.Metadata.AppVersion,
			Updated:      helmUpdated(p.Info.LastDeployed),
			UpdatedAt:    at,
			Description:  p.Info.Description,
		})
	}
	d.Resources = helmResources(all[0].Manifest, releaseNS)
	return d, nil
}

// helmResources разбирает манифест; namespace, не указанный в манифесте, — namespace релиза
// (для кластерных типов остаётся пустым).
func helmResources(manifest, releaseNS string) []HelmResourceRef {
	objs, _ := parseManifest([]byte(manifest)) // частично разобранный манифест лучше, чем ничего
	out := make([]HelmResourceRef, 0, len(objs))
	for _, o := range objs {
		gvk := o.GroupVersionKind()
		ns := o.GetNamespace()
		if ns == "" {
			if info, known := kindTable[strings.ToLower(gvk.Kind)]; !known || info.namespaced {
				ns = releaseNS
			}
		}
		out = append(out, HelmResourceRef{
			Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind, Namespace: ns, Name: o.GetName(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func helmContentFrom(sec *corev1.Secret) (*HelmReleaseContent, error) {
	return helmContentFromStored(helmStored{Name: sec.Name, Namespace: sec.Namespace, Storage: helmStorageSecret, Raw: sec.Data["release"]})
}

func helmContentFromStored(it helmStored) (*HelmReleaseContent, error) {
	var p helmFull
	if err := it.decode(&p); err != nil {
		return nil, err
	}
	c := &HelmReleaseContent{Revision: p.Version, Notes: p.Info.Notes, Manifest: redactHelmManifest(p.Manifest)}
	if len(p.Config) > 0 {
		b, err := yaml.Marshal(p.Config)
		if err != nil {
			return nil, fmt.Errorf("values: %w", err)
		}
		c.Values = string(b)
	}
	b, err := yaml.Marshal(mergeHelmValues(p.Chart.Values, p.Config))
	if err != nil {
		return nil, fmt.Errorf("computed values: %w", err)
	}
	c.ComputedValues = string(b)
	return c, nil
}

// mergeHelmValues: глубокое слияние src поверх dst по правилам Helm (null в src удаляет ключ).
// Исходные карты не изменяются.
func mergeHelmValues(dst, src map[string]interface{}) map[string]interface{} {
	out := deepCopyMap(dst)
	for k, v := range src {
		if v == nil {
			delete(out, k)
			continue
		}
		sm, sIsMap := v.(map[string]interface{})
		dm, dIsMap := out[k].(map[string]interface{})
		if sIsMap && dIsMap {
			out[k] = mergeHelmValues(dm, sm)
			continue
		}
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return deepCopyMap(t)
	case []interface{}:
		c := make([]interface{}, len(t))
		for i := range t {
			c[i] = deepCopyValue(t[i])
		}
		return c
	}
	return v
}

// redactHelmManifest скрывает значения data/stringData у объектов core Secret.
// Остальные документы остаются как есть (с комментариями «# Source:»).
// Если манифест не разбирается, текст не отдаётся совсем: нельзя исключить, что в нём есть Secret.
func redactHelmManifest(manifest string) string {
	reader := k8syaml.NewYAMLReader(bufio.NewReader(strings.NewReader(manifest)))
	var docs []string
	for {
		doc, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "# failed to parse manifest"
		}
		text := strings.TrimSpace(string(doc))
		if text == "" {
			continue
		}
		docs = append(docs, redactSecretDoc(text))
	}
	return strings.Join(docs, "\n---\n")
}

func redactSecretDoc(text string) string {
	var m map[string]interface{}
	if yaml.Unmarshal([]byte(text), &m) != nil || m["kind"] != "Secret" || m["apiVersion"] != "v1" {
		return text
	}
	for _, f := range []string{"data", "stringData"} {
		if d, ok := m[f].(map[string]interface{}); ok {
			for k := range d {
				d[k] = argoSecretRedacted
			}
		}
	}
	if md, ok := m["metadata"].(map[string]interface{}); ok {
		if ann, ok := md["annotations"].(map[string]interface{}); ok {
			delete(ann, argoLastAppliedAnnKey) // копия данных
		}
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return "# Secret (values hidden)"
	}
	// комментарии в начале документа («# Source: ...») сохраняем
	var head []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			break
		}
		head = append(head, line)
	}
	body := strings.TrimRight(string(out), "\n")
	if len(head) > 0 {
		return strings.Join(head, "\n") + "\n" + body
	}
	return body
}
