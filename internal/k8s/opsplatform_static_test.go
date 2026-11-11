// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// opsPackages — пакеты операционных модулей и платформы (ADR-0026 §1). Они не строят конфигурацию кластера и не
// создают клиентов: чтение идёт только через opsplatform.Reader, а клиентов даёт Manager.ConnectionFor (ConfigFor).
// Общий запрет на rest.Config вне ConfigFor (TestRestConfigIsBuiltOnlyInConfigFor) уже обходит весь internal/;
// этот тест добавляет к нему запреты, специфичные для модулей. Пакет, которого ещё нет в дереве, пропускается.
var opsPackages = []string{
	"internal/opsplatform", "internal/incident", "internal/diagnose",
	"internal/readiness", "internal/baseline", "internal/drift",
}

// forbiddenOpsImports — пакеты, через которые строится подключение или создаются клиенты.
var forbiddenOpsImports = map[string]string{
	"k8s.io/client-go/rest":                           "builds a rest.Config",
	"k8s.io/client-go/tools/clientcmd":                "reads a kubeconfig",
	"k8s.io/client-go/tools/clientcmd/api":            "reads a kubeconfig",
	"github.com/24DSO-lab/kubron/internal/k8s":        "ops modules receive clients from the platform, not from the connection manager",
	"github.com/24DSO-lab/kubron/internal/app":        "ops modules must not depend on the application layer",
	"github.com/24DSO-lab/kubron/internal/credential": "credentials are the broker's business (ADR-0018)",
	"github.com/24DSO-lab/kubron/internal/pgstore":    "storage goes through the domain Backend (ADR-0026 §4)",
	"github.com/24DSO-lab/kubron/internal/store":      "storage goes through the domain Backend (ADR-0026 §4)",
}

// forbiddenClientCtors — конструкторы клиентов Kubernetes «из конфигурации».
var forbiddenClientCtors = map[string]bool{
	"NewForConfig": true, "NewForConfigOrDie": true, "NewForConfigAndClient": true,
	"NewDiscoveryClientForConfig": true, "NewDiscoveryClientForConfigOrDie": true, "NewDiscoveryClientForConfigAndClient": true,
	"NewManager": true, "ConfigFor": true, "PlanFor": true,
}

func opsSources(t *testing.T, fn func(rel string, f *ast.File)) {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	for _, pkg := range opsPackages {
		_ = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(pkg)), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Errorf("parsing %s: %v", path, perr)
				return nil
			}
			fn(filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))), f)
			return nil
		})
	}
}

func findBypass(rel string, f *ast.File) []string {
	var bad []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if why, ok := forbiddenOpsImports[p]; ok {
			bad = append(bad, rel+": imports "+p+" ("+why+")")
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && forbiddenClientCtors[sel.Sel.Name] {
				bad = append(bad, rel+": calls "+sel.Sel.Name)
			}
		}
		return true
	})
	return bad
}

func TestOpsPackagesDoNotBypassConfigFor(t *testing.T) {
	var bad []string
	opsSources(t, func(rel string, f *ast.File) { bad = append(bad, findBypass(rel, f)...) })
	if len(bad) > 0 {
		t.Errorf("ops packages must get clusters only through the platform (Reader / Manager.ConnectionFor → ConfigFor):\n  %s",
			strings.Join(bad, "\n  "))
	}
}

func findSecretReads(rel string, f *ast.File) []string {
	typeLits := []string{"GroupVersionKind", "GroupVersionResource", "GroupKind", "GroupResource"}
	var bad []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Secrets" {
				bad = append(bad, rel+": .Secrets(...) call")
			}
		case *ast.CompositeLit:
			sel, ok := x.Type.(*ast.SelectorExpr)
			if !ok || !slices.Contains(typeLits, sel.Sel.Name) {
				return true
			}
			for _, el := range x.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, _ := strconv.Unquote(lit.Value); strings.EqualFold(v, "secret") || strings.EqualFold(v, "secrets") {
						bad = append(bad, rel+": "+sel.Sel.Name+" literal for Secret")
					}
				}
			}
		}
		return true
	})
	return bad
}

// Secret кластера не читается: ни через типизированный клиент, ни через указание GVK/GVR в коде модулей.
func TestOpsPackagesDoNotReadSecrets(t *testing.T) {
	var bad []string
	opsSources(t, func(rel string, f *ast.File) { bad = append(bad, findSecretReads(rel, f)...) })
	if len(bad) > 0 {
		t.Errorf("Secret must never be read by ops packages (ADR-0024 §2, ADR-0027 §4):\n  %s", strings.Join(bad, "\n  "))
	}
}

// Детекторы ловят нарушения (иначе тест выше мог бы проходить вхолостую).
func TestOpsStaticDetectorsCatchViolations(t *testing.T) {
	parse := func(src string) *ast.File {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	bypass := parse(`package x
import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/kubernetes"
)
func f(c *rest.Config) { kubernetes.NewForConfig(c) }`)
	if got := findBypass("x.go", bypass); len(got) != 2 {
		t.Errorf("bypass findings = %v, want the rest import and NewForConfig", got)
	}
	secrets := parse(`package x
func f(c client) {
	c.CoreV1().Secrets("ns").Get(nil, "n", opts)
	_ = schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	_ = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	_ = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
}`)
	if got := findSecretReads("x.go", secrets); len(got) != 3 {
		t.Errorf("secret findings = %v, want 3", got)
	}
	clean := parse(`package x
func f(r Reader) { r.Get(nil, schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, "ns", "p") }`)
	if len(findBypass("x.go", clean))+len(findSecretReads("x.go", clean)) != 0 {
		t.Error("false positive on clean code")
	}
}

// Сам тест не должен проходить вхолостую: платформа существует, и её исходники обходятся.
func TestOpsPackagesAreScanned(t *testing.T) {
	n := 0
	opsSources(t, func(rel string, _ *ast.File) {
		if strings.HasPrefix(rel, "internal/opsplatform/") {
			n++
		}
	})
	if n == 0 {
		t.Fatal("no opsplatform sources were scanned: the static test is vacuous")
	}
}
