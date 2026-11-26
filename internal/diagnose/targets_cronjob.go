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
)

// Цель CronJob (ADR-0031 §4, §6, этап 3). Базовый сбор: сам CronJob, Pod'ы его последнего неудавшегося Job (чтобы к
// ним применялись правила Pod'ов) и события. История Job'ов ограничена последними maxBatchJobs: старые в отчёт не
// попадают, а усечение помечено Partial.

func init() { RegisterTarget("CronJob", collectCronJob) }

func collectCronJob(ctx context.Context, r Reader, target ResourceRef, o CollectOptions) (*Context, error) {
	if target.Namespace == "" {
		return nil, fmt.Errorf("target namespace is empty: %w", ErrNotFound)
	}
	target.Group = groupBatch
	col := newCollector(r, target, o)
	cj, err := primary[batchv1.CronJob](ctx, col, gvkCronJob, target.Namespace, target.Name)
	if err != nil {
		return nil, err
	}
	col.c.Target.UID = string(cj.UID)
	col.listLastFailedJobPods(ctx, cj)
	if err := col.readCommon(ctx); err != nil {
		return nil, err
	}
	return col.c, nil
}

// listLastFailedJobPods: CronJob → последний неудавшийся Job → его Pod'ы. Принадлежность Job'а определяют
// ownerReferences. Отказ чтения Job'ов — пробел источника, а не «неудавшихся Job'ов нет».
func (col *collector) listLastFailedJobPods(ctx context.Context, cj *batchv1.CronJob) {
	ns := cj.Namespace
	res, err := col.r.List(ctx, gvkJob, ListOptions{Namespace: ns, Limit: maxListObjects})
	if err != nil {
		col.c.addMissing("source.unavailable", ResourceRef{Group: groupBatch, Kind: "Job", Namespace: ns}, map[string]string{"reason": errorReason(err)})
		return
	}
	if res.Truncated {
		col.c.addPartial("Job")
	}
	var jobs []*batchv1.Job
	for i := range res.Items {
		j, err := decode[batchv1.Job](&res.Items[i])
		if err != nil {
			col.c.addMissing("object.unreadable", ResourceRef{Group: groupBatch, Kind: "Job", Namespace: ns, Name: res.Items[i].GetName()}, nil)
			continue
		}
		if o := controllerOf(j.OwnerReferences, "CronJob"); o != nil && o.Name == cj.Name {
			jobs = append(jobs, j)
		}
	}
	slices.SortStableFunc(jobs, func(a, b *batchv1.Job) int {
		if c := b.CreationTimestamp.Compare(a.CreationTimestamp.Time); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(jobs) > maxBatchJobs {
		jobs = jobs[:maxBatchJobs]
		col.c.addPartial("Job")
	}
	i := slices.IndexFunc(jobs, func(j *batchv1.Job) bool { return jobHasCondition(j, batchv1.JobFailed) })
	if i < 0 {
		return
	}
	failed := jobs[i]
	col.listPodsWhere(ctx, ListOptions{Namespace: ns, LabelSelector: jobPodSelector(failed), Limit: maxListObjects},
		func(p *corev1.Pod) bool {
			o := controllerOf(p.OwnerReferences, "Job")
			return o != nil && o.Name == failed.Name
		})
	col.keepNewestPods()
}

// jobHasCondition — у Job есть условие t со статусом True.
func jobHasCondition(j *batchv1.Job, t batchv1.JobConditionType) bool {
	_, ok := jobCondition(j, t)
	return ok
}

// jobCondition возвращает условие Job по типу, если оно истинно.
func jobCondition(j *batchv1.Job, t batchv1.JobConditionType) (batchv1.JobCondition, bool) {
	for _, c := range j.Status.Conditions {
		if c.Type == t && c.Status == corev1.ConditionTrue {
			return c, true
		}
	}
	return batchv1.JobCondition{}, false
}
