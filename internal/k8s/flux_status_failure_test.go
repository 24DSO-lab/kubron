// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// Временный сбой discovery не должен прятать Flux на весь fluxDiscoveryTTL: иначе повторная загрузка страницы
// (в e2e это retry теста) получает тот же кэшированный «не установлен».
func TestFluxStatusFailureIsCachedBriefly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newFluxDiscovery(fluxFullInstall())
		d.err, d.dropData = errors.New("context deadline exceeded"), true
		m, _ := fluxStatusManager(d)
		read := func() *FluxStatus {
			t.Helper()
			st, err := m.GetFluxStatus()
			if err != nil {
				t.Fatal(err)
			}
			return st
		}

		if read().Installed {
			t.Fatal("a failed discovery must report Flux as not installed")
		}
		read()
		if n := d.calls.Load(); n != 1 {
			t.Fatalf("within the failure TTL: discovery calls = %d, want 1", n)
		}

		// Кластер ответил: раздел появляется сразу после короткого TTL сбоя, а не через fluxDiscoveryTTL.
		d.err = nil
		time.Sleep(fluxDiscoveryFailureTTL + time.Second)
		if st := read(); !st.Installed || st.State != FluxStatusOK {
			t.Fatalf("Flux must be visible once discovery recovers: %+v", st)
		}
		if n := d.calls.Load(); n != 2 {
			t.Fatalf("after the failure TTL: discovery calls = %d, want 2", n)
		}

		// Успешный результат по-прежнему живёт fluxDiscoveryTTL.
		time.Sleep(fluxDiscoveryTTL - 2*time.Second)
		read()
		if n := d.calls.Load(); n != 2 {
			t.Fatalf("successful result must keep the long TTL: discovery calls = %d, want 2", n)
		}
	})
}
