// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

var update = flag.Bool("update", false, "обновить золотые файлы в testdata")

// golden сравнивает JSON снимка побайтно с testdata/<n>.golden.json. Любое изменение правил требует
// correlation.Version++ и go test ./internal/correlation -update.
func golden(t *testing.T, name string, res *correlation.Result) {
	t.Helper()
	got, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("нет золотого файла (запустите с -update): %v", err)
	}
	if string(want) != string(got) {
		t.Fatalf("снимок не совпал с %s (при намеренной смене правил: Version++ и -update):\n%s", path, got)
	}
}

// E2E-2 (README промптов): 12 Pod'ов на узле с MemoryPressure, владельцы разные.
func TestGoldenSharedNodeHigh(t *testing.T) {
	w := fourDeployments("n1", 20)
	w.node("n1", false, "MemoryPressure=True@30")
	res, _ := run(t, w, "prod", "api-hapi-0")
	if b := res.Best(); b == nil || (b.Confidence != diagnose.ConfidenceMedium && b.Confidence != diagnose.ConfidenceHigh) {
		t.Fatalf("уверенность ниже medium: %+v", b)
	}
	golden(t, "shared_node_high", res)
}

func TestGoldenSharedNodeLow(t *testing.T) {
	w := fourDeployments("n1", 20)
	w.node("n1", false)
	res, _ := run(t, w, "prod", "api-hapi-0")
	golden(t, "shared_node_low", res)
}

func TestGoldenSharedStorage(t *testing.T) {
	w := &world{}
	w.add("apiVersion: storage.k8s.io/v1\nkind: StorageClass\nmetadata: {name: fast}\nprovisioner: kubernetes.io/no-provisioner")
	for i, d := range []string{"db", "queue", "search"} {
		w.add("apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: data-%s, namespace: prod}\nspec: {storageClassName: fast}\nstatus: {phase: Pending}", d)
		w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: %s-0, namespace: prod}\nspec:\n volumes: [{name: v, persistentVolumeClaim: {claimName: data-%s}}]\nstatus:\n phase: Pending\n conditions:\n - {type: PodScheduled, status: \"False\", lastTransitionTime: \"%s\"}", d, d, ts(20+i))
		w.add("apiVersion: v1\nkind: Event\nmetadata: {name: ev-%s, namespace: prod}\nreason: ProvisioningFailed\ninvolvedObject: {kind: PersistentVolumeClaim, namespace: prod, name: data-%s}\nlastTimestamp: \"%s\"", d, d, ts(19+i))
	}
	res, _ := run(t, w, "prod", "db-0")
	golden(t, "shared_storage_high", res)
}

func TestTruncatedList(t *testing.T) {
	w := &world{}
	for i := 0; i < 501; i++ { // больше podListLimit
		w.barePod("prod", fmt.Sprintf("p%03d", i), "n1", 20)
	}
	w.node("n1", false, "Ready=False@30")
	res, _ := run(t, w, "prod", "p000")
	b := res.Best()
	if b == nil || !b.Truncated {
		t.Fatalf("усечение списка не отражено: %+v", b)
	}
	if b.Total > 500 {
		t.Fatalf("Total=%d больше прочитанного (500): нельзя заявлять полноту", b.Total)
	}
	if len(res.Partial) == 0 {
		t.Fatal("усечение не отражено в Partial")
	}
}

func TestNoSecretReads(t *testing.T) {
	w := fourDeployments("n1", 20)
	w.node("n1", false, "MemoryPressure=True@30")
	f := w.reader(t)
	c := diagCtx(t, f, "prod", "api-hapi-0")
	before := len(f.CallLog())
	if _, err := correlation.Run(context.Background(), reader{f}, c, now); err != nil {
		t.Fatal(err)
	}
	log := f.CallLog()[before:]
	if len(log) == 0 {
		t.Fatal("журнал чтений пуст: проверка бессмысленна")
	}
	for _, call := range log {
		if strings.Contains(strings.ToLower(call), "secret") {
			t.Fatalf("обращение к Secret: %s", call)
		}
	}
}
