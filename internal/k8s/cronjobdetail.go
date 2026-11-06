// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

type CronJobDetail struct {
	Name          string            `json:"name"`
	Namespace     string            `json:"namespace"`
	Schedule      string            `json:"schedule"`
	Suspend       bool              `json:"suspend"`
	Active        int               `json:"active"`
	LastSchedule  string            `json:"lastSchedule"`
	NextExecution string            `json:"nextExecution"`
	TimeZone      string            `json:"timeZone"`
	Age           string            `json:"age"`
	Labels        map[string]string `json:"labels"`
	Annotations   map[string]string `json:"annotations"`
}

func (m *Manager) GetCronJobDetail(namespace, name string) (*CronJobDetail, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	cj, err := conn.Client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	lastSchedule := "—"
	if cj.Status.LastScheduleTime != nil {
		lastSchedule = formatAge(cj.Status.LastScheduleTime.Time)
	}

	suspend := false
	if cj.Spec.Suspend != nil {
		suspend = *cj.Spec.Suspend
	}

	nextExecution := "—"
	if !suspend && cj.Spec.Schedule != "" {
		nextExecution = computeNextExecution(cj.Spec.Schedule, cj.Spec.TimeZone)
	}

	timeZone := "—"
	if cj.Spec.TimeZone != nil && *cj.Spec.TimeZone != "" {
		timeZone = *cj.Spec.TimeZone
	}

	return &CronJobDetail{
		Name:          cj.Name,
		Namespace:     cj.Namespace,
		Schedule:      cj.Spec.Schedule,
		Suspend:       suspend,
		Active:        len(cj.Status.Active),
		LastSchedule:  lastSchedule,
		NextExecution: nextExecution,
		TimeZone:      timeZone,
		Age:           formatAge(cj.CreationTimestamp.Time),
		Labels:        cj.Labels,
		Annotations:   cj.Annotations,
	}, nil
}

// TriggerCronJob создаёт Job из CronJob вручную (аналог kubectl create job --from=cronjob/...).
func (m *Manager) TriggerCronJob(namespace, name string) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	cj, err := conn.Client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Формируем имя Job'а: cronjob-name-manual-_timestamp
	jobName := fmt.Sprintf("%s-manual-%d", name, time.Now().Unix())

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName,
			Namespace:   namespace,
			Labels:      cj.Spec.JobTemplate.Labels,
			Annotations: cj.Spec.JobTemplate.Annotations,
		},
		Spec: cj.Spec.JobTemplate.Spec,
	}

	_, err = conn.Client.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	return err
}

// ToggleCronJobSuspend переключает поле suspend CronJob.
func (m *Manager) ToggleCronJobSuspend(namespace, name string) (bool, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	cj, err := conn.Client.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}

	newSuspend := true
	if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
		newSuspend = false
	}

	cj.Spec.Suspend = ptr.To(newSuspend)
	_, err = conn.Client.BatchV1().CronJobs(namespace).Update(ctx, cj, metav1.UpdateOptions{})
	if err != nil {
		return false, err
	}
	return newSuspend, nil
}
