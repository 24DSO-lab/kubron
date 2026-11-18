// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
)

// signatureVersion — версия формата сериализации; входит в хэш, чтобы смена формата не давала ложных совпадений.
const signatureVersion = "kubron.signature.v1"

// SignatureInput — данные сигнатуры расследования (ADR-0034 §4): только владелец-workload цели, коды симптомов
// и пары «правило, код» гипотез. Значений из кластера, текстов и параметров здесь нет.
type SignatureInput struct {
	WorkloadKind, WorkloadNamespace, WorkloadName string
	Symptoms                                      []string
	// Hypotheses — гипотезы с уверенностью medium и выше (отбор делает вызывающий). Эпоха в хэш не входит:
	// повышение эпохи правила не должно разрывать связь «повторение → предыдущее».
	Hypotheses []Ref
}

// Signature возвращает сигнатуру расследования: hex (32 символа) от стабильной сериализации. Порядок входных
// списков и повторы значения не имеют. Пустая строка — сигнатуру построить нельзя (нет имени workload или нет
// ни симптомов, ни гипотез).
func Signature(in SignatureInput) string {
	if in.WorkloadName == "" {
		return ""
	}
	symptoms := uniqueSorted(in.Symptoms)
	type pair struct{ rule, code string }
	pairs := make([]pair, 0, len(in.Hypotheses))
	for _, h := range in.Hypotheses {
		pairs = append(pairs, pair{h.RuleID, h.Code})
	}
	slices.SortFunc(pairs, func(a, b pair) int {
		return cmp.Or(cmp.Compare(a.rule, b.rule), cmp.Compare(a.code, b.code))
	})
	pairs = slices.Compact(pairs)
	if len(symptoms) == 0 && len(pairs) == 0 {
		return ""
	}

	h := sha256.New()
	put := func(s string) { // длина перед значением: границы полей однозначны при любом содержимом
		_, _ = h.Write([]byte(strconv.Itoa(len(s)) + ":" + s + ";"))
	}
	put(signatureVersion)
	put(in.WorkloadKind)
	put(in.WorkloadNamespace)
	put(in.WorkloadName)
	put(strconv.Itoa(len(symptoms)))
	for _, s := range symptoms {
		put(s)
	}
	put(strconv.Itoa(len(pairs)))
	for _, p := range pairs {
		put(p.rule)
		put(p.code)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
