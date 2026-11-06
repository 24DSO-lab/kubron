// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NodeInfo struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Conditions     string `json:"conditions"`
	Roles          string `json:"roles"`
	Version        string `json:"version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	CPUCapacity    string `json:"cpuCapacity"`
	MemoryCapacity string `json:"memoryCapacity"`
	InternalIP     string `json:"internalIP"`
	Age            string `json:"age"`
	PodCIDR        string `json:"podCIDR"`
	Taints         int    `json:"taints"`
	Unschedulable  bool   `json:"unschedulable"`
}

func (m *Manager) GetNodes() ([]NodeInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	nodeList, err := conn.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var nodes []NodeInfo
	for _, node := range nodeList.Items {
		nodes = append(nodes, convertNode(node))
	}

	return nodes, nil
}

func convertNode(node corev1.Node) NodeInfo {
	// Статус
	status := "NotReady"
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			status = "Ready"
			break
		}
	}

	// Conditions: Ready/NotReady плюс активные проблемные (MemoryPressure, DiskPressure, ...)
	conds := []string{status}
	for _, cond := range node.Status.Conditions {
		if cond.Type != corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			conds = append(conds, string(cond.Type))
		}
	}
	if node.Spec.Unschedulable {
		conds = append(conds, "SchedulingDisabled")
	}
	// Роли
	var roles []string
	for label := range node.Labels {
		if strings.HasPrefix(label, "node-role.kubernetes.io/") {
			role := strings.TrimPrefix(label, "node-role.kubernetes.io/")
			if role != "" {
				roles = append(roles, role)
			}
		}
	}
	sort.Strings(roles)
	rolesStr := strings.Join(roles, ", ")
	if rolesStr == "" {
		rolesStr = "<none>"
	}

	// IP
	internalIP := ""
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			internalIP = addr.Address
			break
		}
	}

	cpu := node.Status.Capacity.Cpu()
	mem := node.Status.Capacity.Memory()

	return NodeInfo{
		Name:           node.Name,
		Status:         status,
		Conditions:     strings.Join(conds, ", "),
		Roles:          rolesStr,
		Version:        node.Status.NodeInfo.KubeletVersion,
		OS:             node.Status.NodeInfo.OSImage,
		Arch:           node.Status.NodeInfo.Architecture,
		CPUCapacity:    cpu.String(),
		MemoryCapacity: formatMemory(mem.Value()),
		InternalIP:     internalIP,
		Age:            formatAge(node.CreationTimestamp.Time),
		PodCIDR:        node.Spec.PodCIDR,
		Taints:         len(node.Spec.Taints),
		Unschedulable:  node.Spec.Unschedulable,
	}
}

// NodeTaintInfo — taint узла
type NodeTaintInfo struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

// NodeConditionInfo — condition узла
type NodeConditionInfo struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// NodeAddressInfo — адрес узла (InternalIP, ExternalIP, Hostname)
type NodeAddressInfo struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

// NodeResourceField — одна колонка таблицы Capacity / Allocatable.
// Name — заголовок колонки, Value — уже отформатированное значение («7.6GiB»).
type NodeResourceField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// NodeDetail — полные данные узла для панели деталей.
// KubeVersion — версия kubelet; CPUCores и MemoryBytes — числовая ёмкость узла
// (вкладка Pods считает по ней долю CPU и памяти пода).
type NodeDetail struct {
	Name             string              `json:"name"`
	Status           string              `json:"status"`
	Roles            string              `json:"roles"`
	KubeVersion      string              `json:"kubeVersion"`
	KernelVersion    string              `json:"kernelVersion"`
	ContainerRuntime string              `json:"containerRuntime"`
	OperatingSystem  string              `json:"operatingSystem"`
	OSImage          string              `json:"osImage"`
	Architecture     string              `json:"architecture"`
	InternalIP       string              `json:"internalIP"`
	ExternalIP       string              `json:"externalIP"`
	Addresses        []NodeAddressInfo   `json:"addresses"`
	PodCIDR          string              `json:"podCIDR"`
	CPUCapacity      string              `json:"cpuCapacity"`
	MemoryCapacity   string              `json:"memoryCapacity"`
	Capacity         []NodeResourceField `json:"capacity"`
	Allocatable      []NodeResourceField `json:"allocatable"`
	CPUCores         float64             `json:"cpuCores"`
	MemoryBytes      float64             `json:"memoryBytes"`
	Age              string              `json:"age"`
	Created          string              `json:"created"`
	Labels           map[string]string   `json:"labels"`
	Annotations      map[string]string   `json:"annotations"`
	Taints           []NodeTaintInfo     `json:"taints"`
	Conditions       []NodeConditionInfo `json:"conditions"`
	Unschedulable    bool                `json:"unschedulable"`
}

func (m *Manager) GetNodeDetail(name string) (*NodeDetail, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	node, err := conn.Client.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	info := convertNode(*node)

	internalIP, externalIP := "", ""
	addresses := make([]NodeAddressInfo, 0, len(node.Status.Addresses))
	for _, addr := range node.Status.Addresses {
		addresses = append(addresses, NodeAddressInfo{Type: string(addr.Type), Address: addr.Address})
		switch addr.Type {
		case corev1.NodeInternalIP:
			if internalIP == "" {
				internalIP = addr.Address
			}
		case corev1.NodeExternalIP:
			if externalIP == "" {
				externalIP = addr.Address
			}
		}
	}

	taints := make([]NodeTaintInfo, 0, len(node.Spec.Taints))
	for _, t := range node.Spec.Taints {
		taints = append(taints, NodeTaintInfo{Key: t.Key, Value: t.Value, Effect: string(t.Effect)})
	}

	conditions := make([]NodeConditionInfo, 0, len(node.Status.Conditions))
	for _, c := range node.Status.Conditions {
		conditions = append(conditions, NodeConditionInfo{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	cpu := node.Status.Capacity.Cpu()
	mem := node.Status.Capacity.Memory()

	return &NodeDetail{
		Name:             info.Name,
		Status:           info.Status,
		Roles:            info.Roles,
		KubeVersion:      info.Version,
		KernelVersion:    node.Status.NodeInfo.KernelVersion,
		ContainerRuntime: node.Status.NodeInfo.ContainerRuntimeVersion,
		OperatingSystem:  node.Status.NodeInfo.OperatingSystem,
		OSImage:          info.OS,
		Architecture:     info.Arch,
		InternalIP:       internalIP,
		ExternalIP:       externalIP,
		Addresses:        addresses,
		PodCIDR:          info.PodCIDR,
		CPUCapacity:      info.CPUCapacity,
		MemoryCapacity:   info.MemoryCapacity,
		Capacity:         nodeResourceFields(node.Status.Capacity),
		Allocatable:      nodeResourceFields(node.Status.Allocatable),
		CPUCores:         float64(cpu.MilliValue()) / 1000,
		MemoryBytes:      float64(mem.Value()),
		Age:              info.Age,
		Created:          node.CreationTimestamp.UTC().Format(time.RFC3339),
		Labels:           node.Labels,
		Annotations:      node.Annotations,
		Taints:           taints,
		Conditions:       conditions,
		Unschedulable:    node.Spec.Unschedulable,
	}, nil
}

// standardNodeResources — колонки, которые показываются всегда и в этом порядке
// (hugepages-* даже при нулевом значении: так делают Lens и kubectl describe).
var standardNodeResources = []struct {
	name  corev1.ResourceName
	label string
	bytes bool // значение в байтах: форматируем как 7.6GiB
}{
	{corev1.ResourceCPU, "CPU", false},
	{corev1.ResourceMemory, "Memory", true},
	{corev1.ResourceEphemeralStorage, "Ephemeral Storage", true},
	{"hugepages-1Gi", "Hugepages-1Gi", true},
	{"hugepages-2Mi", "Hugepages-2Mi", true},
	{corev1.ResourcePods, "Pods", false},
}

// nodeResourceFields превращает ResourceList узла в упорядоченные колонки:
// сначала стандартные, затем остальные (например, nvidia.com/gpu) по алфавиту.
func nodeResourceFields(rl corev1.ResourceList) []NodeResourceField {
	out := make([]NodeResourceField, 0, len(rl)+len(standardNodeResources))
	seen := make(map[corev1.ResourceName]bool, len(standardNodeResources))

	for _, s := range standardNodeResources {
		seen[s.name] = true
		value := "0"
		if q, ok := rl[s.name]; ok {
			if s.bytes {
				value = formatBytesIEC(q.Value())
			} else {
				value = q.String()
			}
		}
		out = append(out, NodeResourceField{Name: s.label, Value: value})
	}

	var extra []string
	for name := range rl {
		if !seen[name] {
			extra = append(extra, string(name))
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		q := rl[corev1.ResourceName(name)]
		out = append(out, NodeResourceField{Name: name, Value: q.String()})
	}
	return out
}

// formatBytesIEC: 8160000000 -> "7.6GiB", 0 -> "0".
func formatBytesIEC(b int64) string {
	if b <= 0 {
		return "0"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	v, i := float64(b), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%dB", b)
	}
	return fmt.Sprintf("%.1f%s", v, units[i])
}
