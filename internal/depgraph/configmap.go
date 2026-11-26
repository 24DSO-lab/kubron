// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"
	"slices"

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

// ReasonOptionalAbsent — причина вершины и ребра для ConfigMap, которого нет, но на который Pod ссылается с optional: true.
const ReasonOptionalAbsent = "optional_absent"

// expandConfigMaps связывает Pod с ConfigMap'ами, на которые он ссылается (ADR-0031 §6, этап 2): ребро mounts
// Pod → ConfigMap. refs — имя → optional. Проверяется только существование: объект читается одним Get по имени
// (никогда List), а data, binaryData и managedFields отбрасываются сразу после чтения (addObject → SanitizeConfigMap).
//
// Число чтений ограничено общим пределом MaxReads (ADR-0031 §3): когда он исчерпан, вершина получает Unknown/limit,
// а не Missing — «не знаем» не равно «нет».
//
// Отсутствующий ConfigMap с optional: true — не поломка: ребро ok (причина optional_absent). Если на него больше никто
// не ссылается обязательно, вершина тоже не ошибка (Healthy, не заглушка): иначе несколько Pod'ов с optional-ссылкой
// выглядели бы как общая сломанная зависимость. Обязательная ссылка любого Pod'а возвращает вершине вид заглушки Missing,
// а optional-рёбра остаются ok.
func (b *builder) expandConfigMaps(ctx context.Context, pod Node, refs map[string]bool) {
	for _, name := range sortedKeys(refs) {
		cn, obj := b.get(ctx, specConfigMap, pod.Namespace, name)
		absent := obj == nil && cn.Placeholder
		switch {
		case absent && refs[name]:
			b.optionalAbsentMount(pod.Ref, cn)
		case absent:
			if _, ok := b.g.nodes[cn.Key()]; ok {
				b.g.nodes[cn.Key()] = cn // AddNode не заменяет вершину заглушкой: возвращаем вид Missing явно
			}
			b.edge(pod.Ref, cn.Ref, EdgeMounts)
		default:
			b.edge(pod.Ref, cn.Ref, EdgeMounts)
		}
	}
}

// optionalAbsentMount добавляет ребро optional-ссылки на отсутствующий ConfigMap.
func (b *builder) optionalAbsentMount(from Ref, cn Node) {
	if _, ok := b.g.nodes[cn.Key()]; !ok {
		return // вершина не поместилась в предел графа: ребра без вершины не бывает
	}
	required := slices.ContainsFunc(b.g.EdgesTo(cn.Ref, EdgeMounts), func(e Edge) bool { return !e.Optional })
	if !required {
		cn.Placeholder, cn.Health, cn.Reason = false, Healthy, ReasonOptionalAbsent
		b.g.nodes[cn.Key()] = cn
	}
	b.edge(from, cn.Ref, EdgeMounts)
	k := edgeKey{from.Key(), cn.Key(), EdgeMounts}
	if e, ok := b.g.edges[k]; ok {
		e.Optional, e.Reason = true, ReasonOptionalAbsent
		b.g.edges[k] = e
	}
}
