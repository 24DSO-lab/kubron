// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SanitizeConfigMap отбрасывает всё, что может нести содержимое ConfigMap, до передачи объекта вызывающему коду
// (ADR-0031 §2): data, binaryData, а также аннотацию last-applied-configuration (она хранит копию манифеста целиком)
// и managedFields. Для остальных видов — без изменений.
func SanitizeConfigMap(obj *unstructured.Unstructured) {
	if obj == nil || obj.GetKind() != "ConfigMap" {
		return
	}
	unstructured.RemoveNestedField(obj.Object, "data")
	unstructured.RemoveNestedField(obj.Object, "binaryData")
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	if ann := obj.GetAnnotations(); ann != nil {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		if len(ann) == 0 {
			ann = nil
		}
		obj.SetAnnotations(ann)
	}
}

// expandConfigMaps связывает Pod с ConfigMap'ами, на которые он ссылается (ADR-0031 §6, этап 2): ребро mounts
// Pod → ConfigMap. refs — имя → optional. Проверяется только существование: объект читается одним Get по имени
// (никогда List), а data, binaryData и managedFields отбрасываются сразу после чтения (addObject → SanitizeConfigMap).
//
// Число чтений ограничено общим пределом MaxReads (ADR-0031 §3): когда он исчерпан, вершина получает Unknown/limit,
// а не Missing — «не знаем» не равно «нет».
//
// Отсутствующий ConfigMap с optional: true — не поломка: ссылка не может сломаться, поэтому такая вершина и ребро в
// граф не попадают. Исключение — ConfigMap, на который другой Pod ссылается без optional: вершина-заглушка нужна ему.
func (b *builder) expandConfigMaps(ctx context.Context, pod Node, refs map[string]bool) {
	for _, name := range sortedKeys(refs) {
		cn, obj := b.get(ctx, specConfigMap, pod.Namespace, name)
		if obj == nil && cn.Placeholder && refs[name] {
			if len(b.g.EdgesTo(cn.Ref, "")) == 0 {
				delete(b.g.nodes, cn.Key())
			}
			continue
		}
		b.edge(pod.Ref, cn.Ref, EdgeMounts)
	}
}
