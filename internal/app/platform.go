// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import "github.com/24DSO-labs/kubron/pkg/ext"

// Platform — всё, что ядру нужно от оболочки, в которой оно запущено (ADR-0002).
// Интерфейс публичный (pkg/ext): оболочки могут жить в других модулях.
// Ядро не импортирует Wails: это проверяет imports_test.go.
type Platform = ext.Platform

// Capabilities — возможности оболочки (ADR-0010).
type Capabilities = ext.Capabilities

// nopPlatform — заглушка для тестов и режимов без оболочки.
type nopPlatform struct{}

func (nopPlatform) Emit(string, ...any)             {}
func (nopPlatform) PickFile(string) (string, error) { return "", nil }
func (nopPlatform) OpenURL(string)                  {}
func (nopPlatform) Capabilities() Capabilities      { return Capabilities{} }
