// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// Event — нормализованный Kubernetes Event. Message — сырой текст кластера: слой сервиса редактирует его
// (ADR-0022) до показа и сохранения.
type Event struct {
	Type      string      `json:"type"`
	Reason    string      `json:"reason"`
	Message   string      `json:"message"`
	Count     int32       `json:"count"`
	FirstSeen time.Time   `json:"firstSeen"`
	LastSeen  time.Time   `json:"lastSeen"`
	Object    ResourceRef `json:"object"`
	Component string      `json:"component,omitempty"`
}

// LogCapture — сырой лог контейнера, прочитанный для диагностики. Живёт только в памяти операции: в Report и
// в хранилище попадает лишь отредактированный ограниченный фрагмент (internal/incident).
type LogCapture struct {
	Pod       ResourceRef
	Container string
	Previous  bool
	Raw       []byte
}

// Context — нормализованный контекст диагностики. Поля — типизированные объекты Kubernetes, разобранные из
// unstructured без строгой проверки: неизвестные поля игнорируются, отсутствующие остаются нулевыми.
type Context struct {
	Target      ResourceRef
	ObservedAt  time.Time
	Pods        []*corev1.Pod
	Deployment  *appsv1.Deployment
	ReplicaSets []*appsv1.ReplicaSet
	Namespace   *corev1.Namespace
	Nodes       map[string]*corev1.Node
	Events      []Event
	Logs        []LogCapture
	Missing     []MissingContext
	Partial     []string

	observed map[ResourceRef]time.Time
}

// observedAt — когда объект был прочитан; для неизвестного объекта — время начала сбора.
func (c *Context) observedAt(ref ResourceRef) time.Time {
	if t, ok := c.observed[ref]; ok {
		return t
	}
	return c.ObservedAt
}

func (c *Context) markObserved(ref ResourceRef, t time.Time) {
	if c.observed == nil {
		c.observed = make(map[ResourceRef]time.Time)
	}
	c.observed[ref] = t
}

func (c *Context) addMissing(code string, ref ResourceRef, params map[string]string) {
	c.Missing = append(c.Missing, MissingContext{Code: code, Resource: ref, Params: params})
}

func (c *Context) addPartial(s string) {
	if !slices.Contains(c.Partial, s) {
		c.Partial = append(c.Partial, s)
	}
}

// Ссылки на объекты — единый способ строить ResourceRef.
func podRef(p *corev1.Pod) ResourceRef {
	return ResourceRef{Kind: "Pod", Namespace: p.Namespace, Name: p.Name, UID: string(p.UID)}
}

func rsRef(r *appsv1.ReplicaSet) ResourceRef {
	return ResourceRef{Kind: "ReplicaSet", Namespace: r.Namespace, Name: r.Name, UID: string(r.UID)}
}

func depRef(d *appsv1.Deployment) ResourceRef {
	return ResourceRef{Kind: "Deployment", Namespace: d.Namespace, Name: d.Name, UID: string(d.UID)}
}

// decode разбирает unstructured в типизированный объект. Нестрогий разбор: лишние поля не ошибка; несовпадение
// типа поля — ошибка, которую вызывающий превращает в MissingContext.
func decode[T any](u *unstructured.Unstructured) (*T, error) {
	if u == nil {
		return nil, fmt.Errorf("decode: nil object")
	}
	var out T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", u.GetKind(), err)
	}
	return &out, nil
}

// NormalizeEvent приводит core/v1 Event к Event. Время — последнее известное из полей, которые заполняют разные
// версии API (series, lastTimestamp, eventTime, firstTimestamp, creationTimestamp).
func NormalizeEvent(e *corev1.Event) Event {
	if e == nil {
		return Event{}
	}
	first := firstNonZero(e.FirstTimestamp.Time, e.EventTime.Time, e.CreationTimestamp.Time)
	last := firstNonZero(seriesTime(e), e.LastTimestamp.Time, e.EventTime.Time, e.FirstTimestamp.Time, e.CreationTimestamp.Time)
	count := e.Count
	if e.Series != nil && e.Series.Count > count {
		count = e.Series.Count
	}
	if count <= 0 {
		count = 1
	}
	if first.IsZero() {
		first = last
	}
	comp := e.Source.Component
	if comp == "" {
		comp = e.ReportingController
	}
	return Event{
		Type: e.Type, Reason: e.Reason, Message: e.Message, Count: count,
		FirstSeen: first, LastSeen: last, Component: comp,
		Object: ResourceRef{Kind: e.InvolvedObject.Kind, Namespace: e.InvolvedObject.Namespace,
			Name: e.InvolvedObject.Name, UID: string(e.InvolvedObject.UID)},
	}
}

func seriesTime(e *corev1.Event) time.Time {
	if e.Series == nil {
		return time.Time{}
	}
	return e.Series.LastObservedTime.Time
}

func firstNonZero(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t.UTC()
		}
	}
	return time.Time{}
}

// sortEvents упорядочивает события по времени (от старых к новым), стабильно по причине и объекту.
func sortEvents(ev []Event) {
	slices.SortStableFunc(ev, func(a, b Event) int {
		return cmp.Or(a.LastSeen.Compare(b.LastSeen), cmp.Compare(a.Reason, b.Reason), cmp.Compare(a.Object.Name, b.Object.Name))
	})
}
