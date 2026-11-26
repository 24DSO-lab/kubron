// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package kver — разбор и сравнение версий Kubernetes с точностью до минорной версии. Общий для матрицы API,
// каталога компонентов и сервиса проверки (ADR-0025): везде сравниваются «мажор.минор», патч и суффиксы провайдера
// («-gke.1000», «-eks-abc», «+k3s1») отбрасываются.
package kver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalid — строка не является версией Kubernetes.
var ErrInvalid = errors.New("invalid Kubernetes version")

// Minor — минорная версия Kubernetes («1.35»). Нулевое значение означает «не задано».
type Minor struct {
	Major, Minor int
}

// IsZero сообщает, что версия не задана.
func (m Minor) IsZero() bool { return m == Minor{} }

// String возвращает «мажор.минор» или пустую строку для незаданной версии.
func (m Minor) String() string {
	if m.IsZero() {
		return ""
	}
	return fmt.Sprintf("%d.%d", m.Major, m.Minor)
}

// Compare возвращает -1, 0 или 1.
func (m Minor) Compare(o Minor) int {
	switch {
	case m.Major != o.Major:
		if m.Major < o.Major {
			return -1
		}
		return 1
	case m.Minor != o.Minor:
		if m.Minor < o.Minor {
			return -1
		}
		return 1
	}
	return 0
}

// Less сообщает, что m старше o.
func (m Minor) Less(o Minor) bool { return m.Compare(o) < 0 }

// Distance возвращает, на сколько минорных версий m новее o (отрицательное — старше). Определено только для
// одного мажора: у Kubernetes он всегда 1, и второй мажор означал бы смену правил, а не арифметику.
func (m Minor) Distance(o Minor) (int, bool) {
	if m.Major != o.Major {
		return 0, false
	}
	return m.Minor - o.Minor, true
}

// MarshalText и UnmarshalText позволяют хранить Minor строкой «1.35» в JSON.
func (m Minor) MarshalText() ([]byte, error) { return []byte(m.String()), nil }

func (m *Minor) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*m = Minor{}
		return nil
	}
	v, err := ParseMinor(string(b))
	if err != nil {
		return err
	}
	*m = v
	return nil
}

// ParseMinor разбирает «1.35» (допускается «v1.35» и «1.35.4»: патч отбрасывается).
func ParseMinor(s string) (Minor, error) {
	return parse(s, false)
}

// ParseGitVersion разбирает gitVersion сервера API («v1.30.4-gke.1000», «v1.29.2+k3s1»): суффиксы отбрасываются.
func ParseGitVersion(s string) (Minor, error) {
	return parse(s, true)
}

// ParseInfo разбирает пару полей version.Info: сначала gitVersion, при его непригодности Major/Minor
// (в Minor провайдеры добавляют «+»: «30+»).
func ParseInfo(gitVersion, major, minor string) (Minor, error) {
	if m, err := ParseGitVersion(gitVersion); err == nil {
		return m, nil
	}
	mj, err1 := strconv.Atoi(strings.TrimSpace(major))
	mn, err2 := strconv.Atoi(strings.TrimRight(strings.TrimSpace(minor), "+"))
	if err1 != nil || err2 != nil || mj < 1 || mn < 0 {
		return Minor{}, fmt.Errorf("%w: %q", ErrInvalid, gitVersion)
	}
	return Minor{Major: mj, Minor: mn}, nil
}

func parse(s string, lenient bool) (Minor, error) {
	orig := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if lenient {
		if i := strings.IndexAny(s, "-+"); i >= 0 {
			s = s[:i]
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Minor{}, fmt.Errorf("%w: %q", ErrInvalid, orig)
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return Minor{}, fmt.Errorf("%w: %q", ErrInvalid, orig)
		}
		nums[i] = n
	}
	if nums[0] < 1 {
		return Minor{}, fmt.Errorf("%w: %q", ErrInvalid, orig)
	}
	return Minor{Major: nums[0], Minor: nums[1]}, nil
}
