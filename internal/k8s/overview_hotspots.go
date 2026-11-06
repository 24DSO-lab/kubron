// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const hotspotsTop = 3

// HotPod — под, ближе всего подошедший к своему limit. Percent — доля от limit (может быть > 100 для CPU).
type HotPod struct {
	Namespace string  `json:"namespace"`
	Name      string  `json:"name"`
	Used      float64 `json:"used"` // ядра для CPU, байты для памяти
	Percent   float64 `json:"percent"`
}

// HotVolume — PVC с наибольшей заполненностью (метрики отдаёт kubelet, только для смонтированных томов).
type HotVolume struct {
	Namespace string  `json:"namespace"`
	Claim     string  `json:"claim"`
	Used      float64 `json:"used"`
	Capacity  float64 `json:"capacity"`
	Percent   float64 `json:"percent"`
}

// OverviewHotspots — «где болит»: топ подов по близости к limit и самые заполненные тома.
type OverviewHotspots struct {
	TopMemory  []HotPod    `json:"topMemory"`
	TopCPU     []HotPod    `json:"topCpu"`
	Volumes    []HotVolume `json:"volumes"`
	PVCPending int         `json:"pvcPending"`
	PVCLost    int         `json:"pvcLost"`
}

type promSample struct {
	Labels map[string]string
	Value  float64
}

// promVector выполняет instant-запрос и возвращает все серии; при ошибке — пустой срез.
func (m *Manager) promVector(query string) []promSample {
	res, err := m.QueryPrometheus(query)
	if err != nil || res == nil || res.Data == nil {
		return nil
	}
	var data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(res.Data, &data) != nil {
		return nil
	}
	out := make([]promSample, 0, len(data.Result))
	for _, r := range data.Result {
		if len(r.Value) < 2 {
			continue
		}
		raw, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		out = append(out, promSample{Labels: r.Metric, Value: f})
	}
	return out
}

// Запросы. Под без limit выпадает из деления (нет пары в on(...)), поэтому в топе только поды с limit:
// абсолютное «больше всех» почти всегда одни и те же тяжёлые поды и ничего не подсказывает.
const (
	promPodMemUsed  = `sum by (namespace, pod) (container_memory_working_set_bytes{container!="",container!="POD"})`
	promPodMemLimit = `sum by (namespace, pod) (kube_pod_container_resource_limits{resource="memory"})`
	promPodCPUUsed  = `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",container!="POD"}[5m]))`
	promPodCPULimit = `sum by (namespace, pod) (kube_pod_container_resource_limits{resource="cpu"})`
)

func podRatioQuery(used, limit string) string {
	return "topk(3, (" + used + ") / on (namespace, pod) (" + limit + "))"
}

func podUsedQuery(used, limit string) string {
	return "(" + used + ") and on (namespace, pod) " + podRatioQuery(used, limit)
}

func (m *Manager) topPods(used, limit string) []HotPod {
	var ratio, usage []promSample
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ratio = m.promVector(podRatioQuery(used, limit)) }()
	go func() { defer wg.Done(); usage = m.promVector(podUsedQuery(used, limit)) }()
	wg.Wait()
	return buildHotPods(ratio, usage)
}

// buildHotPods склеивает долю от limit и абсолютное использование по паре namespace/pod.
func buildHotPods(ratio, usage []promSample) []HotPod {
	used := make(map[string]float64, len(usage))
	for _, s := range usage {
		used[s.Labels["namespace"]+"/"+s.Labels["pod"]] = s.Value
	}
	out := make([]HotPod, 0, len(ratio))
	for _, s := range ratio {
		ns, name := s.Labels["namespace"], s.Labels["pod"]
		if name == "" {
			continue
		}
		out = append(out, HotPod{Namespace: ns, Name: name, Used: used[ns+"/"+name], Percent: s.Value * 100})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Percent > out[j].Percent })
	if len(out) > hotspotsTop {
		out = out[:hotspotsTop]
	}
	return out
}

func (m *Manager) topVolumes() []HotVolume {
	const used, capacity = `kubelet_volume_stats_used_bytes`, `kubelet_volume_stats_capacity_bytes`
	ratioQ := "topk(3, " + used + " / on (namespace, persistentvolumeclaim) " + capacity + ")"
	capQ := capacity + " and on (namespace, persistentvolumeclaim) " + ratioQ

	var ratio, caps []promSample
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ratio = m.promVector(ratioQ) }()
	go func() { defer wg.Done(); caps = m.promVector(capQ) }()
	wg.Wait()
	return buildHotVolumes(ratio, caps)
}

func buildHotVolumes(ratio, caps []promSample) []HotVolume {
	capByKey := make(map[string]float64, len(caps))
	for _, s := range caps {
		capByKey[s.Labels["namespace"]+"/"+s.Labels["persistentvolumeclaim"]] = s.Value
	}
	out := make([]HotVolume, 0, len(ratio))
	for _, s := range ratio {
		ns, claim := s.Labels["namespace"], s.Labels["persistentvolumeclaim"]
		if claim == "" {
			continue
		}
		c := capByKey[ns+"/"+claim]
		out = append(out, HotVolume{Namespace: ns, Claim: claim, Capacity: c, Used: s.Value * c, Percent: s.Value * 100})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Percent > out[j].Percent })
	if len(out) > hotspotsTop {
		out = out[:hotspotsTop]
	}
	return out
}

// GetOverviewHotspots собирает топ подов по близости к limit и самые заполненные PVC.
// Требует стек метрик; счётчики PVC Pending/Lost берутся из API и работают без него.
func (m *Manager) GetOverviewHotspots(withMetrics bool) (*OverviewHotspots, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	h := &OverviewHotspots{TopMemory: []HotPod{}, TopCPU: []HotPod{}, Volumes: []HotVolume{}}

	var wg sync.WaitGroup
	// Без стека метрик Prometheus не опрашиваем: счётчики PVC берутся из API и работают всегда.
	if withMetrics {
		wg.Add(3)
		go func() { defer wg.Done(); h.TopMemory = m.topPods(promPodMemUsed, promPodMemLimit) }()
		go func() { defer wg.Done(); h.TopCPU = m.topPods(promPodCPUUsed, promPodCPULimit) }()
		go func() { defer wg.Done(); h.Volumes = m.topVolumes() }()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
		defer cancel()
		if l, err := conn.Client.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{}); err == nil {
			for i := range l.Items {
				switch l.Items[i].Status.Phase {
				case corev1.ClaimPending:
					h.PVCPending++
				case corev1.ClaimLost:
					h.PVCLost++
				}
			}
		}
	}()
	wg.Wait()

	if h.TopMemory == nil {
		h.TopMemory = []HotPod{}
	}
	if h.TopCPU == nil {
		h.TopCPU = []HotPod{}
	}
	if h.Volumes == nil {
		h.Volumes = []HotVolume{}
	}
	return h, nil
}
