// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import "strings"

// fluxInventoryEntry — запись status.inventory.entries[] после разбора: ресурс, которым управляет объект Flux.
type fluxInventoryEntry struct {
	Namespace string // пусто у cluster-scoped ресурсов
	Name      string
	Group     string // пусто у core-группы
	Kind      string
	Version   string // поле v записи; пусто, если его нет
}

// parseFluxInventoryID разбирает id записи inventory: «namespace_name_group_Kind» (формат ObjMetadata из cli-utils,
// который пишут kustomize-controller и helm-controller).
//
// Правила повторяют разбор Flux: namespace — до первого «_», вид — после последнего, группа — между предпоследним и
// последним, остаток — имя; в имени «__» означает «:» (так записаны имена RBAC вроде system:controller:x). У
// cluster-scoped ресурсов namespace пуст, id начинается с «_»; у core-группы пуста группа («ns_name__ConfigMap»).
// Разбор терпимый: id неизвестного формата (меньше трёх «_», пустое имя или вид) даёт false и пропускается, а не ошибку.
func parseFluxInventoryID(id string) (fluxInventoryEntry, bool) {
	ns, rest, ok := strings.Cut(id, "_")
	if !ok {
		return fluxInventoryEntry{}, false
	}
	i := strings.LastIndexByte(rest, '_')
	if i < 0 {
		return fluxInventoryEntry{}, false
	}
	kind, rest := rest[i+1:], rest[:i]
	j := strings.LastIndexByte(rest, '_')
	if j < 0 {
		return fluxInventoryEntry{}, false
	}
	group, name := rest[j+1:], strings.ReplaceAll(rest[:j], "__", ":")
	if name == "" || kind == "" || strings.ContainsAny(kind, " \t\r\n") || strings.ContainsAny(group, " \t\r\n") {
		return fluxInventoryEntry{}, false
	}
	return fluxInventoryEntry{Namespace: ns, Name: name, Group: group, Kind: kind}, true
}

// fluxInventory читает status.inventory.entries[] объекта. present — status.inventory есть (у HelmRelease он
// появляется с Flux 2.8; без него вкладка ресурсов недоступна). skipped — число записей, которые не удалось разобрать.
// Повторы записей убираются, порядок сохраняется. Неожиданные типы полей дают пропуск записи, но не панику.
func fluxInventory(obj map[string]any) (entries []fluxInventoryEntry, present bool, skipped int) {
	inv, ok := mapAt(obj, "status", "inventory")
	if !ok {
		return nil, false, 0
	}
	items, _ := sliceAt(inv, "entries")
	seen := make(map[fluxInventoryEntry]bool, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			skipped++
			continue
		}
		e, ok := parseFluxInventoryID(strAt(m, "id"))
		if !ok {
			skipped++
			continue
		}
		e.Version = strAt(m, "v")
		if !seen[e] {
			seen[e] = true
			entries = append(entries, e)
		}
	}
	return entries, true, skipped
}
