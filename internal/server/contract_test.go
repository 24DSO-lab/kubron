// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"reflect"
	"sort"
	"testing"

	"github.com/24DSO-labs/kubron/internal/app"
)

// Каждый экспортируемый метод app.App обязан быть либо в allowlist, либо явно в списке запрещённых:
// так новый метод не попадает в веб незамеченным, а решение принимается осознанно.
func TestEveryAppMethodIsClassified(t *testing.T) {
	allowed := map[string]bool{}
	for _, n := range allowedMethods {
		if allowed[n] {
			t.Errorf("method %s is in the allowlist twice", n)
		}
		allowed[n] = true
	}
	for n := range deniedMethods {
		if allowed[n] {
			t.Errorf("method %s is in both the allowlist and the denied list", n)
		}
	}

	typ := reflect.TypeOf(&app.App{})
	var unclassified []string
	exists := map[string]bool{}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		exists[name] = true
		if _, denied := deniedMethods[name]; !allowed[name] && !denied {
			unclassified = append(unclassified, name)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("App methods without a decision (add to allowedMethods or deniedMethods in methods.go): %v", unclassified)
	}
	for n := range allowed {
		if !exists[n] {
			t.Errorf("the allowlist has method %s which App does not have (stale entry)", n)
		}
	}
	for n := range deniedMethods {
		if !exists[n] {
			t.Errorf("the denied list has method %s which App does not have (stale entry)", n)
		}
	}
}

// Сигнатуры всех разрешённых методов поддерживаются диспетчером (не variadic, ≤ 2 результатов).
func TestAppDispatcherBuilds(t *testing.T) {
	d, err := NewAppDispatcher(app.NewApp(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(d.Methods()); got != len(allowedMethods) {
		t.Errorf("the dispatcher has %d methods, the allowlist has %d", got, len(allowedMethods))
	}
}

// До входа публичные методы работают через RPC, остальные отказывают — без паники.
func TestAppRPCWithoutLogin(t *testing.T) {
	d, err := NewAppDispatcher(app.NewApp(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Call("GetClusterStatuses", nil); err != nil {
		t.Fatalf("GetClusterStatuses without login returns an empty list, not an error: %v", err)
	}
	if _, err := d.Call("GetNamespaces", nil); err == nil {
		t.Fatal("GetNamespaces without login must return an access error")
	}
	caps, err := d.Call("GetCapabilities", nil)
	if err != nil || caps == nil {
		t.Fatalf("GetCapabilities is public: %v %v", caps, err)
	}
}
