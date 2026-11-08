// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

const notifFixtures = "testdata/flux/notification"

func notifReadFixture(t testing.TB, name string) *unstructured.Unstructured {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(notifFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	obj := map[string]any{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func notifAllFixtures(t testing.TB) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(notifFixtures, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", notifFixtures, err)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	return names
}

// notifNewDyn — fake dynamic client, знающий Provider и Alert в v1beta3 и Receiver в v1.
func notifNewDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{}
	for _, k := range fluxKindsOf(fluxCtrlNotification) {
		lists[k.GVR(k.Versions[0])] = k.Kind + "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
}

func notifObjs(t testing.TB, files ...string) []runtime.Object {
	t.Helper()
	var objs []runtime.Object
	for _, f := range files {
		objs = append(objs, notifReadFixture(t, f))
	}
	return objs
}

func notifFailList(dyn *dynamicfake.FakeDynamicClient, plural string, err error) {
	dyn.PrependReactor("list", plural, func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, err })
}

func TestParseFluxProviderFixtures(t *testing.T) {
	cases := []struct {
		file    string
		want    FluxProviderInfo
		message string
	}{
		{
			// Токен в пути и в query отбрасывается, остаются схема и хост.
			file: "v2.8-provider-slack.yaml",
			want: FluxProviderInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindProvider, Name: "slack", Namespace: "flux-system", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Type:           "slack", Channel: "general", Address: "https://hooks.slack.com", SecretRef: "slack-url",
			},
			message: "Initialized",
		},
		{
			// userinfo и путь отбрасываются, порт остаётся частью хоста.
			file: "v2.8-provider-userinfo.yaml",
			want: FluxProviderInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindProvider, Name: "generic", Namespace: "team-a", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Type:           "generic", Address: "https://hooks.example.com:8443",
			},
		},
		{
			file: "v2.8-provider-git.yaml",
			want: FluxProviderInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindProvider, Name: "github", Namespace: "team-a", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Type:           "github", Address: "https://github.com", SecretRef: "github-token",
			},
		},
		{
			file: "v2.8-provider-suspended.yaml",
			want: FluxProviderInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindProvider, Name: "paused", Namespace: "team-a", Suspended: true, State: FluxStateSuspended, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Type:           "discord", Address: "https://discord.com", SecretRef: "discord",
			},
		},
		{
			// Адрес без схемы и хоста не показывается совсем: по обрывку пути нельзя поручиться, что в нём нет токена.
			file: "v2.8-provider-bad-address.yaml",
			want: FluxProviderInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindProvider, Name: "relative", Namespace: "team-a", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Type:           "generic", Address: "",
			},
		},
		{
			file: "v2.8-provider-failed.yaml",
			want: FluxProviderInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindProvider, Name: "broken", Namespace: "team-a", State: FluxStateFailed, LastReconcileAt: "2026-11-07T10:05:00Z"},
				Type:           "slack", Address: "https://hooks.slack.com", SecretRef: "missing",
			},
			message: "not found",
		},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			got := parseFluxProvider(notifReadFixture(t, tc.file))
			w := tc.want
			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace {
				t.Errorf("identity = %s %s/%s, want %s %s/%s", got.Kind, got.Namespace, got.Name, w.Kind, w.Namespace, w.Name)
			}
			if got.State != w.State || got.Suspended != w.Suspended || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("state/suspended/time = %q/%v/%q, want %q/%v/%q", got.State, got.Suspended, got.LastReconcileAt, w.State, w.Suspended, w.LastReconcileAt)
			}
			if got.Type != w.Type || got.Channel != w.Channel || got.Address != w.Address || got.SecretRef != w.SecretRef {
				t.Errorf("type/channel/address/secretRef = %q/%q/%q/%q, want %q/%q/%q/%q",
					got.Type, got.Channel, got.Address, got.SecretRef, w.Type, w.Channel, w.Address, w.SecretRef)
			}
			if tc.message != "" && !strings.Contains(got.Message, tc.message) {
				t.Errorf("message = %q, want to contain %q", got.Message, tc.message)
			}
			if got.Age == "" || got.Created == "" || got.Annotations["team"] != "platform" {
				t.Errorf("age/created/annotations: %q/%q/%v", got.Age, got.Created, got.Annotations)
			}
			if got.Conditions == nil || got.DependsOn == nil || got.Labels == nil {
				t.Errorf("nil collections: %+v", got)
			}
		})
	}
	for _, f := range notifAllFixtures(t) {
		if strings.Contains(f, "provider") && !covered[f] {
			t.Errorf("fixture %s has no expectations in the table", f)
		}
	}
}

func TestParseFluxAlertFixtures(t *testing.T) {
	cases := []struct {
		file string
		want FluxAlertInfo
	}{
		{
			// Namespace источника по умолчанию — namespace самого Alert; имя «*» сохраняется; matchLabels не отдаются.
			file: "v2.8-alert.yaml",
			want: FluxAlertInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindAlert, Name: "on-call", Namespace: "flux-system", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				ProviderRef:    "slack", EventSeverity: "error",
				EventSources: []FluxRef{
					{Kind: "Kustomization", Namespace: "flux-system", Name: "*"},
					{Kind: "HelmRelease", Namespace: "apps", Name: "podinfo"},
					{Kind: "GitRepository", Namespace: "flux-system", Name: "flux-system"},
				},
			},
		},
		{
			// eventSeverity не задан: по документации значение по умолчанию — info.
			file: "v2.8-alert-info.yaml",
			want: FluxAlertInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindAlert, Name: "all-info", Namespace: "team-a", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				ProviderRef:    "generic", EventSeverity: "info",
				EventSources: []FluxRef{{Kind: "Kustomization", Namespace: "team-a", Name: "apps"}},
			},
		},
		{
			file: "v2.8-alert-suspended.yaml",
			want: FluxAlertInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindAlert, Name: "paused", Namespace: "team-a", Suspended: true, State: FluxStateSuspended, LastReconcileAt: "2026-11-05T09:00:03Z"},
				ProviderRef:    "slack", EventSeverity: "info",
				EventSources: []FluxRef{{Kind: "HelmRelease", Namespace: "team-a", Name: "*"}},
			},
		},
		{
			file: "v2.3-alert.yaml",
			want: FluxAlertInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindAlert, Name: "legacy", Namespace: "flux-system", State: FluxStateReady, LastReconcileAt: "2024-06-12T07:05:00Z"},
				ProviderRef:    "slack", EventSeverity: "info",
				EventSources: []FluxRef{{Kind: "Kustomization", Namespace: "flux-system", Name: "flux-system"}},
			},
		},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			got := parseFluxAlert(notifReadFixture(t, tc.file))
			w := tc.want
			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace || got.State != w.State ||
				got.Suspended != w.Suspended || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("object = %+v, want %+v", got.FluxObjectInfo, w.FluxObjectInfo)
			}
			if got.ProviderRef != w.ProviderRef || got.EventSeverity != w.EventSeverity {
				t.Errorf("providerRef/severity = %q/%q, want %q/%q", got.ProviderRef, got.EventSeverity, w.ProviderRef, w.EventSeverity)
			}
			if !slices.Equal(got.EventSources, w.EventSources) {
				t.Errorf("eventSources = %+v, want %+v", got.EventSources, w.EventSources)
			}
		})
	}
	for _, f := range notifAllFixtures(t) {
		if strings.Contains(f, "alert") && !covered[f] {
			t.Errorf("fixture %s has no expectations in the table", f)
		}
	}
}

func TestParseFluxReceiverFixtures(t *testing.T) {
	cases := []struct {
		file string
		want FluxReceiverInfo
	}{
		{
			file: "v2.8-receiver.yaml",
			want: FluxReceiverInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindReceiver, Name: "github", Namespace: "flux-system", State: FluxStateReady, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Type:           "github", SecretRef: "webhook-token",
				Resources: []FluxRef{
					{Kind: "GitRepository", Namespace: "flux-system", Name: "flux-system"},
					{Kind: "HelmRepository", Namespace: "apps", Name: "*"},
				},
			},
		},
		{
			file: "v2.8-receiver-suspended.yaml",
			want: FluxReceiverInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindReceiver, Name: "paused", Namespace: "team-a", Suspended: true, State: FluxStateSuspended, LastReconcileAt: "2026-11-05T09:00:03Z"},
				Type:           "generic", SecretRef: "generic-token",
				Resources: []FluxRef{{Kind: "Bucket", Namespace: "team-a", Name: "assets"}},
			},
		},
		{
			// Flux 2.3: Receiver в v1beta2, поля те же.
			file: "v2.3-receiver-v1beta2.yaml",
			want: FluxReceiverInfo{
				FluxObjectInfo: FluxObjectInfo{Kind: FluxKindReceiver, Name: "gitlab", Namespace: "flux-system", State: FluxStateReady, LastReconcileAt: "2024-06-12T07:05:00Z"},
				Type:           "gitlab", SecretRef: "gitlab-token",
				Resources: []FluxRef{{Kind: "GitRepository", Namespace: "flux-system", Name: "flux-system"}},
			},
		},
	}
	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.file] = true
		t.Run(tc.file, func(t *testing.T) {
			got := parseFluxReceiver(notifReadFixture(t, tc.file))
			w := tc.want
			if got.Kind != w.Kind || got.Name != w.Name || got.Namespace != w.Namespace || got.State != w.State ||
				got.Suspended != w.Suspended || got.LastReconcileAt != w.LastReconcileAt {
				t.Errorf("object = %+v, want %+v", got.FluxObjectInfo, w.FluxObjectInfo)
			}
			if got.Type != w.Type || got.SecretRef != w.SecretRef {
				t.Errorf("type/secretRef = %q/%q, want %q/%q", got.Type, got.SecretRef, w.Type, w.SecretRef)
			}
			if !slices.Equal(got.Resources, w.Resources) {
				t.Errorf("resources = %+v, want %+v", got.Resources, w.Resources)
			}
			// Путь вебхука не должен просочиться ни в сообщение условия Ready, где контроллер его печатает.
			if strings.Contains(got.Message, "S3CRET") || strings.Contains(got.Message, "/hook/") {
				t.Errorf("webhook path in the message: %q", got.Message)
			}
		})
	}
	for _, f := range notifAllFixtures(t) {
		if strings.Contains(f, "receiver") && !covered[f] {
			t.Errorf("fixture %s has no expectations in the table", f)
		}
	}
}

func TestFluxNotificationAddress(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"token in path", "https://hooks.slack.com/services/T0/B0/S3CRET-TOKEN", "https://hooks.slack.com"},
		{"query token", "https://example.com/hook?token=S3CRET-QUERY", "https://example.com"},
		{"fragment", "https://example.com/#S3CRET-FRAG", "https://example.com"},
		{"userinfo", "https://user:S3CRET-URLPASS@example.com/x", "https://example.com"},
		{"token as user", "https://S3CRET-TOKEN@example.com", "https://example.com"},
		{"port stays", "http://example.com:8080/x", "http://example.com:8080"},
		{"ipv6 host", "https://[::1]:9000/x/S3CRET-P", "https://[::1]:9000"},
		{"no scheme", "hooks.slack.com/services/S3CRET-TOKEN", ""},
		{"scheme-less with credentials", "user:S3CRET-URLPASS@example.com", ""},
		{"relative path", "/relative/S3CRET-PATH", ""},
		{"scheme without host", "https:///S3CRET-PATH", ""},
		{"unparsable", "https://exa mple.com/S3CRET-X", ""},
		{"opaque", "mailto:someone@example.com", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fluxAddressHost(tc.in)
			if got != tc.want {
				t.Errorf("fluxAddressHost(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "S3CRET") {
				t.Errorf("secret leaked: %q", got)
			}
		})
	}
}

func TestParseFluxNotificationToleratesGarbage(t *testing.T) {
	bad := func(spec, status any) *unstructured.Unstructured {
		o := map[string]any{"metadata": map[string]any{"name": "g", "namespace": "ns"}}
		if spec != nil {
			o["spec"] = spec
		}
		if status != nil {
			o["status"] = status
		}
		return &unstructured.Unstructured{Object: o}
	}
	objs := map[string]*unstructured.Unstructured{
		"nil object":            {},
		"spec is a string":      bad("oops", "oops"),
		"status is a list":      bad(map[string]any{}, []any{1}),
		"address is a number":   bad(map[string]any{"address": 5}, nil),
		"secretRef is a list":   bad(map[string]any{"secretRef": []any{"x"}}, nil),
		"providerRef garbage":   bad(map[string]any{"providerRef": "slack"}, nil),
		"severity wrong type":   bad(map[string]any{"eventSeverity": []any{}}, nil),
		"eventSources garbage":  bad(map[string]any{"eventSources": []any{nil, 1, "x", map[string]any{"name": 3}, map[string]any{}}}, nil),
		"eventSources a string": bad(map[string]any{"eventSources": "all"}, nil),
		"resources garbage":     bad(map[string]any{"resources": []any{nil, 1, "x", map[string]any{"kind": 3}}}, nil),
		"resources a map":       bad(map[string]any{"resources": map[string]any{"kind": "x"}}, nil),
		"conditions garbage":    bad(nil, map[string]any{"conditions": []any{"x", 1, map[string]any{"type": 5}}}),
		"webhookPath wrong":     bad(nil, map[string]any{"webhookPath": []any{"x"}}),
	}
	parsers := map[string]func(*unstructured.Unstructured) FluxObjectInfo{
		"Provider": func(u *unstructured.Unstructured) FluxObjectInfo { return parseFluxProvider(u).FluxObjectInfo },
		"Alert":    func(u *unstructured.Unstructured) FluxObjectInfo { return parseFluxAlert(u).FluxObjectInfo },
		"Receiver": func(u *unstructured.Unstructured) FluxObjectInfo { return parseFluxReceiver(u).FluxObjectInfo },
	}
	for kind, parse := range parsers {
		for name, u := range objs {
			t.Run(kind+"/"+name, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic: %v", r)
					}
				}()
				got := parse(u)
				if got.Conditions == nil || got.DependsOn == nil || got.Labels == nil || got.Annotations == nil {
					t.Errorf("nil collections: %+v", got)
				}
				if _, err := json.Marshal(got); err != nil {
					t.Errorf("json: %v", err)
				}
			})
		}
	}
	// Списки не nil даже у пустого объекта: в JSON должны быть [], а не null.
	alert, _ := json.Marshal(parseFluxAlert(&unstructured.Unstructured{}))
	recv, _ := json.Marshal(parseFluxReceiver(&unstructured.Unstructured{}))
	if strings.Contains(string(alert), `"eventSources":null`) || strings.Contains(string(recv), `"resources":null`) {
		t.Errorf("null instead of []: %s %s", alert, recv)
	}
}

func TestFluxNotificationKindsToRead(t *testing.T) {
	names := func(v map[string]string) []string {
		var out []string
		for _, k := range fluxNotificationKindsToRead(v) {
			out = append(out, k.Kind+"/"+v[k.Kind])
		}
		return out
	}
	if got, want := names(map[string]string{FluxKindProvider: "v1beta3", FluxKindAlert: "v1beta3", FluxKindReceiver: "v1"}),
		[]string{"Alert/v1beta3", "Provider/v1beta3", "Receiver/v1"}; !slices.Equal(got, want) {
		t.Errorf("full: %v, want %v", got, want)
	}
	// Receiver в v1beta2 (Flux 2.3) — в allowlist.
	if got, want := names(map[string]string{FluxKindProvider: "v1beta3", FluxKindReceiver: "v1beta2"}),
		[]string{"Provider/v1beta3", "Receiver/v1beta2"}; !slices.Equal(got, want) {
		t.Errorf("v1beta2 receiver: %v, want %v", got, want)
	}
	// Alert только в v1beta2 (старый Flux) или без CRD — пропускается.
	if got := names(map[string]string{FluxKindAlert: "v1beta2", FluxKindKustomization: "v1"}); len(got) != 0 {
		t.Errorf("unsupported or foreign kinds: %v, want none", got)
	}
}

func TestGetFluxNotifications(t *testing.T) {
	files := []string{
		"v2.8-provider-slack.yaml", "v2.8-provider-git.yaml", "v2.8-provider-userinfo.yaml",
		"v2.8-alert.yaml", "v2.8-alert-info.yaml", "v2.8-receiver.yaml", "v2.8-receiver-suspended.yaml",
	}
	m := fluxManager(notifNewDyn(notifObjs(t, files...)...))

	got, err := m.GetFluxNotifications(nil)
	if err != nil {
		t.Fatal(err)
	}
	key := func(ns, name string) string { return ns + "/" + name }
	var providers, alerts, receivers []string
	for _, p := range got.Providers {
		providers = append(providers, key(p.Namespace, p.Name))
	}
	for _, a := range got.Alerts {
		alerts = append(alerts, key(a.Namespace, a.Name))
	}
	for _, r := range got.Receivers {
		receivers = append(receivers, key(r.Namespace, r.Name))
	}
	// Порядок: namespace, затем имя.
	if want := []string{"flux-system/slack", "team-a/generic", "team-a/github"}; !slices.Equal(providers, want) {
		t.Errorf("providers = %v, want %v", providers, want)
	}
	if want := []string{"flux-system/on-call", "team-a/all-info"}; !slices.Equal(alerts, want) {
		t.Errorf("alerts = %v, want %v", alerts, want)
	}
	if want := []string{"flux-system/github", "team-a/paused"}; !slices.Equal(receivers, want) {
		t.Errorf("receivers = %v, want %v", receivers, want)
	}

	scoped, err := m.GetFluxNotifications([]string{"team-a"})
	if err != nil || len(scoped.Providers) != 2 || len(scoped.Alerts) != 1 || len(scoped.Receivers) != 1 {
		t.Fatalf("team-a: err=%v %+v", err, scoped)
	}

	empty, err := m.GetFluxNotifications([]string{"nothing"})
	if err != nil || empty == nil || empty.Providers == nil || empty.Alerts == nil || empty.Receivers == nil {
		t.Fatalf("empty scope must give non-nil empty lists: err=%v %#v", err, empty)
	}

	if _, err := m.GetFluxNotifications([]string{"a/b"}); err == nil || strings.Contains(err.Error(), "\n") {
		t.Errorf("an invalid scope must be one error, got %v", err)
	}
}

func TestGetFluxNotificationsPartialFailure(t *testing.T) {
	boom := errors.New("the server is currently unable to handle the request")
	files := []string{"v2.8-provider-slack.yaml", "v2.8-alert.yaml", "v2.8-receiver.yaml"}
	gr := func(res string) schema.GroupResource {
		return schema.GroupResource{Group: fluxGroupNotification, Resource: res}
	}

	t.Run("one kind failed: the others are returned without an error", func(t *testing.T) {
		dyn := notifNewDyn(notifObjs(t, files...)...)
		notifFailList(dyn, "alerts", boom)
		got, err := fluxManager(dyn).GetFluxNotifications(nil)
		if err != nil {
			t.Fatalf("a partial failure must not fail the call: %v", err)
		}
		if len(got.Providers) != 1 || len(got.Receivers) != 1 || len(got.Alerts) != 0 || got.Alerts == nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("all kinds failed: an error naming them", func(t *testing.T) {
		dyn := notifNewDyn(notifObjs(t, files...)...)
		for _, p := range []string{"providers", "alerts", "receivers"} {
			notifFailList(dyn, p, boom)
		}
		got, err := fluxManager(dyn).GetFluxNotifications(nil)
		if err == nil || got != nil || !errors.Is(err, boom) {
			t.Fatalf("err=%v got=%v", err, got)
		}
		for _, k := range []string{FluxKindProvider, FluxKindAlert, FluxKindReceiver} {
			if !strings.Contains(err.Error(), k) {
				t.Errorf("error does not name %s: %v", k, err)
			}
		}
	})
	t.Run("no permission or no CRD is skipped without an error", func(t *testing.T) {
		dyn := notifNewDyn(notifObjs(t, files...)...)
		notifFailList(dyn, "receivers", apierrors.NewForbidden(gr("receivers"), "", errors.New("denied")))
		notifFailList(dyn, "alerts", apierrors.NewNotFound(gr("alerts"), ""))
		got, err := fluxManager(dyn).GetFluxNotifications(nil)
		if err != nil || len(got.Providers) != 1 || len(got.Alerts) != 0 || len(got.Receivers) != 0 {
			t.Fatalf("err=%v got=%+v", err, got)
		}
	})
	t.Run("everything forbidden: empty, no error", func(t *testing.T) {
		dyn := notifNewDyn()
		for _, p := range []string{"providers", "alerts", "receivers"} {
			notifFailList(dyn, p, apierrors.NewForbidden(gr(p), "", errors.New("denied")))
		}
		got, err := fluxManager(dyn).GetFluxNotifications(nil)
		if err != nil || got == nil || got.Providers == nil {
			t.Fatalf("err=%v got=%#v", err, got)
		}
	})
	t.Run("a real failure is not hidden by skipped kinds", func(t *testing.T) {
		dyn := notifNewDyn()
		notifFailList(dyn, "providers", boom)
		notifFailList(dyn, "alerts", apierrors.NewForbidden(gr("alerts"), "", errors.New("denied")))
		notifFailList(dyn, "receivers", apierrors.NewForbidden(gr("receivers"), "", errors.New("denied")))
		if _, err := fluxManager(dyn).GetFluxNotifications(nil); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the real failure", err)
		}
	})
}

// notifSecretMarkers — контрольные строки запрещённых мест.
var notifSecretMarkers = []string{
	"S3CRET-WEBHOOKTOKEN", "S3CRET-QUERY", "S3CRET-PROXYPASS", "S3CRET-CERTREF", "S3CRET-URLPASS", "S3CRET-PATH",
	"S3CRET-DISCORD", "S3CRET-REL", "S3CRET-MSGPASS", "S3CRET-LASTAPPLIED", "S3CRET-WEBHOOKPATH",
	"S3CRET-MATCHLABEL", "S3CRET-RECVLABEL", "S3CRET-INCLUDE", "S3CRET-EXCLUDE", "S3CRET-SUMMARY",
}

// TestFluxNotificationNoSecretLeak — по образцу TestArgoSettingsNoSecretLeak: запрещённые поля заполнены
// контрольными строками (токен в пути адреса Provider, userinfo, webhookPath Receiver), ни одна не должна попасть
// в json.Marshal ответа. Новое поле DTO добавляется только через этот тест.
func TestFluxNotificationNoSecretLeak(t *testing.T) {
	// 1) Все фикстуры. Receiver v1beta2 (Flux 2.3) в список v1 не попадает, как и в кластере: он разобран в
	// TestParseFluxReceiverFixtures, в том числе вырезание его пути вебхука из сообщения.
	var objs []runtime.Object
	for _, f := range notifAllFixtures(t) {
		objs = append(objs, notifReadFixture(t, f))
	}
	// 2) Receiver, у которого путь вебхука попал и в сообщение условия, и в метку; Provider с токеном в сообщении.
	recv := fluxObj("notification.toolkit.fluxcd.io/v1", "Receiver", "team", "evil-recv", map[string]any{
		"type": "generic", "secretRef": map[string]any{"name": "tok"},
		"resources": []any{map[string]any{"kind": "GitRepository", "name": "x"}},
	})
	recv.Object["status"] = map[string]any{
		"webhookPath": "/hook/S3CRET-WEBHOOKPATH-EVIL",
		"conditions": []any{map[string]any{
			"type": "Ready", "status": "True", "reason": "Succeeded", "lastTransitionTime": "2026-11-07T10:00:00Z",
			"message": "Receiver initialized for path: /hook/S3CRET-WEBHOOKPATH-EVIL",
		}},
	}
	recv.SetAnnotations(map[string]string{fluxLastAppliedAnnotation: `{"x":"S3CRET-LASTAPPLIED"}`, "keep": "me"})
	objs = append(objs, recv)

	m := fluxManager(notifNewDyn(objs...))
	got, err := m.GetFluxNotifications(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 6 || len(got.Alerts) != 4 || len(got.Receivers) != 3 {
		t.Fatalf("got %d providers, %d alerts, %d receivers", len(got.Providers), len(got.Alerts), len(got.Receivers))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	all := string(raw)
	for _, marker := range notifSecretMarkers {
		if strings.Contains(all, marker) {
			t.Errorf("secret %q leaked into the response: %s", marker, all)
		}
	}
	if strings.Contains(all, "S3CRET") {
		t.Errorf("a control string leaked into the response: %s", all)
	}
	if strings.Contains(all, "webhookPath") || strings.Contains(all, "/hook/") {
		t.Errorf("the webhook path or a value derived from it leaked: %s", all)
	}
	for _, r := range got.Receivers {
		if r.Name == "evil-recv" {
			if _, ok := r.Annotations[fluxLastAppliedAnnotation]; ok || r.Annotations["keep"] != "me" {
				t.Errorf("annotations = %v", r.Annotations)
			}
		}
	}
}
