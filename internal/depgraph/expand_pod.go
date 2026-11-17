// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// podRefs — ссылки Pod'а на ConfigMap и Secret (volumes, envFrom, env.valueFrom, projected); ключ — имя,
// значение — optional.
type podRefs struct{ configMaps, secrets map[string]bool }

func collectRefs(p *corev1.Pod) podRefs {
	r := podRefs{map[string]bool{}, map[string]bool{}}
	add := func(m map[string]bool, name string, optional *bool) {
		if name == "" {
			return
		}
		opt := optional != nil && *optional
		if old, seen := m[name]; seen {
			opt = opt && old
		}
		m[name] = opt
	}
	for _, v := range p.Spec.Volumes {
		if v.Secret != nil {
			add(r.secrets, v.Secret.SecretName, v.Secret.Optional)
		}
		if v.ConfigMap != nil {
			add(r.configMaps, v.ConfigMap.Name, v.ConfigMap.Optional)
		}
		if v.Projected != nil {
			for _, s := range v.Projected.Sources {
				if s.Secret != nil {
					add(r.secrets, s.Secret.Name, s.Secret.Optional)
				}
				if s.ConfigMap != nil {
					add(r.configMaps, s.ConfigMap.Name, s.ConfigMap.Optional)
				}
			}
		}
	}
	for _, list := range [][]corev1.Container{p.Spec.InitContainers, p.Spec.Containers} {
		for _, c := range list {
			for _, ef := range c.EnvFrom {
				if ef.SecretRef != nil {
					add(r.secrets, ef.SecretRef.Name, ef.SecretRef.Optional)
				}
				if ef.ConfigMapRef != nil {
					add(r.configMaps, ef.ConfigMapRef.Name, ef.ConfigMapRef.Optional)
				}
			}
			for _, e := range c.Env {
				if e.ValueFrom == nil {
					continue
				}
				if s := e.ValueFrom.SecretKeyRef; s != nil {
					add(r.secrets, s.Name, s.Optional)
				}
				if c := e.ValueFrom.ConfigMapKeyRef; c != nil {
					add(r.configMaps, c.Name, c.Optional)
				}
			}
		}
	}
	return r
}

// claimNames — PVC, которые использует Pod: persistentVolumeClaim и generic ephemeral (<pod>-<volume>).
func claimNames(p *corev1.Pod) []string {
	set := map[string]bool{}
	for _, v := range p.Spec.Volumes {
		switch {
		case v.PersistentVolumeClaim != nil:
			set[v.PersistentVolumeClaim.ClaimName] = true
		case v.Ephemeral != nil:
			set[p.Name+"-"+v.Name] = true
		}
	}
	return sortedKeys(set)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (b *builder) expandPod(ctx context.Context, it item, n Node) {
	var pod corev1.Pod
	if !decode(n.Object, &pod) {
		return
	}
	b.expandOwners(ctx, it, n)
	d := it.depth + 1

	// Node, на которой запущен Pod.
	if pod.Spec.NodeName != "" && b.reach(d) {
		nn, _ := b.get(ctx, specNode, "", pod.Spec.NodeName)
		b.edge(n.Ref, nn.Ref, EdgeRunsOn)
		b.enqueue(nn.Ref, d, true)
	}

	// PVC: для нескольких — один List namespace вместо серии Get.
	if claims := claimNames(&pod); len(claims) > 0 && b.reach(d) {
		byName := map[string]bool{}
		if len(claims) > 1 {
			if res, err := b.list(ctx, specPVC, ListOptions{Namespace: n.Namespace}); err == nil {
				for i := range res.Items {
					byName[res.Items[i].GetName()] = true
					if slices.Contains(claims, res.Items[i].GetName()) {
						b.addObject(specPVC, &res.Items[i])
					}
				}
				if !res.Truncated { // полный список: отсутствующих в нём не существует
					for _, c := range claims {
						if !byName[c] {
							miss := specPVC.ref(n.Namespace, c)
							b.gets[miss.Key()] = ErrNotFound
							b.putNode(b.failureNode(miss, ErrNotFound))
						}
					}
				}
			}
		}
		for _, c := range claims {
			pn, _ := b.get(ctx, specPVC, n.Namespace, c) // из графа или кэша; Get только если не нашли списком
			b.edge(n.Ref, pn.Ref, EdgeMounts)
			b.enqueue(pn.Ref, d, false)
		}
	}

	// Service, выбирающие этот Pod.
	if b.reach(d) {
		if res, err := b.list(ctx, specService, ListOptions{Namespace: n.Namespace}); err == nil {
			found := 0
			for _, s := range sortedItems(res.Items) {
				sel, _, _ := nestedSelector(&s)
				if len(sel) == 0 || !labels.SelectorFromSet(sel).Matches(labels.Set(pod.Labels)) {
					continue
				}
				if found++; found > maxServices {
					b.g.SetTruncated("limit.services")
					break
				}
				sn, ok := b.addObject(specService, &s)
				if !ok {
					break
				}
				b.edge(sn.Ref, n.Ref, EdgeSelects)
				b.enqueue(sn.Ref, d, false)
			}
		}
	}

	// ConfigMap и Secret.
	refs := collectRefs(&pod)
	if len(refs.secrets)+len(refs.configMaps) > 0 && b.reach(d) {
		for _, name := range sortedKeys(refs.secrets) {
			sref := Ref{Kind: "Secret", Namespace: n.Namespace, Name: name}
			if b.putNode(Node{Ref: sref, Health: Unknown, Unknown: UnknownPolicy}) { // Secret не читается никогда
				b.edge(n.Ref, sref, EdgeMounts)
			}
		}
		if b.o.ExpandConfigMapRefs {
			for _, name := range sortedKeys(refs.configMaps) {
				cn, obj := b.get(ctx, specConfigMap, n.Namespace, name)
				if obj == nil && cn.Placeholder && refs.configMaps[name] {
					delete(b.g.nodes, cn.Key()) // optional и отсутствует — не ошибка
					continue
				}
				b.edge(n.Ref, cn.Ref, EdgeMounts)
			}
		}
	}
}
