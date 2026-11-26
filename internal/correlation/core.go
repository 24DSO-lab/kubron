// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Коды Missing и значения причины недоступности данных (Params["reason"]).
const (
	codeUnavailable = "correlation.unavailable"

	reasonForbidden   = "forbidden"
	reasonUnavailable = "unavailable"
	reasonTooLarge    = "too_large"
	reasonBudget      = "budget"
	reasonDeadline    = "deadline"
	reasonNotFound    = "not_found"
)

// PartialName — имя частичного источника корреляции в Result.Partial и в Report.Partial расследования.
const PartialName = "correlation"

// podListLimit — сколько объектов читается за один List (ADR-0033 §3): больше — результат помечается Truncated.
const podListLimit = 500

var (
	gvkPod        = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	gvkNode       = schema.GroupVersionKind{Version: "v1", Kind: "Node"}
	gvkEvent      = schema.GroupVersionKind{Version: "v1", Kind: "Event"}
	gvkReplicaSet = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	gvkPVC        = schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolumeClaim"}
)

// budgetError — внутренний сентинел исчерпания бюджета чтений. До пользователя не доходит: Run превращает его в код
// причины budget в Missing, поэтому текста для локализации нет.
type budgetError struct{}

func (budgetError) Error() string { return "read budget exhausted" }

var errBudget error = budgetError{}

// budgetReader ограничивает число дополнительных чтений кластера за один запуск (MaxExtraReads).
type budgetReader struct {
	r    diagnose.Reader
	left int
}

func (b *budgetReader) Get(ctx context.Context, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
	if b.left <= 0 {
		return nil, errBudget
	}
	b.left--
	return b.r.Get(ctx, gvk, ns, name)
}

func (b *budgetReader) List(ctx context.Context, gvk schema.GroupVersionKind, o diagnose.ListOptions) (*diagnose.ListResult, error) {
	if b.left <= 0 {
		return nil, errBudget
	}
	b.left--
	return b.r.List(ctx, gvk, o)
}

// reasonOf переводит ошибку чтения в код причины без текста ошибки (текст может содержать значения из кластера).
func reasonOf(err error) string {
	switch {
	case errors.Is(err, errBudget):
		return reasonBudget
	case errors.Is(err, diagnose.ErrForbidden):
		return reasonForbidden
	case errors.Is(err, diagnose.ErrTooLarge):
		return reasonTooLarge
	case errors.Is(err, diagnose.ErrNotFound):
		return reasonNotFound
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return reasonDeadline
	default:
		return reasonUnavailable
	}
}

// env — общее окружение правил одного запуска. Правила не читают время и кластер в обход него.
type env struct {
	r   *budgetReader
	dc  *diagnose.Context
	now time.Time

	facts   []diagnose.Fact
	missing []diagnose.MissingContext

	rs workloadResolver
}

// factSpec — отложенный факт: регистрируется, только если кандидат остался в результате.
type factSpec struct {
	code   string
	res    diagnose.ResourceRef
	params map[string]string
}

// addFacts регистрирует факты и возвращает их ID в порядке указания.
func (e *env) addFacts(specs []factSpec) []string {
	ids := make([]string, 0, len(specs))
	for _, s := range specs {
		id := "cf" + itoa(len(e.facts)+1)
		e.facts = append(e.facts, diagnose.Fact{Kind: diagnose.KindFact, ID: id, Code: s.code, Resource: s.res,
			Params: s.params, Source: diagnose.Source{Kind: diagnose.SourceDerived, Resource: s.res}, ObservedAt: e.now})
		ids = append(ids, id)
	}
	return ids
}

// unavailable фиксирует, что данные не прочитаны, и причину (без дублей).
func (e *env) unavailable(res diagnose.ResourceRef, reason string) {
	for _, m := range e.missing {
		if m.Code == codeUnavailable && m.Resource == res && m.Params["reason"] == reason {
			return
		}
	}
	e.missing = append(e.missing, diagnose.MissingContext{Code: codeUnavailable, Resource: res,
		Params: map[string]string{"reason": reason}})
}

// ruleFunc — правило корреляции: возвращает кандидатов своего вида. Недоступность данных правило фиксирует
// через env.unavailable, а не ошибкой: ошибка Run — только программная.
type ruleFunc func(ctx context.Context, e *env) []Candidate

type namedRule struct {
	name string
	fn   ruleFunc
}

var registry []namedRule

// registerRule добавляет правило в реестр; вызывается из init файла правила. Порядок исполнения — по имени,
// поэтому результат не зависит от порядка init файлов.
func registerRule(name string, fn ruleFunc) {
	registry = append(registry, namedRule{name: name, fn: fn})
}

// evidence — четыре признака уверенности ADR-0033 §4.
type evidence struct {
	structural bool // (а) структурная связь: затронутые нагрузки привязаны к источнику
	abnormal   bool // (б) состояние источника ненормально
	temporal   bool // (в) изменение источника не позже первых сбоев и не раньше чем за CorrelationWindow
	multi      bool // (г) затронуты не менее трёх нагрузок
}

// minWorkloadsForMulti — порог признака (г).
const minWorkloadsForMulti = 3

// confidence: high — все четыре признака; medium — (а) и (б) или (в); low — (а) и (г). Без (а) или без перечисленных
// сочетаний кандидата нет: одной структурной связи для гипотезы недостаточно.
func (ev evidence) confidence() (diagnose.Confidence, bool) {
	switch {
	case !ev.structural:
		return "", false
	case ev.abnormal && ev.temporal && ev.multi:
		return diagnose.ConfidenceHigh, true
	case ev.abnormal || ev.temporal:
		return diagnose.ConfidenceMedium, true
	case ev.multi:
		return diagnose.ConfidenceLow, true
	}
	return "", false
}

// withinWindow — изменение источника at предшествует первому сбою first не более чем на CorrelationWindow.
func withinWindow(at, first time.Time) bool {
	if at.IsZero() || first.IsZero() || at.After(first) {
		return false
	}
	return first.Sub(at) <= CorrelationWindow
}

func confidenceRank(c diagnose.Confidence) int {
	switch c {
	case diagnose.ConfidenceHigh:
		return 3
	case diagnose.ConfidenceMedium:
		return 2
	case diagnose.ConfidenceLow:
		return 1
	}
	return 0
}

// candidateID — стабильный идентификатор кандидата: хэш ключа, чтобы обратная связь ссылалась на одно и то же.
func candidateID(c Candidate) string {
	sum := sha256.Sum256([]byte(c.Key()))
	return "cc-" + hex.EncodeToString(sum[:6])
}

// sortCandidates: по убыванию уверенности, затем Total, затем ключ — порядок детерминирован.
func sortCandidates(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		if ri, rj := confidenceRank(cs[i].Confidence), confidenceRank(cs[j].Confidence); ri != rj {
			return ri > rj
		}
		if cs[i].Total != cs[j].Total {
			return cs[i].Total > cs[j].Total
		}
		return cs[i].Key() < cs[j].Key()
	})
}

func itoa(n int) string { return strconv.Itoa(n) }
