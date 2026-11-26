// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"bytes"
	"encoding/json"
	"slices"
)

// Идентификация файла выгрузки (ADR-0034 §8).
const (
	ExportFormat        = "kubron.rule-stats"
	ExportFormatVersion = 1
)

// exportDoc — файл анонимной выгрузки для мантейнеров. Состоит только из агрегатов: кодов правил, счётчиков,
// медиан и долей. Имён ресурсов, namespace, идентификаторов (расследований, организации, команды, кластера),
// пользователей, комментариев, сигнатур и точных моментов времени (включая ComputedAt) в нём нет по построению:
// таких полей нет и в Stats.
type exportDoc struct {
	Format          string `json:"format"`
	FormatVersion   int    `json:"formatVersion"`
	RuleSetVersion  int    `json:"ruleSetVersion"`  // версия набора правил сборки, сделавшей выгрузку
	RuleSetVersions []int  `json:"ruleSetVersions"` // версии наборов в выборке
	Records         int    `json:"records"`
	Unevaluated     int    `json:"unevaluated"`
	Truncated       bool   `json:"truncated"`

	Hypotheses      []HypothesisStat     `json:"hypotheses"`
	Recommendations []RecommendationStat `json:"recommendations"`
	Symptoms        []SymptomStat        `json:"symptoms"`
	Correlations    []CorrelationStat    `json:"correlations"`
	Features        []FeatureStat        `json:"features"`
}

// Export сериализует статистику в анонимный JSON для ручной отправки мантейнерам (ADR-0034 §8). ruleSetVersion —
// версия набора правил текущей сборки (diagnose.RuleSetVersion). Числа ниже порога доверия выгружаются как есть:
// решение, что с ними делать, принимает мантейнер, а идентификаторов, по которым малое число деанонимизирует,
// в файле нет. Вывод детерминирован: списки сортируются независимо от порядка во входе.
func Export(st Stats, ruleSetVersion int) ([]byte, error) {
	doc := exportDoc{
		Format: ExportFormat, FormatVersion: ExportFormatVersion, RuleSetVersion: ruleSetVersion,
		RuleSetVersions: nonNil(slices.Clone(st.RuleSetVersions)), Records: st.Records,
		Unevaluated: st.Unevaluated, Truncated: st.Truncated,
		Hypotheses:      nonNil(slices.Clone(st.Hypotheses)),
		Recommendations: nonNil(slices.Clone(st.Recommendations)),
		Symptoms:        nonNil(slices.Clone(st.Symptoms)),
		Correlations:    nonNil(slices.Clone(st.Correlations)),
		Features:        nonNil(slices.Clone(st.Features)),
	}
	// Канонический порядок: повторно используем сортировку Compute через временную Stats.
	tmp := Stats{RuleSetVersions: doc.RuleSetVersions, Hypotheses: doc.Hypotheses, Recommendations: doc.Recommendations,
		Symptoms: doc.Symptoms, Correlations: doc.Correlations, Features: doc.Features}
	sortStats(&tmp)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
