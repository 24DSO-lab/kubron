// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"time"

	nodesv1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RuntimeClassInfo struct {
	Name    string `json:"name"`
	Handler string `json:"handler"`
	Age     string `json:"age"`

	Overhead     map[string]string `json:"overhead"` // scheduling.podFixed: ресурс -> значение
	NodeSelector map[string]string `json:"nodeSelector"`
	Tolerations  []TolerationInfo  `json:"tolerations"`
	Created      string            `json:"created"`
	Labels       map[string]string `json:"labels"`
	ControlledBy string            `json:"controlledBy"`
	ManagedBy    string            `json:"managedBy"`
	Annotations  map[string]string `json:"annotations"`
	Finalizers   []string          `json:"finalizers"`
}

func (m *Manager) GetRuntimeClasses() ([]RuntimeClassInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	list, err := conn.Client.NodeV1().RuntimeClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	rcs := make([]RuntimeClassInfo, 0, len(list.Items))
	for _, rc := range list.Items {
		rcs = append(rcs, convertRuntimeClass(rc))
	}

	return rcs, nil
}

func convertRuntimeClass(rc nodesv1.RuntimeClass) RuntimeClassInfo {
	overhead := map[string]string{}
	if rc.Overhead != nil {
		for name, q := range rc.Overhead.PodFixed {
			overhead[string(name)] = q.String()
		}
	}

	var nodeSelector map[string]string
	tolerations := make([]TolerationInfo, 0)
	if rc.Scheduling != nil {
		nodeSelector = rc.Scheduling.NodeSelector
		for _, t := range rc.Scheduling.Tolerations {
			tolerations = append(tolerations, TolerationInfo{
				Key:               t.Key,
				Operator:          string(t.Operator),
				Value:             t.Value,
				Effect:            string(t.Effect),
				TolerationSeconds: t.TolerationSeconds,
			})
		}
	}

	meta := metaOf(rc.ObjectMeta)
	return RuntimeClassInfo{
		Name:         rc.Name,
		Handler:      rc.Handler,
		Age:          meta.Age,
		Overhead:     overhead,
		NodeSelector: nodeSelector,
		Tolerations:  tolerations,
		Created:      meta.Created,
		Labels:       meta.Labels,
		ControlledBy: controlledByOf(rc.OwnerReferences),
		ManagedBy:    managedByOf(rc.ObjectMeta),
		Annotations:  meta.Annotations,
		Finalizers:   meta.Finalizers,
	}
}
