// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type VolumeInfo struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Details map[string]string `json:"details"`
}

type ConditionInfo struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	LastTransitionTime string `json:"lastTransitionTime"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
}

type TolerationInfo struct {
	Key               string `json:"key"`
	Operator          string `json:"operator"`
	Value             string `json:"value"`
	Effect            string `json:"effect"`
	TolerationSeconds *int64 `json:"tolerationSeconds"`
}

type ContainerPort struct {
	Name          string `json:"name"`
	ContainerPort int32  `json:"containerPort"`
	Protocol      string `json:"protocol"`
	HostPort      int32  `json:"hostPort"`
	HostIP        string `json:"hostIP"`
}

type EnvVarInfo struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type VolumeMountInfo struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SubPath   string `json:"subPath"`
	ReadOnly  bool   `json:"readOnly"`
}

type ContainerResourcesInfo struct {
	RequestsCPU    string `json:"requestsCpu"`
	RequestsMemory string `json:"requestsMemory"`
	LimitsCPU      string `json:"limitsCpu"`
	LimitsMemory   string `json:"limitsMemory"`
}

type ContainerSecurityCtxInfo struct {
	RunAsUser      *int64 `json:"runAsUser"`
	RunAsNonRoot   *bool  `json:"runAsNonRoot"`
	ReadOnlyFS     *bool  `json:"readOnlyFilesystem"`
	AllowPrivilege *bool  `json:"allowPrivilegeEscalation"`
}

type ProbesInfo struct {
	Liveness  string `json:"liveness"`
	Readiness string `json:"readiness"`
	Startup   string `json:"startup"`
}

type ContainerInfo struct {
	Name               string                    `json:"name"`
	Image              string                    `json:"image"`
	Ready              bool                      `json:"ready"`
	RestartCount       int32                     `json:"restartCount"`
	State              string                    `json:"state"`
	IsInit             bool                      `json:"isInit"`
	IsEphemeral        bool                      `json:"isEphemeral"` // отладочный контейнер (ephemeralContainers)
	Ports              []ContainerPort           `json:"ports"`
	Env                []EnvVarInfo              `json:"env"`
	VolumeMounts       []VolumeMountInfo         `json:"volumeMounts"`
	Resources          *ContainerResourcesInfo   `json:"resources"`
	SecurityCtx        *ContainerSecurityCtxInfo `json:"securityContext"`
	Probes             *ProbesInfo               `json:"probes"`
	Args               []string                  `json:"args"`
	Command            []string                  `json:"command"`
	WorkingDir         string                    `json:"workingDir"`
	ImagePullPolicy    string                    `json:"imagePullPolicy"`
	TerminationMsgPath string                    `json:"terminationMessagePath"`
}

type PodInfo struct {
	Name            string                  `json:"name"`
	Namespace       string                  `json:"namespace"`
	Status          string                  `json:"status"`
	Ready           string                  `json:"ready"`
	Restarts        int32                   `json:"restarts"`
	Age             string                  `json:"age"`
	Node            string                  `json:"node"`
	IP              string                  `json:"ip"`
	Labels          map[string]string       `json:"labels"`
	Annotations     map[string]string       `json:"annotations"`
	ControlledBy    string                  `json:"controlledBy"`
	ServiceAccount  string                  `json:"serviceAccount"`
	NodeSelector    map[string]string       `json:"nodeSelector"`
	Conditions      []ConditionInfo         `json:"conditions"`
	Tolerations     []TolerationInfo        `json:"tolerations"`
	PodAntiAffinity *corev1.PodAntiAffinity `json:"podAntiAffinity"`
	Containers      []ContainerInfo         `json:"containers"`
	Volumes         []VolumeInfo            `json:"volumes"`
	QOS             string                  `json:"qos"`
	Created         string                  `json:"created"`

	PriorityClassName string `json:"priorityClassName"`
	RuntimeClassName  string `json:"runtimeClassName"`
}

func (m *Manager) GetPods(namespaces []string) ([]PodInfo, error) {
	return scopedList(m, "GetPods", "", "pods", namespaces, listPods,
		func(it PodInfo) string { return it.Namespace })
}

// listPods читает ресурсы одного namespace ("" — все) для GetPods.
func listPods(ctx context.Context, conn *ClusterConnection, namespace string) ([]PodInfo, error) {
	podList, err := conn.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var pods []PodInfo
	for _, pod := range podList.Items {
		pods = append(pods, convertPod(pod))
	}

	return pods, nil
}

func convertPod(pod corev1.Pod) PodInfo {
	var totalRestarts int32
	var readyCount int
	var containers []ContainerInfo
	var conditions []ConditionInfo
	var tolerations []TolerationInfo

	// Create map of container specs for quick lookup
	containerSpecMap := make(map[string]corev1.Container)
	for _, c := range pod.Spec.Containers {
		containerSpecMap[c.Name] = c
	}
	initContainerSpecMap := make(map[string]corev1.Container)
	for _, c := range pod.Spec.InitContainers {
		initContainerSpecMap[c.Name] = c
	}

	// Process init containers
	for _, ics := range pod.Status.InitContainerStatuses {
		state := "unknown"
		if ics.State.Running != nil {
			state = "running"
		} else if ics.State.Waiting != nil {
			state = ics.State.Waiting.Reason
		} else if ics.State.Terminated != nil {
			state = ics.State.Terminated.Reason
		}

		spec := initContainerSpecMap[ics.Name]

		containers = append(containers, ContainerInfo{
			Name:               ics.Name,
			Image:              ics.Image,
			Ready:              ics.Ready,
			RestartCount:       ics.RestartCount,
			State:              state,
			IsInit:             true,
			Ports:              extractPorts(spec),
			Env:                extractEnv(spec),
			VolumeMounts:       extractVolumeMounts(spec),
			Resources:          extractResources(spec),
			SecurityCtx:        extractSecurityCtx(spec),
			Probes:             extractProbes(spec),
			Args:               spec.Args,
			Command:            spec.Command,
			WorkingDir:         spec.WorkingDir,
			ImagePullPolicy:    string(spec.ImagePullPolicy),
			TerminationMsgPath: spec.TerminationMessagePath,
		})
	}

	// Process regular containers
	for _, cs := range pod.Status.ContainerStatuses {
		totalRestarts += cs.RestartCount
		if cs.Ready {
			readyCount++
		}

		state := "unknown"
		if cs.State.Running != nil {
			state = "running"
		} else if cs.State.Waiting != nil {
			state = cs.State.Waiting.Reason
		} else if cs.State.Terminated != nil {
			state = cs.State.Terminated.Reason
		}

		spec := containerSpecMap[cs.Name]

		containers = append(containers, ContainerInfo{
			Name:               cs.Name,
			Image:              cs.Image,
			Ready:              cs.Ready,
			RestartCount:       cs.RestartCount,
			State:              state,
			IsInit:             false,
			Ports:              extractPorts(spec),
			Env:                extractEnv(spec),
			VolumeMounts:       extractVolumeMounts(spec),
			Resources:          extractResources(spec),
			SecurityCtx:        extractSecurityCtx(spec),
			Probes:             extractProbes(spec),
			Args:               spec.Args,
			Command:            spec.Command,
			WorkingDir:         spec.WorkingDir,
			ImagePullPolicy:    string(spec.ImagePullPolicy),
			TerminationMsgPath: spec.TerminationMessagePath,
		})
	}

	// Ephemeral (отладочные) контейнеры: остаются в spec до удаления пода, даже завершённые. В готовность и
	// счётчик перезапусков пода они не входят.
	for _, ec := range pod.Spec.EphemeralContainers {
		state, image := "waiting", ec.Image
		var ready bool
		for _, es := range pod.Status.EphemeralContainerStatuses {
			if es.Name != ec.Name {
				continue
			}
			ready = es.Ready
			if es.Image != "" {
				image = es.Image
			}
			switch {
			case es.State.Running != nil:
				state = "running"
			case es.State.Waiting != nil:
				state = es.State.Waiting.Reason
			case es.State.Terminated != nil:
				state = es.State.Terminated.Reason
			}
		}
		containers = append(containers, ContainerInfo{
			Name:            ec.Name,
			Image:           image,
			Ready:           ready,
			State:           state,
			IsEphemeral:     true,
			Command:         ec.Command,
			Args:            ec.Args,
			WorkingDir:      ec.WorkingDir,
			ImagePullPolicy: string(ec.ImagePullPolicy),
			SecurityCtx:     extractSecurityCtx(corev1.Container{SecurityContext: ec.SecurityContext}),
		})
	}

	// Conditions
	for _, cond := range pod.Status.Conditions {
		conditions = append(conditions, ConditionInfo{
			Type:               string(cond.Type),
			Status:             string(cond.Status),
			LastTransitionTime: cond.LastTransitionTime.Format(time.RFC3339),
			Reason:             cond.Reason,
			Message:            cond.Message,
		})
	}

	// Tolerations
	for _, tol := range pod.Spec.Tolerations {
		tolerations = append(tolerations, TolerationInfo{
			Key:               tol.Key,
			Operator:          string(tol.Operator),
			Value:             tol.Value,
			Effect:            string(tol.Effect),
			TolerationSeconds: tol.TolerationSeconds,
		})
	}

	controlledBy := ""
	if len(pod.OwnerReferences) > 0 {
		ref := pod.OwnerReferences[0]
		controlledBy = fmt.Sprintf("%s/%s", ref.Kind, ref.Name)
	}

	var podAntiAffinity *corev1.PodAntiAffinity
	if pod.Spec.Affinity != nil {
		podAntiAffinity = pod.Spec.Affinity.PodAntiAffinity
	}

	qos := string(pod.Status.QOSClass)
	created := pod.CreationTimestamp.Format(time.RFC3339)

	status := string(pod.Status.Phase)
	// Более точный статус
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			status = cs.State.Waiting.Reason
			break
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			status = cs.State.Terminated.Reason
			break
		}
	}
	// Как у kubectl: удаляемый под (graceful termination) остаётся в списке, пока не остановятся контейнеры
	if pod.DeletionTimestamp != nil {
		status = "Terminating"
	}

	var volumes []VolumeInfo
	for _, vol := range pod.Spec.Volumes {
		v := VolumeInfo{
			Name:    vol.Name,
			Details: make(map[string]string),
		}
		switch {
		case vol.HostPath != nil:
			v.Type = "HostPath"
			v.Details["Path"] = vol.HostPath.Path
			if vol.HostPath.Type != nil {
				v.Details["Type"] = string(*vol.HostPath.Type)
			}

		case vol.EmptyDir != nil:
			v.Type = "EmptyDir"
			if vol.EmptyDir.Medium != "" {
				v.Details["Medium"] = string(vol.EmptyDir.Medium)
			}
			if vol.EmptyDir.SizeLimit != nil {
				v.Details["SizeLimit"] = vol.EmptyDir.SizeLimit.String()
			}

		case vol.Secret != nil:
			v.Type = "Secret"
			v.Details["SecretName"] = vol.Secret.SecretName
			v.Details["Items"] = fmt.Sprintf("%d", len(vol.Secret.Items))
			if vol.Secret.DefaultMode != nil {
				v.Details["DefaultFileMode"] = fmt.Sprintf("0%o", *vol.Secret.DefaultMode)
			}
			if vol.Secret.Optional != nil {
				v.Details["Optional"] = fmt.Sprintf("%t", *vol.Secret.Optional)
			}

		case vol.ConfigMap != nil:
			v.Type = "ConfigMap"
			v.Details["ConfigMapName"] = vol.ConfigMap.Name
			v.Details["Items"] = fmt.Sprintf("%d", len(vol.ConfigMap.Items))
			if vol.ConfigMap.DefaultMode != nil {
				v.Details["DefaultFileMode"] = fmt.Sprintf("0%o", *vol.ConfigMap.DefaultMode)
			}
			if vol.ConfigMap.Optional != nil {
				v.Details["Optional"] = fmt.Sprintf("%t", *vol.ConfigMap.Optional)
			}

		case vol.Projected != nil:
			v.Type = "Projected"
			v.Details["Sources"] = fmt.Sprintf("%d", len(vol.Projected.Sources))
			if vol.Projected.DefaultMode != nil {
				v.Details["DefaultMode"] = fmt.Sprintf("0%o", *vol.Projected.DefaultMode)
			}

		case vol.PersistentVolumeClaim != nil:
			v.Type = "PersistentVolumeClaim"
			v.Details["ClaimName"] = vol.PersistentVolumeClaim.ClaimName
			v.Details["ReadOnly"] = fmt.Sprintf("%t", vol.PersistentVolumeClaim.ReadOnly)

		case vol.NFS != nil:
			v.Type = "NFS"
			v.Details["Server"] = vol.NFS.Server
			v.Details["Path"] = vol.NFS.Path
			v.Details["ReadOnly"] = fmt.Sprintf("%t", vol.NFS.ReadOnly)

		case vol.CSI != nil:
			v.Type = "CSI"
			v.Details["Driver"] = vol.CSI.Driver
			if vol.CSI.ReadOnly != nil {
				v.Details["ReadOnly"] = fmt.Sprintf("%t", *vol.CSI.ReadOnly)
			}

		case vol.DownwardAPI != nil:
			v.Type = "DownwardAPI"
			v.Details["Items"] = fmt.Sprintf("%d", len(vol.DownwardAPI.Items))
			if vol.DownwardAPI.DefaultMode != nil {
				v.Details["DefaultMode"] = fmt.Sprintf("0%o", *vol.DownwardAPI.DefaultMode)
			}

		default:
			v.Type = "Other"
		}
		volumes = append(volumes, v)
	}

	return PodInfo{
		Name:              pod.Name,
		Namespace:         pod.Namespace,
		Status:            status,
		Ready:             fmt.Sprintf("%d/%d", readyCount, len(pod.Spec.Containers)),
		Restarts:          totalRestarts,
		Age:               formatAge(pod.CreationTimestamp.Time),
		Node:              pod.Spec.NodeName,
		IP:                pod.Status.PodIP,
		Labels:            pod.Labels,
		Annotations:       pod.Annotations,
		ControlledBy:      controlledBy,
		ServiceAccount:    pod.Spec.ServiceAccountName,
		NodeSelector:      pod.Spec.NodeSelector,
		Conditions:        conditions,
		Tolerations:       tolerations,
		PodAntiAffinity:   podAntiAffinity,
		Containers:        containers,
		Volumes:           volumes,
		QOS:               qos,
		PriorityClassName: pod.Spec.PriorityClassName,
		RuntimeClassName:  derefStr(pod.Spec.RuntimeClassName),
		Created:           created,
	}
}

func extractPorts(c corev1.Container) []ContainerPort {
	var ports []ContainerPort
	for _, p := range c.Ports {
		ports = append(ports, ContainerPort{
			Name:          p.Name,
			ContainerPort: p.ContainerPort,
			Protocol:      string(p.Protocol),
			HostPort:      p.HostPort,
			HostIP:        p.HostIP,
		})
	}
	return ports
}

func extractEnv(c corev1.Container) []EnvVarInfo {
	var env []EnvVarInfo
	for _, e := range c.Env {
		value := e.Value
		if e.ValueFrom != nil {
			if e.ValueFrom.SecretKeyRef != nil {
				value = fmt.Sprintf("secret(%s/%s)", e.ValueFrom.SecretKeyRef.Name, e.ValueFrom.SecretKeyRef.Key)
			} else if e.ValueFrom.ConfigMapKeyRef != nil {
				value = fmt.Sprintf("configmap(%s/%s)", e.ValueFrom.ConfigMapKeyRef.Name, e.ValueFrom.ConfigMapKeyRef.Key)
			} else if e.ValueFrom.FieldRef != nil {
				value = fmt.Sprintf("field(%s)", e.ValueFrom.FieldRef.FieldPath)
			} else if e.ValueFrom.ResourceFieldRef != nil {
				value = fmt.Sprintf("resource(%s)", e.ValueFrom.ResourceFieldRef.Resource)
			}
		}
		env = append(env, EnvVarInfo{
			Name:  e.Name,
			Value: value,
		})
	}
	return env
}

func extractVolumeMounts(c corev1.Container) []VolumeMountInfo {
	var mounts []VolumeMountInfo
	for _, m := range c.VolumeMounts {
		mounts = append(mounts, VolumeMountInfo{
			Name:      m.Name,
			MountPath: m.MountPath,
			SubPath:   m.SubPath,
			ReadOnly:  m.ReadOnly,
		})
	}
	return mounts
}

func extractResources(c corev1.Container) *ContainerResourcesInfo {
	if len(c.Resources.Requests) == 0 && len(c.Resources.Limits) == 0 {
		return nil
	}

	res := &ContainerResourcesInfo{}
	if c.Resources.Requests != nil {
		if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			res.RequestsCPU = q.String()
		}
		if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
			res.RequestsMemory = q.String()
		}
	}
	if c.Resources.Limits != nil {
		if q, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
			res.LimitsCPU = q.String()
		}
		if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
			res.LimitsMemory = q.String()
		}
	}
	return res
}

func extractSecurityCtx(c corev1.Container) *ContainerSecurityCtxInfo {
	if c.SecurityContext == nil {
		return nil
	}

	sec := &ContainerSecurityCtxInfo{
		RunAsUser:      c.SecurityContext.RunAsUser,
		RunAsNonRoot:   c.SecurityContext.RunAsNonRoot,
		ReadOnlyFS:     c.SecurityContext.ReadOnlyRootFilesystem,
		AllowPrivilege: c.SecurityContext.AllowPrivilegeEscalation,
	}
	return sec
}

func extractProbes(c corev1.Container) *ProbesInfo {
	if c.LivenessProbe == nil && c.ReadinessProbe == nil && c.StartupProbe == nil {
		return nil
	}

	probes := &ProbesInfo{}

	if c.LivenessProbe != nil {
		probes.Liveness = formatProbe(c.LivenessProbe)
	}
	if c.ReadinessProbe != nil {
		probes.Readiness = formatProbe(c.ReadinessProbe)
	}
	if c.StartupProbe != nil {
		probes.Startup = formatProbe(c.StartupProbe)
	}

	return probes
}

func formatProbe(probe *corev1.Probe) string {
	if probe.HTTPGet != nil {
		return fmt.Sprintf("HTTP GET %s:%d%s", probe.HTTPGet.Scheme, probe.HTTPGet.Port.IntVal, probe.HTTPGet.Path)
	} else if probe.TCPSocket != nil {
		return fmt.Sprintf("TCP ::%d", probe.TCPSocket.Port.IntVal)
	} else if probe.Exec != nil {
		return fmt.Sprintf("Exec: %v", probe.Exec.Command)
	}
	return "Unknown"
}
