// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"cmp"
	"slices"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Informer-кэш объектов Flux (ADR-0023, раздел 5). Это тонкий слой над обобщённым crdCache[T] и реестром кэшей
// подключения из 0.1 (client.go не меняется): здесь только то, что относится ко Flux — сортировка списка, очистка
// объектов перед сохранением в памяти и привязка события flux:changed к подключению.
//
// Что наследуется от crdCache и реестра:
//   - informer стартует лениво, при первом чтении вида (не при подключении), по одному на GVR: Kustomization и
//     HelmRelease — при первом чтении списков, источники, Notification и Image — при первом обращении к
//     соответствующему виду;
//   - перед стартом проба права list; нет права или CRD — кэша нет, чтение идёт через API, повтор пробы не раньше
//     чем через crdProbeRetry (30 с); отказ по одному виду не блокирует остальные;
//   - debounce уведомлений crdDefaultDebounce (1 с): серия изменений даёт одно событие;
//   - остановка всех кэшей в ClusterConnection.Close.

// fluxCache — кэш объектов одного вида Flux: экземпляр crdCache[T] с разбором через parse вызывающего.
type fluxCache[T any] struct {
	*crdCache[T]
}

// newFluxCache запускает informer вида. Scrub убирает из объектов то, чему не место в памяти процесса
// (см. fluxScrub); OnChange вызывается не чаще раза за crdDefaultDebounce.
func newFluxCache[T any](dyn dynamic.Interface, kind fluxKind, gvr schema.GroupVersionResource,
	parse func(*unstructured.Unstructured) T, onChange func(),
) (*fluxCache[T], error) {
	c, err := newCRDCache(dyn, crdCacheConfig[T]{
		GVR:      gvr,
		Parse:    parse,
		Scrub:    fluxScrub(kind.Kind),
		OnChange: onChange,
	})
	if err != nil {
		return nil, err
	}
	return &fluxCache[T]{crdCache: c}, nil
}

// fluxScrub возвращает очистку объектов вида для SetTransform informer (managedFields crdCache убирает сам).
//
// У всех видов удаляется аннотация kubectl.kubernetes.io/last-applied-configuration: в ней лежит копия манифеста,
// в том числе поля, которые Kubron не отдаёт наружу (postBuild.substitute, values, адреса вебхуков). У HelmRelease
// дополнительно удаляется spec.values: значения чарта (пароли, ключи) не должны лежать в памяти процесса. Парсеры
// эти поля не читают; правка YAML читает объект напрямую через API, не из кэша.
func fluxScrub(kind string) func(*unstructured.Unstructured) {
	return func(u *unstructured.Unstructured) {
		if ann := u.GetAnnotations(); ann != nil {
			if _, ok := ann[fluxLastAppliedAnnotation]; ok {
				delete(ann, fluxLastAppliedAnnotation)
				u.SetAnnotations(ann) // пустые аннотации SetAnnotations убирает совсем
			}
		}
		if kind == FluxKindHelmRelease {
			unstructured.RemoveNestedField(u.Object, "spec", "values")
		}
	}
}

// list возвращает объекты из кэша для набора namespace (пустой набор — все) в порядке «namespace, имя» — как их
// отдаёт API, — разобранные через parse. Фильтр по namespace применяется до разбора: чужие объекты не разбираются.
// false — informer ещё не синхронизирован, вызывающий читает через API.
func (c *fluxCache[T]) list(scope nsScope) ([]T, bool) {
	if !c.inf.HasSynced() {
		return nil, false
	}
	objs := c.inf.GetStore().List()
	matched := make([]*unstructured.Unstructured, 0, len(objs))
	for _, o := range objs {
		if u, ok := o.(*unstructured.Unstructured); ok && scope.has(u.GetNamespace()) {
			matched = append(matched, u)
		}
	}
	slices.SortFunc(matched, func(a, b *unstructured.Unstructured) int {
		return cmp.Or(cmp.Compare(a.GetNamespace(), b.GetNamespace()), cmp.Compare(a.GetName(), b.GetName()))
	})
	items := make([]T, 0, len(matched))
	for _, u := range matched {
		items = append(items, c.parse(u))
	}
	return items, true
}

// fluxCacheOf лениво запускает informer вида Flux у подключения через реестр кэшей и возвращает кэш; nil — кэша нет:
// нет права list, нет CRD, informer не удалось создать (повтор пробы не раньше crdProbeRetry) либо для этого GVR уже
// запущен кэш с другим типом результата. Повторные вызовы возвращают тот же кэш.
func fluxCacheOf[T any](conn *ClusterConnection, kind fluxKind, gvr schema.GroupVersionResource,
	parse func(*unstructured.Unstructured) T, onChange func(),
) *fluxCache[T] {
	c, ok := acquireCRDCache(&conn.crdCaches, conn.DynamicClient, gvr, func() (*fluxCache[T], error) {
		return newFluxCache(conn.DynamicClient, kind, gvr, parse, onChange)
	})
	if !ok {
		return nil
	}
	return c
}
