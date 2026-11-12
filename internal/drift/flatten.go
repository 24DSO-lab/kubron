// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"sort"
	"strings"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/logging"
)

// SaltSize — размер соли версии в байтах (ADR-0027 §4).
const SaltSize = 32

// NewSalt возвращает случайную соль версии: crypto/rand, одна на версию, не связана с ключами keyring.
func NewSalt() []byte {
	b := make([]byte, SaltSize)
	_, _ = rand.Read(b) // с Go 1.24 crypto/rand.Read не возвращает ошибку
	return b
}

// hashDomain разделяет область применения HMAC: тот же ключ-соль не даёт тех же значений в другом контексте.
const hashDomain = "kubron/baseline/hash/v1\x00"

// hashValue вычисляет HMAC-SHA256 литерала значения с солью версии. В сообщение входят ресурс (без версии API) и путь
// поля: одинаковые значения в разных ключах или ресурсах дают разные hash, и по hash нельзя заметить, что два секрета
// совпадают. Сравнение — только через hmac.Equal (Field.Equal).
func hashValue(salt []byte, ref baseline.Ref, path, lit string) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(hashDomain))
	for _, part := range []string{ref.Key(), path, lit} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(part)))
		mac.Write(n[:])
		mac.Write([]byte(part))
	}
	return mac.Sum(nil)
}

// flattener — вторая стадия: дерево → плоская карта «путь → значение»; чувствительные значения заменяются hash.
type flattener struct {
	ref    baseline.Ref
	salt   []byte
	rules  *kindRules
	out    map[string]baseline.Field
	hashed int
}

// value обходит значение; path — путь поля, sp — схемный путь (для правил).
func (f *flattener) value(v any, path, sp string) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			if path != "" {
				f.out[path] = baseline.Field{Plain: "{}"}
			}
			return
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			csp := k
			if sp != "" {
				csp = sp + "." + k
			}
			f.value(t[k], fieldPath(path, k), csp)
		}
	case []any:
		f.list(t, path, sp)
	default:
		f.leaf(v, path, sp)
	}
}

func (f *flattener) list(items []any, path, sp string) {
	ep := sp + "[]"
	keys, keyed := f.rules.keyed[ep]
	if keyed {
		keys = slices.Clone(keys)
		sort.Strings(keys) // имена ключей в пути — по алфавиту, как и в managedFields
	}
	var order []string
	seen := map[string]struct{}{} // два элемента с одним ключом не должны слиться в один путь
	for i, e := range items {
		p := indexPath(path, i)
		if keyed {
			if kp, ok := keyedElementPath(path, e, keys); ok {
				if _, dup := seen[kp]; !dup {
					seen[kp] = struct{}{}
					p = kp
				}
			}
			if f.rules.ordered.has(ep) {
				order = append(order, strings.TrimPrefix(p, path))
			}
		}
		f.value(e, p, ep)
	}
	if len(order) > 0 {
		// Порядок init-контейнеров значим, а путь с ключом его не хранит: порядок записывается отдельным значением.
		f.out[path+".@order"] = baseline.Field{Plain: literal(order)}
	}
}

// keyedElementPath строит путь элемента по значениям его ключей. Отсутствующие ключи в путь не входят.
func keyedElementPath(parent string, elem any, keys []string) (string, bool) {
	m, ok := elem.(map[string]any)
	if !ok {
		return "", false
	}
	var pairs [][2]string
	for _, k := range keys {
		v := lookup(m, k)
		if v == nil {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			s = literal(v)
		}
		pairs = append(pairs, [2]string{k, s})
	}
	if len(pairs) == 0 {
		return "", false
	}
	return keyedPath(parent, pairs), true
}

// leaf записывает скалярное значение: литерал или hash.
func (f *flattener) leaf(v any, path, sp string) {
	lit := literal(v)
	sensitive := isSensitiveSchema(f.ref.Kind, sp)
	if s, ok := v.(string); ok && !sensitive && logging.Redact(s) != s {
		// Остальные текстовые поля проходят редакцию (ADR-0022). Если она что-то нашла, значение не хранится:
		// иначе два разных секрета после редакции выглядели бы одинаково, и их смена осталась бы незамеченной.
		sensitive = true
	}
	if sensitive {
		f.out[path] = baseline.Field{Hash: &baseline.HashedValue{HMAC: hashValue(f.salt, f.ref, path, lit)}}
		f.hashed++
		return
	}
	f.out[path] = baseline.Field{Plain: lit}
}
