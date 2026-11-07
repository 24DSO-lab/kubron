// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// Rule 2 of ADR-0018 for cloud providers: the provider never uses the credentials of the machine it runs on.
// Two checks: by the sources (what the package may import and write) and by behaviour (a machine that is full
// of AWS credentials, a profile and an instance metadata service, none of which may be touched).

var allowedImports = map[string]bool{
	"bytes": true, "context": true, "crypto/sha256": true, "encoding/base64": true, "encoding/hex": true, "encoding/json": true,
	"encoding/xml": true, "errors": true, "io": true, "net/http": true, "net/url": true, "regexp": true, "strconv": true,
	"strings": true, "time": true,
	"github.com/aws/aws-sdk-go-v2/aws":                 true, // the credentials type only
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4":       true, // local signing
	"github.com/24DSO-labs/kubron/internal/credential": true,
	"github.com/24DSO-labs/kubron/internal/idpnet":     true, // address checks and error values only
	"github.com/24DSO-labs/kubron/internal/netpolicy":  true, // IsBlocked only
}

var forbiddenText = []string{
	"Getenv", "LookupEnv", "Environ", "ExpandEnv", "ReadFile", "os.Open", "UserHomeDir", "DefaultClient", "DefaultTransport",
	"http.Transport", "http.Client{", "net.Dial", "LoadDefaultConfig", "NewFromConfig", "imds", "169.254", "fd00:ec2",
	"ec2metadata", "AWS_PROFILE", "AWS_ACCESS_KEY_ID", ".aws/", "credentials.New", "NewStaticCredentialsProvider",
	"exec.Command", "idpnet.NewClient", "NewTransport",
}

func productionSources(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatal("no sources", err)
	}
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	return out
}

func TestSourcesUseNothingOfTheMachine(t *testing.T) {
	for name, src := range productionSources(t) {
		file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowedImports[path] {
				t.Errorf("%s imports %q: not on the list of what a cloud provider may use", name, path)
			}
		}
		for i, line := range strings.Split(src, "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") {
				continue
			}
			for _, bad := range forbiddenText {
				if strings.Contains(code, bad) {
					t.Errorf("%s:%d uses %q", name, i+1, bad)
				}
			}
		}
	}
}

// A machine full of AWS credentials, a named profile and a metadata service. The provider must not touch any
// of it: with no saved data it asks the user (a provider that used the default chain would return a token),
// and with the keys the user typed it signs with those and nothing else.
func TestBehaviourIgnoresTheMachine(t *testing.T) {
	var imds atomic.Int64
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { imds.Add(1); http.Error(w, "no", 404) }))
	t.Cleanup(meta.Close)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o700); err != nil {
		t.Fatal(err)
	}
	creds := "[default]\naws_access_key_id = AKIAMACHINECANARY0001\naws_secret_access_key = machineSecretCANARY0123456789abcdefgh\n" +
		"[work]\naws_access_key_id = AKIAWORKPROFILECANARY\naws_secret_access_key = workSecretCANARY0123456789abcdefghij\n"
	credFile := filepath.Join(home, ".aws", "credentials")
	if err := os.WriteFile(credFile, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"HOME": home, "USERPROFILE": home, "AWS_SHARED_CREDENTIALS_FILE": credFile, "AWS_CONFIG_FILE": filepath.Join(home, ".aws", "config"),
		"AWS_ACCESS_KEY_ID": "AKIAENVIRONMENTCANARY1", "AWS_SECRET_ACCESS_KEY": "envSecretCANARY0123456789abcdefghijkl",
		"AWS_SESSION_TOKEN": "envSessionCANARY", "AWS_PROFILE": "work", "AWS_DEFAULT_PROFILE": "work", "AWS_REGION": "eu-west-1",
		"AWS_EC2_METADATA_SERVICE_ENDPOINT": meta.URL, "AWS_CONTAINER_CREDENTIALS_FULL_URI": meta.URL + "/creds",
		"AWS_WEB_IDENTITY_TOKEN_FILE": credFile, "AWS_ROLE_ARN": testRole,
	} {
		t.Setenv(k, v)
	}
	wd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	p, f := New(), newFakeAWS(t)
	env := f.env()
	d := desc(map[string]string{"profile": "work"}) // the profile of the kubeconfig is not used either
	res, err := p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser()})
	mustChallenge(t, res, err, credential.ChallengeSecretFields)

	res, err = signIn(t, p, env, d, "keys", credential.Answer{"accessKeyId": testAKID, "secretAccessKey": testSecret})
	tok := mustToken(t, res, err)
	if akid, sess := signedWith(t, tok); akid != testAKID || sess != "" {
		t.Errorf("signed with %q %q instead of the keys the user typed", akid, sess)
	}
	// Saved data of another account does not mix with the machine either.
	res, err = p.Begin(t.Context(), env, credential.Request{Descriptor: d, Subject: subjectUser(), Saved: res.Save})
	if akid, _ := signedWith(t, mustToken(t, res, err)); akid != testAKID {
		t.Errorf("signed with %q", akid)
	}

	if imds.Load() != 0 {
		t.Errorf("the metadata service was asked %d times", imds.Load())
	}
	for _, h := range f.hosts {
		if h != "sts.us-east-1.amazonaws.com" {
			t.Errorf("a request to %q: only the endpoint of the region may be asked", h)
		}
	}
	for _, body := range f.bodies {
		for _, c := range []string{"MACHINECANARY", "WORKPROFILECANARY", "ENVIRONMENTCANARY", "envSecret", "machineSecret", "envSession"} {
			if strings.Contains(body, c) {
				t.Errorf("a credential of the machine was sent to AWS (%s)", c)
			}
		}
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("files were written: %v", ents)
	}
	if b, _ := os.ReadFile(credFile); string(b) != creds {
		t.Error("the credentials file was changed")
	}
}
