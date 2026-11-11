// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// scenario — один файл testdata/scenarios/*.yaml: объекты кластера, логи и ожидаемый результат.
type scenario struct {
	Name    string
	Objects []unstructured.Unstructured
	Logs    []logFixture
	Expect  expectation
}

type logFixture struct {
	Pod, Container string
	Previous       bool
	Text           string
}

// expectation — что должен выдать анализ. Hypotheses — «код:уверенность»; Absent — коды, которых быть не должно.
type expectation struct {
	Target     ResourceRef       `json:"target"`
	Symptoms   []string          `json:"symptoms"`
	Hypotheses []string          `json:"hypotheses"`
	Absent     []string          `json:"absent"`
	Missing    []string          `json:"missing"`
	Recs       []string          `json:"recommendations"`
	Reader     map[string]string `json:"reader"` // ошибки чтения: "Kind" → not_found|forbidden|unavailable
	Error      string            `json:"error"`  // ожидаемая ошибка сбора (not_found|unsupported|...)
}

var fixedNow = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

func loadScenarios(t *testing.T) []scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "scenarios", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	var out []scenario
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := scenario{Name: strings.TrimSuffix(filepath.Base(f), ".yaml")}
		dec := utilyaml.NewYAMLReader(bufioReader(raw))
		for {
			doc, err := dec.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if len(bytes.TrimSpace(doc)) == 0 {
				continue
			}
			var m map[string]any
			if err := yaml.Unmarshal(doc, &m); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			switch m["kind"] {
			case "Expect":
				if err := yaml.Unmarshal(doc, &sc.Expect); err != nil {
					t.Fatalf("%s: expect: %v", f, err)
				}
			case "Log":
				var l struct {
					Pod, Container, Text string
					Previous             bool
				}
				if err := yaml.Unmarshal(doc, &l); err != nil {
					t.Fatalf("%s: log: %v", f, err)
				}
				sc.Logs = append(sc.Logs, logFixture{Pod: l.Pod, Container: l.Container, Previous: l.Previous, Text: l.Text})
			default:
				sc.Objects = append(sc.Objects, unstructured.Unstructured{Object: m})
			}
		}
		out = append(out, sc)
	}
	return out
}

func bufioReader(raw []byte) *bufioR { return newBufio(raw) }

// fakeReader — Reader поверх набора объектов. Считает вызовы и записывает прочитанные виды (проверка allowlist).
type fakeReader struct {
	mu      sync.Mutex
	objects []unstructured.Unstructured
	logs    []logFixture
	errs    map[string]error // по виду ресурса
	logErr  error
	kinds   map[string]int
	logReqs []LogRequest
}

func newFakeReader(objs []unstructured.Unstructured, logs []logFixture) *fakeReader {
	return &fakeReader{objects: objs, logs: logs, errs: map[string]error{}, kinds: map[string]int{}}
}

func (f *fakeReader) count(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds[kind]++
}

func (f *fakeReader) Get(_ context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
	f.count(gvk.Kind)
	if err := f.errs[gvk.Kind]; err != nil {
		return nil, err
	}
	for i := range f.objects {
		o := &f.objects[i]
		if o.GetKind() == gvk.Kind && o.GetNamespace() == ns && o.GetName() == name {
			return o.DeepCopy(), nil
		}
	}
	return nil, fmt.Errorf("get %s %s/%s: %w", gvk.Kind, ns, name, ErrNotFound)
}

func (f *fakeReader) List(_ context.Context, gvk schema.GroupVersionKind, opts ListOptions) (*ListResult, error) {
	f.count(gvk.Kind)
	if err := f.errs[gvk.Kind]; err != nil {
		return nil, err
	}
	sel := labels.Everything()
	if opts.LabelSelector != "" {
		var err error
		if sel, err = labels.Parse(opts.LabelSelector); err != nil {
			return nil, err
		}
	}
	res := &ListResult{ObservedAt: fixedNow}
	for i := range f.objects {
		o := &f.objects[i]
		if o.GetKind() != gvk.Kind || (opts.Namespace != "" && o.GetNamespace() != opts.Namespace) {
			continue
		}
		if !sel.Matches(labels.Set(o.GetLabels())) {
			continue
		}
		res.Items = append(res.Items, *o.DeepCopy())
	}
	return res, nil
}

func (f *fakeReader) PodLogs(_ context.Context, req LogRequest) ([]byte, error) {
	f.count("pods/log")
	f.mu.Lock()
	f.logReqs = append(f.logReqs, req)
	f.mu.Unlock()
	if f.logErr != nil {
		return nil, f.logErr
	}
	for _, l := range f.logs {
		if l.Pod == req.Pod && l.Container == req.Container && l.Previous == req.Previous {
			return []byte(l.Text), nil
		}
	}
	return nil, fmt.Errorf("no log: %w", ErrNotFound)
}
