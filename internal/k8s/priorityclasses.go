// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"time"

	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PriorityClassInfo struct {
	Name  string `json:"name"`
	Value int32  `json:"value"`
	Age   string `json:"age"`

	GlobalDefault    bool              `json:"globalDefault"`
	PreemptionPolicy string            `json:"preemptionPolicy"`
	Description      string            `json:"description"`
	Created          string            `json:"created"`
	Labels           map[string]string `json:"labels"`
	ControlledBy     string            `json:"controlledBy"`
	ManagedBy        string            `json:"managedBy"`
	Annotations      map[string]string `json:"annotations"`
	Finalizers       []string          `json:"finalizers"`
}

func (m *Manager) GetPriorityClasses() ([]PriorityClassInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	list, err := conn.Client.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	pcs := make([]PriorityClassInfo, 0, len(list.Items))
	for _, pc := range list.Items {
		pcs = append(pcs, convertPriorityClass(pc))
	}

	return pcs, nil
}

func convertPriorityClass(pc schedulingv1.PriorityClass) PriorityClassInfo {
	policy := "PreemptLowerPriority" // значение по умолчанию, когда поле не задано
	if pc.PreemptionPolicy != nil {
		policy = string(*pc.PreemptionPolicy)
	}
	meta := metaOf(pc.ObjectMeta)
	return PriorityClassInfo{
		Name:             pc.Name,
		Value:            pc.Value,
		Age:              meta.Age,
		GlobalDefault:    pc.GlobalDefault,
		PreemptionPolicy: policy,
		Description:      pc.Description,
		Created:          meta.Created,
		Labels:           meta.Labels,
		ControlledBy:     controlledByOf(pc.OwnerReferences),
		ManagedBy:        managedByOf(pc.ObjectMeta),
		Annotations:      meta.Annotations,
		Finalizers:       meta.Finalizers,
	}
}
