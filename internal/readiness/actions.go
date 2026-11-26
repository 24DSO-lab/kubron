// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"fmt"
)

// buildActions строит подготовительные действия и проверки после обновления (ADR-0025 §2). Это рекомендации:
// Kubron ничего не выполняет в кластере (ADR-0025 §8). Действия, вытекающие из findings, ссылаются на их ключи;
// общие действия (заметки о релизе, проверка манифестов в Git, проверки после обновления) есть всегда: они
// закрывают то, что проверка по живым объектам видеть не может.
func buildActions(rep *Report) []Action {
	var acts []Action
	keys := func(match func(f *Finding) bool) []string {
		var out []string
		for i := range rep.Findings {
			if match(&rep.Findings[i]) {
				out = append(out, rep.Findings[i].Key)
			}
		}
		return out
	}
	add := func(phase, code string, related []string, url string) {
		acts = append(acts, Action{Phase: phase, Message: Message{Code: code}, Related: related, URL: url})
	}

	if k := keys(func(f *Finding) bool { return f.Type == TypeAPI && f.Status == StatusRemoved }); len(k) > 0 {
		add(PhaseBefore, CodeActMigrateRemoved, k, apiGuide(rep))
	}
	if k := keys(func(f *Finding) bool { return f.Type == TypeAPI && f.Status == StatusDeprecated }); len(k) > 0 {
		add(PhaseBefore, CodeActMigrateDeprecate, k, apiGuide(rep))
	}
	if k := keys(func(f *Finding) bool { return f.Type == TypePlatform && f.Status == StatusAttention }); len(k) > 0 {
		add(PhaseBefore, CodeActStepwise, k, "")
	}
	if k := keys(func(f *Finding) bool {
		return f.Type == TypeComponent && f.Status == StatusKnownLimitation
	}); len(k) > 0 {
		add(PhaseBefore, CodeActComponents, k, "")
	}
	if k := keys(func(f *Finding) bool {
		return f.Type == TypeComponent && f.Component != nil && (f.Component.State == StateDegraded || f.Component.State == StateUnavailable)
	}); len(k) > 0 {
		add(PhaseBefore, CodeActUnhealthy, k, "")
	}
	if k := keys(func(f *Finding) bool { return f.Type == TypeComponent && f.Status == StatusInsufficient }); len(k) > 0 {
		add(PhaseBefore, CodeActVerifyDocs, k, "")
	}
	if k := keys(func(f *Finding) bool { return f.Type == TypeComponent && f.Status == StatusNotRecognized }); len(k) > 0 {
		add(PhaseBefore, CodeActUnrecognized, k, "")
	}
	if k := keys(func(f *Finding) bool { return f.Type == TypeSource }); len(k) > 0 {
		add(PhaseBefore, CodeActNotChecked, k, "")
	}
	// Общие действия: что проверка по живым объектам не видит.
	add(PhaseBefore, CodeActGitOps, nil, "")
	add(PhaseBefore, CodeActReleaseNotes, nil, releaseNotesURL(rep))
	add(PhaseBefore, CodeActBackup, nil, "")

	add(PhaseAfter, CodeActNodesReady, nil, "")
	add(PhaseAfter, CodeActWorkloads, nil, "")
	if k := keys(func(f *Finding) bool { return f.Type == TypeComponent && f.Status != StatusNotRecognized }); len(k) > 0 {
		add(PhaseAfter, CodeActAddons, k, "")
	}
	add(PhaseAfter, CodeActRerun, nil, "")

	// Приоритет — порядок внутри фазы: 1 — первым.
	n := map[string]int{}
	for i := range acts {
		n[acts[i].Phase]++
		acts[i].Priority = n[acts[i].Phase]
	}
	return acts
}

func apiGuide(rep *Report) string {
	for _, f := range rep.Findings {
		if f.API != nil && f.API.GuideURL != "" {
			return f.API.GuideURL
		}
	}
	return ""
}

// releaseNotesURL — журнал изменений целевой версии в репозитории Kubernetes; без цели — следующей минорной.
func releaseNotesURL(rep *Report) string {
	v := rep.Target
	if v.IsZero() {
		v = rep.Current.Minor
	}
	return fmt.Sprintf("https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-%d.%d.md", v.Major, v.Minor)
}
