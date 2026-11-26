// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package feedback

import "time"

// Пороги и окна статистики. Заданы в коде и не настраиваются: изменить их можно только новым ADR.
const (
	// MinSample — минимальное число оценённых случаев, с которого показывается доля (ADR-0034 §6).
	MinSample = 5
	// UselessShare — доля «не помогла» среди оценок рекомендации, с которой она получает пометку «ранее не
	// помогала» (ADR-0034 §7); применяется при Sample >= MinSample.
	UselessShare = 0.7
	// RecurrenceWindow — окно повторения: новое расследование с той же сигнатурой в течение этого срока после
	// устранения считается повторением (ADR-0034 §4).
	RecurrenceWindow = 30 * 24 * time.Hour
	// StatsWindow — сколько последних закрытых расследований кластера входит в статистику (ADR-0034 §6).
	StatsWindow = 200
)

// Результаты расследования, которые различает статистика. Значения совпадают с incident.Result: пакет не импортирует
// incident, поэтому повторяет строки (контракт JSON, менять нельзя).
const (
	ResultResolved = "resolved"
)

// Вердикты человека о кандидате корреляции (значения совпадают с incident.Verdict*).
const (
	VerdictConfirmed = "confirmed"
	VerdictRejected  = "rejected"
)

// Признаки отчёта (Record.Features): какие необязательные источники были в расследовании (ADR-0034 §5).
const (
	FeatureGraph       = "graph"
	FeatureMetrics     = "metrics"
	FeatureCorrelation = "correlation"
)

// Ref — ключ статистики: правило, его эпоха и код гипотезы или рекомендации (ADR-0034 §3). Эпоха поднимается
// только при изменении смысла правила, поэтому данные разных эпох не смешиваются.
type Ref struct {
	RuleID string `json:"ruleId"`
	Epoch  int    `json:"epoch"`
	Code   string `json:"code"`
}

// Record — закрытое расследование в виде, достаточном для статистики. Строится адаптером из incident.Summary
// (internal/incident/feedback_stats.go); в записи только коды, числа и идентификатор расследования, которым
// статистика пользуется для связи «повторение → предыдущее» и не выводит наружу.
type Record struct {
	Result         string     // resolved | unresolved | inconclusive
	OpenedAt       time.Time  // момент открытия
	ResolvedAt     *time.Time // момент устранения (только у resolved)
	RuleSetVersion int
	Symptoms       []string // коды симптомов отчёта
	Confirmed      *Ref     // подтверждённая гипотеза
	Rejected       []Ref    // отвергнутые гипотезы
	Helpful        []Ref    // помогшие рекомендации
	Useless        []Ref    // не помогшие рекомендации
	RecurrenceOf   string   // расследование, повторением которого является это
	ID             string
	Features       []string // graph, metrics, correlation

	CorrelationKind       string // вид кандидата корреляции, который оценил человек
	CorrelationConfidence string // уверенность этого кандидата
	CorrelationVerdict    string // confirmed | rejected | unknown
}

// HypothesisStat — статистика гипотезы по ключу (RuleID, Epoch, Code).
type HypothesisStat struct {
	Ref
	Confirmed int `json:"confirmed"` // подтверждена (только расследования с результатом resolved)
	Rejected  int `json:"rejected"`  // отвергнута (только resolved)
	// Precision — Confirmed ÷ (Confirmed + Rejected); nil, пока Sample < MinSample.
	Precision *float64 `json:"precision,omitempty"`
	Sample    int      `json:"sample"`  // Confirmed + Rejected: число оценённых случаев
	Trusted   bool     `json:"trusted"` // Sample >= MinSample: доле можно верить
	// MedianResolveSeconds — медиана ResolvedAt − OpenedAt по resolved с этой подтверждённой гипотезой; nil — нет данных.
	MedianResolveSeconds *int64 `json:"medianResolveSeconds,omitempty"`
	Resolved             int    `json:"resolved"` // устранено с этой подтверждённой гипотезой
	Recurred             int    `json:"recurred"` // из них — за чем последовало расследование-повторение
}

// RecommendationStat — статистика рекомендации по ключу (RuleID, Epoch, Code).
type RecommendationStat struct {
	Ref
	Helpful int `json:"helpful"`
	Useless int `json:"useless"`
	// Ratio — Helpful ÷ (Helpful + Useless); nil, пока Sample < MinSample.
	Ratio   *float64 `json:"ratio,omitempty"`
	Sample  int      `json:"sample"`  // Helpful + Useless
	Trusted bool     `json:"trusted"` // Sample >= MinSample
	// PreviouslyUnhelpful — пометка «ранее не помогала»: Sample >= MinSample и доля Useless не ниже UselessShare.
	PreviouslyUnhelpful bool `json:"previouslyUnhelpful"`
}

// SymptomStat — покрытие симптома: на скольких закрытых расследованиях с этим кодом человек подтвердил гипотезу.
type SymptomStat struct {
	Code          string `json:"code"`
	Closed        int    `json:"closed"`        // закрытых расследований с симптомом (любой результат)
	WithConfirmed int    `json:"withConfirmed"` // из них с подтверждённой гипотезой
	// Coverage — WithConfirmed ÷ Closed; nil, пока Closed < MinSample.
	Coverage *float64 `json:"coverage,omitempty"`
}

// CorrelationStat — точность оценок корреляции по виду кандидата и его уверенности (ADR-0033 §4).
type CorrelationStat struct {
	Kind       string `json:"kind"`
	Confidence string `json:"confidence"`
	Confirmed  int    `json:"confirmed"`
	Rejected   int    `json:"rejected"`
	// Precision — Confirmed ÷ (Confirmed + Rejected); nil, пока их сумма < MinSample.
	Precision *float64 `json:"precision,omitempty"`
}

// FeatureCounts — оценки гипотез в расследованиях с признаком или без него.
type FeatureCounts struct {
	Records   int `json:"records"`   // расследований в группе
	Confirmed int `json:"confirmed"` // подтверждённых гипотез (resolved)
	Rejected  int `json:"rejected"`  // отвергнутых гипотез (resolved)
	// Precision — Confirmed ÷ (Confirmed + Rejected); nil, пока их сумма < MinSample.
	Precision *float64 `json:"precision,omitempty"`
}

// FeatureStat — сравнение точности гипотез с источником (graph, metrics, correlation) и без него
// (ADR-0034 §5: основание для порогов ADR-0032).
type FeatureStat struct {
	Feature string        `json:"feature"`
	With    FeatureCounts `json:"with"`
	Without FeatureCounts `json:"without"`
}

// Stats — детерминированная статистика правил кластера (ADR-0034 §6). Только коды и числа.
type Stats struct {
	Records     int  `json:"records"`     // расследований в статистике
	Unevaluated int  `json:"unevaluated"` // из них без явных оценок человека: в знаменатели не входят
	Truncated   bool `json:"truncated"`   // есть закрытые расследования сверх окна StatsWindow
	// ComputedAt — момент расчёта; в выгрузку не попадает.
	ComputedAt      time.Time `json:"computedAt"`
	RuleSetVersions []int     `json:"ruleSetVersions"` // версии наборов правил в выборке, по возрастанию

	Hypotheses      []HypothesisStat     `json:"hypotheses"`
	Recommendations []RecommendationStat `json:"recommendations"`
	Symptoms        []SymptomStat        `json:"symptoms"`
	Correlations    []CorrelationStat    `json:"correlations"`
	Features        []FeatureStat        `json:"features"`
}
