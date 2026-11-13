// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/24DSO-lab/kubron/internal/baseline"
)

// Ошибки нормализации.
var (
	// ErrSecretUnsupported — Secret не поддерживается и не читается ни при каких условиях (ADR-0027 §3).
	ErrSecretUnsupported = errors.New("secrets are not supported by baselines")
	// ErrUnsupportedKind — тип ресурса вне списка MVP.
	ErrUnsupportedKind = errors.New("resource kind is not supported by baselines")
)

// normOptions — параметры нормализации, зависящие от окружения объекта.
type normOptions struct {
	// excludeReplicas исключает spec.replicas у Deployment и StatefulSet, на которые указывает HPA: число реплик
	// управляется автомасштабированием, а не конфигурацией.
	excludeReplicas bool
}

// refOf строит ссылку на ресурс по объекту кластера.
func refOf(u *unstructured.Unstructured) baseline.Ref {
	gvk := u.GroupVersionKind()
	return baseline.Ref{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind, Namespace: u.GetNamespace(), Name: u.GetName()}
}

// specFor возвращает описание типа или ошибку: Secret — ErrSecretUnsupported, остальное — ErrUnsupportedKind.
func specFor(group, kind string) (*kindSpec, error) {
	if group == "" && strings.EqualFold(kind, "Secret") {
		return nil, ErrSecretUnsupported
	}
	ks, ok := kindFor(group, kind)
	if !ok {
		return nil, ErrUnsupportedKind
	}
	return ks, nil
}

// normalizeObject нормализует живой объект и вычисляет keyed hash чувствительных значений с солью salt.
// Второй результат — число значений, сохранённых только как hash.
func normalizeObject(u *unstructured.Unstructured, salt []byte, opt normOptions) (baseline.Object, int, error) {
	ref := refOf(u)
	ks, err := specFor(ref.Group, ref.Kind)
	if err != nil {
		return baseline.Object{}, 0, err
	}
	tree := normalizeTree(ks, u.Object, opt)
	f := &flattener{ref: ref, salt: salt, rules: ks.rules, out: map[string]baseline.Field{}}
	f.value(tree, "", "")
	return baseline.Object{Ref: ref, Fields: f.out}, f.hashed, nil
}

// normalizeTree — первая стадия: убирает служебные и назначаемые сервером поля, значения по умолчанию, null и пустые
// контейнеры, упорядочивает списки. Результат — новое дерево той же формы, в нём могут лежать исходные строки
// (в том числе чувствительные: их превращает в hash вторая стадия). Функция идемпотентна: повторный вызов на своём
// результате ничего не меняет (проверяется свойством в тестах).
func normalizeTree(ks *kindSpec, obj map[string]any, opt normOptions) map[string]any {
	r := ks.rules
	n := &normalizer{rules: r, exclude: r.exclude}
	if opt.excludeReplicas {
		n.exclude = mergeSets(r.exclude, newSet("spec.replicas"))
	}
	out := map[string]any{}

	if md, ok := obj["metadata"].(map[string]any); ok {
		meta := map[string]any{}
		if labels, ok := md["labels"].(map[string]any); ok && len(labels) > 0 {
			meta["labels"] = cloneValue(labels)
		}
		if ann, ok := md["annotations"].(map[string]any); ok {
			kept := map[string]any{}
			for k, v := range ann {
				if !r.annotationExcluded(k) {
					kept[k] = v
				}
			}
			if len(kept) > 0 {
				meta["annotations"] = kept
			}
		}
		if len(meta) > 0 {
			out["metadata"] = meta
		}
	}
	for _, name := range ks.top {
		v, ok := obj[name]
		if !ok || n.exclude.has(name) {
			continue
		}
		nv, keep := n.walk(v, name)
		if !keep || n.empty(name, nv) {
			continue
		}
		out[name] = nv
	}
	return out
}

type normalizer struct {
	rules   *kindRules
	exclude set
}

// empty сообщает, что значение — пустой объект или список, который нужно отбросить (кроме полей из keepEmpty).
func (n *normalizer) empty(path string, v any) bool {
	name := path
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		name = path[i+1:]
	}
	switch t := v.(type) {
	case map[string]any:
		return len(t) == 0 && !keepEmpty.has(name)
	case []any:
		return len(t) == 0
	}
	return false
}

// walk нормализует значение по схемному пути sp. Второй результат false — значение нужно отбросить (null).
func (n *normalizer) walk(v any, sp string) (any, bool) {
	switch t := v.(type) {
	case nil:
		return nil, false
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			csp := k
			if sp != "" {
				csp = sp + "." + k
			}
			if n.exclude.has(csp) {
				continue
			}
			nv, keep := n.walk(child, csp)
			if !keep || n.empty(csp, nv) {
				continue
			}
			if d, ok := n.rules.defaults[csp]; ok && literal(nv) == d {
				continue
			}
			out[k] = nv
		}
		dropDefaultPullPolicy(sp, out)
		return out, true
	case []any:
		ep := sp + "[]"
		items := make([]any, 0, len(t))
		for _, e := range t {
			if nv, keep := n.walk(e, ep); keep {
				items = append(items, nv)
			}
		}
		keys, keyed := n.rules.keyed[ep]
		switch {
		case n.rules.ordered.has(ep):
			// порядок значим
		case keyed:
			slices.SortStableFunc(items, func(a, b any) int {
				if c := strings.Compare(keyTuple(a, keys), keyTuple(b, keys)); c != 0 {
					return c
				}
				return strings.Compare(literal(a), literal(b))
			})
		case n.rules.sets.has(ep):
			slices.SortStableFunc(items, func(a, b any) int { return strings.Compare(literal(a), literal(b)) })
		}
		return items, true
	default:
		return v, true
	}
}

// dropDefaultPullPolicy убирает imagePullPolicy, равную значению по умолчанию для образа: Always для тега latest
// и образа без тега, иначе IfNotPresent. Таблица defaults так не умеет: значение зависит от соседнего поля.
func dropDefaultPullPolicy(sp string, container map[string]any) {
	// containers[], initContainers[] и ephemeralContainers[]: у всех одна и та же схема контейнера.
	if !strings.HasSuffix(strings.ToLower(sp), "containers[]") {
		return
	}
	img, _ := container["image"].(string)
	pol, _ := container["imagePullPolicy"].(string)
	if img == "" || pol == "" {
		return
	}
	if pol == defaultPullPolicy(img) {
		delete(container, "imagePullPolicy")
	}
}

func defaultPullPolicy(image string) string {
	if i := strings.IndexByte(image, '@'); i >= 0 {
		return "IfNotPresent" // образ по digest не обновляется
	}
	name := image[strings.LastIndexByte(image, '/')+1:]
	tag, hasTag := "", false
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		tag, hasTag = name[i+1:], true
	}
	if !hasTag || tag == "latest" {
		return "Always"
	}
	return "IfNotPresent"
}

// keyTuple собирает значения ключей элемента списка для упорядочивания.
func keyTuple(elem any, keys []string) string {
	m, _ := elem.(map[string]any)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(literal(lookup(m, k)))
		b.WriteByte(0)
	}
	return b.String()
}

// lookup читает значение по пути из точек («metadata.name»).
func lookup(m map[string]any, path string) any {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[part]
	}
	return cur
}

// literal — канонический JSON-литерал значения: ключи объектов по алфавиту, числа без лишних знаков.
func literal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null" // типы из unstructured всегда сериализуются; недостижимо
	}
	return string(b)
}

// cloneValue копирует дерево, чтобы нормализация не меняла объект вызывающего.
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, c := range t {
			m[k] = cloneValue(c)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, c := range t {
			s[i] = cloneValue(c)
		}
		return s
	default:
		return v
	}
}
