// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const defaultIngressClassAnnotation = "ingressclass.kubernetes.io/is-default-class"

// IngressRef — Ingress, использующий класс. Implicit: класс не указан, Ingress попал в default.
type IngressRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Implicit  bool   `json:"implicit"`
}

type IngressClassInfo struct {
	Name            string            `json:"name"`
	Controller      string            `json:"controller"`
	Default         bool              `json:"default"`
	ParamsAPIGroup  string            `json:"paramsApiGroup"`
	ParamsKind      string            `json:"paramsKind"`
	ParamsName      string            `json:"paramsName"`
	ParamsScope     string            `json:"paramsScope"`
	ParamsNamespace string            `json:"paramsNamespace"`
	UsedBy          []IngressRef      `json:"usedBy"`
	Age             string            `json:"age"`
	Created         string            `json:"created"`
	Labels          map[string]string `json:"labels"`
	Annotations     map[string]string `json:"annotations"`
}

// SetDefaultIngressClass делает класс default (makeDefault=true) или снимает признак (false).
// Default-класс в кластере должен быть один, поэтому у остальных признак снимается первым.
func (m *Manager) SetDefaultIngressClass(name string, makeDefault bool) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	ics := conn.Client.NetworkingV1().IngressClasses()
	patch := func(n string, on bool) error {
		val := "null" // merge patch: null удаляет аннотацию
		if on {
			val = `"true"`
		}
		body := fmt.Sprintf(`{"metadata":{"annotations":{%q:%s}}}`, defaultIngressClassAnnotation, val)
		if _, err := ics.Patch(ctx, n, types.MergePatchType, []byte(body), metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to update IngressClass %s: %w", n, err)
		}
		return nil
	}

	list, err := ics.List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	found := false
	var others []string
	for _, ic := range list.Items {
		if ic.Name == name {
			found = true
		} else if ic.Annotations[defaultIngressClassAnnotation] == "true" {
			others = append(others, ic.Name)
		}
	}
	if !found {
		return fmt.Errorf("IngressClass %s not found", name)
	}
	if makeDefault {
		for _, n := range others {
			if err := patch(n, false); err != nil {
				return err
			}
		}
	}
	return patch(name, makeDefault)
}

func (m *Manager) GetIngressClasses() ([]IngressClassInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	list, err := conn.Client.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// Ingress по классам. Нет прав на Ingress — список классов всё равно отдаём.
	usedBy := map[string][]IngressRef{}
	var noClass []IngressRef
	if ings, err := conn.Client.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{}); err == nil {
		for i := range ings.Items {
			ing := &ings.Items[i]
			ref := IngressRef{Namespace: ing.Namespace, Name: ing.Name}
			if c := ingressClassOf(ing); c != "" {
				usedBy[c] = append(usedBy[c], ref)
			} else {
				ref.Implicit = true
				noClass = append(noClass, ref)
			}
		}
	}
	defaults := 0
	for _, ic := range list.Items {
		if ic.Annotations[defaultIngressClassAnnotation] == "true" {
			defaults++
		}
	}

	items := make([]IngressClassInfo, 0, len(list.Items))
	for _, ic := range list.Items {
		info := IngressClassInfo{
			Name:        ic.Name,
			Controller:  ic.Spec.Controller,
			Default:     ic.Annotations[defaultIngressClassAnnotation] == "true",
			Age:         formatAge(ic.CreationTimestamp.Time),
			Created:     ic.CreationTimestamp.Format(time.RFC3339),
			Labels:      ic.Labels,
			Annotations: ic.Annotations,
		}
		if p := ic.Spec.Parameters; p != nil {
			info.ParamsKind, info.ParamsName = p.Kind, p.Name
			if p.APIGroup != nil {
				info.ParamsAPIGroup = *p.APIGroup
			}
			if p.Scope != nil {
				info.ParamsScope = *p.Scope
			}
			if p.Namespace != nil {
				info.ParamsNamespace = *p.Namespace
			}
		}

		refs := append([]IngressRef{}, usedBy[ic.Name]...)
		if info.Default && defaults == 1 {
			refs = append(refs, noClass...)
		}
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Namespace != refs[j].Namespace {
				return refs[i].Namespace < refs[j].Namespace
			}
			return refs[i].Name < refs[j].Name
		})
		info.UsedBy = refs

		items = append(items, info)
	}
	return items, nil
}
