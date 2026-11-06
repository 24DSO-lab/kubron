// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// === StatefulSets ===

type StatefulSetInfo struct {
	Name          string   `json:"name"`
	Namespace     string   `json:"namespace"`
	Replicas      int32    `json:"replicas"`
	ReadyReplicas int32    `json:"readyReplicas"`
	Age           string   `json:"age"`
	Images        []string `json:"images"`
	ServiceName   string   `json:"serviceName"`

	Selector        string               `json:"selector"`
	Strategy        string               `json:"strategy"`
	NodeSelector    string               `json:"nodeSelector"`
	Labels          map[string]string    `json:"labels"`
	Annotations     map[string]string    `json:"annotations"`
	Tolerations     []TolerationInfo     `json:"tolerations"`
	Affinity        *AffinityInfo        `json:"affinity"`
	PodAntiAffinity *PodAntiAffinityInfo `json:"podAntiAffinity"`
}

func (m *Manager) GetStatefulSets(namespaces []string) ([]StatefulSetInfo, error) {
	return scopedList(m, "GetStatefulSets", "apps", "statefulsets", namespaces, listStatefulSets,
		func(it StatefulSetInfo) string { return it.Namespace })
}

// listStatefulSets читает ресурсы одного namespace ("" — все) для GetStatefulSets.
func listStatefulSets(ctx context.Context, conn *ClusterConnection, namespace string) ([]StatefulSetInfo, error) {
	list, err := conn.Client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var items []StatefulSetInfo
	for _, s := range list.Items {
		var images []string
		for _, c := range s.Spec.Template.Spec.Containers {
			images = append(images, c.Image)
		}
		var replicas int32 = 1
		if s.Spec.Replicas != nil {
			replicas = *s.Spec.Replicas
		}
		selector := formatMatchLabels(s.Spec.Selector)

		// Convert tolerations
		var tolerations []TolerationInfo
		for _, tol := range s.Spec.Template.Spec.Tolerations {
			tolInfo := TolerationInfo{
				Key:      tol.Key,
				Operator: string(tol.Operator),
				Value:    tol.Value,
				Effect:   string(tol.Effect),
			}
			tolerations = append(tolerations, tolInfo)
		}

		// Extract node affinity information
		var affinity *AffinityInfo
		if s.Spec.Template.Spec.Affinity != nil && s.Spec.Template.Spec.Affinity.NodeAffinity != nil {
			na := s.Spec.Template.Spec.Affinity.NodeAffinity
			affinity = &AffinityInfo{
				NodeAffinity: &NodeAffinityInfo{},
			}

			if na.RequiredDuringSchedulingIgnoredDuringExecution != nil {
				terms := na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
				selectorTerms := make([]NodeSelectorTerm, 0)
				for _, term := range terms {
					matchExpressions := make([]MatchExpression, 0)
					for _, expr := range term.MatchExpressions {
						matchExpressions = append(matchExpressions, MatchExpression{
							Key:      expr.Key,
							Operator: string(expr.Operator),
							Values:   expr.Values,
						})
					}
					selectorTerms = append(selectorTerms, NodeSelectorTerm{
						MatchExpressions: matchExpressions,
					})
				}
				affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &NodeSelectorInfo{
					NodeSelectorTerms: selectorTerms,
				}
			}

			if na.PreferredDuringSchedulingIgnoredDuringExecution != nil {
				preferred := make([]PreferredNodeSelector, 0)
				for _, pref := range na.PreferredDuringSchedulingIgnoredDuringExecution {
					matchExpressions := make([]MatchExpression, 0)
					for _, expr := range pref.Preference.MatchExpressions {
						matchExpressions = append(matchExpressions, MatchExpression{
							Key:      expr.Key,
							Operator: string(expr.Operator),
							Values:   expr.Values,
						})
					}
					preferred = append(preferred, PreferredNodeSelector{
						Weight: pref.Weight,
						Preference: NodeSelectorTerm{
							MatchExpressions: matchExpressions,
						},
					})
				}
				affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = preferred
			}
		}

		// Build node selector string
		nodeSelectorStr := ""
		if len(s.Spec.Template.Spec.NodeSelector) > 0 {
			first := true
			for k, v := range s.Spec.Template.Spec.NodeSelector {
				if !first {
					nodeSelectorStr += ", "
				}
				nodeSelectorStr += k + "=" + v
				first = false
			}
		}

		// Build pod anti-affinity info
		var podAntiAffinity *PodAntiAffinityInfo
		if s.Spec.Template.Spec.Affinity != nil && s.Spec.Template.Spec.Affinity.PodAntiAffinity != nil {
			paa := s.Spec.Template.Spec.Affinity.PodAntiAffinity
			podAntiAffinity = &PodAntiAffinityInfo{}

			// Preferred affinities
			if paa.PreferredDuringSchedulingIgnoredDuringExecution != nil {
				for _, pref := range paa.PreferredDuringSchedulingIgnoredDuringExecution {
					term := PodAffinityTermInfo{
						TopologyKey: pref.PodAffinityTerm.TopologyKey,
						Namespaces:  pref.PodAffinityTerm.Namespaces,
					}
					if pref.PodAffinityTerm.LabelSelector != nil {
						term.LabelSelector = &LabelSelectorInfo{
							MatchLabels: pref.PodAffinityTerm.LabelSelector.MatchLabels,
						}
					}
					podAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
						podAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
						PreferredAffinityTermInfo{
							Weight:          pref.Weight,
							PodAffinityTerm: term,
						},
					)
				}
			}

			// Required affinities
			if paa.RequiredDuringSchedulingIgnoredDuringExecution != nil {
				for _, req := range paa.RequiredDuringSchedulingIgnoredDuringExecution {
					term := PodAffinityTermInfo{
						TopologyKey: req.TopologyKey,
						Namespaces:  req.Namespaces,
					}
					if req.LabelSelector != nil {
						term.LabelSelector = &LabelSelectorInfo{
							MatchLabels: req.LabelSelector.MatchLabels,
						}
					}
					podAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
						podAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
						RequiredAffinityTermInfo{
							PodAffinityTerm: term,
						},
					)
				}
			}
		}

		items = append(items, StatefulSetInfo{
			Name:            s.Name,
			Namespace:       s.Namespace,
			Replicas:        replicas,
			ReadyReplicas:   s.Status.ReadyReplicas,
			Age:             formatAge(s.CreationTimestamp.Time),
			Images:          images,
			ServiceName:     s.Spec.ServiceName,
			Selector:        selector,
			Strategy:        string(s.Spec.UpdateStrategy.Type),
			NodeSelector:    nodeSelectorStr,
			Labels:          s.Labels,
			Annotations:     s.Annotations,
			Tolerations:     tolerations,
			Affinity:        affinity,
			PodAntiAffinity: podAntiAffinity,
		})
	}
	return items, nil
}

func (m *Manager) RestartStatefulSet(namespace, name string) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	sts, err := conn.Client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if sts.Spec.Template.Annotations == nil {
		sts.Spec.Template.Annotations = make(map[string]string)
	}
	sts.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err = conn.Client.AppsV1().StatefulSets(namespace).Update(ctx, sts, metav1.UpdateOptions{})
	return err
}

// === DaemonSets ===

type NodeAffinityInfo struct {
	RequiredDuringSchedulingIgnoredDuringExecution  *NodeSelectorInfo       `json:"requiredDuringSchedulingIgnoredDuringExecution"`
	PreferredDuringSchedulingIgnoredDuringExecution []PreferredNodeSelector `json:"preferredDuringSchedulingIgnoredDuringExecution"`
}

type NodeSelectorInfo struct {
	NodeSelectorTerms []NodeSelectorTerm `json:"nodeSelectorTerms"`
}

type NodeSelectorTerm struct {
	MatchExpressions []MatchExpression `json:"matchExpressions"`
}

type PreferredNodeSelector struct {
	Weight     int32            `json:"weight"`
	Preference NodeSelectorTerm `json:"preference"`
}

type MatchExpression struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

type AffinityInfo struct {
	NodeAffinity *NodeAffinityInfo `json:"nodeAffinity"`
}

type DaemonSetInfo struct {
	Name                   string            `json:"name"`
	Namespace              string            `json:"namespace"`
	Desired                int32             `json:"desired"`
	Current                int32             `json:"current"`
	Ready                  int32             `json:"ready"`
	Available              int32             `json:"available"`
	DesiredNumberScheduled int32             `json:"desiredNumberScheduled"`
	CurrentNumberScheduled int32             `json:"currentNumberScheduled"`
	NumberReady            int32             `json:"numberReady"`
	NumberAvailable        int32             `json:"numberAvailable"`
	NumberUpdatedScheduled int32             `json:"updated"`
	Age                    string            `json:"age"`
	Created                string            `json:"created"`
	Images                 []string          `json:"images"`
	Labels                 map[string]string `json:"labels"`
	Annotations            map[string]string `json:"annotations"`
	Selector               string            `json:"selector"`
	NodeSelector           string            `json:"nodeSelector"`
	Strategy               string            `json:"strategy"`
	Conditions             []ConditionInfo   `json:"conditions"`
	Tolerations            []TolerationInfo  `json:"tolerations"`
	Affinity               *AffinityInfo     `json:"affinity"`
}

func (m *Manager) GetDaemonSets(namespaces []string) ([]DaemonSetInfo, error) {
	return scopedList(m, "GetDaemonSets", "apps", "daemonsets", namespaces, listDaemonSets,
		func(it DaemonSetInfo) string { return it.Namespace })
}

// listDaemonSets читает ресурсы одного namespace ("" — все) для GetDaemonSets.
func listDaemonSets(ctx context.Context, conn *ClusterConnection, namespace string) ([]DaemonSetInfo, error) {
	list, err := conn.Client.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var items []DaemonSetInfo
	for _, d := range list.Items {
		var images []string
		for _, c := range d.Spec.Template.Spec.Containers {
			images = append(images, c.Image)
		}
		ns := ""
		if d.Spec.Template.Spec.NodeSelector != nil {
			var parts []string
			for k, v := range d.Spec.Template.Spec.NodeSelector {
				parts = append(parts, fmt.Sprintf("%s=%s", k, v))
			}
			ns = strings.Join(parts, ", ")
		}

		// Build selector string from MatchLabels
		selectorStr := formatMatchLabels(d.Spec.Selector)

		// Convert conditions
		var conditions []ConditionInfo
		for _, cond := range d.Status.Conditions {
			conditions = append(conditions, ConditionInfo{
				Type:               string(cond.Type),
				Status:             string(cond.Status),
				LastTransitionTime: cond.LastTransitionTime.Format(time.RFC3339),
				Reason:             cond.Reason,
				Message:            cond.Message,
			})
		}

		// Convert tolerations
		var tolerations []TolerationInfo
		for _, tol := range d.Spec.Template.Spec.Tolerations {
			tolInfo := TolerationInfo{
				Key:      tol.Key,
				Operator: string(tol.Operator),
				Value:    tol.Value,
				Effect:   string(tol.Effect),
			}
			tolerations = append(tolerations, tolInfo)
		}

		// Extract node affinity information
		var affinity *AffinityInfo
		if d.Spec.Template.Spec.Affinity != nil && d.Spec.Template.Spec.Affinity.NodeAffinity != nil {
			na := d.Spec.Template.Spec.Affinity.NodeAffinity
			affinity = &AffinityInfo{
				NodeAffinity: &NodeAffinityInfo{},
			}

			// Process required node affinity
			if na.RequiredDuringSchedulingIgnoredDuringExecution != nil {
				terms := na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
				selectorTerms := make([]NodeSelectorTerm, 0)
				for _, term := range terms {
					matchExpressions := make([]MatchExpression, 0)
					for _, expr := range term.MatchExpressions {
						matchExpressions = append(matchExpressions, MatchExpression{
							Key:      expr.Key,
							Operator: string(expr.Operator),
							Values:   expr.Values,
						})
					}
					selectorTerms = append(selectorTerms, NodeSelectorTerm{
						MatchExpressions: matchExpressions,
					})
				}
				affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &NodeSelectorInfo{
					NodeSelectorTerms: selectorTerms,
				}
			}

			// Process preferred node affinity
			if na.PreferredDuringSchedulingIgnoredDuringExecution != nil {
				preferred := make([]PreferredNodeSelector, 0)
				for _, pref := range na.PreferredDuringSchedulingIgnoredDuringExecution {
					matchExpressions := make([]MatchExpression, 0)
					for _, expr := range pref.Preference.MatchExpressions {
						matchExpressions = append(matchExpressions, MatchExpression{
							Key:      expr.Key,
							Operator: string(expr.Operator),
							Values:   expr.Values,
						})
					}
					preferred = append(preferred, PreferredNodeSelector{
						Weight: pref.Weight,
						Preference: NodeSelectorTerm{
							MatchExpressions: matchExpressions,
						},
					})
				}
				affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = preferred
			}
		}

		items = append(items, DaemonSetInfo{
			Name:                   d.Name,
			Namespace:              d.Namespace,
			Desired:                d.Status.DesiredNumberScheduled,
			Current:                d.Status.CurrentNumberScheduled,
			Ready:                  d.Status.NumberReady,
			Available:              d.Status.NumberAvailable,
			DesiredNumberScheduled: d.Status.DesiredNumberScheduled,
			CurrentNumberScheduled: d.Status.CurrentNumberScheduled,
			NumberReady:            d.Status.NumberReady,
			NumberAvailable:        d.Status.NumberAvailable,
			NumberUpdatedScheduled: d.Status.UpdatedNumberScheduled,
			Age:                    formatAge(d.CreationTimestamp.Time),
			Created:                d.CreationTimestamp.Format(time.RFC3339),
			Images:                 images,
			Labels:                 d.Labels,
			Annotations:            d.Annotations,
			Selector:               selectorStr,
			NodeSelector:           ns,
			Strategy:               string(d.Spec.UpdateStrategy.Type),
			Conditions:             conditions,
			Tolerations:            tolerations,
			Affinity:               affinity,
		})
	}
	return items, nil
}

// === Jobs ===

type JobInfo struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Completions  string            `json:"completions"`
	Succeeded    int32             `json:"succeeded"`
	Failed       int32             `json:"failed"`
	Active       int32             `json:"active"`
	Parallelism  int32             `json:"parallelism"`
	Duration     string            `json:"duration"`
	Age          string            `json:"age"`
	Created      string            `json:"created"`
	Status       string            `json:"status"`
	Selector     string            `json:"selector"`
	Images       []string          `json:"images"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	Conditions   []ConditionInfo   `json:"conditions"`
	PodStatuses  map[string]int32  `json:"podStatuses"`
	BackoffLimit int32             `json:"backoffLimit"`
	ControlledBy string            `json:"controlledBy"` // "CronJob/name"
}

// jobPodStatuses: счётчики подов по фазам для каждого Job ("ns/name" -> phase -> n).
// Одним запросом на все Job'ы; ошибка не ломает список.
func jobPodStatuses(ctx context.Context, conn *ClusterConnection, namespace string) map[string]map[string]int32 {
	out := map[string]map[string]int32{}
	pods, err := conn.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out
	}
	for _, pod := range pods.Items {
		for _, ref := range pod.OwnerReferences {
			if ref.Kind != "Job" || ref.Controller == nil || !*ref.Controller {
				continue
			}
			key := pod.Namespace + "/" + ref.Name
			if out[key] == nil {
				out[key] = map[string]int32{}
			}
			out[key][string(pod.Status.Phase)]++
		}
	}
	return out
}

func convertJob(j *batchv1.Job, podStatuses map[string]int32) JobInfo {
	completions := "1"
	if j.Spec.Completions != nil {
		completions = fmt.Sprintf("%d", *j.Spec.Completions)
	}
	parallelism := int32(1)
	if j.Spec.Parallelism != nil {
		parallelism = *j.Spec.Parallelism
	}

	conditions := make([]ConditionInfo, 0, len(j.Status.Conditions))
	var failed, complete, suspended bool
	for _, c := range j.Status.Conditions {
		conditions = append(conditions, ConditionInfo{
			Type:               string(c.Type),
			Status:             string(c.Status),
			LastTransitionTime: c.LastTransitionTime.Format(time.RFC3339),
			Reason:             c.Reason,
			Message:            c.Message,
		})
		if string(c.Status) != "True" {
			continue
		}
		switch c.Type {
		case batchv1.JobFailed:
			failed = true
		case batchv1.JobComplete:
			complete = true
		case batchv1.JobSuspended:
			suspended = true
		}
	}
	status := "Running"
	switch {
	case failed:
		status = "Failed"
	case complete:
		status = "Complete"
	case suspended:
		status = "Suspended"
	}

	duration := ""
	if j.Status.StartTime != nil {
		end := time.Now()
		if j.Status.CompletionTime != nil {
			end = j.Status.CompletionTime.Time
		}
		// formatAge форматирует «сколько прошло с момента t»: сдвигаем t на длительность
		duration = formatAge(time.Now().Add(-end.Sub(j.Status.StartTime.Time)))
	}

	selector := formatMatchLabels(j.Spec.Selector)

	var images []string
	for _, c := range j.Spec.Template.Spec.Containers {
		images = append(images, c.Image)
	}

	backoff := int32(6) // значение по умолчанию в k8s
	if j.Spec.BackoffLimit != nil {
		backoff = *j.Spec.BackoffLimit
	}

	return JobInfo{
		Name:         j.Name,
		Namespace:    j.Namespace,
		Completions:  fmt.Sprintf("%d/%s", j.Status.Succeeded, completions),
		Succeeded:    j.Status.Succeeded,
		Failed:       j.Status.Failed,
		Active:       j.Status.Active,
		Parallelism:  parallelism,
		Duration:     duration,
		Age:          formatAge(j.CreationTimestamp.Time),
		Created:      j.CreationTimestamp.Format(time.RFC3339),
		Status:       status,
		Selector:     selector,
		Images:       images,
		Labels:       j.Labels,
		Annotations:  j.Annotations,
		ControlledBy: controlledByOf(j.OwnerReferences),
		Conditions:   conditions,
		PodStatuses:  podStatuses,
		BackoffLimit: backoff,
	}
}

func (m *Manager) GetJobs(namespaces []string) ([]JobInfo, error) {
	return scopedList(m, "GetJobs", "batch", "jobs", namespaces, listJobs,
		func(it JobInfo) string { return it.Namespace })
}

// listJobs читает ресурсы одного namespace ("" — все) для GetJobs.
func listJobs(ctx context.Context, conn *ClusterConnection, namespace string) ([]JobInfo, error) {
	list, err := conn.Client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods := jobPodStatuses(ctx, conn, namespace)

	items := make([]JobInfo, 0, len(list.Items))
	for i := range list.Items {
		j := &list.Items[i]
		items = append(items, convertJob(j, pods[j.Namespace+"/"+j.Name]))
	}
	return items, nil
}

// RestartDaemonSet триггерит rolling restart: меняет аннотацию в шаблоне пода (как kubectl rollout restart)
func (m *Manager) RestartDaemonSet(namespace, name string) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	ds, err := conn.Client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if ds.Spec.Template.Annotations == nil {
		ds.Spec.Template.Annotations = make(map[string]string)
	}
	ds.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err = conn.Client.AppsV1().DaemonSets(namespace).Update(ctx, ds, metav1.UpdateOptions{})
	return err
}

// RetryJob creates a new job from an existing job configuration
// RetryJob пересоздаёт Job с той же спецификацией: удаляет старый и создаёт новый.
// Копия собирается с нуля, поэтому uid, resourceVersion, status и managedFields не переносятся;
// сгенерированные селектор и controller-uid чистим, иначе apiserver отклонит создание.
func (m *Manager) RetryJob(namespace, jobName string) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 20*time.Second)
	defer cancel()

	job, err := conn.Client.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	newJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            job.Name,
			Namespace:       job.Namespace,
			Labels:          job.Labels,
			Annotations:     job.Annotations,
			OwnerReferences: job.OwnerReferences,
		},
		Spec: job.Spec,
	}
	for _, k := range []string{"controller-uid", "batch.kubernetes.io/controller-uid"} {
		delete(newJob.Labels, k)
		delete(newJob.Spec.Template.Labels, k)
	}
	newJob.Spec.Selector = nil
	newJob.Spec.ManualSelector = nil

	policy := metav1.DeletePropagationBackground
	if err := conn.Client.BatchV1().Jobs(namespace).Delete(ctx, jobName, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil {
		return err
	}

	// Имя освобождается не мгновенно: повторяем создание, пока объект не исчезнет
	for i := 0; i < 10; i++ {
		_, err = conn.Client.BatchV1().Jobs(namespace).Create(ctx, newJob, metav1.CreateOptions{})
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

// === CronJobs ===

type CronJobInfo struct {
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`
	Schedule      string `json:"schedule"`
	Suspend       bool   `json:"suspend"`
	Active        int    `json:"active"`
	LastSchedule  string `json:"lastSchedule"`
	NextExecution string `json:"nextExecution"`
	TimeZone      string `json:"timeZone"`
	Age           string `json:"age"`
}

func (m *Manager) GetCronJobs(namespaces []string) ([]CronJobInfo, error) {
	return scopedList(m, "GetCronJobs", "batch", "cronjobs", namespaces, listCronJobs,
		func(it CronJobInfo) string { return it.Namespace })
}

// listCronJobs читает ресурсы одного namespace ("" — все) для GetCronJobs.
func listCronJobs(ctx context.Context, conn *ClusterConnection, namespace string) ([]CronJobInfo, error) {
	list, err := conn.Client.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var items []CronJobInfo
	for _, cj := range list.Items {
		lastSchedule := "—"
		if cj.Status.LastScheduleTime != nil {
			lastSchedule = formatAge(cj.Status.LastScheduleTime.Time)
		}
		suspend := false
		if cj.Spec.Suspend != nil {
			suspend = *cj.Spec.Suspend
		}
		// Вычисляем следующее время выполнения
		nextExecution := "—"
		if !suspend && cj.Spec.Schedule != "" {
			nextExecution = computeNextExecution(cj.Spec.Schedule, cj.Spec.TimeZone)
		}

		// Временная зона
		timeZone := "—"
		if cj.Spec.TimeZone != nil && *cj.Spec.TimeZone != "" {
			timeZone = *cj.Spec.TimeZone
		}

		items = append(items, CronJobInfo{
			Name:          cj.Name,
			Namespace:     cj.Namespace,
			Schedule:      cj.Spec.Schedule,
			Suspend:       suspend,
			Active:        len(cj.Status.Active),
			LastSchedule:  lastSchedule,
			NextExecution: nextExecution,
			TimeZone:      timeZone,
			Age:           formatAge(cj.CreationTimestamp.Time),
		})
	}
	return items, nil
}

// GetCronJobJobs возвращает Job'ы, созданные указанным CronJob (по ownerReference).
func (m *Manager) GetCronJobJobs(namespace, cronJobName string) ([]JobInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	list, err := conn.Client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	pods := jobPodStatuses(ctx, conn, namespace)

	var items []JobInfo
	for i := range list.Items {
		j := &list.Items[i]
		// Проверяем ownerReference — Job должен принадлежать указанному CronJob
		owned := false
		for _, ref := range j.OwnerReferences {
			if ref.Kind == "CronJob" && ref.Name == cronJobName {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		items = append(items, convertJob(j, pods[j.Namespace+"/"+j.Name]))
	}
	return items, nil
}

// computeNextExecution вычисляет следующее время выполнения cron-расписания.
// Если указана timeZone, используем её, иначе UTC.
func computeNextExecution(schedule string, timeZone *string) string {
	loc := time.UTC
	if timeZone != nil && *timeZone != "" {
		var err error
		loc, err = time.LoadLocation(*timeZone)
		if err != nil {
			loc = time.UTC
		}
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	spec, err := parser.Parse(schedule)
	if err != nil {
		return "—"
	}

	next := spec.Next(time.Now().In(loc))
	duration := time.Until(next)

	if duration < time.Minute {
		secs := int(duration.Seconds())
		return fmt.Sprintf("In %d seconds", secs)
	}
	if duration < time.Hour {
		mins := int(duration.Minutes())
		if mins == 1 {
			return "In 1 minute"
		}
		return fmt.Sprintf("In %d minutes", mins)
	}
	if duration < 24*time.Hour {
		hours := int(duration.Hours())
		mins := int(duration.Minutes()) % 60
		if mins == 0 {
			if hours == 1 {
				return "In 1 hour"
			}
			return fmt.Sprintf("In %d hours", hours)
		}
		return fmt.Sprintf("In %d hours %d minutes", hours, mins)
	}
	days := int(duration.Hours()) / 24
	if days == 1 {
		return "In 1 day"
	}
	return fmt.Sprintf("In %d days", days)
}

func (m *Manager) ScaleStatefulSet(namespace, name string, replicas int32) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	scale, err := conn.Client.AppsV1().StatefulSets(namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get scale: %w", err)
	}

	scale.Spec.Replicas = replicas

	_, err = conn.Client.AppsV1().StatefulSets(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale: %w", err)
	}

	return nil
}
