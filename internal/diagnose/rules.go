// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// Источники фактов — единые конструкторы, чтобы поле Field всегда было путём в объекте.
func srcPod(p *corev1.Pod, field string) Source {
	return Source{Kind: SourceResource, Resource: podRef(p), Field: field}
}

func srcLog(p *corev1.Pod, container string) Source {
	return Source{Kind: SourceLog, Resource: podRef(p), Field: "log/" + container}
}

func containerField(c containerView, tail string) string {
	list := "status.containerStatuses"
	if c.Init {
		list = "status.initContainerStatuses"
	}
	return list + "[" + c.Name + "]." + tail
}

// rulePending — Pod не назначен на узел (Pending): причины планирования по условию PodScheduled и событиям.
func rulePending(b *builder) {
	for _, p := range b.c.Pods {
		if p.Status.Phase != corev1.PodPending {
			continue
		}
		cond, hasCond := podCondition(p, corev1.PodScheduled)
		if (hasCond && cond.Status == corev1.ConditionTrue) || (!hasCond && p.Spec.NodeName != "") {
			continue // назначен на узел: причины запуска разбирает pod.startup
		}
		ref := podRef(p)
		b.symptom(SymptomPending)
		phase := b.fact(ref, "pod.pending", srcPod(p, "status.phase"), map[string]string{"phase": string(p.Status.Phase)})

		events := b.c.podEvents(p.Name, "FailedScheduling")
		condText, eventText := "", ""
		var facts []string
		if hasCond && cond.Message != "" {
			condText = cond.Message
			facts = append(facts, b.fact(ref, "pod.unscheduled", srcPod(p, "status.conditions[PodScheduled]"),
				map[string]string{"reason": cond.Reason, "message": cond.Message}))
		}
		if ev, ok := lastEvent(events); ok {
			eventText = ev.Message
			facts = append(facts, b.factFromEvent(ref, ev))
		}
		if len(facts) == 0 {
			b.missing("pending.no_scheduling_info", ref, nil)
			b.step(ref, "step.check_events", nil)
			continue
		}
		matched := false
		for _, m := range schedulingMatchers {
			inCond, inEvent := condText != "" && m.match(condText), eventText != "" && m.match(eventText)
			if !inCond && !inEvent {
				continue
			}
			matched = true
			conf := ConfidenceMedium
			if inCond && inEvent {
				conf = ConfidenceHigh
			}
			params := m.params(firstText(condText, eventText))
			h := b.hypothesis(ref, m.code, conf, params, append([]string{phase}, facts...), nil)
			b.recommend(ref, m.recommendation, params, h)
			if m.step != "" {
				b.step(ref, m.step, params, h)
			}
		}
		if !matched {
			b.missing("pending.reason_unrecognized", ref, nil)
			b.step(ref, "step.check_events", nil)
		}
	}
}

func firstText(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// matcher связывает признаки текста планировщика с гипотезой.
type matcher struct {
	code           string
	markers        []string
	recommendation string
	step           string
	params         func(text string) map[string]string
}

func (m matcher) match(text string) bool { return contains(text, m.markers...) }

func noParams(string) map[string]string { return nil }

// resourceParam определяет, какого ресурса не хватило («Insufficient cpu»/«memory»).
func resourceParam(text string) map[string]string {
	switch {
	case contains(text, "insufficient cpu") && contains(text, "insufficient memory"):
		return map[string]string{"resource": "cpu,memory"}
	case contains(text, "insufficient cpu"):
		return map[string]string{"resource": "cpu"}
	case contains(text, "insufficient memory"):
		return map[string]string{"resource": "memory"}
	case contains(text, "insufficient ephemeral-storage"):
		return map[string]string{"resource": "ephemeral-storage"}
	}
	return map[string]string{"resource": "other"}
}

var schedulingMatchers = []matcher{
	{"pending.insufficient_resources", []string{"insufficient cpu", "insufficient memory", "insufficient ephemeral-storage", "insufficient pods"},
		"rec.reduce_requests_or_add_capacity", "step.check_node_capacity", resourceParam},
	{"pending.node_selector_mismatch", []string{"node affinity", "node selector", "didn't match pod's node"},
		"rec.fix_node_selector", "", noParams},
	{"pending.untolerated_taint", []string{"untolerated taint", "had taint"},
		"rec.add_toleration", "step.check_node_taints", noParams},
	{"pending.unbound_pvc", []string{"unbound immediate persistentvolumeclaims", "persistentvolumeclaim", "volume node affinity"},
		"rec.fix_volume_claim", "step.check_pvc", noParams},
	{"pending.no_nodes", []string{"0/0 nodes", "no nodes available"},
		"rec.add_nodes", "step.check_node_capacity", noParams},
}

// ruleStartup — ошибки запуска контейнера: образ, конфигурация, среда выполнения, тома.
func ruleStartup(b *builder) {
	for _, p := range b.c.Pods {
		ref := podRef(p)
		for _, c := range containers(p) {
			if c.WaitingReason != "" && isStartupWaiting(c.WaitingReason) {
				startupWaitingState(b, p, c)
				continue
			}
			if c.Terminated != nil && (c.Terminated.Reason == "StartError" || c.Terminated.Reason == "ContainerCannotRun") {
				startupFailed(b, p, c, c.Terminated.Message, "terminated")
			}
		}
		if ev, ok := lastEvent(b.c.podEvents(p.Name, "FailedMount", "FailedAttachVolume")); ok && !podReady(p) {
			b.symptom(SymptomStartupFailure)
			f := b.factFromEvent(ref, ev)
			h := b.hypothesis(ref, "startup.volume_mount_failed", ConfidenceMedium, nil, []string{f}, nil)
			b.recommend(ref, "rec.fix_volume_claim", nil, h)
			b.step(ref, "step.check_pvc", nil, h)
		}
	}
}

func startupWaitingState(b *builder, p *corev1.Pod, c containerView) {
	ref := podRef(p)
	b.symptom(SymptomStartupFailure)
	params := map[string]string{"container": c.Name, "reason": c.WaitingReason, "message": c.WaitingMessage, "image": c.Image}
	state := b.fact(ref, "container.waiting", srcPod(p, containerField(c, "state.waiting")), params)
	supporting := []string{state}
	text := c.WaitingMessage
	if ev, ok := lastEvent(b.c.podEvents(p.Name, "Failed", "BackOff", "ErrImagePull")); ok {
		supporting = append(supporting, b.factFromEvent(ref, ev))
		text += " " + ev.Message
	}
	switch c.WaitingReason {
	case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull", "RegistryUnavailable":
		imagePullHypotheses(b, p, c, text, supporting)
	case "CreateContainerConfigError":
		configHypotheses(b, p, c, text, supporting)
	default:
		startupFailed(b, p, c, text, "waiting")
	}
}

func imagePullHypotheses(b *builder, p *corev1.Pod, c containerView, text string, supporting []string) {
	ref := podRef(p)
	params := map[string]string{"container": c.Name, "image": c.Image}
	n := 0
	add := func(code string, conf Confidence, rec, step string, sup, contra []string) {
		n++
		h := b.hypothesis(ref, code, conf, params, sup, contra)
		b.recommend(ref, rec, params, h)
		if step != "" {
			b.step(ref, step, params, h)
		}
	}
	switch {
	case c.WaitingReason == "InvalidImageName":
		add("image.invalid_name", ConfidenceHigh, "rec.fix_image_reference", "", supporting, nil)
	case c.WaitingReason == "ErrImageNeverPull":
		add("image.never_pull_policy", ConfidenceHigh, "rec.fix_image_pull_policy", "", supporting, nil)
	}
	if contains(text, "not found", "manifest unknown", "no such manifest", "name unknown") {
		add("image.not_found", ConfidenceHigh, "rec.fix_image_reference", "step.check_image_exists", supporting, nil)
	}
	if contains(text, "unauthorized", "authentication required", "pull access denied", "denied", "forbidden") {
		sup, contra := supporting, []string(nil)
		if len(p.Spec.ImagePullSecrets) == 0 {
			sup = append(append([]string(nil), supporting...), b.fact(ref, "pod.no_image_pull_secrets", srcPod(p, "spec.imagePullSecrets"), nil))
		} else {
			contra = []string{b.fact(ref, "pod.image_pull_secrets", srcPod(p, "spec.imagePullSecrets"),
				map[string]string{"count": strconv.Itoa(len(p.Spec.ImagePullSecrets))})}
		}
		add("image.registry_auth", ConfidenceMedium, "rec.fix_registry_credentials", "step.check_pull_secret_reference", sup, contra)
	}
	if contains(text, "timeout", "no such host", "connection refused", "i/o timeout", "tls", "temporary failure") {
		add("image.registry_unreachable", ConfidenceMedium, "rec.check_registry_network", "step.check_node_network", supporting, nil)
	}
	if n == 0 {
		b.missing("startup.reason_unrecognized", ref, map[string]string{"container": c.Name})
		b.step(ref, "step.check_events", nil)
	}
}

func configHypotheses(b *builder, p *corev1.Pod, c containerView, text string, supporting []string) {
	ref := podRef(p)
	kind := "reference"
	switch {
	case contains(text, "configmap"):
		kind = "configmap"
	case contains(text, "secret"):
		kind = "secret"
	}
	if !contains(text, "not found", "couldn't find key", "cannot find key") {
		startupFailed(b, p, c, text, "waiting")
		return
	}
	params := map[string]string{"container": c.Name, "kind": kind}
	h := b.hypothesis(ref, "config.missing_reference", ConfidenceHigh, params, supporting, nil)
	b.recommend(ref, "rec.fix_config_reference", params, h)
	// Kubron не читает Secret: проверка существования — шаг для человека.
	b.step(ref, "step.check_referenced_object", params, h)
}

// startupFailed — ошибки среды выполнения и команды; text — сообщение кластера.
func startupFailed(b *builder, p *corev1.Pod, c containerView, text, via string) {
	ref := podRef(p)
	b.symptom(SymptomStartupFailure)
	params := map[string]string{"container": c.Name, "message": text}
	field := containerField(c, "state."+via)
	f := b.fact(ref, "container.start_failed", srcPod(p, field), params)
	sup := []string{f}
	code, rec := "startup.runtime_error", "rec.fix_container_command"
	conf := ConfidenceLow
	switch {
	case contains(text, "exec format error"):
		code, rec, conf = "startup.wrong_architecture", "rec.fix_image_architecture", ConfidenceHigh
	case contains(text, "executable file not found", "no such file or directory", "not found in $path"):
		code, conf = "startup.command_not_found", ConfidenceHigh
	case contains(text, "permission denied"):
		code, conf = "startup.permission_denied", ConfidenceMedium
	case contains(text, "runasnonroot", "non-numeric user"):
		code, rec, conf = "startup.run_as_non_root", "rec.fix_security_context", ConfidenceHigh
	}
	h := b.hypothesis(ref, code, conf, map[string]string{"container": c.Name}, sup, nil)
	b.recommend(ref, rec, map[string]string{"container": c.Name}, h)
	b.step(ref, "step.check_events", nil, h)
}

// ruleCrashLoop — контейнер падает и перезапускается (CrashLoopBackOff).
func ruleCrashLoop(b *builder) {
	for _, p := range b.c.Pods {
		ref := podRef(p)
		for _, c := range containers(p) {
			loop := c.WaitingReason == "CrashLoopBackOff" || (c.Last != nil && c.Last.ExitCode != 0 && c.RestartCount >= 3 && !c.Ready)
			if !loop {
				continue
			}
			b.symptom(SymptomCrashLoop)
			sup := []string{b.fact(ref, "container.restarts", srcPod(p, containerField(c, "restartCount")),
				map[string]string{"container": c.Name, "count": itoa(c.RestartCount)})}
			last := c.Last
			if last == nil {
				last = c.Terminated
			}
			if last == nil {
				b.missing("crash.no_termination_info", ref, map[string]string{"container": c.Name})
				b.step(ref, "step.read_previous_logs", map[string]string{"container": c.Name})
				continue
			}
			exit := b.fact(ref, "container.last_terminated", srcPod(p, containerField(c, "lastState.terminated")), map[string]string{
				"container": c.Name, "exitCode": itoa(last.ExitCode), "reason": last.Reason, "finishedAt": formatTime(last.FinishedAt)})
			sup = append(sup, exit)
			if ev, ok := lastEvent(b.c.podEvents(p.Name, "BackOff")); ok {
				sup = append(sup, b.factFromEvent(ref, ev))
			}
			sup = append(sup, b.factLog(ref, "container.log_tail", srcLog(p, c.Name), map[string]string{"container": c.Name},
				LogRef{Container: c.Name, Previous: true}))
			if last.Reason == "OOMKilled" {
				continue // причину разбирает pod.oom_killed
			}
			crashHypotheses(b, p, c, last, sup)
			b.step(ref, "step.read_previous_logs", map[string]string{"container": c.Name})
		}
	}
}

func crashHypotheses(b *builder, p *corev1.Pod, c containerView, last *terminatedView, sup []string) {
	ref := podRef(p)
	params := map[string]string{"container": c.Name, "exitCode": itoa(last.ExitCode)}
	probe, hasProbe := lastEvent(b.c.podEvents(p.Name, "Unhealthy"))
	if hasProbe && contains(probe.Message, "liveness") {
		f := b.factFromEvent(ref, probe)
		h := b.hypothesis(ref, "crash.liveness_probe_failed", ConfidenceHigh, params, append(append([]string(nil), sup...), f), nil)
		b.recommend(ref, "rec.tune_liveness_probe", params, h)
	}
	code, rec, conf := "crash.application_error", "rec.fix_application_error", ConfidenceMedium
	switch last.ExitCode {
	case 0:
		code, rec = "crash.exited_normally", "rec.use_job_or_keep_process"
	case 126:
		code, rec, conf = "crash.command_not_executable", "rec.fix_container_command", ConfidenceHigh
	case 127:
		code, rec, conf = "crash.command_not_found", "rec.fix_container_command", ConfidenceHigh
	case 137:
		code, rec, conf = "crash.killed", "rec.check_kill_reason", ConfidenceLow
	case 139:
		code, rec = "crash.segfault", "rec.fix_application_error"
	case 143:
		code, rec, conf = "crash.terminated", "rec.check_kill_reason", ConfidenceLow
	}
	h := b.hypothesis(ref, code, conf, params, sup, nil)
	b.recommend(ref, rec, params, h)
}

// ruleOOMKilled — контейнер завершён по нехватке памяти.
func ruleOOMKilled(b *builder) {
	for _, p := range b.c.Pods {
		ref := podRef(p)
		for _, c := range containers(p) {
			oom := firstOOM(c)
			if oom == nil {
				continue
			}
			b.symptom(SymptomOOMKilled)
			field := containerField(c, "lastState.terminated")
			if c.Terminated != nil && c.Terminated.Reason == "OOMKilled" {
				field = containerField(c, "state.terminated")
			}
			params := map[string]string{"container": c.Name}
			killed := b.fact(ref, "container.oom_killed", srcPod(p, field), map[string]string{
				"container": c.Name, "exitCode": itoa(oom.ExitCode), "restarts": itoa(c.RestartCount), "finishedAt": formatTime(oom.FinishedAt)})
			logf := b.factLog(ref, "container.log_tail", srcLog(p, c.Name), params, LogRef{Container: c.Name, Previous: c.Terminated == nil || c.Terminated.Reason != "OOMKilled"})
			limit, hasLimit := memoryLimit(p, c.Name)
			var limitFact string
			if hasLimit {
				limitFact = b.fact(ref, "container.memory_limit", srcPod(p, "spec.containers["+c.Name+"].resources.limits.memory"),
					map[string]string{"container": c.Name, "limit": limit})
			} else {
				limitFact = b.fact(ref, "container.no_memory_limit", srcPod(p, "spec.containers["+c.Name+"].resources.limits.memory"), params)
			}
			if hasLimit {
				h := b.hypothesis(ref, "oom.limit_too_low", ConfidenceMedium, map[string]string{"container": c.Name, "limit": limit},
					[]string{killed, limitFact, logf}, nil)
				b.recommend(ref, "rec.raise_memory_limit", map[string]string{"container": c.Name, "limit": limit}, h)
			}
			leakSup := []string{killed}
			if c.RestartCount > 1 {
				leakSup = append(leakSup, b.fact(ref, "container.restarts", srcPod(p, containerField(c, "restartCount")),
					map[string]string{"container": c.Name, "count": itoa(c.RestartCount)}))
			}
			h2 := b.hypothesis(ref, "oom.memory_growth", ConfidenceLow, params, leakSup, nil)
			b.recommend(ref, "rec.investigate_memory_usage", params, h2)
			b.step(ref, "step.check_memory_metrics", params, h2)

			nodeSup, nodeContra := []string(nil), []string(nil)
			if !hasLimit {
				nodeSup = append(nodeSup, killed, limitFact)
			} else {
				nodeContra = append(nodeContra, limitFact)
			}
			if node := b.c.Nodes[p.Spec.NodeName]; node != nil && nodeMemoryPressure(node) {
				nodeSup = append(nodeSup, b.fact(ref, "node.memory_pressure",
					Source{Kind: SourceResource, Resource: ResourceRef{Kind: "Node", Name: node.Name, UID: string(node.UID)}, Field: "status.conditions[MemoryPressure]"},
					map[string]string{"node": node.Name}))
			}
			if len(nodeSup) > 0 {
				conf := ConfidenceLow
				if !hasLimit {
					conf = ConfidenceMedium
				}
				h3 := b.hypothesis(ref, "oom.node_memory_pressure", conf, map[string]string{"node": p.Spec.NodeName}, nodeSup, nodeContra)
				b.recommend(ref, "rec.set_memory_limit", params, h3)
			}
		}
	}
}

func nodeMemoryPressure(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeMemoryPressure {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func firstOOM(c containerView) *terminatedView {
	if c.Terminated != nil && c.Terminated.Reason == "OOMKilled" {
		return c.Terminated
	}
	if c.Last != nil && c.Last.Reason == "OOMKilled" {
		return c.Last
	}
	return nil
}
