// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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
