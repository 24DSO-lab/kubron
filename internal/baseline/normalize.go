// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"maps"
	"slices"
	"time"
)

// Методы Normalized приводят запись к каноническому виду хранилища: время в UTC с точностью до микросекунды,
// пустые необязательные коллекции — nil, обязательные — непустые контейнеры (не null в JSON). Оба бэкенда применяют их
// перед записью и после чтения, поэтому файловая и PostgreSQL реализации возвращают одно и то же значение
// (иначе расхождение семантики было бы «особенностью режима», ADR-0026 §4). Методы идемпотентны и не меняют получателя.

func normTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	n := Normalize(*t)
	return &n
}

func emptyToNil[S ~[]E, E any](s S) S {
	if len(s) == 0 {
		return nil
	}
	return s
}

// Normalized — каноническая копия документа baseline.
func (b Baseline) Normalized() Baseline {
	b.CreatedAt = Normalize(b.CreatedAt)
	b.Namespaces = emptyToNil(slices.Clone(b.Namespaces))
	b.Kinds = emptyToNil(slices.Clone(b.Kinds))
	b.Refs = slices.Clone(b.Refs)
	if b.Refs == nil {
		b.Refs = []Ref{}
	}
	return b
}

// Normalized — каноническая копия версии (содержимое глубоко копируется).
func (v Version) Normalized() Version {
	v.CreatedAt = Normalize(v.CreatedAt)
	v.Salt = slices.Clone(v.Salt)
	objs := make([]Object, 0, len(v.Objects))
	for _, o := range v.Objects {
		objs = append(objs, o.Normalized())
	}
	v.Objects = objs
	return v
}

// Normalized — каноническая копия объекта: поля всегда карта (не nil), hash копируется.
func (o Object) Normalized() Object {
	fields := make(map[string]Field, len(o.Fields))
	for k, f := range o.Fields {
		if f.Hash != nil {
			f.Hash = &HashedValue{HMAC: slices.Clone(f.Hash.HMAC)}
		}
		fields[k] = f
	}
	o.Fields = fields
	return o
}

// Normalized — каноническая копия исключения.
func (e Exception) Normalized() Exception {
	e.CreatedAt = Normalize(e.CreatedAt)
	e.ExpiresAt = normTimePtr(e.ExpiresAt)
	return e
}

// NormalizedExceptions — каноническая копия списка исключений (пустой список — nil).
func NormalizedExceptions(list []Exception) []Exception {
	if len(list) == 0 {
		return nil
	}
	out := make([]Exception, len(list))
	for i, e := range list {
		out[i] = e.Normalized()
	}
	return out
}

// Normalized — каноническая копия результата проверки.
func (r CheckResult) Normalized() CheckResult {
	r.CheckedAt = Normalize(r.CheckedAt)
	items := make([]ItemResult, 0, len(r.Items))
	for _, it := range r.Items {
		it.Changes = emptyToNil(slices.Clone(it.Changes))
		for i := range it.Changes {
			if s := it.Changes[i].Source; s != nil {
				c := *s
				c.At = Normalize(c.At)
				it.Changes[i].Source = &c
			}
		}
		it.Suppressed = emptyToNil(slices.Clone(it.Suppressed))
		items = append(items, it)
	}
	r.Items = items
	if len(r.Summary.UncheckedByReason) == 0 {
		r.Summary.UncheckedByReason = nil
	} else {
		r.Summary.UncheckedByReason = maps.Clone(r.Summary.UncheckedByReason)
	}
	applied := make([]AppliedException, len(r.Exceptions.Applied))
	for i, a := range r.Exceptions.Applied {
		a.ExpiresAt = normTimePtr(a.ExpiresAt)
		applied[i] = a
	}
	r.Exceptions.Applied = emptyToNil(applied)
	r.Exceptions.Expired = emptyToNil(slices.Clone(r.Exceptions.Expired))
	r.Caveats = emptyToNil(slices.Clone(r.Caveats))
	return r
}
