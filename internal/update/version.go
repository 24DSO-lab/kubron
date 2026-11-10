// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// parseVersion разбирает версию строго по SemVer 2.0: «1.2.3», «v1.2.3», «1.2.3-rc.1».
// «1.2» и «1» не принимаются: тег релиза должен быть полной версией (проверяет release.yml).
func parseVersion(s string) (*semver.Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	v, err := semver.StrictNewVersion(s)
	if err != nil {
		return nil, false
	}
	return v, true
}

// NormalizeVersion приводит версию к виду без префикса «v»; ok=false, если это не SemVer
// (в том числе «dev»).
func NormalizeVersion(s string) (string, bool) {
	v, ok := parseVersion(s)
	if !ok {
		return "", false
	}
	return v.String(), true
}

// IsNewer сообщает, что latest новее current по правилам SemVer (предрелиз младше релиза:
// 1.3.0-rc.1 < 1.3.0). Если любая из версий не разбирается, например у сборки «dev», — false:
// предлагать обновление, не зная, с чем сравниваем, нельзя.
func IsNewer(current, latest string) bool {
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	return l.GreaterThan(c)
}

// imageTag — версия для тега образа: у тегов Docker нет символа «+», поэтому метаданные сборки отбрасываются.
func imageTag(v string) string {
	core, _, _ := strings.Cut(v, "+")
	return core
}
