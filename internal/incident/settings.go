// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Settings — настройки хранения организации (server) или приложения (desktop). Читаются при каждой операции:
// изменение действует сразу.
type Settings struct {
	// DisableLogEvidence — выключатель «не сохранять фрагменты логов»: finding хранит только метаданные и источник.
	DisableLogEvidence bool `json:"disableLogEvidence"`
	// ExcludedNamespaces — namespace, логи которых не сохраняются никогда. По умолчанию пуст.
	ExcludedNamespaces []string `json:"excludedNamespaces"`
	// FragmentRetentionDays — срок хранения фрагментов, 1…15 (по умолчанию и максимум 15): только сокращение.
	FragmentRetentionDays int `json:"fragmentRetentionDays"`
	// IncidentRetentionDays — срок хранения инцидентов, 1…365 (по умолчанию 90).
	IncidentRetentionDays int `json:"incidentRetentionDays"`
}

// DefaultSettings — значения по умолчанию из ADR-0024 §5.
func DefaultSettings() Settings {
	return Settings{ExcludedNamespaces: []string{}, FragmentRetentionDays: MaxFragmentRetentionDays,
		IncidentRetentionDays: DefaultIncidentRetentionDays}
}

// Normalize подставляет значения по умолчанию в незаполненные поля и убирает повторы и пустые namespace.
func (s Settings) Normalize() Settings {
	if s.FragmentRetentionDays == 0 {
		s.FragmentRetentionDays = MaxFragmentRetentionDays
	}
	if s.IncidentRetentionDays == 0 {
		s.IncidentRetentionDays = DefaultIncidentRetentionDays
	}
	ns := make([]string, 0, len(s.ExcludedNamespaces))
	for _, n := range s.ExcludedNamespaces {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(ns, n) {
			ns = append(ns, n)
		}
	}
	slices.Sort(ns)
	s.ExcludedNamespaces = ns
	return s
}

// Validate проверяет границы. Срок фрагментов выше 15 дней отвергается, а не обрезается молча.
func (s Settings) Validate() error {
	switch {
	case s.FragmentRetentionDays < 1 || s.FragmentRetentionDays > MaxFragmentRetentionDays:
		return fmt.Errorf("%w: fragment retention must be 1-%d days", ErrInvalidSettings, MaxFragmentRetentionDays)
	case s.IncidentRetentionDays < 1 || s.IncidentRetentionDays > MaxIncidentRetentionDays:
		return fmt.Errorf("%w: incident retention must be 1-%d days", ErrInvalidSettings, MaxIncidentRetentionDays)
	case len(s.ExcludedNamespaces) > 500:
		return fmt.Errorf("%w: too many excluded namespaces", ErrInvalidSettings)
	}
	for _, n := range s.ExcludedNamespaces {
		if len(n) > 253 || strings.ContainsAny(n, " \t\r\n/\\") {
			return fmt.Errorf("%w: invalid namespace name", ErrInvalidSettings)
		}
	}
	return nil
}

// LogsAllowed сообщает, можно ли сохранять фрагменты логов namespace и по какой причине нельзя.
func (s Settings) LogsAllowed(namespace string) (bool, string) {
	switch {
	case s.DisableLogEvidence:
		return false, ReasonDisabledOrg
	case slices.Contains(s.ExcludedNamespaces, namespace):
		return false, ReasonDisabledNamespace
	}
	return true, ""
}

// FragmentCutoff — момент, раньше которого фрагменты подлежат удалению.
func (s Settings) FragmentCutoff(now time.Time) time.Time {
	return now.AddDate(0, 0, -s.Normalize().FragmentRetentionDays)
}

// IncidentCutoff — момент, раньше которого (по последней активности) инциденты подлежат удалению.
func (s Settings) IncidentCutoff(now time.Time) time.Time {
	return now.AddDate(0, 0, -s.Normalize().IncidentRetentionDays)
}

// Переменные окружения KUBRON_INCIDENT_*: задают значения по умолчанию для организаций без сохранённых настроек.
// Сохранённые настройки (интерфейс) имеют приоритет; верхние границы из ADR действуют всегда.
const (
	EnvLogEvidence       = "KUBRON_INCIDENT_LOG_EVIDENCE"            // on|off
	EnvFragmentRetention = "KUBRON_INCIDENT_FRAGMENT_RETENTION_DAYS" // 1…15
	EnvRetention         = "KUBRON_INCIDENT_RETENTION_DAYS"          // 1…365
	EnvExcludedNS        = "KUBRON_INCIDENT_EXCLUDED_NAMESPACES"     // список через запятую
	EnvPurgeInterval     = "KUBRON_INCIDENT_PURGE_INTERVAL"          // период задачи retention, по умолчанию 1h
)

// DefaultPurgeInterval — период задачи retention.
const DefaultPurgeInterval = time.Hour

// Config — настройки процесса из окружения.
type Config struct {
	Defaults      Settings
	PurgeInterval time.Duration
}

// ConfigFromEnv читает KUBRON_INCIDENT_*; getenv — os.Getenv или двойник теста. Неверное значение — ошибка
// запуска, а не молчаливое значение по умолчанию.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{Defaults: DefaultSettings(), PurgeInterval: DefaultPurgeInterval}
	if v := strings.TrimSpace(getenv(EnvLogEvidence)); v != "" {
		switch strings.ToLower(v) {
		case "on", "true", "1":
		case "off", "false", "0":
			cfg.Defaults.DisableLogEvidence = true
		default:
			return Config{}, fmt.Errorf("%s: expected on or off", EnvLogEvidence)
		}
	}
	for _, f := range []struct {
		env string
		dst *int
	}{{EnvFragmentRetention, &cfg.Defaults.FragmentRetentionDays}, {EnvRetention, &cfg.Defaults.IncidentRetentionDays}} {
		if v := strings.TrimSpace(getenv(f.env)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return Config{}, fmt.Errorf("%s: expected a number of days", f.env)
			}
			*f.dst = n
		}
	}
	if v := getenv(EnvExcludedNS); strings.TrimSpace(v) != "" {
		cfg.Defaults.ExcludedNamespaces = strings.Split(v, ",")
	}
	if v := strings.TrimSpace(getenv(EnvPurgeInterval)); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			return Config{}, fmt.Errorf("%s: expected a duration of at least 1m", EnvPurgeInterval)
		}
		cfg.PurgeInterval = d
	}
	cfg.Defaults = cfg.Defaults.Normalize()
	if err := cfg.Defaults.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
