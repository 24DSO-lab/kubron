// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DeploymentInfo struct {
	Name            string               `json:"name"`
	Namespace       string               `json:"namespace"`
	Replicas        int32                `json:"replicas"`
	ReadyReplicas   int32                `json:"readyReplicas"`
	UpdatedReplicas int32                `json:"updatedReplicas"`
	Age             string               `json:"age"`
	Created         string               `json:"created"`
	Labels          map[string]string    `json:"labels"`
	Annotations     map[string]string    `json:"annotations"`
	Strategy        string               `json:"strategy"`
	Selector        string               `json:"selector"`
	NodeSelector    string               `json:"nodeSelector"`
	Images          []string             `json:"images"`
	Conditions      []ConditionInfo      `json:"conditions"`
	PodAntiAffinity *PodAntiAffinityInfo `json:"podAntiAffinity"`
	Revisions       []RevisionInfo       `json:"revisions"`
}

type PodAntiAffinityInfo struct {
	PreferredDuringSchedulingIgnoredDuringExecution []PreferredAffinityTermInfo `json:"preferredDuringSchedulingIgnoredDuringExecution"`
	RequiredDuringSchedulingIgnoredDuringExecution  []RequiredAffinityTermInfo  `json:"requiredDuringSchedulingIgnoredDuringExecution"`
}

type PreferredAffinityTermInfo struct {
	Weight          int32               `json:"weight"`
	PodAffinityTerm PodAffinityTermInfo `json:"podAffinityTerm"`
}

type RequiredAffinityTermInfo struct {
	PodAffinityTerm PodAffinityTermInfo `json:"podAffinityTerm"`
}

type PodAffinityTermInfo struct {
	LabelSelector *LabelSelectorInfo `json:"labelSelector"`
	TopologyKey   string             `json:"topologyKey"`
	Namespaces    []string           `json:"namespaces"`
}

type LabelSelectorInfo struct {
	MatchLabels map[string]string `json:"matchLabels"`
}

func (m *Manager) GetDeployments(namespaces []string) ([]DeploymentInfo, error) {
	return scopedList(m, "GetDeployments", "apps", "deployments", namespaces, listDeployments,
		func(it DeploymentInfo) string { return it.Namespace })
}

// listDeployments читает ресурсы одного namespace ("" — все) для GetDeployments.
func listDeployments(ctx context.Context, conn *ClusterConnection, namespace string) ([]DeploymentInfo, error) {
	depList, err := conn.Client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var deployments []DeploymentInfo
	for _, dep := range depList.Items {
		var images []string
		for _, c := range dep.Spec.Template.Spec.Containers {
			images = append(images, c.Image)
		}

		var replicas int32 = 0
		if dep.Spec.Replicas != nil {
			replicas = *dep.Spec.Replicas
		}

		// Convert conditions
		var conditions []ConditionInfo
		for _, cond := range dep.Status.Conditions {
			conditions = append(conditions, ConditionInfo{
				Type:               string(cond.Type),
				Status:             string(cond.Status),
				LastTransitionTime: cond.LastTransitionTime.Format(time.RFC3339),
				Reason:             cond.Reason,
				Message:            cond.Message,
			})
		}

		// Build selector string from MatchLabels
		selectorStr := formatMatchLabels(dep.Spec.Selector)

		// Build node selector string
		nodeSelectorStr := ""
		if len(dep.Spec.Template.Spec.NodeSelector) > 0 {
			first := true
			for k, v := range dep.Spec.Template.Spec.NodeSelector {
				if !first {
					nodeSelectorStr += ", "
				}
				nodeSelectorStr += k + "=" + v
				first = false
			}
		}

		// Build pod anti-affinity info
		var podAntiAffinity *PodAntiAffinityInfo
		if dep.Spec.Template.Spec.Affinity != nil && dep.Spec.Template.Spec.Affinity.PodAntiAffinity != nil {
			paa := dep.Spec.Template.Spec.Affinity.PodAntiAffinity
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

		deployments = append(deployments, DeploymentInfo{
			Name:            dep.Name,
			Namespace:       dep.Namespace,
			Replicas:        replicas,
			ReadyReplicas:   dep.Status.ReadyReplicas,
			UpdatedReplicas: dep.Status.UpdatedReplicas,
			Age:             formatAge(dep.CreationTimestamp.Time),
			Created:         dep.CreationTimestamp.Format(time.RFC3339),
			Labels:          dep.Labels,
			Annotations:     dep.Annotations,
			Strategy:        string(dep.Spec.Strategy.Type),
			Selector:        selectorStr,
			NodeSelector:    nodeSelectorStr,
			Images:          images,
			Conditions:      conditions,
			PodAntiAffinity: podAntiAffinity,
			Revisions:       nil,
		})
	}

	return deployments, nil
}

func (m *Manager) ScaleDeployment(namespace, name string, replicas int32) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	scale, err := conn.Client.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get scale: %w", err)
	}

	scale.Spec.Replicas = replicas

	_, err = conn.Client.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale: %w", err)
	}

	return nil
}
