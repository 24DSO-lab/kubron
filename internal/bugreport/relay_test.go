// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package bugreport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testID = "0123456789abcdef0123456789abcdef"

func sampleReport(t *testing.T) Report {
	t.Helper()
	r := Report{
		Description: "the grid is empty",
		Contact:     "me@example.org",
		Screenshot:  pngBytes(t, 4, 4),
		Diagnostics: Diagnostics{AppVersion: "1.2.3", Shell: "web", User: "alice", OS: "linux", Arch: "amd64", Locale: "ru", View: "pods"},
	}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewIDIsValidAndUnique(t *testing.T) {
	a, err := NewID()
	if err != nil || !ValidID(a) {
		t.Fatalf("got %q, %v", a, err)
	}
	if b, _ := NewID(); a == b {
		t.Fatal("two ids are equal")
	}
	for _, bad := range []string{"", "xyz", strings.ToUpper(testID), testID + "0", "../" + testID[:29]} {
		if ValidID(bad) {
			t.Errorf("%q must not be valid", bad)
		}
	}
}

func TestResolveRelayURL(t *testing.T) {
	cases := []struct {
		raw, def, want string
		err            bool
	}{
		{"", DefaultRelayURL, DefaultRelayURL, false},
		{"", "", "", false}, // the web server: no relay unless the administrator names one
		{"off", DefaultRelayURL, "", false},
		{" OFF ", DefaultRelayURL, "", false},
		{"https://relay.example.org/api/bugreport", DefaultRelayURL, "https://relay.example.org/api/bugreport", false},
		{"http://127.0.0.1:8080/r", "", "http://127.0.0.1:8080/r", false},
		{"http://localhost/r", "", "http://localhost/r", false},
		{"http://relay.example.org/r", "", "", true}, // a screenshot is never sent in clear text over a network
		{"https://user:pw@relay.example.org/r", "", "", true},
		{"ftp://relay.example.org/r", "", "", true},
		{"https://", "", "", true},
		{"https://relay.example.org/r#frag", "", "", true},
	}
	for _, c := range cases {
		got, err := ResolveRelayURL(c.raw, c.def)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("ResolveRelayURL(%q, %q) = %q, %v; want %q, err=%v", c.raw, c.def, got, err, c.want, c.err)
		}
		if err != nil && !errors.Is(err, ErrRelayURLInvalid) {
			t.Errorf("wrong error %v", err)
		}
	}
}

func TestSendRelayPostsTheReportWithoutTheLogin(t *testing.T) {
	var got relayPayload
	var ctype, agent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctype, agent = req.Header.Get("Content-Type"), req.Header.Get("User-Agent")
		if req.Method != http.MethodPost {
			t.Errorf("method %s", req.Method)
		}
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	r := sampleReport(t)
	if err := SendRelay(context.Background(), nil, srv.URL, testID, r); err != nil {
		t.Fatal(err)
	}
	if ctype != "application/json" || agent != "Kubron/1.2.3" {
		t.Errorf("headers: %q, %q", ctype, agent)
	}
	if got.ID != testID || got.Description != r.Description || got.Contact != r.Contact {
		t.Errorf("payload: %+v", got)
	}
	if png, err := base64.StdEncoding.DecodeString(got.Screenshot); err != nil || string(png) != string(r.Screenshot) {
		t.Errorf("screenshot differs (%v)", err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "alice") {
		t.Error("the login of the web user must not leave the organization")
	}
	if got.Diagnostics.View != "pods" || got.Diagnostics.OS != "linux" {
		t.Errorf("diagnostics: %+v", got.Diagnostics)
	}
}

func TestSendRelayWithoutScreenshotOmitsIt(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&raw)
	}))
	defer srv.Close()
	r := sampleReport(t)
	r.Screenshot = nil
	if err := SendRelay(context.Background(), nil, srv.URL, testID, r); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["screenshot"]; ok {
		t.Error("an empty screenshot must be left out")
	}
}

func TestSendRelayClassifiesTheReply(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{200, nil}, {201, nil}, {204, nil},
		{400, ErrRelayRejected}, {403, ErrRelayRejected}, {413, ErrRelayRejected}, {422, ErrRelayRejected},
		{429, ErrRelayUnavailable}, {408, ErrRelayUnavailable}, {500, ErrRelayUnavailable}, {502, ErrRelayUnavailable}, {503, ErrRelayUnavailable},
		{302, ErrRelayRejected}, // a redirect is not followed
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if c.status == 302 {
				w.Header().Set("Location", "http://127.0.0.1:1/elsewhere")
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte("secret server text"))
		}))
		err := SendRelay(context.Background(), nil, srv.URL, testID, sampleReport(t))
		srv.Close()
		if !errors.Is(err, c.want) && !(c.want == nil && err == nil) {
			t.Errorf("status %d: got %v, want %v", c.status, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "secret server text") {
			t.Errorf("status %d: the reply of the server reached the error", c.status)
		}
	}
}

func TestSendRelayUnreachableIsTemporary(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens there now
	if err := SendRelay(context.Background(), nil, url, testID, sampleReport(t)); !errors.Is(err, ErrRelayUnavailable) {
		t.Fatalf("got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SendRelay(ctx, nil, url, testID, sampleReport(t)); !errors.Is(err, ErrRelayUnavailable) {
		t.Fatalf("a cancelled context: got %v", err)
	}
}

func TestSendRelayRefusesABadID(t *testing.T) {
	if err := SendRelay(context.Background(), nil, "http://127.0.0.1:1", "../x", sampleReport(t)); !errors.Is(err, ErrRelayRejected) {
		t.Fatalf("got %v", err)
	}
}
