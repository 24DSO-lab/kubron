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

// ReasonOptionalAbsent — причина вершины и ребра для ConfigMap, которого нет, но на который Pod ссылается с optional: true.
const ReasonOptionalAbsent = "optional_absent"

// cmUse — как на отсутствующий ConfigMap ссылаются Pod'ы, раскрытые в этом построении графа.
type cmUse struct{ required bool }

// expandConfigMaps связывает Pod с ConfigMap'ами, на которые он ссылается (ADR-0031 §6, этап 2): ребро mounts
// Pod → ConfigMap. refs — имя → optional (true, только если ВСЕ ссылки этого Pod'а на имя необязательные: collectRefs).
// Проверяется только существование: объект читается одним Get по имени (никогда List), а data, binaryData и
// managedFields отбрасываются сразу после чтения (addObject → SanitizeConfigMap).
//
// Число чтений ограничено общим пределом MaxReads (ADR-0031 §3): когда он исчерпан, вершина получает Unknown/limit,
// а не Missing — «не знаем» не равно «нет». Отрицательный ответ (ErrNotFound) кэшируется: один ConfigMap читается один
// раз, сколько бы Pod'ов на него ни ссылалось.
//
// Отсутствующий ConfigMap с optional: true — не поломка: ребро ok (причина optional_absent). Итог для вершины
// не зависит от порядка обхода Pod'ов: пока граф строится, отсутствующий ConfigMap всегда остаётся заглушкой Missing,
// а факт «на него ссылались обязательно» только запоминается; решение принимает resolveAbsentConfigMaps при
// завершении. Поэтому сначала optional, потом required — то же самое, что наоборот.
func (b *builder) expandConfigMaps(ctx context.Context, pod Node, refs map[string]bool) {
	for _, name := range sortedKeys(refs) {
		cn, obj := b.get(ctx, specConfigMap, pod.Namespace, name)
		b.edge(pod.Ref, cn.Ref, EdgeMounts)
		if obj != nil || !cn.Placeholder {
			continue // существует или «не знаем» (Unknown): optional здесь ничего не меняет
		}
		if b.cmUse == nil {
			b.cmUse = map[string]*cmUse{}
		}
		use := b.cmUse[cn.Key()]
		if use == nil {
			use = &cmUse{}
			b.cmUse[cn.Key()] = use
		}
		if !refs[name] {
			use.required = true // даже если ребро не поместилось в предел графа: ссылка была
			continue
		}
		k := edgeKey{pod.Key(), cn.Key(), EdgeMounts}
		if e, ok := b.g.edges[k]; ok {
			e.Optional = true // состояние (ok, optional_absent) вычислит finalize по итоговой вершине
			b.g.edges[k] = e
		}
	}
}

// resolveAbsentConfigMaps вызывается один раз при завершении построения. Отсутствующий ConfigMap, на который никто не
// ссылался обязательно, не выглядит сломанной зависимостью: Healthy с причиной optional_absent и без признака
// заглушки. Это не утверждение, что объект есть: Object пуст, а причина прямо говорит, что объекта нет и это допустимо.
// Любая обязательная ссылка (в том числе от Pod'а, ребро которого не поместилось в предел графа) оставляет заглушку Missing.
func (b *builder) resolveAbsentConfigMaps() {
	for key, use := range b.cmUse {
		n, ok := b.g.nodes[key]
		if !ok || use.required || !n.Placeholder || n.Object != nil {
			continue
		}
		n.Placeholder, n.Health, n.Reason = false, Healthy, ReasonOptionalAbsent
		b.g.nodes[key] = n
	}
}
