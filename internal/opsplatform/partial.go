// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrPartial — источник доступен лишь частично (например, часть API-групп discovery не ответила). Вместе с ошибкой
// метод возвращает всё, что удалось прочитать: потребитель использует данные и ОБЯЗАН показать неполноту
// (передать описание в Op.End как partial), а не выдавать результат за исчерпывающий (ADR-0026 §2).
// Дополнение к замороженному контракту; зафиксировано в NOTES.md.
var ErrPartial = errors.New("source is partially available")

// PartialError — ErrPartial с перечнем недоступных частей. Is(ErrPartial) истинно.
type PartialError struct {
	// Failed — недоступные части (например, «metrics.k8s.io/v1beta1»), отсортированы.
	Failed []string
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("%v: %s", ErrPartial, strings.Join(e.Failed, ", "))
}

// Is связывает PartialError с ErrPartial.
func (e *PartialError) Is(target error) bool { return target == ErrPartial }

// PartialDetails возвращает недоступные части из err, если он — или его цепочка — PartialError.
func PartialDetails(err error) []string {
	var pe *PartialError
	if errors.As(err, &pe) {
		return slices.Clone(pe.Failed)
	}
	return nil
}
