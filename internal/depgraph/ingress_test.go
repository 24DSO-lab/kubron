// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

var refIngress = depgraph.Ref{Group: "networking.k8s.io", Kind: "Ingress", Namespace: "app", Name: "web"}

func ingressYAML(class, backends string) string {
	spec := "rules: [{host: example.test, http: {paths: [" + backends + "]}}]"
	if class != "" {
		spec = "ingressClassName: " + class + ", " + spec
	}
	return `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: web, namespace: app, uid: i1}
spec: {` + spec + `}
`
}

func path(svc, port string) string {
	return "{path: /, pathType: Prefix, backend: {service: {name: " + svc + ", port: {" + port + "}}}}"
}

const nginxClass = `
apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata: {name: nginx}
spec: {controller: k8s.io/ingress-nginx}
`

func TestIngressHealthyChain(t *testing.T) {
	f := world(t, ingressYAML("nginx", path("web", "number: 80")), nginxClass)
	g := build(t, f, refIngress, depgraph.Options{})
	ing := mustNode(t, g, refIngress)
	if ing.Health != depgraph.Healthy {
		t.Fatalf("ingress = %s/%s, want Healthy", ing.Health, ing.Reason)
	}
	svc := depgraph.Ref{Kind: "Service", Namespace: "app", Name: "web"}
	edgeOf(t, g, refIngress, svc, depgraph.EdgeRoutes)
	edgeOf(t, g, refIngress, depgraph.Ref{Group: "networking.k8s.io", Kind: "IngressClass", Name: "nginx"}, depgraph.EdgeBinds)
	if _, ok := g.Node(depgraph.Ref{Kind: "Pod", Namespace: "app", Name: "web-7d-x"}); !ok && len(g.Nodes()) < 4 {
		t.Errorf("the service was not expanded: %v", refsOf(g.Nodes()))
	}
}

func TestIngressMissingBackendService(t *testing.T) {
	f := world(t, ingressYAML("nginx", path("ghost", "number: 80")), nginxClass)
	g := build(t, f, refIngress, depgraph.Options{})
	ing := mustNode(t, g, refIngress)
	if ing.Health != depgraph.Degraded || ing.Reason != depgraph.ReasonIngressBackendMissing {
		t.Fatalf("ingress = %s/%s", ing.Health, ing.Reason)
	}
	svc := mustNode(t, g, depgraph.Ref{Kind: "Service", Namespace: "app", Name: "ghost"})
	if svc.Health != depgraph.Missing {
		t.Errorf("service = %s, want Missing", svc.Health)
	}
}

func TestIngressMissingClass(t *testing.T) {
	f := world(t, ingressYAML("nope", path("web", "number: 80")))
	g := build(t, f, refIngress, depgraph.Options{})
	ing := mustNode(t, g, refIngress)
	if ing.Health != depgraph.Degraded || ing.Reason != depgraph.ReasonIngressClassMissing {
		t.Fatalf("ingress = %s/%s", ing.Health, ing.Reason)
	}
}

func TestIngressBackendPortNotDeclared(t *testing.T) {
	f := world(t, ingressYAML("nginx", path("api", "number: 9999")), nginxClass, `
apiVersion: v1
kind: Service
metadata: {name: api, namespace: app}
spec: {selector: {app: web}, ports: [{name: http, port: 80}]}
`)
	g := build(t, f, refIngress, depgraph.Options{})
	ing := mustNode(t, g, refIngress)
	if ing.Reason != depgraph.ReasonIngressBackendPortMissing {
		t.Fatalf("ingress = %s/%s", ing.Health, ing.Reason)
	}
}

func TestIngressDefaultClassIsFoundWithOneList(t *testing.T) {
	f := world(t, ingressYAML("", path("web", "number: 80")), `
apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata: {name: std, annotations: {ingressclass.kubernetes.io/is-default-class: "true"}}
spec: {controller: k8s.io/ingress-nginx}
`)
	g := build(t, f, refIngress, depgraph.Options{})
	e := edgeOf(t, g, refIngress, depgraph.Ref{Group: "networking.k8s.io", Kind: "IngressClass", Name: "std"}, depgraph.EdgeBinds)
	if e.Reason != "default_class" {
		t.Errorf("edge = %+v", e)
	}
	lists := 0
	for _, c := range f.CallLog() {
		if strings.HasPrefix(c, "LIST IngressClass") {
			lists++
		}
	}
	if lists > 1 {
		t.Errorf("IngressClass listed %d times", lists)
	}
}

func TestIngressWithoutClassAndDefaultIsNotMissing(t *testing.T) {
	f := world(t, ingressYAML("", path("web", "number: 80")))
	g := build(t, f, refIngress, depgraph.Options{})
	if ing := mustNode(t, g, refIngress); ing.Health != depgraph.Healthy {
		t.Fatalf("ingress = %s/%s", ing.Health, ing.Reason)
	}
}

func TestIngressResourceBackendIsUnknownPolicy(t *testing.T) {
	f := world(t, `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: web, namespace: app}
spec:
  ingressClassName: nginx
  defaultBackend: {resource: {apiGroup: example.io, kind: Bucket, name: static}}
`, nginxClass)
	g := build(t, f, refIngress, depgraph.Options{})
	n := mustNode(t, g, depgraph.Ref{Group: "example.io", Kind: "Bucket", Namespace: "app", Name: "static"})
	if n.Health != depgraph.Unknown || n.Unknown != depgraph.UnknownPolicy {
		t.Errorf("resource backend = %s/%s", n.Health, n.Unknown)
	}
	if ing := mustNode(t, g, refIngress); ing.Health == depgraph.Degraded {
		t.Errorf("unknown backend must not degrade the ingress: %s", ing.Reason)
	}
}

func TestIngressForbiddenClassIsUnknownNotMissing(t *testing.T) {
	f := world(t, ingressYAML("nginx", path("web", "number: 80")), nginxClass)
	f.Forbid(schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "IngressClass"})
	g := build(t, f, refIngress, depgraph.Options{})
	if ing := mustNode(t, g, refIngress); ing.Reason == depgraph.ReasonIngressClassMissing {
		t.Errorf("forbidden class reported as missing")
	}
}

func TestIngressManyBackendsAreLimited(t *testing.T) {
	var ps []string
	for i := 0; i < 14; i++ {
		ps = append(ps, path("svc-"+string(rune('a'+i)), "number: 80"))
	}
	f := world(t, ingressYAML("nginx", strings.Join(ps, ",")), nginxClass)
	g := build(t, f, refIngress, depgraph.Options{})
	if !g.Truncated() {
		t.Fatal("graph must be truncated")
	}
	found := false
	for _, w := range g.Warnings() {
		found = found || w == "limit.backends"
	}
	if !found {
		t.Errorf("warnings = %v", g.Warnings())
	}
}

func TestIngressHelpers(t *testing.T) {
	// Каталог зависимостей без кластера: чистые функции не должны паниковать на пустом Ingress.
	if got := depgraph.IngressBackends(nil); got != nil {
		t.Errorf("nil ingress: %v", got)
	}
	if n, ok := depgraph.IngressClassOf(nil); n != "" || ok {
		t.Errorf("nil ingress class: %q %v", n, ok)
	}
	if got := depgraph.IngressTLSSecrets(nil); got != nil {
		t.Errorf("nil ingress tls: %v", got)
	}
}
