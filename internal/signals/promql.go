// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signals

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/util/validation"
)

// MaxPodsPerMatcher — наибольшее число имён Pod в одном регулярном сопоставлении (ADR-0032 §4).
const MaxPodsPerMatcher = 50

// promQuote возвращает строковый литерал PromQL для значения. Литералы PromQL следуют правилам экранирования Go
// (обратная косая черта, \xNN, \uNNNN), поэтому strconv.Quote даёт заведомо корректный литерал: кавычка, обратная
// косая черта, перенос строки и любые непечатные символы не могут выйти за его пределы.
func promQuote(s string) string { return strconv.Quote(s) }

// matchAny строит сопоставление метки label с одним из значений values (непустой набор, не более MaxPodsPerMatcher).
// Одно значение — строгое равенство (=). Несколько — регулярное выражение из альтернатив с regexp.QuoteMeta: PromQL
// привязывает его к началу и концу значения, а QuoteMeta лишает значения метасимволов. Экранирование регулярного
// выражения делается ДО экранирования литерала: иначе обратные косые черты QuoteMeta сломали бы литерал.
func matchAny(label string, values []string) (string, error) {
	switch {
	case len(values) == 0:
		return "", fmt.Errorf("matcher %s: no values", label)
	case len(values) > MaxPodsPerMatcher:
		return "", fmt.Errorf("matcher %s: %d values exceed the limit of %d", label, len(values), MaxPodsPerMatcher)
	case slices.ContainsFunc(values, func(v string) bool { return !utf8.ValidString(v) }):
		return "", fmt.Errorf("matcher %s: a value is not valid UTF-8", label) // RE2 такой шаблон не примет
	case len(values) == 1:
		return label + "=" + promQuote(values[0]), nil
	}
	alts := make([]string, len(values))
	for i, v := range values {
		alts[i] = regexp.QuoteMeta(v)
	}
	return label + "=~" + promQuote(strings.Join(alts, "|")), nil
}

// eq — строгое сопоставление метки с одним значением; значение проверено до вызова (validRefs), но литерал
// экранируется в любом случае: защита не должна зависеть от порядка вызовов.
func eq(label, value string) string {
	m, _ := matchAny(label, []string{value}) // ошибка невозможна: значение ровно одно
	return m
}

// promDuration записывает длительность в синтаксисе PromQL: 15m, 1h, 90s.
func promDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	default:
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
}

// Допустимость значений меток — DNS-1123, как у имён объектов Kubernetes (ADR-0032 §4). Проверка идёт ДО подстановки:
// значение, не прошедшее её, даёт invalid_ref, а не запрос.
func validNamespace(s string) bool { return validation.IsDNS1123Label(s) == nil }
func validPod(s string) bool       { return validation.IsDNS1123Subdomain(s) == nil }
func validContainer(s string) bool { return validation.IsDNS1123Label(s) == nil }
func validNode(s string) bool      { return validation.IsDNS1123Subdomain(s) == nil }
func validPVC(s string) bool       { return validation.IsDNS1123Subdomain(s) == nil }

// Имена Deployment, HPA и Ingress — DNS-1123 subdomain, как у PVC.
func validDeployment(s string) bool { return validation.IsDNS1123Subdomain(s) == nil }
func validHPA(s string) bool        { return validation.IsDNS1123Subdomain(s) == nil }
func validIngress(s string) bool    { return validation.IsDNS1123Subdomain(s) == nil }

// field — поле Subject, которое нужно сигналу.
type field uint8

const (
	fNamespace field = iota
	fPod
	fContainer
	fNode
	fPVC
	fDeployment
	fHPA
	fIngress
)

// validRefs проверяет, что нужные поля заполнены и допустимы.
func validRefs(s Subject, need ...field) bool {
	for _, f := range need {
		var v string
		var ok func(string) bool
		switch f {
		case fNamespace:
			v, ok = s.Namespace, validNamespace
		case fPod:
			v, ok = s.Pod, validPod
		case fContainer:
			v, ok = s.Container, validContainer
		case fNode:
			v, ok = s.Node, validNode
		case fPVC:
			v, ok = s.PVC, validPVC
		case fDeployment:
			v, ok = s.Deployment, validDeployment
		case fHPA:
			v, ok = s.HPA, validHPA
		case fIngress:
			v, ok = s.Ingress, validIngress
		}
		if v == "" || !ok(v) {
			return false
		}
	}
	return true
}
