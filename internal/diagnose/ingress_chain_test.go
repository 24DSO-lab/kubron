// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

var ingressTarget = ResourceRef{Kind: "Ingress", Namespace: "app", Name: "site"}

func ingressManifest(extraSpec string) string {
	return `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: site, namespace: app, uid: i1}
spec:
` + extraSpec + `
  tls: [{hosts: [site.example.test], secretName: site-tls}]
  rules:
    - host: site.example.test
      http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: ghost, port: {number: 80}}}}]}
`
}

func analyzeIngress(t *testing.T, r *graphtest.FakeReader) (Report, *Context) {
	t.Helper()
	c, err := Collect(t.Context(), chainReader{r}, ingressTarget, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	return Analyze(c), c
}

func hasHypothesis(rep Report, code string, conf Confidence) bool {
	return slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool { return h.Code == code && h.Confidence == conf })
}

// Secret и ConfigMap не читаются: имя TLS-Secret берётся из самого Ingress, а в отчёте остаётся шаг для человека.
func TestIngressNeverReadsSecretsOrConfigMaps(t *testing.T) {
	t.Parallel()
	r := graphtest.NewFakeReader()
	r.PutYAML(t, ingressManifest("  ingressClassName: gone"))
	rep, _ := analyzeIngress(t, r)
	for _, call := range r.CallLog() {
		if strings.Contains(call, "Secret") || strings.Contains(call, "ConfigMap") {
			t.Errorf("forbidden read: %s", call)
		}
	}
	if !slices.ContainsFunc(rep.Steps, func(s Step) bool { return s.Code == "step.check_tls_secret" && s.Params["secret"] == "site-tls" }) {
		t.Errorf("no TLS step: %+v", rep.Steps)
	}
	for _, f := range rep.Facts {
		for _, v := range f.Params {
			if strings.Contains(v, "BEGIN") {
				t.Errorf("certificate material in a fact: %v", f.Params)
			}
		}
	}
}

// Цель — Service, Ingress в графе нет: правило молчит.
func TestIngressRuleIsSilentForOtherTargets(t *testing.T) {
	t.Parallel()
	r := newChain(t, `
apiVersion: v1
kind: Service
metadata: {name: web, namespace: app, uid: s1}
spec: {selector: {app: web}}
`)
	c, err := Collect(t.Context(), r, ResourceRef{Kind: "Service", Namespace: "app", Name: "web"}, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	rep := Analyze(c)
	for _, h := range rep.Hypotheses {
		if strings.HasPrefix(h.Code, "ingress.") {
			t.Errorf("unexpected %s", h.Code)
		}
	}
	for _, s := range rep.Symptoms {
		if s == SymptomIngressUnreachable {
			t.Error("unexpected ingress symptom")
		}
	}
}

// Класс из устаревшей аннотации, которого нет, — уверенность ниже, чем из spec: контроллер может сопоставлять
// аннотацию и без объекта IngressClass.
func TestIngressClassConfidence(t *testing.T) {
	t.Parallel()
	bySpec := graphtest.NewFakeReader()
	bySpec.PutYAML(t, ingressManifest("  ingressClassName: gone"))
	if rep, _ := analyzeIngress(t, bySpec); !hasHypothesis(rep, "ingress.class_not_found", ConfidenceHigh) {
		t.Errorf("spec class: %+v", rep.Hypotheses)
	}
	byAnnotation := graphtest.NewFakeReader()
	byAnnotation.PutYAML(t, strings.Replace(ingressManifest(""), "metadata: {name: site, namespace: app, uid: i1}",
		"metadata: {name: site, namespace: app, uid: i1, annotations: {kubernetes.io/ingress.class: old}}", 1))
	if rep, _ := analyzeIngress(t, byAnnotation); !hasHypothesis(rep, "ingress.class_not_found", ConfidenceMedium) {
		t.Errorf("annotation class: %+v", rep.Hypotheses)
	}
}

// Класс по умолчанию есть — «класс не указан» не гипотеза.
func TestIngressUnspecifiedClassWithDefaultIsFine(t *testing.T) {
	t.Parallel()
	r := graphtest.NewFakeReader()
	r.PutYAML(t, ingressManifest(""))
	r.PutYAML(t, `
apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata: {name: std, annotations: {ingressclass.kubernetes.io/is-default-class: "true"}}
spec: {controller: k8s.io/ingress-nginx}
`)
	rep, _ := analyzeIngress(t, r)
	if slices.ContainsFunc(rep.Hypotheses, func(h Hypothesis) bool { return h.Code == "ingress.class_not_found" }) {
		t.Errorf("default class ignored: %+v", rep.Hypotheses)
	}
}

// Разные Service из backend дают по гипотезе, но не больше maxIngressFindings.
func TestIngressFindingsAreLimited(t *testing.T) {
	t.Parallel()
	var paths []string
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		paths = append(paths, "{path: /"+n+", pathType: Prefix, backend: {service: {name: svc-"+n+", port: {number: 80}}}}")
	}
	r := graphtest.NewFakeReader()
	r.PutYAML(t, `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: site, namespace: app, uid: i1}
spec:
  ingressClassName: nginx
  rules: [{http: {paths: [`+strings.Join(paths, ",")+`]}}]
`)
	r.PutYAML(t, "apiVersion: networking.k8s.io/v1\nkind: IngressClass\nmetadata: {name: nginx}\nspec: {controller: x}\n")
	rep, _ := analyzeIngress(t, r)
	n := 0
	for _, h := range rep.Hypotheses {
		if h.Code == "ingress.backend_service_missing" {
			n++
		}
	}
	if n != maxIngressFindings {
		t.Errorf("got %d hypotheses, want %d", n, maxIngressFindings)
	}
}

// Порт backend, которого нет у Service, — отдельная гипотеза; существующий порт — нет.
func TestIngressBackendPort(t *testing.T) {
	t.Parallel()
	svc := `
apiVersion: v1
kind: Service
metadata: {name: ghost, namespace: app, uid: s1}
spec: {selector: {app: web}, ports: [{name: http, port: %s}]}
`
	for port, want := range map[string]bool{"8080": true, "80": false} {
		r := graphtest.NewFakeReader()
		r.PutYAML(t, ingressManifest("  ingressClassName: nginx"))
		r.PutYAML(t, "apiVersion: networking.k8s.io/v1\nkind: IngressClass\nmetadata: {name: nginx}\nspec: {controller: x}\n")
		r.PutYAML(t, strings.Replace(svc, "%s", port, 1))
		rep, _ := analyzeIngress(t, r)
		if got := hasHypothesis(rep, "ingress.backend_port_mismatch", ConfidenceMedium); got != want {
			t.Errorf("service port %s: mismatch hypothesis = %v, want %v", port, got, want)
		}
	}
}
