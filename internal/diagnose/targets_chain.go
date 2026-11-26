// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Цели этапа 1 ADR-0031 §4–§6: Service, PersistentVolumeClaim и Node. Базовый сбор здесь минимален: сама цель, поды,
// которых она касается, и события. Всё остальное (владельцы, PV и StorageClass, Endpoints, узлы) приходит из графа
// (хук сбора 100, graph_hook.go), а цепочечные правила читают его, а не разбирают текст.
func init() {
	RegisterTarget("Service", collectService)
	RegisterTarget("PersistentVolumeClaim", collectPVC)
	RegisterTarget("Node", collectNode)
}

func newCollector(r Reader, target ResourceRef, o CollectOptions) *collector {
	return &collector{r: r, opts: o, c: &Context{Target: target, ObservedAt: o.now(), Nodes: map[string]*corev1.Node{}}}
}

// collectService читает Service и поды по его селектору: нездоровые поды объясняют пустые endpoints так же, как
// для Deployment, поэтому к ним применяются те же правила подов.
func collectService(ctx context.Context, r Reader, target ResourceRef, o CollectOptions) (*Context, error) {
	if target.Namespace == "" {
		return nil, fmt.Errorf("target namespace is empty: %w", ErrNotFound)
	}
	col := newCollector(r, target, o)
	svc, err := primary[corev1.Service](ctx, col, gvkService, target.Namespace, target.Name)
	if err != nil {
		return nil, err
	}
	col.c.Target.UID = string(svc.UID)
	if sel := labels.SelectorFromSet(svc.Spec.Selector).String(); sel == "" {
		// Service без селектора (ExternalName, ручные Endpoints) не выбирает поды: это не ошибка, но объяснять нечем.
		col.c.addMissing("service.selector_missing", col.c.Target, nil)
	} else {
		col.listPods(ctx, target.Namespace, sel)
	}
	if err := col.readCommon(ctx); err != nil {
		return nil, err
	}
	return col.c, nil
}

// collectPVC читает PVC и поды, которые его монтируют: их события (FailedMount, FailedAttachVolume) объясняют
// проблему тома со стороны потребителя.
func collectPVC(ctx context.Context, r Reader, target ResourceRef, o CollectOptions) (*Context, error) {
	if target.Namespace == "" {
		return nil, fmt.Errorf("target namespace is empty: %w", ErrNotFound)
	}
	col := newCollector(r, target, o)
	pvc, err := primary[corev1.PersistentVolumeClaim](ctx, col, gvkPVC, target.Namespace, target.Name)
	if err != nil {
		return nil, err
	}
	col.c.Target.UID = string(pvc.UID)
	col.listPodsWhere(ctx, ListOptions{Namespace: target.Namespace, Limit: maxListObjects},
		func(p *corev1.Pod) bool { return mountsClaim(p, target.Name) })
	if err := col.readCommon(ctx); err != nil {
		return nil, err
	}
	return col.c, nil
}

func mountsClaim(p *corev1.Pod, claim string) bool {
	for _, v := range p.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claim {
			return true
		}
	}
	return false
}

// nodeEventsLimit — сколько событий узла читается: события узла редки, а область чтения — весь кластер.
const nodeEventsLimit = 200

// collectNode читает Node, поды на нём (по всем namespace) и события узла. Node — единственная цель без namespace:
// заданный namespace игнорируется, чтобы отчёт не зависел от ошибки вызывающего. Логи подов узла не
// читаются, а события подов других namespace — тоже: это решение по объёму чтения, правила сообщают
// о нехватке через Missing.
func collectNode(ctx context.Context, r Reader, target ResourceRef, o CollectOptions) (*Context, error) {
	target.Namespace = ""
	col := newCollector(r, target, o)
	node, err := primary[corev1.Node](ctx, col, gvkNode, "", target.Name)
	if err != nil {
		return nil, err
	}
	col.c.Target.UID = string(node.UID)
	col.c.Nodes[target.Name] = node
	col.listPodsWhere(ctx, ListOptions{FieldSelector: "spec.nodeName=" + target.Name, Limit: maxListObjects},
		func(*corev1.Pod) bool { return true })
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	col.readNodeEvents(ctx)
	return col.c, nil
}

// readNodeEvents читает события, относящиеся к узлу-цели. События узла лежат в namespace, который выбрал их автор
// (обычно default), поэтому выборка идёт по всему кластеру с полевым селектором.
func (col *collector) readNodeEvents(ctx context.Context) {
	c := col.c
	res, err := col.r.List(ctx, gvkEvent, ListOptions{
		FieldSelector: "involvedObject.kind=Node,involvedObject.name=" + c.Target.Name, Limit: nodeEventsLimit})
	if err != nil {
		c.addMissing("events.unavailable", c.Target, map[string]string{"reason": errorReason(err)})
		return
	}
	if res.Truncated {
		c.addPartial("Event")
		c.addMissing("events.truncated", c.Target, nil)
	}
	for i := range res.Items {
		e, err := decode[corev1.Event](&res.Items[i])
		if err != nil {
			continue
		}
		ev := NormalizeEvent(e)
		c.eventPool = append(c.eventPool, ev)
		if ev.Object.Kind == "Node" && ev.Object.Name == c.Target.Name {
			c.Events = append(c.Events, ev)
		}
	}
	sortEvents(c.Events)
	for _, r := range c.eventRefs() {
		c.markObserved(r, res.ObservedAt)
	}
}
