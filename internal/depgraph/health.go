// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// Коды причин состояния вершины (Node.Reason). Короткие стабильные строки; полный список — в doc.go.
const (
	ReasonTerminating            = "terminating"
	ReasonCompleted              = "completed"
	ReasonFailed                 = "failed"
	ReasonEvicted                = "evicted"
	ReasonOOMKilled              = "oom_killed"
	ReasonCrashLoop              = "crash_loop"
	ReasonImagePullFailed        = "image_pull_failed"
	ReasonConfigError            = "config_error"
	ReasonContainerFailed        = "container_failed"
	ReasonInitFailed             = "init_failed"
	ReasonPendingUnscheduled     = "pending_unscheduled"
	ReasonPending                = "pending"
	ReasonContainerCreating      = "container_creating"
	ReasonNotReady               = "not_ready"
	ReasonScaledToZero           = "scaled_to_zero"
	ReasonPaused                 = "paused"
	ReasonSuspended              = "suspended"
	ReasonReplicasUnavailable    = "replicas_unavailable"
	ReasonReplicaFailure         = "replica_failure"
	ReasonRolloutInProgress      = "rollout_in_progress"
	ReasonProgressDeadline       = "progress_deadline_exceeded"
	ReasonNotAvailable           = "not_available"
	ReasonUnbound                = "unbound"
	ReasonLost                   = "lost"
	ReasonReleased               = "released"
	ReasonNoDefaultClass         = "no_default_storageclass"
	ReasonWaitingFirstConsumer   = "waiting_first_consumer"
	ReasonNoEndpoints            = "no_endpoints"
	ReasonNoReadyEndpoints       = "no_ready_endpoints"
	ReasonSelectorMatchesNothing = "selector_matches_nothing"
	ReasonNodeNotReady           = "node_not_ready"
	ReasonNodeUnreachable        = "node_unreachable"
	ReasonNodePressureMemory     = "node_pressure_memory"
	ReasonNodePressureDisk       = "node_pressure_disk"
	ReasonNodePressurePID        = "node_pressure_pid"
	ReasonNodeNetworkDown        = "node_network_unavailable"
	ReasonNodeUnschedulable      = "node_unschedulable"
	ReasonJobFailed              = "job_failed"
	ReasonRunning                = "running"
	ReasonUnreadable             = "unreadable"
)

// HealthOf выводит состояние объекта и код причины. Только по самому объекту, без чтения кластера: связи
// (Service без Pod, PVC без класса) уточняет построитель. Нераспознанный вид — Healthy без причины; объект,
// который не удалось разобрать, — Unknown с причиной «unreadable».
func HealthOf(obj *unstructured.Unstructured) (Health, string) {
	if obj == nil {
		return Unknown, ReasonUnreadable
	}
	switch obj.GetKind() {
	case "Pod":
		var p corev1.Pod
		if !decode(obj, &p) {
			return Unknown, ReasonUnreadable
		}
		return podHealth(&p)
	case "ReplicaSet":
		var rs appsv1.ReplicaSet
		if !decode(obj, &rs) {
			return Unknown, ReasonUnreadable
		}
		return replicaSetHealth(&rs)
	case "Deployment":
		var d appsv1.Deployment
		if !decode(obj, &d) {
			return Unknown, ReasonUnreadable
		}
		return deploymentHealth(&d)
	case "StatefulSet":
		var s appsv1.StatefulSet
		if !decode(obj, &s) {
			return Unknown, ReasonUnreadable
		}
		return countedHealth(ptrInt(s.Spec.Replicas, 1), s.Status.ReadyReplicas)
	case "DaemonSet":
		var d appsv1.DaemonSet
		if !decode(obj, &d) {
			return Unknown, ReasonUnreadable
		}
		if d.Status.NumberUnavailable > 0 || d.Status.NumberReady < d.Status.DesiredNumberScheduled {
			return Degraded, ReasonReplicasUnavailable
		}
		return Healthy, ""
	case "Job":
		var j batchv1.Job
		if !decode(obj, &j) {
			return Unknown, ReasonUnreadable
		}
		return jobHealth(&j)
	case "CronJob":
		var c batchv1.CronJob
		if !decode(obj, &c) {
			return Unknown, ReasonUnreadable
		}
		if c.Spec.Suspend != nil && *c.Spec.Suspend {
			return Suspended, ReasonSuspended
		}
		return Healthy, ""
	case "PersistentVolumeClaim":
		var c corev1.PersistentVolumeClaim
		if !decode(obj, &c) {
			return Unknown, ReasonUnreadable
		}
		return pvcHealth(&c)
	case "PersistentVolume":
		var v corev1.PersistentVolume
		if !decode(obj, &v) {
			return Unknown, ReasonUnreadable
		}
		return pvHealth(&v)
	case "Node":
		var n corev1.Node
		if !decode(obj, &n) {
			return Unknown, ReasonUnreadable
		}
		return nodeHealth(&n)
	case "EndpointSlice":
		var s discoveryv1.EndpointSlice
		if !decode(obj, &s) {
			return Unknown, ReasonUnreadable
		}
		return sliceHealth(&s)
	}
	if h, reason, ok := extHealth(obj); ok {
		return h, reason
	}
	return Healthy, ""
}

func decode(obj *unstructured.Unstructured, out any) bool {
	return runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, out) == nil
}

func ptrInt(p *int32, def int32) int32 {
	if p == nil {
		return def
	}
	return *p
}

var imagePullReasons = []string{"ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull", "RegistryUnavailable"}

func podHealth(p *corev1.Pod) (Health, string) {
	if p.DeletionTimestamp != nil {
		return Progressing, ReasonTerminating
	}
	switch p.Status.Phase {
	case corev1.PodSucceeded:
		return Healthy, ReasonCompleted
	case corev1.PodFailed:
		if p.Status.Reason == "Evicted" {
			return Degraded, ReasonEvicted
		}
		return Degraded, ReasonFailed
	}
	// Худшее состояние контейнера по приоритету: меньший ранг важнее.
	rank, reason := 99, ""
	note := func(r int, code string) {
		if r < rank {
			rank, reason = r, code
		}
	}
	creating := false
	scan := func(list []corev1.ContainerStatus, init bool) {
		for _, s := range list {
			w, t, last := s.State.Waiting, s.State.Terminated, s.LastTerminationState.Terminated
			switch {
			case w != nil && w.Reason == "CrashLoopBackOff":
				if last != nil && last.Reason == "OOMKilled" {
					note(1, ReasonOOMKilled)
				} else {
					note(2, ReasonCrashLoop)
				}
			case t != nil && t.Reason == "OOMKilled":
				note(1, ReasonOOMKilled)
			case w != nil && slices.Contains(imagePullReasons, w.Reason):
				note(3, ReasonImagePullFailed)
			case w != nil && (w.Reason == "CreateContainerConfigError" || w.Reason == "CreateContainerError"):
				note(4, ReasonConfigError)
			case t != nil && t.ExitCode != 0 && init:
				note(5, ReasonInitFailed)
			case t != nil && t.ExitCode != 0 && p.Status.Phase == corev1.PodRunning:
				note(6, ReasonContainerFailed)
			case w != nil && (w.Reason == "ContainerCreating" || w.Reason == "PodInitializing"):
				creating = true
			}
		}
	}
	scan(p.Status.InitContainerStatuses, true)
	scan(p.Status.ContainerStatuses, false)
	if reason != "" {
		return Degraded, reason
	}
	if p.Status.Phase == corev1.PodRunning {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return Healthy, ""
			}
		}
		return Degraded, ReasonNotReady
	}
	// Pending (или фаза ещё не заполнена).
	if p.Spec.NodeName == "" {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
				return Degraded, ReasonPendingUnscheduled
			}
		}
		return Progressing, ReasonPending
	}
	if creating {
		return Progressing, ReasonContainerCreating
	}
	return Progressing, ReasonPending
}

func countedHealth(desired, ready int32) (Health, string) {
	switch {
	case desired == 0:
		return Suspended, ReasonScaledToZero
	case ready < desired:
		return Degraded, ReasonReplicasUnavailable
	}
	return Healthy, ""
}

func replicaSetHealth(rs *appsv1.ReplicaSet) (Health, string) {
	for _, c := range rs.Status.Conditions {
		if c.Type == appsv1.ReplicaSetReplicaFailure && c.Status == corev1.ConditionTrue {
			return Degraded, ReasonReplicaFailure
		}
	}
	return countedHealth(ptrInt(rs.Spec.Replicas, 1), rs.Status.ReadyReplicas)
}

func deploymentHealth(d *appsv1.Deployment) (Health, string) {
	if d.Spec.Paused {
		return Suspended, ReasonPaused
	}
	desired := ptrInt(d.Spec.Replicas, 1)
	if desired == 0 {
		return Suspended, ReasonScaledToZero
	}
	for _, c := range d.Status.Conditions {
		switch {
		case c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue:
			return Degraded, ReasonReplicaFailure
		case c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse && c.Reason == "ProgressDeadlineExceeded":
			return Degraded, ReasonProgressDeadline
		}
	}
	if d.Status.ObservedGeneration < d.Generation || d.Status.UpdatedReplicas < desired {
		return Progressing, ReasonRolloutInProgress
	}
	if d.Status.AvailableReplicas < desired {
		for _, c := range d.Status.Conditions {
			if c.Type == appsv1.DeploymentAvailable && c.Status == corev1.ConditionFalse {
				return Degraded, ReasonNotAvailable
			}
		}
		return Degraded, ReasonReplicasUnavailable
	}
	return Healthy, ""
}

func jobHealth(j *batchv1.Job) (Health, string) {
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobFailed:
			return Degraded, ReasonJobFailed
		case batchv1.JobComplete:
			return Healthy, ReasonCompleted
		case batchv1.JobSuspended:
			return Suspended, ReasonSuspended
		}
	}
	return Progressing, ReasonRunning
}

func pvcHealth(c *corev1.PersistentVolumeClaim) (Health, string) {
	if c.DeletionTimestamp != nil {
		return Progressing, ReasonTerminating
	}
	switch c.Status.Phase {
	case corev1.ClaimBound:
		return Healthy, ""
	case corev1.ClaimLost:
		return Degraded, ReasonLost
	}
	return Degraded, ReasonUnbound
}

func pvHealth(v *corev1.PersistentVolume) (Health, string) {
	switch v.Status.Phase {
	case corev1.VolumeReleased:
		return Degraded, ReasonReleased
	case corev1.VolumeFailed:
		return Degraded, ReasonFailed
	case corev1.VolumePending:
		return Progressing, ReasonPending
	}
	return Healthy, ""
}

func nodeHealth(n *corev1.Node) (Health, string) {
	cond := func(t corev1.NodeConditionType) corev1.ConditionStatus {
		for _, c := range n.Status.Conditions {
			if c.Type == t {
				return c.Status
			}
		}
		return ""
	}
	switch cond(corev1.NodeReady) {
	case corev1.ConditionFalse:
		return Degraded, ReasonNodeNotReady
	case corev1.ConditionUnknown:
		return Degraded, ReasonNodeUnreachable
	}
	switch {
	case cond(corev1.NodeMemoryPressure) == corev1.ConditionTrue:
		return Degraded, ReasonNodePressureMemory
	case cond(corev1.NodeDiskPressure) == corev1.ConditionTrue:
		return Degraded, ReasonNodePressureDisk
	case cond(corev1.NodePIDPressure) == corev1.ConditionTrue:
		return Degraded, ReasonNodePressurePID
	case cond(corev1.NodeNetworkUnavailable) == corev1.ConditionTrue:
		return Degraded, ReasonNodeNetworkDown
	case n.Spec.Unschedulable:
		return Suspended, ReasonNodeUnschedulable
	}
	return Healthy, ""
}

func sliceHealth(s *discoveryv1.EndpointSlice) (Health, string) {
	if len(s.Endpoints) == 0 {
		return Degraded, ReasonNoEndpoints
	}
	for _, e := range s.Endpoints {
		if e.Conditions.Ready == nil || *e.Conditions.Ready {
			return Healthy, ""
		}
	}
	return Degraded, ReasonNoReadyEndpoints
}
