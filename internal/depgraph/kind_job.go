// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Job (ADR-0031 §6, этап 3). Job — вид графа и цель расследования: от него вниз идут рёбра owns к его Pod'ам. Состояние
// самого Job (Failed, Complete, Suspended) выводит встроенная таблица health.go, а причину падения Pod'а граф не
// пересобирает: её объясняют правила Pod'ов (OOM, crash loop, image pull), см. rules_chain_job.go.
//
// Job и CronJob уже встроены в таблицу видов (kinds.go), поэтому registerKind для них недопустим (он отвергает встроенные
// виды). Целью расследования вид делает markSeedable: запись в реестре расширений без собственного вывода состояния.
// Файл kinds.go заморожен (README второй итерации), а ждать правки общего файла разработчики вида не должны.

// Решения реализации, не меняющие пределы графа ADR-0031 §3.
const (
	maxJobPods  = 10               // ADR-0031 §3: сколько последних Pod'ов одного Job попадает в граф
	warnJobPods = "limit.job_pods" // у Job Pod'ов больше предела: старые в граф не попали

	labelJobName = "batch.kubernetes.io/job-name" // метка Pod'ов Job (Kubernetes 1.27+)
)

func init() {
	markSeedable(specJob)
	registerExpander("Job", expandJob)
}

// markSeedable разрешает строить граф от встроенного вида spec. Повторный вызов безвреден. Вид остаётся встроенным:
// его чтение, состояние и родителей по-прежнему задают allSpecs, HealthOf и expandOwners.
func markSeedable(spec kindSpec) {
	if slices.ContainsFunc(extKinds, func(e kindExt) bool { return e.Spec.gvk == spec.gvk }) {
		return
	}
	extKinds = append(extKinds, kindExt{Spec: spec, Seedable: true})
}

// JobPodSelector возвращает селектор Pod'ов Job: spec.selector (его выставляет контроллер Job), иначе метка с именем
// Job. Пустая строка — отобрать Pod'ы нечем (имя пустое).
func JobPodSelector(job *unstructured.Unstructured) string {
	if job == nil {
		return ""
	}
	if sel, ok := selectorOf(job); ok {
		return sel.String()
	}
	if job.GetName() == "" {
		return ""
	}
	return labelJobName + "=" + job.GetName()
}

// newestFirst упорядочивает объекты от новых к старым; равные по времени — по имени, чтобы порядок был детерминирован.
func newestFirst(items []unstructured.Unstructured) []unstructured.Unstructured {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b unstructured.Unstructured) int {
		return cmp.Or(
			b.GetCreationTimestamp().Time.Compare(a.GetCreationTimestamp().Time),
			strings.Compare(a.GetName(), b.GetName()))
	})
	return out
}

// expandJob: Job → его Pod'ы (owns). Читается один List Pod'ов namespace по селектору Job; в граф попадают самые новые
// maxJobPods. Расширение работает только «вниз»: при подъёме от Pod'а к Job соседей Job граф не раскрывает.
// Приостановленный Job (spec.suspend) без условия Suspended получает состояние Suspended: это факт, не проблема.
func expandJob(ctx context.Context, b *builder, it item, n Node) {
	if n.Health == Progressing && boolField(n.Object, "spec", "suspend") {
		b.setHealth(n.Ref, Suspended, ReasonSuspended)
	}
	if !it.down {
		return
	}
	d := it.depth + 1
	sel := JobPodSelector(n.Object)
	if sel == "" || !b.reach(d) {
		return
	}
	res, err := b.list(ctx, specPod, ListOptions{Namespace: n.Namespace, LabelSelector: sel})
	if err != nil {
		return
	}
	var mine []unstructured.Unstructured
	for _, p := range res.Items {
		if ownedBy(&p, "Job", n.Name) {
			mine = append(mine, p)
		}
	}
	mine = newestFirst(mine)
	if len(mine) > maxJobPods {
		mine = mine[:maxJobPods]
		b.g.SetTruncated(warnJobPods)
	}
	for i := range mine {
		pn, ok := b.addObject(specPod, &mine[i])
		if !ok {
			return
		}
		b.edge(n.Ref, pn.Ref, EdgeOwns)
		if b.takePod() {
			b.enqueue(pn.Ref, d, true)
		}
	}
}

// boolField читает булево поле объекта; нет поля или другой тип — false.
func boolField(obj *unstructured.Unstructured, path ...string) bool {
	v, found, err := unstructured.NestedBool(obj.Object, path...)
	return found && err == nil && v
}
