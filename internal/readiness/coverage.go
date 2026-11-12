// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

// SourceKind — вид источника данных проверки (ADR-0025 §2).
type SourceKind string

const (
	// SourceLiveAPI — живые объекты Kubernetes API кластера.
	SourceLiveAPI SourceKind = "live_api"
	// SourceGitOps — манифесты GitOps (Argo CD, Flux, Git). В первой версии не анализируются.
	SourceGitOps SourceKind = "gitops_manifests"
	// SourceLocalFiles — локальные файлы манифестов. В первой версии не анализируются.
	SourceLocalFiles SourceKind = "local_files"
)

// AllSources — виды источников, которые обязаны быть перечислены в coverage: «не проверено» — часть структуры.
var AllSources = []SourceKind{SourceLiveAPI, SourceGitOps, SourceLocalFiles}

// Valid сообщает, что вид источника известен.
func (k SourceKind) Valid() bool { return slices.Contains(AllSources, k) }

// Методы обнаружения живого API (SourceCoverage.Methods).
const (
	MethodServerVersion   = "server_version"
	MethodServerResources = "server_resources"
	MethodManagedFields   = "managed_fields"
	MethodLastApplied     = "last_applied"
	MethodWorkloadImages  = "workload_images"
	MethodNodeInfo        = "node_info"
)

// Коды ограничений coverage. Подписи переводит интерфейс; английский текст не хранится.
const (
	LimitProviderNotChecked  = "coverage.provider.notChecked"
	LimitNotAGuarantee       = "coverage.notAGuarantee"
	LimitNoTarget            = "coverage.noTarget"
	LimitLiveOnly            = "coverage.live.onlyLiveObjects"
	LimitLiveManagedFields   = "coverage.live.managedFieldsOnly"
	LimitLiveReadOnlyClients = "coverage.live.readClientsInvisible"
	LimitLiveKindsScanned    = "coverage.live.kindsScanned"
	LimitLiveCustomResources = "coverage.live.customResources"
	LimitLiveNoHelmReleases  = "coverage.live.helmReleases"
	LimitLiveComponents      = "coverage.live.componentsCatalogOnly"
	LimitNotAnalyzed         = "coverage.source.notAnalyzed"
	LimitStaleMatrix         = "coverage.matrix.staleForCluster"
)

// SourceCoverage — покрытие одного источника. Непроверенный источник обязан называть причину (Reason).
type SourceCoverage struct {
	Kind    SourceKind `json:"kind"`
	Checked bool       `json:"checked"`
	// Partial — источник прочитан не полностью (недоступна часть API, обрезан список, истёк deadline).
	Partial    bool      `json:"partial,omitempty"`
	ObservedAt time.Time `json:"observedAt,omitzero"`
	// Methods — способы обнаружения, которые реально отработали.
	Methods []string `json:"methods,omitempty"`
	// Scanned — виды ресурсов «группа/Kind», объекты которых прочитаны на предмет использования API.
	Scanned []string `json:"scanned,omitempty"`
	// NotCovered — виды «группа/Kind» с устаревшими или удалёнными версиями в матрице, которые прочитать нельзя
	// (вне allowlist платформы, нет в кластере, недоступны): использование этих API НЕ проверено.
	NotCovered  []string  `json:"notCovered,omitempty"`
	Limitations []Message `json:"limitations,omitempty"`
	// Reason — почему источник не проверен.
	Reason *Message `json:"reason,omitempty"`
}

// Coverage — обязательное поле покрытия отчёта (ADR-0025 §2): какие источники реально проверены, какие нет, режим
// проверки, версии и даты используемых матриц. Отчёт без заполненного coverage невалиден.
type Coverage struct {
	// Mode — режим проверки; в первой версии только ModeGenericKubernetes (общий Kubernetes, без провайдера).
	Mode string `json:"mode"`
	// TargetSpecified — целевая версия указана пользователем. Иначе API оцениваются на горизонте EvaluatedAgainst.
	TargetSpecified bool `json:"targetSpecified"`
	// EvaluatedAgainst — версия Kubernetes, относительно которой оценены устаревшие и удалённые API.
	EvaluatedAgainst kver.Minor       `json:"evaluatedAgainst"`
	Sources          []SourceCoverage `json:"sources"`
	APIMatrix        MatrixInfo       `json:"apiMatrix"`
	Catalog          CatalogInfo      `json:"catalog"`
	// Limitations — общие ограничения проверки (не гарантия обновления, нет provider-specific проверок и т. п.).
	Limitations []Message `json:"limitations"`
}

// Source возвращает покрытие источника вида k.
func (c *Coverage) Source(k SourceKind) (*SourceCoverage, bool) {
	for i := range c.Sources {
		if c.Sources[i].Kind == k {
			return &c.Sources[i], true
		}
	}
	return nil, false
}

// IsZero сообщает, что coverage не заполнен вовсе.
func (c *Coverage) IsZero() bool {
	return c.Mode == "" && len(c.Sources) == 0 && len(c.Limitations) == 0 &&
		c.APIMatrix.Version == "" && c.Catalog.Version == "" && c.EvaluatedAgainst.IsZero()
}

// ErrCoverage — coverage пуст или заполнен не полностью. Оборачивается в ErrInvalidReport.
var ErrCoverage = errors.New("coverage is missing or incomplete")

// Validate проверяет coverage. Правила защищают смысл поля: «не проверено» присутствует в структуре данных,
// проверенный источник не может заявить полноту без ограничений.
func (c *Coverage) Validate() error {
	if c.IsZero() {
		return fmt.Errorf("%w: coverage is empty", ErrCoverage)
	}
	if c.Mode == "" {
		return fmt.Errorf("%w: mode is not set", ErrCoverage)
	}
	if c.EvaluatedAgainst.IsZero() {
		return fmt.Errorf("%w: the version the APIs were evaluated against is not set", ErrCoverage)
	}
	if len(c.Limitations) == 0 {
		return fmt.Errorf("%w: general limitations are not listed", ErrCoverage)
	}
	if c.Mode == ModeGenericKubernetes && !hasCode(c.Limitations, LimitProviderNotChecked) {
		return fmt.Errorf("%w: the generic mode must state that provider-specific checks are absent", ErrCoverage)
	}
	for _, m := range c.Limitations {
		if m.Code == "" {
			return fmt.Errorf("%w: a limitation has no code", ErrCoverage)
		}
	}
	if c.APIMatrix.Version == "" || c.APIMatrix.GeneratedAt == "" || len(c.APIMatrix.Window) == 0 {
		return fmt.Errorf("%w: the API matrix version, date and window are not recorded", ErrCoverage)
	}
	if c.Catalog.Version == "" || c.Catalog.CheckedAt == "" {
		return fmt.Errorf("%w: the component catalog version and date are not recorded", ErrCoverage)
	}
	seen := map[SourceKind]bool{}
	for _, s := range c.Sources {
		if !s.Kind.Valid() {
			return fmt.Errorf("%w: unknown source %q", ErrCoverage, s.Kind)
		}
		if seen[s.Kind] {
			return fmt.Errorf("%w: source %q is listed twice", ErrCoverage, s.Kind)
		}
		seen[s.Kind] = true
		if err := s.validate(); err != nil {
			return fmt.Errorf("%w: source %q: %w", ErrCoverage, s.Kind, err)
		}
	}
	for _, k := range AllSources {
		if !seen[k] {
			return fmt.Errorf("%w: source %q is not listed: an unchecked source must be stated, not omitted", ErrCoverage, k)
		}
	}
	return nil
}

func (s *SourceCoverage) validate() error {
	if !s.Checked {
		if s.Reason == nil || s.Reason.Code == "" {
			return errors.New("an unchecked source needs a reason")
		}
		if s.Partial || len(s.Methods) > 0 || len(s.Scanned) > 0 {
			return errors.New("an unchecked source cannot report methods, scanned kinds or partial reading")
		}
		return nil
	}
	if s.ObservedAt.IsZero() {
		return errors.New("a checked source needs the observation time")
	}
	if len(s.Methods) == 0 {
		return errors.New("a checked source must name how it was checked")
	}
	if len(s.Limitations) == 0 {
		return errors.New("a checked source must state its limitations: no source is checked exhaustively")
	}
	for _, m := range s.Limitations {
		if m.Code == "" {
			return errors.New("a limitation has no code")
		}
	}
	return nil
}

func hasCode(list []Message, code string) bool {
	return slices.ContainsFunc(list, func(m Message) bool { return m.Code == code })
}
