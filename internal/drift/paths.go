// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"strconv"
	"strings"
)

// Формат пути поля нормализованного объекта (ключи плоской карты baseline.Object.Fields):
//
//	spec.replicas                                           — поля и ключи карт, разделитель «.»
//	metadata.labels["app.kubernetes.io/name"]               — ключ с «особыми» символами в скобках и кавычках
//	spec.template.spec.containers[name=web].image           — элемент списка, найденный по ключу (merge key)
//	spec.ports[port=53,protocol=UDP].targetPort             — составной ключ, имена ключей по алфавиту
//	spec.policyTypes[0]                                     — элемент без ключа: порядковый номер
//
// Путь строится одним кодировщиком и сравнивается как строка: исключения применяются по границе сегмента, а путь
// managedFields приводится к тому же виду.

// simpleName — имя, которое пишется после точки без кавычек.
func simpleName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// simpleKeyValue — значение ключа списка, которое пишется без кавычек.
func simpleKeyValue(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.', r == '/', r == ':':
		default:
			return false
		}
	}
	return true
}

// fieldPath добавляет к пути поле или ключ карты.
func fieldPath(parent, name string) string {
	if simpleName(name) {
		if parent == "" {
			return name
		}
		return parent + "." + name
	}
	return parent + "[" + strconv.Quote(name) + "]"
}

// keyedPath добавляет к пути элемент списка с ключом: пары «имя=значение» по алфавиту имён.
func keyedPath(parent string, pairs [][2]string) string {
	var b strings.Builder
	b.WriteString(parent)
	b.WriteByte('[')
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p[0])
		b.WriteByte('=')
		if simpleKeyValue(p[1]) {
			b.WriteString(p[1])
		} else {
			b.WriteString(strconv.Quote(p[1]))
		}
	}
	b.WriteByte(']')
	return b.String()
}

// indexPath добавляет к пути элемент списка по номеру.
func indexPath(parent string, i int) string { return parent + "[" + strconv.Itoa(i) + "]" }

// segment — разобранный сегмент пути.
type segment struct {
	// Name — имя поля или ключ карты (Kind == segName).
	Name string
	Kind segKind
	// Text — исходный текст сегмента списка: «name=web» или «0» (Kind == segList).
	Text string
}

type segKind uint8

const (
	segName segKind = iota // поле или ключ карты
	segList                // элемент списка
)

// splitPath разбирает путь на сегменты. Путь, построенный кодировщиком этого пакета, разбирается без потерь;
// на чужом тексте возвращает то, что смог разобрать (ошибок нет: результат нужен для показа и сопоставления).
func splitPath(p string) []segment {
	var out []segment
	i := 0
	for i < len(p) {
		switch p[i] {
		case '.':
			i++
		case '[':
			if i+1 < len(p) && p[i+1] == '"' {
				name, n := unquoteAt(p, i+1)
				out = append(out, segment{Name: name, Kind: segName})
				i = i + 1 + n
				if i < len(p) && p[i] == ']' {
					i++
				}
				continue
			}
			j := i + 1
			inQuote := false
			for j < len(p) {
				c := p[j]
				if c == '"' {
					inQuote = !inQuote
				} else if c == '\\' && inQuote {
					j++
				} else if c == ']' && !inQuote {
					break
				}
				j++
			}
			if j > len(p) {
				j = len(p)
			}
			out = append(out, segment{Text: p[i+1 : min(j, len(p))], Kind: segList})
			i = j + 1
		default:
			j := i
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			out = append(out, segment{Name: p[i:j], Kind: segName})
			i = j
		}
	}
	return out
}

// unquoteAt читает строку в кавычках, начинающуюся с p[at] == '"', и возвращает значение и число прочитанных байт.
func unquoteAt(p string, at int) (string, int) {
	j := at + 1
	for j < len(p) {
		if p[j] == '\\' {
			j += 2
			continue
		}
		if p[j] == '"' {
			break
		}
		j++
	}
	if j >= len(p) {
		return p[at+1:], len(p) - at
	}
	s, err := strconv.Unquote(p[at : j+1])
	if err != nil {
		s = p[at+1 : j]
	}
	return s, j + 1 - at
}

// schemaOf превращает путь поля в «схемный» путь без ключей списков: elements → «[]». Ключи карт остаются как есть.
// По нему ищутся правила приоритета и чувствительности.
func schemaOf(p string) string {
	var b strings.Builder
	for _, s := range splitPath(p) {
		if s.Kind == segList {
			b.WriteString("[]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.Name)
	}
	return b.String()
}

// keyName возвращает «имя ключа» для показа изменения чувствительного значения: имя env-переменной, ключ ConfigMap,
// аннотации. Это последний ключ карты или значение ключа элемента списка, но не имя листового поля («value»).
func keyName(p string) string {
	segs := splitPath(p)
	for i := len(segs) - 1; i >= 0; i-- {
		s := segs[i]
		if s.Kind == segList {
			if _, v, ok := strings.Cut(s.Text, "="); ok {
				v = strings.TrimSpace(v)
				if u, err := strconv.Unquote(v); err == nil {
					return u
				}
				return v
			}
			continue
		}
		if i == len(segs)-1 && s.Name == "value" && i > 0 {
			continue // …env[name=X].value — ключом считается X
		}
		return s.Name
	}
	return p
}
