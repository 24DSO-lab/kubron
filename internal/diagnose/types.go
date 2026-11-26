// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package diagnose — детерминированная диагностика Pod, Deployment и ReplicaSet (ADR-0024 §2–4).
//
// Пакет читает кластер только через узкий интерфейс Reader (подмножество opsplatform.Reader; платформу пакет не
// импортирует, адаптер живёт в слое связки), строит нормализованный Context и прогоняет версионированный набор правил.
// Результат — Report, где факт, гипотеза и рекомендация — РАЗНЫЕ типы: гипотеза никогда не становится
// «установленной причиной», у неё есть лишь уверенность и перечни подтверждающих и противоречащих фактов.
//
// Правила защитные: нет необязательных данных — это MissingContext в отчёте, а не ошибка и не паника.
// Тексты отчёта — коды и параметры (английский текст в интерфейсе подставляется по коду); значения из кластера
// (сообщения Events, условий) лежат в Params и редактируются слоем сервиса (internal/incident) до сохранения.
// AI в MVP не используется.
package diagnose

import (
	"slices"
	"time"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// RuleSetVersion — версия набора правил. Поднимается при любом изменении правил или кодов: сохранённый инцидент
// хранит версию, которой построен его отчёт, и изменение диагностики остаётся проверяемым.
//
// Версия 2 (ADR-0031, ADR-0034): граф зависимостей, RuleID/RuleEpoch у гипотез и рекомендаций, Explains,
// Group у ссылки на ресурс. Изменение аддитивное: отчёты версии 1 остаются читаемыми, новые поля в них пусты.
//
// Версия 3 (ADR-0031 этапы 2–3, ADR-0032 этап B, ADR-0033 этап C): цепочечные правила Ingress, HPA, ConfigMap,
// StatefulSet, Job и CronJob, метрические правила этапа B, расчёт диапазона ordinal'ов StatefulSet с учётом
// spec.ordinals.start. Изменение аддитивное: отчёты версий 1 и 2 остаются читаемыми без миграции, а статистика
// обратной связи по-прежнему ключуется (RuleID, RuleEpoch), а не версией набора (ADR-0034).
const RuleSetVersion = 3

// Kind — тип записи отчёта. Он входит в контракт (JSON) и в интерфейс: факт, гипотеза и рекомендация различимы
// не только оформлением, но и типом.
type Kind string

const (
	KindFact           Kind = "fact"
	KindHypothesis     Kind = "hypothesis"
	KindRecommendation Kind = "recommendation"
	KindStep           Kind = "step"
)

// Confidence — уверенность в гипотезе. Значения «установлено» нет намеренно: подтвердить причину может только
// человек (результат расследования в internal/incident), а не правило.
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

// Valid сообщает, что значение — одно из определённых.
func (c Confidence) Valid() bool {
	return c == ConfidenceLow || c == ConfidenceMedium || c == ConfidenceHigh
}

// ResourceRef — ссылка на ресурс кластера.
type ResourceRef struct {
	// Group — API-группа вида; пусто для core и для отчётов версии 1 (там вид однозначен по имени).
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// SourceKind — откуда получен факт.
type SourceKind string

const (
	SourceResource SourceKind = "resource" // поле объекта
	SourceEvent    SourceKind = "event"    // Kubernetes Event
	SourceLog      SourceKind = "log"      // лог контейнера (содержимое здесь не хранится)
	SourceDerived  SourceKind = "derived"  // вычислено из нескольких источников
	SourceMetric   SourceKind = "metric"   // сигнал метрик (ADR-0032); имя сигнала — в Source.Signal
)

// Source — источник факта: что именно прочитано. Поле Field — путь в объекте (например, status.phase).
type Source struct {
	Kind     SourceKind  `json:"kind"`
	Resource ResourceRef `json:"resource"`
	Field    string      `json:"field,omitempty"`
	// Signal — имя сигнала метрик; заполнено только при Kind == SourceMetric.
	Signal string `json:"signal,omitempty"`
}

// LogRef — указание, что к факту относится фрагмент лога контейнера. Само содержимое в отчёт не входит:
// сохранение фрагмента (редакция, лимиты, право) — забота internal/incident.
type LogRef struct {
	Container string `json:"container"`
	Previous  bool   `json:"previous,omitempty"`
}

// Fact — проверяемое наблюдение: источник и время получения обязательны.
type Fact struct {
	Kind       Kind              `json:"kind"`
	ID         string            `json:"id"`
	Code       string            `json:"code"`
	Resource   ResourceRef       `json:"resource"`
	Params     map[string]string `json:"params,omitempty"`
	Source     Source            `json:"source"`
	ObservedAt time.Time         `json:"observedAt"`
	Log        *LogRef           `json:"log,omitempty"`
}

// Hypothesis — предположение о причине. Подтверждающие и противоречащие данные — идентификаторы фактов отчёта.
type Hypothesis struct {
	Kind          Kind              `json:"kind"`
	ID            string            `json:"id"`
	Code          string            `json:"code"`
	Resource      ResourceRef       `json:"resource"`
	Params        map[string]string `json:"params,omitempty"`
	Confidence    Confidence        `json:"confidence"`
	Supporting    []string          `json:"supporting"`
	Contradicting []string          `json:"contradicting"`
	// RuleID и RuleEpoch — правило, выдавшее гипотезу, и его эпоха (ADR-0034 §3). Ставит builder; в отчётах
	// версии 1 пусты.
	RuleID    string `json:"ruleId,omitempty"`
	RuleEpoch int    `json:"ruleEpoch,omitempty"`
	// Explains — идентификаторы гипотез этого же отчёта, которые данная гипотеза объясняет (ADR-0031 §5).
	// Это связь, а не вердикт: «первопричина установлена» и поля root_cause нет (ADR-0024 §3).
	Explains []string `json:"explains,omitempty"`
}

// Recommendation — предлагаемое изменение или решение. Kubron его не выполняет (MVP только читает).
type Recommendation struct {
	Kind       Kind              `json:"kind"`
	ID         string            `json:"id"`
	Code       string            `json:"code"`
	Resource   ResourceRef       `json:"resource"`
	Params     map[string]string `json:"params,omitempty"`
	Hypotheses []string          `json:"hypotheses"` // на какие гипотезы опирается
	// RuleID и RuleEpoch — правило, выдавшее рекомендацию, и его эпоха (ADR-0034 §3).
	RuleID    string `json:"ruleId,omitempty"`
	RuleEpoch int    `json:"ruleEpoch,omitempty"`
}

// Step — диагностический шаг: что ещё проверить (read-only), чтобы подтвердить или опровергнуть гипотезы.
type Step struct {
	Kind       Kind              `json:"kind"`
	ID         string            `json:"id"`
	Code       string            `json:"code"`
	Resource   ResourceRef       `json:"resource"`
	Params     map[string]string `json:"params,omitempty"`
	Hypotheses []string          `json:"hypotheses"`
}

// MissingContext — чего не хватило правилу: отчёт обязан показать это, а не завышать уверенность.
type MissingContext struct {
	Code     string            `json:"code"`
	Resource ResourceRef       `json:"resource"`
	Params   map[string]string `json:"params,omitempty"`
}

// Report — диагностический отчёт «симптом → факты → гипотезы → рекомендации».
type Report struct {
	RuleSetVersion  int              `json:"ruleSetVersion"`
	Target          ResourceRef      `json:"target"`
	ObservedAt      time.Time        `json:"observedAt"`
	Symptoms        []string         `json:"symptoms"` // коды симптомов (pod.pending, pod.crash_loop, ...)
	Resources       []ResourceRef    `json:"resources"`
	Facts           []Fact           `json:"facts"`
	Hypotheses      []Hypothesis     `json:"hypotheses"`
	Recommendations []Recommendation `json:"recommendations"`
	Steps           []Step           `json:"steps"`
	Missing         []MissingContext `json:"missing"`
	Partial         []string         `json:"partial,omitempty"` // источники, прочитанные частично
	// Graph — слепок графа зависимостей, по которому построены цепочки (ADR-0031). Пусто, если граф не строился.
	Graph *depgraph.Snapshot `json:"graph,omitempty"`
	// Chains — пути по графу от затронутого ресурса до гипотезы-кандидата.
	Chains []Chain `json:"chains,omitempty"`
}

// Chain — путь по графу от затронутого ресурса к ресурсу гипотезы-кандидата. Это объяснение «как связаны»,
// а не установленная причина.
type Chain struct {
	Path []ChainStep `json:"path"`
	// Candidate — идентификатор гипотезы-кандидата, на которой цепочка заканчивается.
	Candidate string `json:"candidate,omitempty"`
}

// ChainStep — звено цепочки: ресурс, его состояние в графе и код причины состояния (без значений из кластера).
type ChainStep struct {
	Ref    ResourceRef `json:"ref"`
	Health string      `json:"health"`
	Reason string      `json:"reason,omitempty"`
}

// RootCandidates возвращает идентификаторы гипотез, которые объясняют другие гипотезы и сами не объясняются
// никакой другой (ADR-0031 §5). Порядок — как в отчёте. Это «кандидаты», а не «первопричины»: подтвердить
// причину может только человек. Циклы Explains безопасны: участники цикла объясняются друг другом, поэтому
// кандидатами не становятся, а обход графа здесь не нужен.
func (r *Report) RootCandidates() []string {
	out := []string{}
	if r == nil {
		return out
	}
	explained := map[string]bool{}
	for _, h := range r.Hypotheses {
		for _, id := range h.Explains {
			if id != h.ID {
				explained[id] = true
			}
		}
	}
	for _, h := range r.Hypotheses {
		if explained[h.ID] {
			continue
		}
		if slices.ContainsFunc(h.Explains, func(id string) bool { return id != h.ID }) {
			out = append(out, h.ID)
		}
	}
	return out
}

// Коды симптомов.
const (
	SymptomPending        = "pod.pending"
	SymptomCrashLoop      = "pod.crash_loop"
	SymptomOOMKilled      = "pod.oom_killed"
	SymptomStartupFailure = "pod.startup_failure"
	SymptomRollout        = "workload.rollout"
	SymptomNone           = "none"
)
