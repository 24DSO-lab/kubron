// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// cronManifest — CronJob с расписанием и дополнениями spec и status.
func cronManifest(sched, specExtra, status string) string {
	if status == "" {
		status = "{}"
	}
	return `
apiVersion: batch/v1
kind: CronJob
metadata: {name: nightly, namespace: app, uid: c1, creationTimestamp: "2026-10-01T00:00:00Z"}
spec: {schedule: "` + sched + `", jobTemplate: {spec: {}}` + specExtra + `}
status: ` + status + "\n"
}

const (
	lastTwoDaysAgo = `{lastScheduleTime: "2026-11-09T03:00:00Z"}` // fixedNow: 2026-11-11T10:00Z
	lastToday      = `{lastScheduleTime: "2026-11-11T03:00:00Z"}`
)

func eventManifest(reason string) string {
	return `
apiVersion: v1
kind: Event
metadata: {name: e1, namespace: app}
involvedObject: {kind: CronJob, name: nightly, namespace: app}
type: Warning
reason: ` + reason + `
message: "controller message"
count: 1
firstTimestamp: "2026-11-11T09:00:00Z"
lastTimestamp: "2026-11-11T09:50:00Z"
`
}

// Пропуск запуска: порог — допуск контроллера или startingDeadlineSeconds; события поднимают уверенность на уровень.
func TestCronJobMissedScheduleConfidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		cron  string
		event string
		want  Confidence // пусто — гипотезы быть не должно
	}{
		{"no deadline, no events", cronManifest("0 3 * * *", "", lastTwoDaysAgo), "", ConfidenceLow},
		{"deadline set", cronManifest("0 3 * * *", ", startingDeadlineSeconds: 300", lastTwoDaysAgo), "", ConfidenceMedium},
		{"deadline and event", cronManifest("0 3 * * *", ", startingDeadlineSeconds: 300", lastTwoDaysAgo), "FailedNeedsStart", ConfidenceHigh},
		{"event raises low to medium", cronManifest("0 3 * * *", "", lastTwoDaysAgo), "TooManyMissedTimes", ConfidenceMedium},
		{"on schedule", cronManifest("0 3 * * *", "", lastToday), "", ""},
		{"tick inside the controller grace", cronManifest("59 9 * * *", "", `{lastScheduleTime: "2026-11-10T09:59:00Z"}`), "", ""},
		{"tick inside a long deadline", cronManifest("0 9 * * *", ", startingDeadlineSeconds: 7200", `{lastScheduleTime: "2026-11-10T09:00:00Z"}`), "", ""},
		{"next tick is still ahead", cronManifest("0 12 * * *", "", `{lastScheduleTime: "2026-11-10T12:00:00Z"}`), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manifests := []string{tc.cron}
			if tc.event != "" {
				manifests = append(manifests, eventManifest(tc.event))
			}
			rep := batchReport(t, "CronJob", "nightly", nil, manifests...)
			got := Confidence("")
			for _, h := range rep.Hypotheses {
				if h.Code == "cronjob.missed_schedule" {
					got = h.Confidence
				}
			}
			if got != tc.want {
				t.Errorf("missed_schedule confidence = %q, want %q", got, tc.want)
			}
		})
	}
}

// Расписание не разобрано: пропуск оценивается только по событиям, иначе — пробел, а не вывод.
func TestCronJobUnreadableScheduleFallsBackToEvents(t *testing.T) {
	t.Parallel()
	bad := cronManifest("not a schedule", "", lastTwoDaysAgo)
	rep := batchReport(t, "CronJob", "nightly", nil, bad)
	if !batchHasMissing(rep, "cronjob.schedule_unreadable") || len(rep.Hypotheses) != 0 {
		t.Errorf("no events: missing=%+v hypotheses=%+v", rep.Missing, rep.Hypotheses)
	}
	rep = batchReport(t, "CronJob", "nightly", nil, bad, eventManifest("FailedNeedsStart"))
	if !batchHasHypothesis(rep, "cronjob.missed_schedule", ConfidenceLow) {
		t.Errorf("with an event: %+v", rep.Hypotheses)
	}
}

// Приостановка без пропущенных запусков — факт; с ожидаемыми запусками — гипотеза и счётчик.
func TestCronJobSuspendedNeedsExpectedRuns(t *testing.T) {
	t.Parallel()
	rep := batchReport(t, "CronJob", "nightly", nil, cronManifest("0 3 * * *", ", suspend: true", lastToday))
	if batchHasHypothesis(rep, "cronjob.suspended", "") {
		t.Errorf("a suspended CronJob with no expected runs is not a problem: %+v", rep.Hypotheses)
	}
	if !slices.ContainsFunc(rep.Facts, func(f Fact) bool { return f.Code == "cronjob.suspended" }) {
		t.Errorf("no cronjob.suspended fact")
	}
	rep = batchReport(t, "CronJob", "nightly", nil, cronManifest("0 3 * * *", ", suspend: true", lastTwoDaysAgo))
	i := slices.IndexFunc(rep.Hypotheses, func(h Hypothesis) bool { return h.Code == "cronjob.suspended" })
	if i < 0 || rep.Hypotheses[i].Params["missed"] != "2" {
		t.Fatalf("hypotheses = %+v, want cronjob.suspended with missed=2", rep.Hypotheses)
	}
}

// Число пропусков ограничено константой контроллера: «много» не превращается в точное число.
func TestCronJobMissedCountIsCapped(t *testing.T) {
	t.Parallel()
	rep := batchReport(t, "CronJob", "nightly", nil, cronManifest("* * * * *", ", suspend: true", `{lastScheduleTime: "2026-11-10T00:00:00Z"}`))
	i := slices.IndexFunc(rep.Hypotheses, func(h Hypothesis) bool { return h.Code == "cronjob.suspended" })
	if i < 0 || rep.Hypotheses[i].Params["missed"] != "100" {
		t.Fatalf("hypotheses = %+v, want missed=100", rep.Hypotheses)
	}
}

const activeJobManifest = `
apiVersion: batch/v1
kind: Job
metadata:
  name: nightly-27
  namespace: app
  uid: j27
  creationTimestamp: "%s"
  ownerReferences: [{apiVersion: batch/v1, kind: CronJob, name: nightly, uid: c1, controller: true}]
spec: {selector: {matchLabels: {controller-uid: j27}}}
status: {}
`

// Forbid блокирует запуск, только если активный Job работает дольше интервала; иначе — это обычная работа.
func TestCronJobConcurrencyBlockedNeedsASkippedRun(t *testing.T) {
	t.Parallel()
	forbid := cronManifest("0 3 * * *", ", concurrencyPolicy: Forbid", lastToday)
	started := func(ts string) string { return strings.Replace(activeJobManifest, "%s", ts, 1) }
	// Job запущен в 03:00 сегодня: следующий запуск завтра, пропуска нет.
	rep := batchReport(t, "CronJob", "nightly", nil, forbid, started("2026-11-11T03:00:00Z"))
	if batchHasHypothesis(rep, "cronjob.concurrency_blocked", "") {
		t.Errorf("a recent Job does not block: %+v", rep.Hypotheses)
	}
	// Job запущен позавчера: запуск 03:00 сегодня пропущен.
	rep = batchReport(t, "CronJob", "nightly", nil,
		cronManifest("0 3 * * *", ", concurrencyPolicy: Forbid", `{lastScheduleTime: "2026-11-10T03:00:00Z"}`), started("2026-11-10T03:00:00Z"))
	if !batchHasHypothesis(rep, "cronjob.concurrency_blocked", ConfidenceHigh) || batchHasHypothesis(rep, "cronjob.missed_schedule", "") {
		t.Errorf("hypotheses = %+v, want only concurrency_blocked", rep.Hypotheses)
	}
	// Политика Allow ничего не блокирует: тот же Job — пропуск расписания, но не блокировка.
	rep = batchReport(t, "CronJob", "nightly", nil,
		cronManifest("0 3 * * *", "", `{lastScheduleTime: "2026-11-09T03:00:00Z"}`), started("2026-11-09T03:00:00Z"))
	if batchHasHypothesis(rep, "cronjob.concurrency_blocked", "") {
		t.Errorf("Allow policy must not report a block: %+v", rep.Hypotheses)
	}
}

// Часовой пояс расписания учитывается: 12:00 по Красноярску — это 05:00 UTC.
func TestCronJobTimeZone(t *testing.T) {
	t.Parallel()
	if _, err := time.LoadLocation("Asia/Krasnoyarsk"); err != nil {
		t.Skipf("no time zone database: %v", err)
	}
	// Последний запуск вчера в 12:00 UTC. С зоной следующий запуск — сегодня в 05:00 UTC (12:00 по Красноярску),
	// он уже просрочен; без зоны — сегодня в 12:00 UTC, он ещё впереди.
	last := `{lastScheduleTime: "2026-11-10T12:00:00Z"}`
	rep := batchReport(t, "CronJob", "nightly", nil, cronManifest("0 12 * * *", `, timeZone: "Asia/Krasnoyarsk"`, last))
	if !batchHasHypothesis(rep, "cronjob.missed_schedule", "") {
		t.Errorf("the 05:00 UTC run is overdue: %+v", rep.Hypotheses)
	}
	rep = batchReport(t, "CronJob", "nightly", nil, cronManifest("0 12 * * *", "", last))
	if batchHasHypothesis(rep, "cronjob.missed_schedule", "") {
		t.Errorf("without a zone the 12:00 UTC run is still ahead: %+v", rep.Hypotheses)
	}
}

// Отчёт CronJob не зависит от ObservedAt, если расписание не нужно: нулевое время не даёт ложных пропусков.
func TestCronJobWithoutObservedAtRaisesNoScheduleFindings(t *testing.T) {
	t.Parallel()
	r := newChain(t, cronManifest("0 3 * * *", ", suspend: true", lastTwoDaysAgo))
	c, err := Collect(t.Context(), r, ResourceRef{Kind: "CronJob", Namespace: "app", Name: "nightly"}, chainOpts())
	if err != nil {
		t.Fatal(err)
	}
	c.ObservedAt = time.Time{}
	rep := Analyze(c)
	if batchHasHypothesis(rep, "cronjob.suspended", "") || batchHasHypothesis(rep, "cronjob.missed_schedule", "") {
		t.Errorf("no ObservedAt, yet schedule hypotheses: %+v", rep.Hypotheses)
	}
}
