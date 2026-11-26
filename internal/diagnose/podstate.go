// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// containerView — упрощённое состояние контейнера (init и обычного), устойчивое к отсутствующим полям.
type containerView struct {
	Name         string
	Init         bool
	Ready        bool
	RestartCount int32
	Image        string

	WaitingReason, WaitingMessage string

	Terminated *terminatedView // текущее завершённое состояние
	Last       *terminatedView // предыдущее завершение (lastState)
}

type terminatedView struct {
	Reason, Message string
	ExitCode        int32
	Signal          int32
	FinishedAt      time.Time
}

func termView(t *corev1.ContainerStateTerminated) *terminatedView {
	if t == nil {
		return nil
	}
	return &terminatedView{Reason: t.Reason, Message: t.Message, ExitCode: t.ExitCode, Signal: t.Signal, FinishedAt: t.FinishedAt.Time}
}

func viewOf(s corev1.ContainerStatus, init bool) containerView {
	v := containerView{Name: s.Name, Init: init, Ready: s.Ready, RestartCount: s.RestartCount, Image: s.Image,
		Terminated: termView(s.State.Terminated), Last: termView(s.LastTerminationState.Terminated)}
	if w := s.State.Waiting; w != nil {
		v.WaitingReason, v.WaitingMessage = w.Reason, w.Message
	}
	return v
}

// containers возвращает состояния init-контейнеров, затем обычных. Отсутствие статусов — пустой срез.
func containers(p *corev1.Pod) []containerView {
	out := make([]containerView, 0, len(p.Status.InitContainerStatuses)+len(p.Status.ContainerStatuses))
	for _, s := range p.Status.InitContainerStatuses {
		out = append(out, viewOf(s, true))
	}
	for _, s := range p.Status.ContainerStatuses {
		out = append(out, viewOf(s, false))
	}
	return out
}

// podReady — условие Ready истинно.
func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podCondition возвращает условие пода по типу.
func podCondition(p *corev1.Pod, t corev1.PodConditionType) (corev1.PodCondition, bool) {
	for _, c := range p.Status.Conditions {
		if c.Type == t {
			return c, true
		}
	}
	return corev1.PodCondition{}, false
}

// Причины ожидания, относящиеся к запуску (не к CrashLoopBackOff).
var startupWaiting = []string{
	"ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull", "RegistryUnavailable",
	"CreateContainerConfigError", "CreateContainerError", "RunContainerError", "PreCreateHookError",
	"PostStartHookError", "PreStartHookError",
}

func isStartupWaiting(reason string) bool { return slices.Contains(startupWaiting, reason) }

// logTargets — контейнеры пода, чей лог стоит прочитать как доказательство. Предыдущий экземпляр — для
// перезапускавшихся контейнеров с завершением, текущий — для завершившихся с ошибкой.
func logTargets(p *corev1.Pod) []LogRef {
	var out []LogRef
	for _, c := range containers(p) {
		switch {
		case c.WaitingReason == "CrashLoopBackOff" || (c.RestartCount > 0 && c.Last != nil):
			out = append(out, LogRef{Container: c.Name, Previous: true})
		case c.Terminated != nil && c.Terminated.ExitCode != 0:
			out = append(out, LogRef{Container: c.Name})
		}
	}
	return out
}

// memoryLimit возвращает лимит памяти контейнера из spec (строкой, например "128Mi") и признак наличия.
func memoryLimit(p *corev1.Pod, container string) (string, bool) {
	for _, list := range [][]corev1.Container{p.Spec.Containers, p.Spec.InitContainers} {
		for _, c := range list {
			if c.Name == container {
				if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok && !q.IsZero() {
					return q.String(), true
				}
				return "", false
			}
		}
	}
	return "", false
}

func itoa(n int32) string { return strconv.FormatInt(int64(n), 10) }

// AnchorLine выбирает якорную строку лога: последнюю строку с признаком ошибки; нет такой — последнюю
// непустую строку («последние строки перед завершением контейнера», ADR-0024 §5). Возвращает индекс строки
// или -1 для пустого лога. Не паникует на любом входе.
func AnchorLine(lines []string) int {
	last := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if last < 0 {
			last = i
		}
		l := strings.ToLower(lines[i])
		for _, m := range errorMarkers {
			if strings.Contains(l, m) {
				return i
			}
		}
	}
	return last
}

var errorMarkers = []string{"panic", "fatal", "error", "exception", "out of memory", "oom", "failed", "refused", "timeout", "denied"}
