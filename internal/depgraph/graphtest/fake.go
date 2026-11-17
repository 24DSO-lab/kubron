// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package graphtest — тестовые двойники для depgraph.Reader. Используется тестами depgraph, diagnose и correlation:
// FakeReader можно встроить в собственный фейк diagnose.Reader, добавив метод PodLogs.
package graphtest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// ObservedAt — время наблюдения, которое возвращают все List фейка (фиксировано ради детерминизма).
var ObservedAt = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

// FakeReader — depgraph.Reader поверх набора объектов в памяти. Потокобезопасен. Считает и записывает вызовы,
// умеет отказывать по виду целиком (Fail, Forbid) и по одному объекту (FailObject).
type FakeReader struct {
	mu      sync.Mutex
	objects map[string]*unstructured.Unstructured // gk|ns|name
	kindErr map[schema.GroupKind]error
	objErr  map[string]error
	log     []string
}

// NewFakeReader создаёт пустой фейк.
func NewFakeReader() *FakeReader {
	return &FakeReader{
		objects: map[string]*unstructured.Unstructured{},
		kindErr: map[schema.GroupKind]error{},
		objErr:  map[string]error{},
	}
}

func objKey(gk schema.GroupKind, ns, name string) string {
	return gk.String() + "|" + ns + "|" + name
}

// Put кладёт копию объекта; повтор того же вида, namespace и имени заменяет предыдущий.
func (f *FakeReader) Put(o *unstructured.Unstructured) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lazyInit()
	f.objects[objKey(o.GroupVersionKind().GroupKind(), o.GetNamespace(), o.GetName())] = o.DeepCopy()
}

// PutYAML разбирает обычные манифесты (несколько документов через ---) и кладёт каждый объект.
func (f *FakeReader) PutYAML(t testing.TB, manifests string) {
	t.Helper()
	rd := utilyaml.NewYAMLReader(bufioOf(manifests))
	for {
		doc, err := rd.Read()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("graphtest: reading manifest: %v", err)
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		js, err := yaml.YAMLToJSON(doc)
		if err != nil {
			t.Fatalf("graphtest: parsing manifest: %v", err)
		}
		var m map[string]any
		if err := utiljson.Unmarshal(js, &m); err != nil { // числа как int64, как у настоящих клиентов
			t.Fatalf("graphtest: parsing manifest: %v", err)
		}
		if len(m) == 0 {
			continue
		}
		f.Put(&unstructured.Unstructured{Object: m})
	}
}

// Fail заставляет любое чтение вида gvk (Get и List, версия не учитывается) вернуть err.
func (f *FakeReader) Fail(gvk schema.GroupVersionKind, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lazyInit()
	f.kindErr[gvk.GroupKind()] = err
}

// Forbid — Fail с depgraph.ErrForbidden.
func (f *FakeReader) Forbid(gvk schema.GroupVersionKind) { f.Fail(gvk, depgraph.ErrForbidden) }

// FailObject заставляет Get одного объекта вернуть err (List по виду продолжает работать).
func (f *FakeReader) FailObject(gvk schema.GroupVersionKind, ns, name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lazyInit()
	f.objErr[objKey(gvk.GroupKind(), ns, name)] = err
}

// Calls — число выполненных чтений (Get + List).
func (f *FakeReader) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.log)
}

// CallLog — журнал чтений в порядке вызова: «GET Kind ns/name» и «LIST Kind ns [label=…] [field=…] [limit=N]».
func (f *FakeReader) CallLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func (f *FakeReader) lazyInit() {
	if f.objects == nil {
		f.objects = map[string]*unstructured.Unstructured{}
		f.kindErr = map[schema.GroupKind]error{}
		f.objErr = map[string]error{}
	}
}

// Get реализует depgraph.Reader.
func (f *FakeReader) Get(ctx context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lazyInit()
	f.log = append(f.log, fmt.Sprintf("GET %s %s/%s", gvk.Kind, ns, name))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gk := gvk.GroupKind()
	if err := f.kindErr[gk]; err != nil {
		return nil, err
	}
	if err := f.objErr[objKey(gk, ns, name)]; err != nil {
		return nil, err
	}
	if o, ok := f.objects[objKey(gk, ns, name)]; ok {
		return o.DeepCopy(), nil
	}
	return nil, fmt.Errorf("get %s %s/%s: %w", gvk.Kind, ns, name, depgraph.ErrNotFound)
}

// List реализует depgraph.Reader: namespace, label- и field-селекторы (поля — путь через точку), Limit и Truncated.
// Результат отсортирован по namespace и имени.
func (f *FakeReader) List(ctx context.Context, gvk schema.GroupVersionKind, o depgraph.ListOptions) (*depgraph.ListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lazyInit()
	entry := fmt.Sprintf("LIST %s %s", gvk.Kind, o.Namespace)
	if o.LabelSelector != "" {
		entry += " label=" + o.LabelSelector
	}
	if o.FieldSelector != "" {
		entry += " field=" + o.FieldSelector
	}
	if o.Limit > 0 {
		entry += fmt.Sprintf(" limit=%d", o.Limit)
	}
	f.log = append(f.log, entry)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gk := gvk.GroupKind()
	if err := f.kindErr[gk]; err != nil {
		return nil, err
	}
	sel := labels.Everything()
	if o.LabelSelector != "" {
		var err error
		if sel, err = labels.Parse(o.LabelSelector); err != nil {
			return nil, fmt.Errorf("graphtest: label selector: %w", err)
		}
	}
	fields, err := parseFieldSelector(o.FieldSelector)
	if err != nil {
		return nil, err
	}
	var items []*unstructured.Unstructured
	for _, obj := range f.objects {
		if obj.GroupVersionKind().GroupKind() != gk || (o.Namespace != "" && obj.GetNamespace() != o.Namespace) {
			continue
		}
		if !sel.Matches(labels.Set(obj.GetLabels())) || !fields.match(obj) {
			continue
		}
		items = append(items, obj)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].GetNamespace() != items[j].GetNamespace() {
			return items[i].GetNamespace() < items[j].GetNamespace()
		}
		return items[i].GetName() < items[j].GetName()
	})
	res := &depgraph.ListResult{ObservedAt: ObservedAt}
	if o.Limit > 0 && int64(len(items)) > o.Limit {
		items, res.Truncated = items[:o.Limit], true
	}
	for _, it := range items {
		res.Items = append(res.Items, *it.DeepCopy())
	}
	return res, nil
}

type fieldReq struct {
	path  []string
	value string
	neg   bool
}

type fieldSet []fieldReq

func parseFieldSelector(s string) (fieldSet, error) {
	var out fieldSet
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		neg := false
		k, v, ok := strings.Cut(part, "!=")
		if ok {
			neg = true
		} else if k, v, ok = strings.Cut(part, "="); !ok {
			return nil, fmt.Errorf("graphtest: bad field selector %q", part)
		}
		v = strings.TrimPrefix(v, "=")
		out = append(out, fieldReq{path: strings.Split(strings.TrimSpace(k), "."), value: v, neg: neg})
	}
	return out, nil
}

func (fs fieldSet) match(o *unstructured.Unstructured) bool {
	for _, r := range fs {
		got, _, _ := unstructured.NestedString(o.Object, r.path...)
		if (got == r.value) == r.neg {
			return false
		}
	}
	return true
}
