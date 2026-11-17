// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yamlutil "sigs.k8s.io/yaml"

	"github.com/24DSO-lab/kubron/internal/signals"
)

// Каталог сигналов (internal/signals) и манифест встроенного стека (manifests/metrics-stack.yaml) не должны
// разойтись молча: сигнал, которому манифест не даёт метрик, превратился бы в вечное «нет данных» (ADR-0032 §4).
// Тест сверяет с манифестом всё, что объявлено в signals.Requirements().

type scrapeJob struct {
	Name           string    `json:"job_name"`
	RelabelConfigs []relabel `json:"relabel_configs"`
	SD             []struct {
		Role       string `json:"role"`
		Namespaces struct {
			Names []string `json:"names"`
		} `json:"namespaces"`
	} `json:"kubernetes_sd_configs"`
	MetricsPath string `json:"metrics_path"`
}

type relabel struct {
	Action       string   `json:"action"`
	TargetLabel  string   `json:"target_label"`
	SourceLabels []string `json:"source_labels"`
}

// stackManifest разбирает вшитый манифест: задачи сбора, виды ресурсов kube-state-metrics, сервисы и их аннотации.
type stackManifest struct {
	jobs       map[string]scrapeJob
	ksmKinds   []string
	ksmImage   string
	scraped    map[string]bool   // сервисы с prometheus.io/scrape: "true"
	scrapedNS  map[string]string // namespace этих сервисов
	promYAMLOK bool              // конфигурация сбора найдена
}

func loadStackManifest(t *testing.T) stackManifest {
	t.Helper()
	data, err := loadManifestData()
	if err != nil {
		t.Fatal(err)
	}
	objs, err := parseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	m := stackManifest{jobs: map[string]scrapeJob{}, scraped: map[string]bool{}, scrapedNS: map[string]string{}}
	for _, o := range objs {
		switch o.GetKind() {
		case "ConfigMap":
			cm, _, _ := unstructured.NestedStringMap(o.Object, "data")
			for _, v := range cm {
				var cfg struct {
					Scrape []scrapeJob `json:"scrape_configs"`
				}
				if yamlutil.Unmarshal([]byte(v), &cfg) == nil && len(cfg.Scrape) > 0 {
					m.promYAMLOK = true
					for _, j := range cfg.Scrape {
						m.jobs[j.Name] = j
					}
				}
			}
		case "Service":
			if o.GetAnnotations()["prometheus.io/scrape"] == "true" {
				m.scraped[o.GetName()] = true
				m.scrapedNS[o.GetName()] = o.GetNamespace()
			}
		case "Deployment":
			if o.GetName() != "kube-state-metrics" {
				continue
			}
			ctrs, _, _ := unstructured.NestedSlice(o.Object, "spec", "template", "spec", "containers")
			for _, c := range ctrs {
				cm, _ := c.(map[string]any)
				m.ksmImage, _ = cm["image"].(string)
				args, _ := cm["args"].([]any)
				for _, a := range args {
					if s, _ := a.(string); strings.HasPrefix(s, "--resources=") {
						m.ksmKinds = strings.Split(strings.TrimPrefix(s, "--resources="), ",")
					}
				}
			}
		}
	}
	return m
}

func TestSignalCatalogMatchesMetricsStackManifest(t *testing.T) {
	m := loadStackManifest(t)
	if !m.promYAMLOK || len(m.jobs) == 0 {
		t.Fatal("the scrape configuration was not found in the manifest")
	}

	for _, req := range signals.Requirements() {
		name := string(req.Signal)
		if name == "" {
			name = "history coverage"
		}
		t.Run(name, func(t *testing.T) {
			for _, k := range req.KubeKind {
				if !slices.Contains(m.ksmKinds, k) {
					t.Errorf("kube-state-metrics does not collect %q (--resources=%v)", k, m.ksmKinds)
				}
			}
			for _, jn := range req.Jobs {
				if _, ok := m.jobs[jn]; !ok {
					t.Errorf("the scrape job %q is not in the manifest", jn)
				}
			}
			for _, lbl := range req.Labels {
				found := false
				for _, jn := range req.Jobs {
					for _, rc := range m.jobs[jn].RelabelConfigs {
						found = found || rc.TargetLabel == lbl
					}
				}
				if !found {
					t.Errorf("no job of %v sets the label %q", req.Jobs, lbl)
				}
			}
			for _, svc := range req.Services {
				if !m.scraped[svc] {
					t.Errorf("the service %q is not annotated prometheus.io/scrape: \"true\"", svc)
				}
			}
		})
	}
}

// Сервисы собирает задача kubernetes-service-endpoints, но только в своём namespace и только с аннотацией scrape.
func TestServiceEndpointsJobScrapesStackServices(t *testing.T) {
	m := loadStackManifest(t)
	job, ok := m.jobs["kubernetes-service-endpoints"]
	if !ok {
		t.Fatal("kubernetes-service-endpoints is missing")
	}
	if len(job.SD) != 1 || job.SD[0].Role != "endpoints" || !slices.Equal(job.SD[0].Namespaces.Names, []string{MetricsNamespace}) {
		t.Fatalf("service discovery = %+v, want endpoints in %s", job.SD, MetricsNamespace)
	}
	var keepsAnnotated bool
	for _, rc := range job.RelabelConfigs {
		keepsAnnotated = keepsAnnotated || rc.Action == "keep" &&
			slices.Contains(rc.SourceLabels, "__meta_kubernetes_service_annotation_prometheus_io_scrape")
	}
	if !keepsAnnotated {
		t.Error("the job must keep only services annotated with prometheus.io/scrape")
	}
	for _, svc := range []string{"node-exporter", "kube-state-metrics", "prometheus"} {
		if !m.scraped[svc] || m.scrapedNS[svc] != MetricsNamespace {
			t.Errorf("service %s must be scraped from %s", svc, MetricsNamespace)
		}
	}
}

// cAdvisor — отдельный путь метрик узла: без него сигналов контейнеров нет.
func TestCAdvisorJobPath(t *testing.T) {
	job, ok := loadStackManifest(t).jobs["kubernetes-cadvisor"]
	if !ok || job.MetricsPath != "/metrics/cadvisor" {
		t.Errorf("kubernetes-cadvisor = %+v", job)
	}
}

// Запросы каталога написаны под kube-state-metrics v2 (метки namespace, pod, container, node и resource у лимитов).
func TestKubeStateMetricsIsV2(t *testing.T) {
	img := loadStackManifest(t).ksmImage
	if !strings.Contains(img, "kube-state-metrics:v2.") {
		t.Errorf("kube-state-metrics image = %q: the catalog queries assume v2", img)
	}
}
