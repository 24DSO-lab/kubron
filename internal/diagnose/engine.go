// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"fmt"
	"slices"
	"strings"
)

// builder накапливает записи отчёта и выдаёт стабильные идентификаторы (f1, h1, r1, s1).
type builder struct {
	c        *Context
	rep      *Report
	refs     map[ResourceRef]bool
	podFacts map[string][]string // имя пода → идентификаторы его фактов (для связи с правилом rollout)
}

func newBuilder(c *Context) *builder {
	return &builder{c: c, rep: &Report{
		RuleSetVersion: RuleSetVersion, Target: c.Target, ObservedAt: c.ObservedAt,
		Symptoms: []string{}, Resources: []ResourceRef{}, Facts: []Fact{}, Hypotheses: []Hypothesis{},
		Recommendations: []Recommendation{}, Steps: []Step{}, Missing: []MissingContext{},
	}, refs: map[ResourceRef]bool{}, podFacts: map[string][]string{}}
}

func (b *builder) touch(r ResourceRef) {
	if !b.refs[r] {
		b.refs[r] = true
		b.rep.Resources = append(b.rep.Resources, r)
	}
}

func (b *builder) symptom(code string) {
	if !slices.Contains(b.rep.Symptoms, code) {
		b.rep.Symptoms = append(b.rep.Symptoms, code)
	}
}

// fact добавляет факт и возвращает его ID. Время получения берётся из контекста по ресурсу источника.
func (b *builder) fact(res ResourceRef, code string, src Source, params map[string]string) string {
	id := fmt.Sprintf("f%d", len(b.rep.Facts)+1)
	b.touch(res)
	b.rep.Facts = append(b.rep.Facts, Fact{Kind: KindFact, ID: id, Code: code, Resource: res, Params: params,
		Source: src, ObservedAt: b.c.observedAt(src.Resource)})
	if res.Kind == "Pod" {
		b.podFacts[res.Name] = append(b.podFacts[res.Name], id)
	}
	return id
}

// factLog — факт со ссылкой на фрагмент лога контейнера.
func (b *builder) factLog(res ResourceRef, code string, src Source, params map[string]string, ref LogRef) string {
	id := b.fact(res, code, src, params)
	b.rep.Facts[len(b.rep.Facts)-1].Log = &ref
	return id
}

func (b *builder) factFromEvent(res ResourceRef, ev Event) string {
	id := b.fact(res, "event", Source{Kind: SourceEvent, Resource: ev.Object}, map[string]string{
		"type": ev.Type, "reason": ev.Reason, "message": ev.Message, "count": itoa(ev.Count)})
	b.rep.Facts[len(b.rep.Facts)-1].ObservedAt = ev.LastSeen
	return id
}

func (b *builder) hypothesis(res ResourceRef, code string, conf Confidence, params map[string]string, supporting, contradicting []string) string {
	id := fmt.Sprintf("h%d", len(b.rep.Hypotheses)+1)
	b.touch(res)
	b.rep.Hypotheses = append(b.rep.Hypotheses, Hypothesis{Kind: KindHypothesis, ID: id, Code: code, Resource: res,
		Params: params, Confidence: conf, Supporting: nonNil(supporting), Contradicting: nonNil(contradicting)})
	return id
}

func (b *builder) recommend(res ResourceRef, code string, params map[string]string, hyps ...string) {
	b.rep.Recommendations = append(b.rep.Recommendations, Recommendation{Kind: KindRecommendation,
		ID: fmt.Sprintf("r%d", len(b.rep.Recommendations)+1), Code: code, Resource: res, Params: params, Hypotheses: nonNil(hyps)})
}

func (b *builder) step(res ResourceRef, code string, params map[string]string, hyps ...string) {
	b.rep.Steps = append(b.rep.Steps, Step{Kind: KindStep, ID: fmt.Sprintf("s%d", len(b.rep.Steps)+1),
		Code: code, Resource: res, Params: params, Hypotheses: nonNil(hyps)})
}

func (b *builder) missing(code string, res ResourceRef, params map[string]string) {
	m := MissingContext{Code: code, Resource: res, Params: params}
	if !slices.ContainsFunc(b.rep.Missing, func(x MissingContext) bool { return x.Code == code && x.Resource == res }) {
		b.rep.Missing = append(b.rep.Missing, m)
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// rule — одно версионируемое правило. Правило не должно паниковать, но движок защищается и от этого.
type rule struct {
	id    string
	apply func(b *builder)
}

// rules — набор правил версии RuleSetVersion. Порядок определяет порядок записей отчёта.
var rules = []rule{
	{"pod.pending", rulePending},
	{"pod.startup", ruleStartup},
	{"pod.crash_loop", ruleCrashLoop},
	{"pod.oom_killed", ruleOOMKilled},
	{"workload.rollout", ruleRollout},
}

// RuleIDs возвращает идентификаторы правил набора (для документации и тестов).
func RuleIDs() []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.id
	}
	return out
}

// Analyze прогоняет правила по контексту и строит отчёт. Не возвращает ошибку и не паникует: сбой правила — это
// MissingContext «rule.failed», остальные правила продолжают работу.
func Analyze(c *Context) Report {
	if c == nil {
		return *newBuilder(&Context{}).rep
	}
	b := newBuilder(c)
	b.touch(c.Target)
	for _, r := range rules {
		runRule(b, r)
	}
	for _, m := range c.Missing {
		b.missing(m.Code, m.Resource, m.Params)
	}
	b.rep.Partial = slices.Clone(c.Partial)
	if len(b.rep.Symptoms) == 0 {
		b.symptom(SymptomNone)
		b.fact(c.Target, "no_problem_detected", Source{Kind: SourceDerived, Resource: c.Target}, map[string]string{"pods": fmt.Sprint(len(c.Pods))})
		b.missing("context.insufficient", c.Target, nil)
		b.step(c.Target, "step.check_events", nil)
	}
	return *b.rep
}

func runRule(b *builder, r rule) {
	defer func() {
		if p := recover(); p != nil {
			b.missing("rule.failed", b.c.Target, map[string]string{"rule": r.id})
		}
	}()
	r.apply(b)
}

// podEvents возвращает события, относящиеся к поду, с указанными причинами (все, если reasons пуст).
func (c *Context) podEvents(name string, reasons ...string) []Event {
	var out []Event
	for _, e := range c.Events {
		if e.Object.Kind == "Pod" && e.Object.Name == name && (len(reasons) == 0 || slices.Contains(reasons, e.Reason)) {
			out = append(out, e)
		}
	}
	return out
}

// lastEvent — самое свежее событие из списка (список упорядочен по времени).
func lastEvent(ev []Event) (Event, bool) {
	if len(ev) == 0 {
		return Event{}, false
	}
	return ev[len(ev)-1], true
}

func contains(s string, subs ...string) bool {
	l := strings.ToLower(s)
	return slices.ContainsFunc(subs, func(x string) bool { return strings.Contains(l, strings.ToLower(x)) })
}
