// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package gke

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"

	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// Rule: the provider never uses the credentials of the machine it runs on. Two tests: one reads the
// sources, one watches the behaviour with the usual places of server credentials filled in.

// allowedImports is everything the non-test files may import. There is no os, no os/exec, no net, no log
// and no Google or cloud SDK: the provider cannot read a file, an environment variable or a socket of its
// own, and cannot write a log line.
var allowedImports = map[string]bool{
	"bytes":                   true,
	"context":                 true,
	"crypto/rand":             true,
	"crypto/rsa":              true,
	"crypto/sha256":           true,
	"crypto/subtle":           true,
	"crypto/x509":             true,
	"encoding/base64":         true,
	"encoding/json":           true,
	"encoding/pem":            true,
	"errors":                  true,
	"io":                      true,
	"net":                     true,
	"sync":                    true,
	"net/url":                 true,
	"net/http":                true,
	"strconv":                 true,
	"strings":                 true,
	"time":                    true,
	"golang.org/x/oauth2":     true,
	"golang.org/x/oauth2/jwt": true,
	"github.com/24DSO-lab/kubron/internal/credential": true,
}

// allowedHTTP lists the names of net/http that the sources may use: types, status and method constants, and the
// constructor of a request that is then sent with Env.HTTP. The default client, the
// default transport, the package-level request functions and Transport are not on the list.
var allowedHTTP = map[string]bool{
	"Client": true, "RoundTripper": true, "Request": true, "Response": true, "ErrUseLastResponse": true,
	"NewRequestWithContext": true, "MethodPost": true, "StatusOK": true, "StatusTooManyRequests": true,
	// The listener of the desktop sign-in (loopback.go): a server for 127.0.0.1 only.
	"Server": true, "Handler": true, "ResponseWriter": true, "Error": true, "NotFound": true, "MethodGet": true,
	"StatusBadRequest": true, "StatusMisdirectedRequest": true,
}

// allowedNet lists the names of net that the sources may use: the listener of the desktop sign-in on
// 127.0.0.1 and the split of a host and port. No Dial, no resolver, no other server.
var allowedNet = map[string]bool{"Listen": true, "Listener": true, "SplitHostPort": true}

func sourceFiles(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob("*.go")
	if err != nil || len(all) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	var out []string
	for _, f := range all {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

func TestSourcesDoNotReachForServerCredentials(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range sourceFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		httpName, netName := "", ""
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowedImports[path] {
				t.Errorf("%s imports %q, which is not on the list of allowed imports", name, path)
			}
			if path == "net" {
				netName = "net"
			}
			if path == "net/http" {
				httpName = "http"
				if imp.Name != nil {
					httpName = imp.Name.Name
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && httpName != "" && id.Name == httpName && !allowedHTTP[sel.Sel.Name] {
				t.Errorf("%s: http.%s is not allowed: only Env.HTTP may carry a request", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			if id, ok := sel.X.(*ast.Ident); ok && netName != "" && id.Name == netName && !allowedNet[sel.Sel.Name] {
				t.Errorf("%s: net.%s is not allowed: the only socket is the loopback listener", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			switch sel.Sel.Name {
			case "Getenv", "LookupEnv", "Environ", "ReadFile", "FindDefaultCredentials", "DefaultClient", "DefaultTransport",
				"DefaultTokenSource", "CredentialsFromJSON", "ComputeTokenSource", "OnGCE":
				t.Errorf("%s: %s must not be used", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

// TestDependenciesDoNotLinkTheGoogleSearchChain checks the compiled dependency list: the package golang.org/x/oauth2/google
// (Application Default Credentials, the metadata server) must not be part of the build at all.
func TestDependenciesDoNotLinkTheGoogleSearchChain(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	out, err := exec.Command(goBin, "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Skipf("go list failed (offline module setup?): %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "golang.org/x/oauth2/google") || strings.HasPrefix(dep, "cloud.google.com/") || strings.HasPrefix(dep, "google.golang.org/") {
			t.Errorf("the build links %s", dep)
		}
	}
}

// TestServerCredentialsAreNotUsed fills in every usual place of server-side Google credentials: the ADC
// variable and files, the gcloud configuration directory, the metadata server variables. A key of the
// "server" is there and a metadata server counts its visitors. The sign-in must use the pasted key and
// nothing else.
func TestServerCredentialsAreNotUsed(t *testing.T) {
	var metadataHits atomic.Int32
	md := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataHits.Add(1)
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte(`{"access_token":"metadata-token","token_type":"Bearer","expires_in":3599}`))
	}))
	defer md.Close()

	home := t.TempDir()
	serverKey := keyJSON(t, map[string]any{"client_email": "server-own@host-project.iam.gserviceaccount.com"})
	adcFile := filepath.Join(home, "adc.json")
	if err := os.WriteFile(adcFile, []byte(serverKey), 0o600); err != nil {
		t.Fatal(err)
	}
	gcloud := filepath.Join(home, ".config", "gcloud")
	if err := os.MkdirAll(gcloud, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gcloud, "application_default_credentials.json"), []byte(serverKey), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("CLOUDSDK_CONFIG", gcloud)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adcFile)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "host-project")
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(md.URL, "http://"))
	t.Setenv("GCE_METADATA_IP", strings.TrimPrefix(md.URL, "http://"))

	g := newGoogle(t, tokenOK(3599))
	env := envFor(g)

	// Begin with "use-adc": it must ask for a key, as it does everywhere else.
	req := beginReq(nil)
	req.Descriptor.Params["use-adc"] = "true"
	res, err := New().Begin(context.Background(), env, req)
	if err != nil || res.Challenge == nil || res.Token != nil {
		t.Fatalf("Begin with use-adc: res=%+v err=%v", res, err)
	}
	if g.callCount() != 0 {
		t.Fatal("Begin with use-adc made a request")
	}

	// Continue with the pasted key: the assertion is the user's.
	req = continueReq(keyJSON(t, nil))
	req.Descriptor.Params["use-adc"] = "true"
	if _, err := New().Continue(context.Background(), env, req); err != nil {
		t.Fatal(err)
	}
	calls, urls := g.snapshot()
	if len(calls) != 1 || len(urls) != 1 || urls[0] != TokenURL {
		t.Fatalf("calls=%d urls=%v: the only request is the token request to Google", len(calls), urls)
	}
	_, claims := decodeAssertion(t, calls[0].Form.Get("assertion"), &testRSA().PublicKey)
	if claims["iss"] != testEmail {
		t.Fatalf("iss = %v: the assertion must be the user's", claims["iss"])
	}
	if n := metadataHits.Load(); n != 0 {
		t.Fatalf("the metadata server was visited %d times", n)
	}
}
