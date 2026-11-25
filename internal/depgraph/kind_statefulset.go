// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
)

// StatefulSet в графе (ADR-0031 §6, этап 3): цель расследования и звено цепочки. Расширитель добавляет Pod'ы по
// ordinal (owns) и PVC, которые контроллер создаёт по volumeClaimTemplates (owns, имя <шаблон>-<sts>-<ordinal>).
//
// ControllerRevision не читаются (ADR-0031 §6): о ревизиях судим по status.currentRevision и status.updateRevision.

// Пределы вида: решения реализации внутри пределов ADR-0031 §3, сами пределы графа не меняются.
const maxStatefulSetClaims = 30 // PVC шаблонов в графе: шаблон × ordinal; сверх — граф усечён (limit.nodes)

func init() {
	// StatefulSet перечислен среди встроенных видов (kinds.go) как владелец Pod'ов, а реестр отказывается принимать
	// встроенный вид. Переводим его в расширения здесь, в собственном файле: общие таблицы не правятся. Чтение,
	// владельцев и здоровье по числу готовых реплик (health.go) это не меняет.
	allSpecs = slices.DeleteFunc(allSpecs, func(s kindSpec) bool { return s == specStatefulSet })
	registerKind(kindExt{Spec: specStatefulSet, Seedable: true})
	registerExpander("StatefulSet", expandStatefulSet)
}

// podAt — Pod StatefulSet с его ordinal.
type podAt struct {
	ordinal int
	node    Node
}

// ordinalOf разбирает имя Pod'а StatefulSet: <sts>-<ordinal>. Ordinal — десятичное число без ведущих нулей.
func ordinalOf(pod, sts string) (int, bool) {
	rest, ok := strings.CutPrefix(pod, sts+"-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 || strconv.Itoa(n) != rest {
		return 0, false
	}
	return n, true
}

// claimNameOf — имя PVC, которое контроллер даёт шаблону template для Pod'а ordinal.
func claimNameOf(template, sts string, ordinal int) string {
	return template + "-" + sts + "-" + strconv.Itoa(ordinal)
}

// statefulSetPartition — spec.updateStrategy.rollingUpdate.partition (0, если не задан).
func statefulSetPartition(sts *appsv1.StatefulSet) int32 {
	if ru := sts.Spec.UpdateStrategy.RollingUpdate; ru != nil && ru.Partition != nil {
		return *ru.Partition
	}
	return 0
}

func expandStatefulSet(ctx context.Context, b *builder, it item, n Node) {
	var sts appsv1.StatefulSet
	if !decode(n.Object, &sts) {
		return
	}
	refineStatefulSetHealth(b, n, &sts)
	if !it.down {
		return // StatefulSet как владелец Pod'а: Pod'ы и PVC раскрывает сам Pod
	}
	d := it.depth + 1
	sel, ok := selectorOf(n.Object)
	if !ok || !b.reach(d) {
		return
	}
	res, err := b.list(ctx, specPod, ListOptions{Namespace: n.Namespace, LabelSelector: sel.String()})
	if err != nil {
		return
	}
	var pods []podAt
	for _, p := range sortedItems(res.Items) {
		ord, ok := ordinalOf(p.GetName(), n.Name)
		if !ok || !ownedBy(&p, "StatefulSet", n.Name) {
			continue
		}
		pn, ok := b.addObject(specPod, &p)
		if !ok {
			return
		}
		b.edge(n.Ref, pn.Ref, EdgeOwns)
		pods = append(pods, podAt{ord, pn})
	}
	slices.SortFunc(pods, func(a, c podAt) int { return cmp.Compare(a.ordinal, c.ordinal) })
	expandStatefulSetClaims(ctx, b, n, &sts, pods, d)

	// Дальше раскрываются Pod'ы, которых больше всего стоит объяснить: неготовые, затем по ordinal.
	order := slices.Clone(pods)
	slices.SortStableFunc(order, func(a, c podAt) int {
		return cmp.Compare(rank(a.node.Health == Healthy), rank(c.node.Health == Healthy))
	})
	for _, p := range order {
		if b.takePod() {
			b.enqueue(p.node.Ref, d, true)
		}
	}
}

func rank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// expandStatefulSetClaims добавляет PVC, созданные по volumeClaimTemplates, одним List namespace. Ожидаются PVC
// ordinal'ов, которые уже созданы или должны существовать при текущем числе реплик. Отсутствие PVC — заглушка
// Missing, но только для ordinal'а с существующим Pod'ом: у ещё не созданного Pod'а PVC может законно не быть.
func expandStatefulSetClaims(ctx context.Context, b *builder, n Node, sts *appsv1.StatefulSet, pods []podAt, d int) {
	templates := make([]string, 0, len(sts.Spec.VolumeClaimTemplates))
	for _, t := range sts.Spec.VolumeClaimTemplates {
		if t.Name != "" {
			templates = append(templates, t.Name)
		}
	}
	slices.Sort(templates)
	if len(templates) == 0 || !b.reach(d) {
		return
	}
	hasPod := map[int]bool{}
	ordinals := map[int]bool{}
	for _, p := range pods {
		hasPod[p.ordinal], ordinals[p.ordinal] = true, true
	}
	for o := range int(ptrInt(sts.Spec.Replicas, 1)) {
		ordinals[o] = true
	}
	res, err := b.list(ctx, specPVC, ListOptions{Namespace: n.Namespace})
	if err != nil {
		return
	}
	byName := map[string]int{}
	for i := range res.Items {
		byName[res.Items[i].GetName()] = i
	}
	ordinalList := slices.Sorted(maps.Keys(ordinals))
	count := 0
	for _, t := range templates {
		for _, o := range ordinalList {
			if count++; count > maxStatefulSetClaims {
				b.g.SetTruncated(warnNodes)
				return
			}
			name := claimNameOf(t, n.Name, o)
			if i, found := byName[name]; found {
				pn, ok := b.addObject(specPVC, &res.Items[i])
				if !ok {
					return
				}
				b.edge(n.Ref, pn.Ref, EdgeOwns)
				b.enqueue(pn.Ref, d, false)
				continue
			}
			if res.Truncated || !hasPod[o] {
				continue
			}
			miss := specPVC.ref(n.Namespace, name)
			b.gets[miss.Key()] = ErrNotFound // Pod'у этот PVC читать повторно не нужно
			if b.putNode(b.failureNode(miss, ErrNotFound)) {
				b.edge(n.Ref, miss, EdgeOwns)
			}
		}
	}
}

// refineStatefulSetHealth уточняет здоровье по ревизиям: идущее обновление — Progressing, а не Healthy. Обновление,
// остановленное partition, и стратегия OnDelete не уточняются: там расхождение ревизий намеренное, о нём скажет
// правило sts.update_partitioned, а не состояние вершины.
func refineStatefulSetHealth(b *builder, n Node, sts *appsv1.StatefulSet) {
	if n.Health != Healthy || sts.Spec.UpdateStrategy.Type == appsv1.OnDeleteStatefulSetStrategyType ||
		statefulSetPartition(sts) > 0 {
		return
	}
	st := sts.Status
	if st.CurrentRevision != "" && st.UpdateRevision != "" && st.CurrentRevision != st.UpdateRevision &&
		st.UpdatedReplicas < ptrInt(sts.Spec.Replicas, 1) {
		b.setHealth(n.Ref, Progressing, ReasonRolloutInProgress)
	}
}
