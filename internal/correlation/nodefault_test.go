// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation_test

import (
	"testing"

	"github.com/24DSO-lab/kubron/internal/correlation"
	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// claimWorld — три Pod'а в ожидании, каждый с PVC; classLine — строка spec PVC про класс ("" — поле не задано).
func claimWorld(extra, classLine, phase string) *world {
	w := &world{}
	if extra != "" {
		w.add("%s", extra)
	}
	for i, d := range []string{"db", "queue", "search"} {
		w.add("apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: data-%s, namespace: prod}\nspec: {%s}\nstatus: {phase: %s}", d, classLine, phase)
		w.add("apiVersion: v1\nkind: Pod\nmetadata: {name: %s-0, namespace: prod}\nspec:\n volumes: [{name: v, persistentVolumeClaim: {claimName: data-%s}}]\nstatus:\n phase: Pending\n conditions:\n - {type: PodScheduled, status: \"False\", lastTransitionTime: \"%s\"}", d, d, ts(20+i))
	}
	return w
}

func TestNoDefaultStorageClass(t *testing.T) {
	// Класса по умолчанию нет вовсе: PVC без storageClassName не привязаны.
	res, _ := run(t, claimWorld("", "volumeMode: Filesystem", "Pending"), "prod", "db-0")
	b := res.Best()
	if b == nil || b.Kind != correlation.KindSharedStorage || b.Total != 3 || b.Confidence != diagnose.ConfidenceMedium {
		t.Fatalf("кандидат: %+v", b)
	}
	if got, want := b.Key(), "shared_storage:storage.k8s.io/StorageClass//"; got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
	var found bool
	for _, f := range res.Facts {
		found = found || f.Code == "correlation.shared_storage.no_default_class"
	}
	if !found {
		t.Fatalf("нет факта no_default_class: %+v", res.Facts)
	}
	golden(t, "shared_storage_no_default", res)
}

func TestNoDefaultClassNotReportedWhenNotApplicable(t *testing.T) {
	const nonDefault = "apiVersion: storage.k8s.io/v1\nkind: StorageClass\nmetadata: {name: slow}\nprovisioner: csi.example/x"
	tests := []struct {
		name, class, phase string
	}{
		{"PVC привязаны", "volumeMode: Filesystem", "Bound"},
		{"явно пустой класс — статическая привязка", "storageClassName: \"\"", "Pending"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, _ := run(t, claimWorld(nonDefault, tt.class, tt.phase), "prod", "db-0")
			if b := res.Best(); b != nil {
				t.Fatalf("неожиданный кандидат: %+v", *b)
			}
		})
	}
}

// Класс по умолчанию есть: PVC без storageClassName относятся к нему, а не к «безымянному» источнику.
func TestClaimsWithoutClassBelongToDefaultClass(t *testing.T) {
	const def = "apiVersion: storage.k8s.io/v1\nkind: StorageClass\nmetadata:\n name: standard\n annotations: {storageclass.kubernetes.io/is-default-class: \"true\"}\nprovisioner: kubernetes.io/no-provisioner"
	res, _ := run(t, claimWorld(def, "volumeMode: Filesystem", "Pending"), "prod", "db-0")
	b := res.Best()
	if b == nil || b.Total != 3 {
		t.Fatalf("кандидат: %+v", b)
	}
	if got, want := b.Key(), "shared_storage:storage.k8s.io/StorageClass//standard"; got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("кандидатов %d, want 1 (источник «нет класса по умолчанию» не должен дублироваться)", len(res.Candidates))
	}
}
