// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	quotaWarnPercent = 80
	maxRiskRows      = 10
)

// NodeHeadroom — сколько на узле осталось под новые requests (allocatable минус requests запланированных подов).
type NodeHeadroom struct {
	Name              string  `json:"name"`
	CPUFree           float64 `json:"cpuFree"` // ядра
	CPUAllocatable    float64 `json:"cpuAllocatable"`
	MemoryFree        float64 `json:"memoryFree"` // байты
	MemoryAllocatable float64 `json:"memoryAllocatable"`
}

// QuotaPressure — ресурс ResourceQuota, использованный на ≥ quotaWarnPercent.
type QuotaPressure struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Resource  string `json:"resource"`
	Used      string `json:"used"`
	Hard      string `json:"hard"`
	Percent   int    `json:"percent"`
}

// PDBBlock — PodDisruptionBudget, который сейчас не допускает ни одного добровольного вытеснения (drain встанет).
type PDBBlock struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Healthy   int32  `json:"healthy"`
	Desired   int32  `json:"desired"`
	Expected  int32  `json:"expected"`
}

// OverviewRisks — «что помешает запланировать под или обслужить узел».
type OverviewRisks struct {
	Nodes      []NodeHeadroom  `json:"nodes"`
	Quotas     []QuotaPressure `json:"quotas"`
	QuotaTotal int             `json:"quotaTotal"`
	PDBs       []PDBBlock      `json:"pdbs"`
	PDBTotal   int             `json:"pdbTotal"`
}

func (m *Manager) GetOverviewRisks() (*OverviewRisks, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		wg     sync.WaitGroup
		nodes  *corev1.NodeList
		pods   *corev1.PodList
		quotas *corev1.ResourceQuotaList
		pdbs   *policyv1.PodDisruptionBudgetList
	)
	run := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }

	oc := conn.overviewCache(func() { m.emitCluster(EventClusterChanged, conn) })
	run(func() {
		if l, ok := oc.nodeList(); ok {
			nodes = l
		} else if l, err := conn.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
			nodes = l
		}
	})
	run(func() {
		if l, ok := oc.podList(); ok {
			pods = l
		} else if l, err := conn.Client.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
			pods = l
		}
	})
	run(func() {
		if l, err := conn.Client.CoreV1().ResourceQuotas("").List(ctx, metav1.ListOptions{}); err == nil {
			quotas = l
		}
	})
	run(func() {
		if l, err := conn.Client.PolicyV1().PodDisruptionBudgets("").List(ctx, metav1.ListOptions{}); err == nil {
			pdbs = l
		}
	})
	wg.Wait()

	out := &OverviewRisks{Nodes: []NodeHeadroom{}, Quotas: []QuotaPressure{}, PDBs: []PDBBlock{}}
	if nodes != nil && pods != nil {
		out.Nodes = nodeHeadroom(nodes.Items, pods.Items)
	}
	if quotas != nil {
		out.Quotas, out.QuotaTotal = quotaPressure(quotas.Items)
	}
	if pdbs != nil {
		out.PDBs, out.PDBTotal = pdbBlocks(pdbs.Items)
	}
	return out, nil
}

// nodeHeadroom считает запас по узлам. Закрытые для планирования (cordon) и не-Ready узлы пропускаются:
// новые поды на них не попадут, и их запас только вводил бы в заблуждение.
func nodeHeadroom(nodes []corev1.Node, pods []corev1.Pod) []NodeHeadroom {
	type req struct{ cpu, mem float64 }
	byNode := map[string]*req{}
	for i := range pods {
		p := &pods[i]
		if p.Spec.NodeName == "" || (p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodPending) {
			continue
		}
		r := byNode[p.Spec.NodeName]
		if r == nil {
			r = &req{}
			byNode[p.Spec.NodeName] = r
		}
		for _, c := range p.Spec.Containers {
			r.cpu += float64(c.Resources.Requests.Cpu().MilliValue()) / 1000
			r.mem += float64(c.Resources.Requests.Memory().Value())
		}
	}

	out := make([]NodeHeadroom, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		if n.Spec.Unschedulable || !isNodeReady(n) {
			continue
		}
		h := NodeHeadroom{
			Name:              n.Name,
			CPUAllocatable:    float64(n.Status.Allocatable.Cpu().MilliValue()) / 1000,
			MemoryAllocatable: float64(n.Status.Allocatable.Memory().Value()),
		}
		h.CPUFree, h.MemoryFree = h.CPUAllocatable, h.MemoryAllocatable
		if r := byNode[n.Name]; r != nil {
			h.CPUFree -= r.cpu
			h.MemoryFree -= r.mem
		}
		if h.CPUFree < 0 {
			h.CPUFree = 0
		}
		if h.MemoryFree < 0 {
			h.MemoryFree = 0
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// quotaPressure возвращает ресурсы квот, использованные на ≥ quotaWarnPercent, самые заполненные первыми, и их общее число.
func quotaPressure(quotas []corev1.ResourceQuota) ([]QuotaPressure, int) {
	var all []QuotaPressure
	for i := range quotas {
		q := &quotas[i]
		hard := q.Status.Hard
		if len(hard) == 0 {
			hard = q.Spec.Hard
		}
		for name, limit := range hard {
			used, ok := q.Status.Used[name]
			if !ok || limit.IsZero() {
				continue
			}
			pct := int(used.AsApproximateFloat64() / limit.AsApproximateFloat64() * 100)
			if pct < quotaWarnPercent {
				continue
			}
			all = append(all, QuotaPressure{
				Namespace: q.Namespace, Name: q.Name, Resource: string(name),
				Used: used.String(), Hard: limit.String(), Percent: pct,
			})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Percent != b.Percent {
			return a.Percent > b.Percent
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Resource < b.Resource
	})
	total := len(all)
	if len(all) > maxRiskRows {
		all = all[:maxRiskRows]
	}
	if all == nil {
		all = []QuotaPressure{}
	}
	return all, total
}

// pdbBlocks возвращает бюджеты без допустимых вытеснений. PDB без подов (expectedPods = 0) не мешает ничему.
func pdbBlocks(pdbs []policyv1.PodDisruptionBudget) ([]PDBBlock, int) {
	all := []PDBBlock{}
	for i := range pdbs {
		p := &pdbs[i]
		if p.Status.DisruptionsAllowed > 0 || p.Status.ExpectedPods == 0 {
			continue
		}
		all = append(all, PDBBlock{
			Namespace: p.Namespace, Name: p.Name,
			Healthy: p.Status.CurrentHealthy, Desired: p.Status.DesiredHealthy, Expected: p.Status.ExpectedPods,
		})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Namespace != all[j].Namespace {
			return all[i].Namespace < all[j].Namespace
		}
		return all[i].Name < all[j].Name
	})
	total := len(all)
	if len(all) > maxRiskRows {
		all = all[:maxRiskRows]
	}
	return all, total
}
