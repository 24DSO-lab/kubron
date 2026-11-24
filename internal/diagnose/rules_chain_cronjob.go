// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	batchv1 "k8s.io/api/batch/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цепочка для цели CronJob (ADR-0031 §6, этап 3): расписание приостановлено при ожидаемых запусках, запуски
// пропущены (lastScheduleTime, расписание, startingDeadlineSeconds, события), concurrencyPolicy: Forbid блокирует
// запуск из-за активного Job'а. Последний неудавшийся Job разбирается теми же кодами, что цель Job (jobFindings), а
// причину падения его Pod'ов объясняют правила Pod'ов (цепочка CronJob > Job > Pod).
//
// Время — только Context.ObservedAt: правило детерминировано и не читает часы. Расписание разбирает стандартный
// парсер cron из зависимостей проекта (robfig/cron/v3, им же пользуется Kubernetes); если расписание не разобрано,
// пропуск оценивается только по событиям FailedNeedsStart и TooManyMissedTimes.

const (
	// SymptomCronJobNotScheduling — симптом «CronJob не запускает задания по расписанию».
	SymptomCronJobNotScheduling = "cronjob.not_scheduling"

	ruleCronJobChainID = "chain.cronjob"

	maxMissedRuns = 100             // ADR-0031 §3: как у контроллера CronJob (больше 100 пропусков — TooManyMissedTimes)
	cronSyncGrace = 2 * time.Minute // ADR-0031 §3: допуск на цикл контроллера и расхождение часов
)

func init() { registerChainRule(ruleCronJobChainID, 1, ruleCronJobChain) }

func ruleCronJobChain(b *builder) {
	g := b.c.Graph
	t := b.c.Target
	if g == nil || t.Kind != "CronJob" {
		return
	}
	n, ok := g.Node(toGraphRef(t))
	if !ok || n.Placeholder || n.Object == nil || n.Health == depgraph.Unknown {
		return // цель не прочитана графом: пробел уже записан в Missing
	}
	cj, err := decode[batchv1.CronJob](n.Object)
	if err != nil {
		return
	}
	ref := fromGraphRef(n.Ref)
	ref.UID = string(cj.UID)
	if graphWarned(g, "limit.cronjob_jobs") {
		b.missing("cronjob.history_truncated", ref, map[string]string{"cronjob": cj.Name})
	}
	jobs := cronJobs(g, n.Ref) // от новых к старым
	if i := slices.IndexFunc(jobs, func(j cronJobNode) bool { return jobHasCondition(j.job, batchv1.JobFailed) }); i >= 0 {
		jobFindings(b, g, jobs[i].node, jobs[i].job)
	}
	sched := cronSchedule(cj)
	now := b.c.ObservedAt
	switch {
	case cj.Spec.Suspend != nil && *cj.Spec.Suspend:
		cronSuspended(b, ref, cj, sched, now)
	case cronBlocked(b, ref, cj, jobs, sched, now):
	default:
		cronMissed(b, ref, cj, sched, now)
	}
}

// cronJobNode — Job из графа вместе с разобранным объектом.
type cronJobNode struct {
	node depgraph.Node
	job  *batchv1.Job
}

// cronJobs возвращает Job'ы CronJob'а из графа: от новых к старым, равные по времени — по имени.
func cronJobs(g *depgraph.Graph, cron depgraph.Ref) []cronJobNode {
	var out []cronJobNode
	for _, e := range g.EdgesFrom(cron, depgraph.EdgeOwns) {
		n, ok := g.Node(e.To)
		if !ok || n.Kind != "Job" || n.Object == nil {
			continue
		}
		if j, err := decode[batchv1.Job](n.Object); err == nil {
			out = append(out, cronJobNode{n, j})
		}
	}
	slices.SortStableFunc(out, func(a, b cronJobNode) int {
		if c := b.job.CreationTimestamp.Time.Compare(a.job.CreationTimestamp.Time); c != 0 {
			return c
		}
		return strings.Compare(a.job.Name, b.job.Name)
	})
	return out
}

// cronSchedule разбирает расписание стандартным парсером с учётом spec.timeZone. Нет разбора — nil.
func cronSchedule(cj *batchv1.CronJob) cron.Schedule {
	spec := strings.TrimSpace(cj.Spec.Schedule)
	if tz := cj.Spec.TimeZone; tz != nil && *tz != "" && !strings.HasPrefix(spec, "CRON_TZ=") && !strings.HasPrefix(spec, "TZ=") {
		spec = "CRON_TZ=" + *tz + " " + spec
	}
	s, err := cron.ParseStandard(spec)
	if err != nil {
		return nil
	}
	return s
}

// countTicks считает запуски расписания в полуинтервале (from, to], не больше maxMissedRuns+1 (дальше число не
// важно), и возвращает время первого. Время приводится к UTC: метки Kubernetes разбираются в локальной зоне машины, а
// расписание без timeZone считалось бы в ней же, и отчёт зависел бы от окружения (детерминизм, 00-common.md).
func countTicks(s cron.Schedule, from, to time.Time) (n int, first time.Time) {
	from, to = from.UTC(), to.UTC()
	for t := s.Next(from); !t.IsZero() && !t.After(to) && n <= maxMissedRuns; t = s.Next(t) {
		if n == 0 {
			first = t
		}
		n++
	}
	return n, first
}

func cappedCount(n int) string { return itoa(int32(min(n, maxMissedRuns))) }

// scheduleStart — от какого момента считаются ожидаемые запуски: последний запуск, иначе создание CronJob'а.
func scheduleStart(cj *batchv1.CronJob) time.Time {
	if cj.Status.LastScheduleTime != nil {
		return cj.Status.LastScheduleTime.Time
	}
	return cj.CreationTimestamp.Time
}

// cronEvents — события CronJob'а контроллера с одной из причин.
func cronEvents(b *builder, reasons ...string) []Event {
	var out []Event
	for _, ev := range b.c.Events {
		if ev.Object.Kind == "CronJob" && ev.Object.Name == b.c.Target.Name && slices.Contains(reasons, ev.Reason) {
			out = append(out, ev)
		}
	}
	return out
}

// cronSuspended: spec.suspend и ожидаемые запуски → cronjob.suspended. Приостановка без пропущенных запусков — факт.
func cronSuspended(b *builder, ref ResourceRef, cj *batchv1.CronJob, s cron.Schedule, now time.Time) {
	params := map[string]string{"cronjob": cj.Name}
	f := b.fact(ref, "cronjob.suspended", Source{Kind: SourceResource, Resource: ref, Field: "spec.suspend"}, params)
	if s == nil {
		b.missing("cronjob.schedule_unreadable", ref, params)
		return
	}
	if now.IsZero() {
		return
	}
	n, _ := countTicks(s, scheduleStart(cj), now)
	if n == 0 {
		return
	}
	p := map[string]string{"cronjob": cj.Name, "missed": cappedCount(n)}
	b.symptom(SymptomCronJobNotScheduling)
	h := b.hypothesis(ref, "cronjob.suspended", ConfidenceHigh, p, []string{f}, nil)
	b.recommend(ref, "rec.resume_cronjob", params, h)
}

// cronBlocked: concurrencyPolicy: Forbid и активный Job дольше интервала расписания → cronjob.concurrency_blocked.
// Возвращает true, если гипотеза выдана: пропуск запуска тогда объяснён блокировкой, а не сбоем контроллера.
func cronBlocked(b *builder, ref ResourceRef, cj *batchv1.CronJob, jobs []cronJobNode, s cron.Schedule, now time.Time) bool {
	if cj.Spec.ConcurrencyPolicy != batchv1.ForbidConcurrent || s == nil || now.IsZero() {
		return false
	}
	// Самый давний из выполняющихся Job'ов: он блокирует дольше всех.
	var active *cronJobNode
	for i := len(jobs) - 1; i >= 0; i-- {
		if jobs[i].node.Health == depgraph.Progressing {
			active = &jobs[i]
			break
		}
	}
	if active == nil {
		return false
	}
	start := active.job.CreationTimestamp.Time
	if active.job.Status.StartTime != nil {
		start = active.job.Status.StartTime.Time
	}
	n, _ := countTicks(s, start, now.Add(-cronSyncGrace))
	if n == 0 {
		return false
	}
	params := map[string]string{"cronjob": cj.Name, "job": active.job.Name, "skipped": cappedCount(n)}
	policy := b.fact(ref, "cronjob.concurrency_forbid", Source{Kind: SourceResource, Resource: ref, Field: "spec.concurrencyPolicy"}, map[string]string{"cronjob": cj.Name})
	running := b.fact(ref, "cronjob.active_job_blocking", Source{Kind: SourceResource, Resource: jobRefOf(active.node, active.job), Field: "status.active"},
		map[string]string{"cronjob": cj.Name, "job": active.job.Name, "since": formatTime(start), "skipped": cappedCount(n)})
	sup := []string{policy, running}
	for _, ev := range cronEvents(b, "JobAlreadyActive") {
		sup = append(sup, b.factFromEvent(ref, ev))
	}
	b.symptom(SymptomCronJobNotScheduling)
	h := b.hypothesis(ref, "cronjob.concurrency_blocked", ConfidenceHigh, params, sup, nil)
	b.recommend(ref, "rec.fix_blocking_job", params, h)
	b.step(ref, "step.check_job_pods", map[string]string{"job": active.job.Name}, h)
	return true
}

// cronMissed: расписание не приостановлено и не заблокировано, а запуски по нему не создавались → cronjob.missed_schedule.
// Порог: startingDeadlineSeconds (не меньше допуска контроллера), без него — допуск контроллера. Уверенность растёт
// на уровень от событий FailedNeedsStart и TooManyMissedTimes.
func cronMissed(b *builder, ref ResourceRef, cj *batchv1.CronJob, s cron.Schedule, now time.Time) {
	params := map[string]string{"cronjob": cj.Name}
	events := cronEvents(b, "FailedNeedsStart", "TooManyMissedTimes")
	if s == nil {
		b.missing("cronjob.schedule_unreadable", ref, params)
		if len(events) == 0 {
			return
		}
		// Расписание не разобрано: пропуск виден только по событиям контроллера.
		sup := make([]string, 0, len(events))
		for _, ev := range events {
			sup = append(sup, b.factFromEvent(ref, ev))
		}
		b.symptom(SymptomCronJobNotScheduling)
		h := b.hypothesis(ref, "cronjob.missed_schedule", ConfidenceLow, params, sup, nil)
		b.recommend(ref, "rec.check_cronjob_controller", params, h)
		b.step(ref, "step.check_cronjob_events", params, h)
		return
	}
	if now.IsZero() {
		return
	}
	threshold, conf := cronSyncGrace, ConfidenceLow
	var sup []string
	if d := cj.Spec.StartingDeadlineSeconds; d != nil && *d > 0 {
		threshold, conf = max(time.Duration(*d)*time.Second, cronSyncGrace), ConfidenceMedium
	}
	start := scheduleStart(cj)
	n, _ := countTicks(s, start, now.Add(-threshold))
	if n == 0 {
		return
	}
	p := map[string]string{"cronjob": cj.Name, "missed": cappedCount(n), "since": formatTime(start)}
	sup = append(sup, b.fact(ref, "cronjob.missed_runs", Source{Kind: SourceResource, Resource: ref, Field: "status.lastScheduleTime"}, p))
	if d := cj.Spec.StartingDeadlineSeconds; d != nil && *d > 0 {
		sup = append(sup, b.fact(ref, "cronjob.starting_deadline", Source{Kind: SourceResource, Resource: ref, Field: "spec.startingDeadlineSeconds"},
			map[string]string{"cronjob": cj.Name, "seconds": itoa64(*d)}))
	}
	if len(events) > 0 {
		conf = raiseConfidence(conf)
		for _, ev := range events {
			sup = append(sup, b.factFromEvent(ref, ev))
		}
	}
	b.symptom(SymptomCronJobNotScheduling)
	h := b.hypothesis(ref, "cronjob.missed_schedule", conf, p, sup, nil)
	if cj.Spec.StartingDeadlineSeconds != nil {
		b.recommend(ref, "rec.review_starting_deadline", params, h)
	}
	b.recommend(ref, "rec.check_cronjob_controller", params, h)
	b.step(ref, "step.check_cronjob_events", params, h)
}

// raiseConfidence поднимает уверенность на один уровень, но не выше high.
func raiseConfidence(c Confidence) Confidence {
	if c == ConfidenceLow {
		return ConfidenceMedium
	}
	return ConfidenceHigh
}
