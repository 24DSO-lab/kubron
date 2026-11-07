// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	changeKindRollout = "Rollout"
	changeKindHelm    = "Helm"
	changeKindArgo    = "Argo CD"
	maxChanges        = 15
)

// ChangeEvent — одно изменение в кластере: «что поменяли» перед тем, как что-то сломалось.
// Target — id ресурса для открытия панели деталей (пусто, если открывать нечего).
type ChangeEvent struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Detail    string `json:"detail"`
	Time      string `json:"time"` // RFC3339
	Target    string `json:"target"`

	at time.Time
}

// GetRecentChanges собирает rollout'ы Deployment, обновления Helm-релизов и синки Argo CD за последние windowMinutes
// (5 минут … 24 часа). Источник, который недоступен (нет прав, нет Argo CD), просто не даёт записей.
func (m *Manager) GetRecentChanges(windowMinutes int) ([]ChangeEvent, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	if windowMinutes < 5 {
		windowMinutes = 5
	}
	if windowMinutes > 24*60 {
		windowMinutes = 24 * 60
	}
	since := time.Now().Add(-time.Duration(windowMinutes) * time.Minute)
	out := []ChangeEvent{}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()
	if rss, err := conn.Client.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{}); err == nil {
		out = append(out, rolloutChanges(rss.Items, since)...)
	}

	if payloads, err := m.listHelmPayloads(); err == nil {
		for _, p := range payloads {
			t := p.Info.LastDeployed
			if t.IsZero() || t.Before(since) {
				continue
			}
			out = append(out, ChangeEvent{
				Kind: changeKindHelm, Name: p.Name, Namespace: p.Namespace, at: t,
				Detail: p.Chart.Metadata.Name + " " + p.Chart.Metadata.Version + " · revision " + strconv.Itoa(p.Version) + " · " + p.Info.Status,
			})
		}
	}

	if apps, err := m.GetArgoApplications(nil); err == nil {
		for _, a := range apps {
			t, err := time.Parse(time.RFC3339, a.LastSyncAt)
			if err != nil || t.Before(since) {
				continue
			}
			out = append(out, ChangeEvent{
				Kind: changeKindArgo, Name: a.Name, Namespace: a.Namespace, at: t, Target: "application",
				Detail: strings.TrimSpace(a.LastSyncPhase + " · " + shortRev(a.Revision)),
			})
		}
	}

	return finishChanges(out), nil
}

// rolloutChanges: новый ReplicaSet, принадлежащий Deployment, — это начало rollout'а (или первый запуск).
func rolloutChanges(rss []appsv1.ReplicaSet, since time.Time) []ChangeEvent {
	var out []ChangeEvent
	for i := range rss {
		rs := &rss[i]
		t := rs.CreationTimestamp.Time
		if t.Before(since) {
			continue
		}
		owner := metav1.GetControllerOf(rs)
		if owner == nil || owner.Kind != "Deployment" {
			continue
		}
		detail := "revision " + rs.Annotations["deployment.kubernetes.io/revision"]
		if len(rs.Spec.Template.Spec.Containers) > 0 {
			detail += " · " + rs.Spec.Template.Spec.Containers[0].Image
		}
		out = append(out, ChangeEvent{
			Kind: changeKindRollout, Name: owner.Name, Namespace: rs.Namespace, Detail: detail, at: t, Target: "deployment",
		})
	}
	return out
}

// finishChanges сортирует (новые первыми), ограничивает список и проставляет Time.
func finishChanges(list []ChangeEvent) []ChangeEvent {
	sort.SliceStable(list, func(i, j int) bool { return list[i].at.After(list[j].at) })
	if len(list) > maxChanges {
		list = list[:maxChanges]
	}
	for i := range list {
		list[i].Time = list[i].at.UTC().Format(time.RFC3339)
	}
	if list == nil {
		list = []ChangeEvent{}
	}
	return list
}

func shortRev(rev string) string {
	if len(rev) == 40 { // git sha
		return rev[:7]
	}
	return rev
}
