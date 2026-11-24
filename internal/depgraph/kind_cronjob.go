// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CronJob (ADR-0031 §6, этап 3). CronJob — вид графа и цель расследования: от него вниз идут рёбра owns к его Job'ам,
// а Job раскрывается до Pod'ов расширением kind_job.go. История Job'ов ограничена: читается один List Job'ов namespace,
// в граф попадают самые новые maxCronJobJobs, а вниз раскрывается только последний неудавшийся Job (CronJob → Job → Pod):
// остальные Job'ы нужны правилам по своим условиям и времени, а не по Pod'ам. Расписание, время и значения полей
// в граф не попадают: их читает правило из объекта вершины.

// Решения реализации, не меняющие пределы графа ADR-0031 §3.
const (
	maxCronJobJobs = 10                   // ADR-0031 §3: сколько последних Job'ов CronJob попадает в граф
	warnCronJobs   = "limit.cronjob_jobs" // у CronJob Job'ов больше предела: старые в граф не попали
)

func init() {
	markSeedable(specCronJob)
	registerExpander("CronJob", expandCronJob)
}

// expandCronJob: CronJob → его Job'ы (owns). Принадлежность определяют ownerReferences: метки Job'ов задаёт шаблон
// пользователя и надёжным признаком быть не могут. Усечённая выборка Job'ов (list_truncated:Job) и превышение предела
// помечают граф усечённым: вывод «неудавшихся запусков нет» по нему делать нельзя.
func expandCronJob(ctx context.Context, b *builder, it item, n Node) {
	if !it.down {
		return
	}
	d := it.depth + 1
	if !b.reach(d) {
		return
	}
	res, err := b.list(ctx, specJob, ListOptions{Namespace: n.Namespace})
	if err != nil {
		return
	}
	var mine []unstructured.Unstructured
	for _, j := range res.Items {
		if ownedBy(&j, "CronJob", n.Name) {
			mine = append(mine, j)
		}
	}
	mine = newestFirst(mine)
	if len(mine) > maxCronJobJobs {
		mine = mine[:maxCronJobJobs]
		b.g.SetTruncated(warnCronJobs)
	}
	failedTaken := false
	for i := range mine {
		jn, ok := b.addObject(specJob, &mine[i])
		if !ok {
			return
		}
		b.edge(n.Ref, jn.Ref, EdgeOwns)
		if !failedTaken && jn.Health == Degraded {
			failedTaken = true // самый новый из неудавшихся: порядок выборки — от новых к старым
			b.enqueue(jn.Ref, d, true)
		}
	}
}
