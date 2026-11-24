// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import (
	"context"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Реестр расширений графа (ADR-0031 §6, этапы 2 и 3). Новый вид объекта подключается из init() своего файла
// kind_<вид>.go и не правит общие таблицы: ни allSpecs, ни switch в expand и HealthOf. Благодаря этому разработчики
// разных видов (Ingress, HPA, StatefulSet, Job, CronJob, ссылки на ConfigMap) работают параллельно и не пересекаются.
//
// Два способа расширения, оба вызываются из init():
//   - registerKind — вид, который граф умеет читать: его описание, признак «можно задать целью» и вывод состояния;
//   - registerExpander — функция, которая достраивает граф вокруг вершины УЖЕ известного вида (Service ← Ingress,
//     Deployment ← HPA, Pod → ConfigMap) или нового вида (StatefulSet → Pod). Вызывается после встроенного
//     расширения вершины, в порядке регистрации (порядок файлов по имени: детерминирован).

// kindExt — вид этапов 2–3. Состояние Unknown и Missing выставляет построитель (по ответу кластера), а Health
// получает только прочитанный объект.
type kindExt struct {
	Spec     kindSpec
	Seedable bool // вид можно задать целью расследования
	// Health выводит состояние и код причины по самому объекту. nil — Healthy без причины.
	Health func(obj *unstructured.Unstructured) (Health, string)
}

// expander достраивает граф вокруг вершины n (объект n.Object всегда прочитан). Обязан соблюдать пределы графа:
// читать только через b.get и b.list, добавлять только через b.putNode и b.edge, ставить в очередь через b.enqueue.
type expander func(ctx context.Context, b *builder, it item, n Node)

var (
	extKinds  []kindExt
	expanders = map[string][]expander{}
)

// registerKind добавляет вид. Повторная регистрация вида и вид, уже встроенный в таблицу, — ошибка программиста.
func registerKind(e kindExt) {
	if e.Spec.gvk.Kind == "" {
		panic("depgraph: extension kind needs a kind")
	}
	if _, builtin := lookupBuiltin(e.Spec.gvk.Group, e.Spec.gvk.Kind); builtin {
		panic(fmt.Sprintf("depgraph: kind %s is built in", e.Spec.gvk.Kind))
	}
	if slices.ContainsFunc(extKinds, func(x kindExt) bool { return x.Spec.gvk == e.Spec.gvk }) {
		panic(fmt.Sprintf("depgraph: kind %s is already registered", e.Spec.gvk.Kind))
	}
	extKinds = append(extKinds, e)
}

// registerExpander подключает расширение вершин вида kind (имя вида без группы, как в Ref.Kind).
func registerExpander(kind string, fn expander) {
	if kind == "" || fn == nil {
		panic("depgraph: expander needs a kind and a function")
	}
	expanders[kind] = append(expanders[kind], fn)
}

// lookupExt ищет вид среди расширений: точное совпадение группы, затем, при пустой группе, единственный по имени.
func lookupExt(group, kind string) (kindExt, bool) {
	for _, e := range extKinds {
		if e.Spec.gvk.Kind == kind && e.Spec.gvk.Group == group {
			return e, true
		}
	}
	if group == "" {
		for _, e := range extKinds {
			if e.Spec.gvk.Kind == kind {
				return e, true
			}
		}
	}
	return kindExt{}, false
}

// extHealth — состояние вида расширения; второй результат false, если вид не из расширений.
func extHealth(obj *unstructured.Unstructured) (Health, string, bool) {
	e, ok := lookupExt(obj.GroupVersionKind().Group, obj.GetKind())
	if !ok {
		return "", "", false
	}
	if e.Health == nil {
		return Healthy, "", true
	}
	h, reason := e.Health(obj)
	return h, reason, true
}

// runExpanders вызывает расширения вершины после встроенного расширения.
func (b *builder) runExpanders(ctx context.Context, it item, n Node) {
	for _, fn := range expanders[it.ref.Kind] {
		fn(ctx, b, it, n)
	}
}
