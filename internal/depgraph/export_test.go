// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// InstallFakeKind подключает через реестр вымышленный вид example.io/Widget и расширение вершин Pod, чтобы
// проверить механизм реестра без реальных видов этапов 2–3. Состояние Widget с меткой broken=true — Degraded.
// Расширение Pod читает Widget по аннотации test/widget и связывает его ребром scales. Откат — в t.Cleanup.
func InstallFakeKind(t *testing.T) {
	t.Helper()
	kinds, exps := append([]kindExt(nil), extKinds...), map[string][]expander{}
	for k, v := range expanders {
		exps[k] = append([]expander(nil), v...)
	}
	t.Cleanup(func() { extKinds, expanders = kinds, exps })

	widget := kindSpec{schema.GroupVersionKind{Group: "example.io", Version: "v1", Kind: "Widget"}, true}
	registerKind(kindExt{Spec: widget, Seedable: true, Health: func(o *unstructured.Unstructured) (Health, string) {
		if o.GetLabels()["broken"] == "true" {
			return Degraded, "widget_broken"
		}
		return Healthy, ""
	}})
	registerExpander("Pod", func(ctx context.Context, b *builder, it item, n Node) {
		name := n.Object.GetAnnotations()["test/widget"]
		if name == "" || !b.reach(it.depth+1) {
			return
		}
		w, _ := b.get(ctx, widget, n.Namespace, name)
		b.edge(w.Ref, n.Ref, EdgeScales)
	})
}
