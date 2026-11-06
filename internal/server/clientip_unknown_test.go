// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http/httptest"
	"testing"
)

// Пустой адрес клиента отключил бы блокировку по адресу (auth.Service.ipKey): клиент без адреса получает общий ключ.
func TestClientIPIsNeverEmpty(t *testing.T) {
	r := httptest.NewRequest("POST", "/rpc", nil)
	r.RemoteAddr = ""

	for name, g := range map[string]*Guard{"nil guard": nil, "zero guard": {}} {
		if got := g.clientIP(r); got != unknownClient {
			t.Errorf("%s: clientIP = %q, want %q", name, got, unknownClient)
		}
	}

	r.RemoteAddr = "192.0.2.10:4000"
	if got := (&Guard{}).clientIP(r); got != "192.0.2.10" {
		t.Errorf("a known address must stay as is: %q", got)
	}
}
