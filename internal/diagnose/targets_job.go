// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"
	"slices"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Цель Job (ADR-0031 §4, §6, этап 3). Базовый сбор: сам Job, его самые новые Pod'ы (чтобы к ним применялись правила
// Pod'ов: OOM, crash loop, image pull) и события. Причина падения Pod'а здесь не пересобирается. Остальное приходит из
// графа (хук сбора 100): Job → Pod'ы и, при подъёме, владелец CronJob.

const groupBatch = "batch"

const (
	maxBatchPods = 10 // ADR-0031 §3: сколько последних Pod'ов Job попадает в контекст (как в depgraph)
	maxBatchJobs = 10 // ADR-0031 §3: сколько последних Job'ов CronJob читается (как в depgraph)
)

var (
	gvkJob     = schema.GroupVersionKind{Group: groupBatch, Version: "v1", Kind: "Job"}
	gvkCronJob = schema.GroupVersionKind{Group: groupBatch, Version: "v1", Kind: "CronJob"}
)

func init() { RegisterTarget("Job", collectJob) }

func collectJob(ctx context.Context, r Reader, target ResourceRef, o CollectOptions) (*Context, error) {
	if target.Namespace == "" {
		return nil, fmt.Errorf("target namespace is empty: %w", ErrNotFound)
	}
	target.Group = groupBatch // граф различает виды по группе; вызывающий мог её не указать
	col := newCollector(r, target, o)
	job, err := primary[batchv1.Job](ctx, col, gvkJob, target.Namespace, target.Name)
	if err != nil {
		return nil, err
	}
	col.c.Target.UID = string(job.UID)
	col.listJobPods(ctx, job)
	if err := col.readCommon(ctx); err != nil {
		return nil, err
	}
	return col.c, nil
}

// jobPodSelector — селектор Pod'ов Job: spec.selector (его выставляет контроллер), иначе метка с именем Job.
func jobPodSelector(job *batchv1.Job) string {
	if s := selectorString(job.Spec.Selector); s != "" {
		return s
	}
	return "batch.kubernetes.io/job-name=" + job.Name
}

// listJobPods читает Pod'ы Job одним List namespace и оставляет принадлежащие ему по ownerReferences.
// Срок хранения Pod'ов мог истечь: пустой результат — «Pod'ов нет в кластере», а не «их не было».
func (col *collector) listJobPods(ctx context.Context, job *batchv1.Job) {
	col.listPodsWhere(ctx, ListOptions{Namespace: job.Namespace, LabelSelector: jobPodSelector(job), Limit: maxListObjects},
		func(p *corev1.Pod) bool {
			o := controllerOf(p.OwnerReferences, "Job")
			return o != nil && o.Name == job.Name
		})
	col.keepNewestPods()
}

// keepNewestPods оставляет самые новые Pod'ы (равные по времени — по имени): порядок детерминирован, предел —
// maxBatchPods; сверх него выборка помечена Partial.
func (col *collector) keepNewestPods() {
	pods := col.c.Pods
	slices.SortStableFunc(pods, func(a, b *corev1.Pod) int {
		if c := b.CreationTimestamp.Time.Compare(a.CreationTimestamp.Time); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(pods) > maxBatchPods {
		col.c.Pods = pods[:maxBatchPods]
		col.c.addPartial("Pod")
	}
}
