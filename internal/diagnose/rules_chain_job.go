// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strings"

	batchv1 "k8s.io/api/batch/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цепочка для цели Job (ADR-0031 §6, этап 3): Job упал по условию Failed — «BackoffLimitExceeded» или
// «DeadlineExceeded». Причину падения Pod'а правило не пересобирает: её выдают существующие правила Pod'ов (OOM, crash
// loop, image pull), а пост-хук 90 помечает их гипотезы как объясняющие гипотезу Job'а, и хук 100 с хуком 110 строят
// цепочку Job > Pod. Приостановленный Job (spec.suspend) — не проблема, а факт.
//
// Разбор Job'а общий с правилом CronJob (rules_chain_cronjob.go): последний неудавшийся Job CronJob'а даёт те же коды.

const (
	// SymptomJobFailed — симптом «Job завершился неудачей».
	SymptomJobFailed = "job.failed"

	ruleJobChainID = "chain.job"

	defaultBackoffLimit = 6 // значение spec.backoffLimit по умолчанию в Kubernetes: показывается числом
)

func init() {
	registerChainRule(ruleJobChainID, 1, ruleJobChain)
	registerPostHook(90, "job_explains", jobExplains)
	registerPostHook(110, "job_chains", jobChains)
}

func ruleJobChain(b *builder) {
	g := b.c.Graph
	t := b.c.Target
	if g == nil || t.Kind != "Job" {
		return
	}
	n, ok := g.Node(toGraphRef(t))
	if !ok || n.Placeholder || n.Object == nil || n.Health == depgraph.Unknown {
		return // цель не прочитана графом: пробел уже записан в Missing
	}
	job, err := decode[batchv1.Job](n.Object)
	if err != nil {
		return
	}
	jobFindings(b, g, n, job)
}

// jobRefOf — ссылка на Job для отчёта: с группой и UID, как у цели.
func jobRefOf(n depgraph.Node, job *batchv1.Job) ResourceRef {
	r := fromGraphRef(n.Ref)
	r.UID = string(job.UID)
	return r
}

// graphWarned: у графа есть предупреждение code (например «limit.job_pods»).
func graphWarned(g *depgraph.Graph, code string) bool { return slices.Contains(g.Warnings(), code) }

// batchListFailed: список объектов вида kind не прочитан (запрет, недоступность), значит «их нет» не доказано.
func batchListFailed(g *depgraph.Graph, kind string) bool {
	for _, w := range g.Warnings() {
		if k, arg, _ := strings.Cut(w, ":"); arg == kind && graphWarnKinds[k] != "" {
			return true
		}
	}
	return false
}

// jobFindings разбирает один Job по его условиям и возвращает идентификаторы выданных гипотез.
func jobFindings(b *builder, g *depgraph.Graph, n depgraph.Node, job *batchv1.Job) []string {
	ref := jobRefOf(n, job)
	params := map[string]string{"job": job.Name}
	if job.Spec.Suspend != nil && *job.Spec.Suspend {
		// Приостановленный Job — факт, не проблема: ни симптома, ни гипотезы.
		b.fact(ref, "job.suspended", Source{Kind: SourceResource, Resource: ref, Field: "spec.suspend"}, params)
		return nil
	}
	cond, failed := jobCondition(job, batchv1.JobFailed)
	if !failed {
		return nil
	}
	b.symptom(SymptomJobFailed)
	src := Source{Kind: SourceResource, Resource: ref, Field: "status.conditions[Failed]"}
	var raised []string
	switch cond.Reason {
	case batchv1.JobReasonBackoffLimitExceeded:
		limit := int32(defaultBackoffLimit)
		if job.Spec.BackoffLimit != nil {
			limit = *job.Spec.BackoffLimit
		}
		p := map[string]string{"job": job.Name, "failed": itoa(job.Status.Failed), "limit": itoa(limit)}
		f := b.fact(ref, "job.backoff_limit_exceeded", src, p)
		h := b.hypothesis(ref, "job.backoff_limit_exceeded", ConfidenceHigh, p, []string{f}, nil)
		b.recommend(ref, "rec.fix_job_pod_failures", params, h)
		b.recommend(ref, "rec.raise_job_backoff_limit", p, h)
		raised = append(raised, h)
	case batchv1.JobReasonDeadlineExceeded:
		p := map[string]string{"job": job.Name}
		if job.Spec.ActiveDeadlineSeconds != nil {
			p["seconds"] = itoa64(*job.Spec.ActiveDeadlineSeconds)
		}
		f := b.fact(ref, "job.deadline_exceeded", src, p)
		h := b.hypothesis(ref, "job.deadline_exceeded", ConfidenceHigh, p, []string{f}, nil)
		b.recommend(ref, "rec.fix_job_pod_failures", params, h)
		b.recommend(ref, "rec.raise_job_deadline", p, h)
		raised = append(raised, h)
	default:
		// Причина не из известных (политика сбоев Pod'ов, индексы и т. п.): факт и пробел, гипотезы нет.
		b.fact(ref, "job.failed", src, params)
		b.missing("job.failure_reason_unrecognized", ref, params)
	}
	b.step(ref, "step.check_job_pods", params, raised...)
	jobBoundaries(b, g, n, ref, params)
	return raised
}

// jobBoundaries описывает границы вывода (ADR-0031 §3): история ограничена, а Pod'ы мог удалить срок хранения.
// Пустой список Pod'ов при полном и прочитанном графе — это MissingContext, а не Missing: Pod'ы были, но их уже нет.
func jobBoundaries(b *builder, g *depgraph.Graph, n depgraph.Node, ref ResourceRef, params map[string]string) {
	if graphWarned(g, "limit.job_pods") {
		b.missing("job.history_truncated", ref, params)
		return
	}
	if graphLimited(g) || batchListFailed(g, "Pod") {
		return
	}
	if len(g.EdgesFrom(n.Ref, depgraph.EdgeOwns)) == 0 {
		b.missing("job.pods_not_retained", ref, params)
	}
}

func itoa64(n int64) string { return itoa(int32(min(n, 1<<31-1))) }

// batchHypotheses — гипотезы правил chain.job и chain.cronjob в порядке отчёта.
func batchHypotheses(b *builder) []Hypothesis {
	var out []Hypothesis
	for _, h := range b.rep.Hypotheses {
		if h.RuleID == ruleJobChainID || h.RuleID == ruleCronJobChainID {
			out = append(out, h)
		}
	}
	return out
}

// jobFailureCode — гипотеза о причине падения Job'а (а не о расписании CronJob'а).
func jobFailureCode(code string) bool { return strings.HasPrefix(code, "job.") }

// jobExplains (пост-хук 90, до связей и цепочек хука 100): гипотеза Pod'а, принадлежащего Job'у, объясняет падение
// этого Job'а. Pod, который не смог запуститься (pending.*), объясняет только превышение срока, но не исчерпание
// попыток: до исчерпания попыток нужно, чтобы контейнеры успели запуститься и упасть.
func jobExplains(b *builder) {
	g := b.c.Graph
	if g == nil || (b.c.Target.Kind != "Job" && b.c.Target.Kind != "CronJob") {
		return
	}
	for _, eff := range batchHypotheses(b) {
		if !jobFailureCode(eff.Code) {
			continue
		}
		owned := map[string]bool{}
		for _, e := range g.EdgesFrom(toGraphRef(eff.Resource), depgraph.EdgeOwns) {
			if e.To.Kind == "Pod" {
				owned[e.To.Namespace+"/"+e.To.Name] = true
			}
		}
		for i := range b.rep.Hypotheses {
			h := &b.rep.Hypotheses[i]
			if h.Resource.Kind != "Pod" || isChainRule(h.RuleID) || slices.Contains(h.Explains, eff.ID) {
				continue
			}
			if !owned[h.Resource.Namespace+"/"+h.Resource.Name] {
				continue
			}
			if eff.Code == "job.backoff_limit_exceeded" && strings.HasPrefix(h.Code, "pending.") {
				continue
			}
			h.Explains = append(h.Explains, eff.ID)
		}
	}
}

// pathNodes собирает путь по ссылкам; нет вершины в графе — пути нет. Подряд идущие повторы схлопываются.
func pathNodes(g *depgraph.Graph, refs ...depgraph.Ref) []depgraph.Node {
	var out []depgraph.Node
	for _, r := range refs {
		n, ok := g.Node(r)
		if !ok {
			return nil
		}
		if len(out) > 0 && out[len(out)-1].Ref == n.Ref {
			continue
		}
		out = append(out, n)
	}
	return out
}

// jobChains (пост-хук 110, после хука 100): цепочки «от затронутого к причине» для Job и CronJob. Общий построитель
// хука 100 идёт от следствия к зависимости и для «Job владеет Pod'ом» даёт обратный путь Pod > Job, поэтому цепочки
// гипотез этих правил строятся здесь заново: Job > Pod (CronJob > Job > Pod) с кандидатом-причиной от правил Pod'ов,
// а у находки без Pod'ов — CronJob > Job. Общий предел maxChains сохраняется.
func jobChains(b *builder) {
	g := b.c.Graph
	kind := b.c.Target.Kind
	if g == nil || (kind != "Job" && kind != "CronJob") {
		return
	}
	mine := map[string]bool{}
	for _, h := range batchHypotheses(b) {
		mine[h.ID] = true
	}
	b.rep.Chains = slices.DeleteFunc(b.rep.Chains, func(c Chain) bool { return mine[c.Candidate] })
	top := toGraphRef(b.c.Target)
	add := func(cand string, refs ...depgraph.Ref) {
		if len(b.rep.Chains) >= maxChains {
			return
		}
		if path := pathNodes(g, refs...); len(path) >= 2 {
			b.rep.Chains = append(b.rep.Chains, Chain{Path: chainSteps(path), Candidate: cand})
		}
	}
	for _, eff := range batchHypotheses(b) {
		job := toGraphRef(eff.Resource)
		switch {
		case jobFailureCode(eff.Code):
			explained := false
			for _, h := range b.rep.Hypotheses {
				if slices.Contains(h.Explains, eff.ID) {
					explained = true
					add(h.ID, top, job, toGraphRef(h.Resource))
				}
			}
			if !explained {
				add(eff.ID, top, job)
			}
		case eff.Code == "cronjob.concurrency_blocked" && eff.Params["job"] != "":
			add(eff.ID, top, depgraph.Ref{Group: groupBatch, Kind: "Job", Namespace: b.c.Target.Namespace, Name: eff.Params["job"]})
		}
	}
}
