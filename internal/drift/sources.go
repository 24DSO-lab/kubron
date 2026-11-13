// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/24DSO-lab/kubron/internal/baseline"
)

// auditWindow — на сколько время записи аудита может отличаться от времени записи managedFields, чтобы считаться
// относящейся к тому же изменению.
const auditWindow = 30 * time.Second

// maxFieldsDepth ограничивает глубину разбора fieldsV1: объект из кластера не должен вызывать переполнение стека.
const maxFieldsDepth = 64

// ownedField — путь поля (в формате путей baseline) и кто его записал.
type ownedField struct {
	path    string
	manager string
	at      time.Time
}

// parseManagedFields извлекает поля, записанные менеджерами через Update/Apply (подресурсы status и scale не
// сравниваются, их запись в основной объект не входит). Записи без времени пропускаются: без времени доказательства нет.
func parseManagedFields(u *unstructured.Unstructured) []ownedField {
	var out []ownedField
	for _, e := range u.GetManagedFields() {
		if e.Subresource != "" || e.Time == nil || e.FieldsV1 == nil {
			continue
		}
		if e.Operation != metav1.ManagedFieldsOperationUpdate && e.Operation != metav1.ManagedFieldsOperationApply {
			continue
		}
		var tree map[string]any
		if json.Unmarshal(e.FieldsV1.GetRawBytes(), &tree) != nil {
			continue
		}
		var paths []string
		collectOwned(tree, "", 0, &paths)
		for _, p := range paths {
			out = append(out, ownedField{path: p, manager: e.Manager, at: e.Time.Time})
		}
	}
	return out
}

// collectOwned обходит fieldsV1: «f:имя» — поле, «k:{…}» — элемент списка по ключам; «v:» и «i:» (значения и индексы)
// отдельного пути не дают — владелец считается владельцем списка целиком.
func collectOwned(node map[string]any, parent string, depth int, out *[]string) {
	if depth > maxFieldsDepth {
		return
	}
	for k, v := range node {
		child, _ := v.(map[string]any)
		var p string
		switch {
		case strings.HasPrefix(k, "f:"):
			p = fieldPath(parent, k[2:])
		case strings.HasPrefix(k, "k:"):
			kp, ok := keyedFromFieldsV1(parent, k[2:])
			if !ok {
				continue
			}
			p = kp
		default:
			continue // «.», «v:», «i:»
		}
		*out = append(*out, p)
		collectOwned(child, p, depth+1, out)
	}
}

func keyedFromFieldsV1(parent, raw string) (string, bool) {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil || len(m) == 0 {
		return "", false
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	pairs := make([][2]string, 0, len(names))
	for _, n := range names {
		s, isStr := m[n].(string)
		if !isStr {
			s = literal(m[n])
		}
		pairs = append(pairs, [2]string{n, s})
	}
	return keyedPath(parent, pairs), true
}

// related сообщает, что путь изменения и путь поля относятся к одному поддереву (один внутри другого).
func related(changePath, owned string) bool {
	return matchesPrefix(changePath, owned) || matchesPrefix(owned, changePath)
}

// attributor устанавливает источник изменения. Источник ставится только при доказательстве (ADR-0027 §6): запись
// managedFields, содержащая это поле; запись аудита Kubron — только вместе с записью managedFields, время которой
// совпадает с временем записи аудита. Отличие само по себе источника не доказывает.
type attributor struct {
	audit AuditLookup
}

func (a attributor) attribute(ctx context.Context, live *unstructured.Unstructured, ref baseline.Ref, changes []baseline.Change) {
	owned := parseManagedFields(live)
	if len(owned) == 0 {
		return
	}
	auditCache := map[time.Time]bool{}
	for i := range changes {
		c := &changes[i]
		if c.Op == baseline.OpRemoved {
			continue // поля уже нет: записи о нём нет, значит нет и доказательства
		}
		var best *ownedField
		for j := range owned {
			if !related(c.Path, owned[j].path) {
				continue
			}
			if best == nil || owned[j].at.After(best.at) {
				best = &owned[j]
			}
		}
		if best == nil {
			continue
		}
		src := &baseline.ChangeSource{Kind: baseline.SourceManagedFields, Manager: best.manager, At: best.at}
		if a.audit != nil {
			hit, seen := auditCache[best.at]
			if !seen {
				hit = a.corroborated(ctx, ref, best.at)
				auditCache[best.at] = hit
			}
			if hit {
				// Имя менеджера у Kubron не уникально (Update без FieldManager), поэтому по нему источник не
				// определяется; подтверждение — только совпадение времени с записью аудита.
				src = &baseline.ChangeSource{Kind: baseline.SourceKubronAudit, At: best.at}
			}
		}
		c.Source = src
	}
}

func (a attributor) corroborated(ctx context.Context, ref baseline.Ref, at time.Time) bool {
	t, found, err := a.audit.LastApply(ctx, ref, at.Add(-auditWindow))
	if err != nil || !found {
		return false // нет ответа аудита — нет и доказательства
	}
	d := t.Sub(at)
	if d < 0 {
		d = -d
	}
	return d <= auditWindow
}
