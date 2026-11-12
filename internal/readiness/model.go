// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package readiness — Upgrade Readiness Report: оценка готовности кластера к обновлению Kubernetes (ADR-0025).
//
// Пакет только оценивает и ничего не меняет в кластере. Отчёт не гарантия успешного обновления: отсутствие данных
// о совместимости обозначается как «не проверено» и никогда не превращается в «совместимо». Поэтому отчёт обязан
// нести заполненное поле Coverage — какие источники реально проверены, какие нет, режим проверки, версии и даты
// матриц; отчёт без него невалиден (Report.Validate), не сохраняется и не экспортируется.
//
// Пакет не импортирует платформу операционных модулей: чтение кластера идёт через узкий интерфейс Reader, а
// tenant.Scope приходит параметром. Платформу импортирует только слой связки (делегаты App).
//
// Тексты для интерфейса — коды и параметры (Message), без форматирования под язык: подписи переводит фронтенд.
package readiness

import (
	"slices"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness/apimatrix"
	"github.com/24DSO-lab/kubron/internal/readiness/catalog"
	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

// SchemaVersion — версия формата отчёта (хранится и экспортируется вместе с ним).
const SchemaVersion = 1

// Режим проверки. В первой версии provider-specific проверок нет (ADR-0025 §7): только Kubernetes API.
const ModeGenericKubernetes = "generic-kubernetes"

// Severity и Status общие с каталогом компонентов.
type (
	Severity = catalog.Severity
	// Status — состояние finding; допустимые значения зависят от типа finding.
	Status string
)

const (
	SeverityBlocker = catalog.SeverityBlocker
	SeverityWarning = catalog.SeverityWarning
	SeverityInfo    = catalog.SeverityInfo
)

// Статусы API findings.
const (
	StatusDeprecated Status = "deprecated"
	StatusRemoved    Status = "removed"
)

// Статусы компонентов: ровно четыре (ADR-0025 §4).
const (
	StatusConfirmed       = Status(catalog.StatusConfirmed)
	StatusKnownLimitation = Status(catalog.StatusKnownLimitation)
	StatusInsufficient    = Status(catalog.StatusInsufficient)
	StatusNotRecognized   = Status(catalog.StatusNotRecognized)
)

// Статусы проверок пути обновления.
const (
	StatusOK        Status = "ok"
	StatusAttention Status = "attention"
)

// StatusNotChecked — источник не проверен (недоступен, запрещён, обрезан). Не «совместимо» и не «нет проблем».
const StatusNotChecked Status = "not_checked"

// FindingType — вид finding.
type FindingType string

const (
	// TypeAPI — использование устаревшего или удалённого API.
	TypeAPI FindingType = "api"
	// TypeComponent — версия и состояние компонента.
	TypeComponent FindingType = "component"
	// TypePlatform — проверки пути обновления Kubernetes (шаги версий, окно поддержки).
	TypePlatform FindingType = "platform"
	// TypeSource — источник данных недоступен или прочитан не полностью: предупреждение «не проверено».
	TypeSource FindingType = "source"
)

// Message — пояснение: код и параметры. Подпись по коду переводит интерфейс; параметры — только идентификаторы,
// версии и счётчики, без содержимого объектов.
type Message struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
}

// ResourceRef — идентификатор ресурса, где найдено использование API.
type ResourceRef struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	// Evidence — откуда известна версия API: managed_fields или last_applied.
	Evidence []string `json:"evidence"`
}

// Способы обнаружения использования API (Evidence и Coverage.Sources[].Methods).
const (
	EvidenceManagedFields = "managed_fields"
	EvidenceLastApplied   = "last_applied"
)

// APIDetail — подробности API finding (ADR-0025 §3): идентификатор ресурса (если установим), apiVersion и kind,
// характер проблемы для целевой версии, источник данных и рекомендация по миграции.
type APIDetail struct {
	APIVersion   string `json:"apiVersion"`
	Kind         string `json:"kind"`
	DeprecatedIn string `json:"deprecatedIn,omitempty"`
	RemovedIn    string `json:"removedIn,omitempty"`
	// Replacement — «apiVersion, Kind», на который апстрим рекомендует перейти; пусто — не указан.
	Replacement string `json:"replacement,omitempty"`
	// Resources — ресурсы с найденным использованием (не больше MaxResourcesPerFinding); ResourceCount — сколько всего.
	Resources          []ResourceRef `json:"resources,omitempty"`
	ResourceCount      int           `json:"resourceCount"`
	ResourcesTruncated bool          `json:"resourcesTruncated,omitempty"`
	GuideURL           string        `json:"guideUrl,omitempty"`
}

// ComponentDetail — подробности finding о компоненте (ADR-0025 §4): обнаруженная версия, состояние, целевая
// версия Kubernetes и источник матрицы совместимости.
type ComponentDetail struct {
	// ID — запись каталога; пусто у нераспознанного компонента.
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	// Version — обнаруженная версия (тег образа или версия kubelet); пусто, если определить не удалось.
	Version string `json:"version,omitempty"`
	// Kind, Namespace, Workload — где найден; у kubelet Kind=Node и указано число узлов.
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Workload  string `json:"workload,omitempty"`
	Image     string `json:"image,omitempty"`
	NodeCount int    `json:"nodeCount,omitempty"`
	// State — ready, degraded, unavailable или unknown; Ready/Desired — число готовых и желаемых экземпляров.
	State   string `json:"state,omitempty"`
	Ready   int    `json:"ready,omitempty"`
	Desired int    `json:"desired,omitempty"`
	// TargetVersion — версия Kubernetes, для которой оценена совместимость (целевая, а без неё — текущая).
	TargetVersion string `json:"targetVersion,omitempty"`
	// DocsURL и CheckedAt — источник матрицы совместимости и дата его проверки (запись каталога).
	DocsURL   string `json:"docsUrl,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
	// NoteCode и Note — пояснение правила или замечания каталога (запасной английский текст в Note).
	NoteCode string `json:"noteCode,omitempty"`
	Note     string `json:"note,omitempty"`
	NoteURL  string `json:"noteUrl,omitempty"`
}

// Finding — один результат проверки. Key стабилен между проверками: по нему отчёты сравниваются во времени.
type Finding struct {
	Key      string      `json:"key"`
	Type     FindingType `json:"type"`
	Status   Status      `json:"status"`
	Severity Severity    `json:"severity"`
	// Priority — порядок исправления: 1 — первым. Назначается при сборке отчёта.
	Priority    int     `json:"priority"`
	Explanation Message `json:"explanation"`
	// Recommendation — что сделать; nil у findings, не требующих действий.
	Recommendation *Message `json:"recommendation,omitempty"`
	// Source — источник данных (live_api); ObservedAt — когда данные получены.
	Source     SourceKind `json:"source"`
	ObservedAt time.Time  `json:"observedAt"`

	API       *APIDetail       `json:"api,omitempty"`
	Component *ComponentDetail `json:"component,omitempty"`
}

// Фазы действий.
const (
	PhaseBefore = "before"
	PhaseAfter  = "after"
)

// Action — подготовительное действие или проверка после обновления. Рекомендация, не команда: Kubron ничего
// не выполняет в кластере (ADR-0025 §8).
type Action struct {
	Phase    string  `json:"phase"`
	Priority int     `json:"priority"`
	Message  Message `json:"message"`
	// Related — ключи findings, из-за которых действие предложено.
	Related []string `json:"related,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// ClusterVersion — версия Kubernetes кластера на момент проверки (ответ сервера API).
type ClusterVersion struct {
	GitVersion string     `json:"gitVersion"`
	Minor      kver.Minor `json:"minor"`
	Platform   string     `json:"platform,omitempty"`
}

// Summary — краткая сводка отчёта: строка истории и счётчики для сравнения «с первого взгляда».
type Summary struct {
	ID             string    `json:"id"`
	ClusterID      string    `json:"clusterId"`
	CreatedAt      time.Time `json:"createdAt"`
	Initiator      string    `json:"initiator,omitempty"`
	CurrentVersion string    `json:"currentVersion"`
	TargetVersion  string    `json:"targetVersion,omitempty"`
	Blockers       int       `json:"blockers"`
	Warnings       int       `json:"warnings"`
	Infos          int       `json:"infos"`
	// NotChecked — число findings «не проверено» и источников coverage с оговорками.
	NotChecked int `json:"notChecked"`
}

// Report — Upgrade Readiness Report. Coverage обязателен (Validate).
type Report struct {
	SchemaVersion int       `json:"schemaVersion"`
	ID            string    `json:"id"`
	ClusterID     string    `json:"clusterId"`
	CreatedAt     time.Time `json:"createdAt"`
	// Initiator — кто запустил проверку (идентификатор пользователя из сессии).
	Initiator     string `json:"initiator,omitempty"`
	CorrelationID string `json:"correlationId,omitempty"`

	Current ClusterVersion `json:"current"`
	// Target — целевая версия; нулевая — не указана (тогда оценка относительно текущей версии, и coverage это называет).
	Target kver.Minor `json:"target,omitzero"`

	Findings []Finding `json:"findings"`
	Actions  []Action  `json:"actions"`
	Coverage Coverage  `json:"coverage"`
}

// Использованные в Summary и интерфейсе производные значения.

// Counts возвращает число findings по важности.
func (r *Report) Counts() (blockers, warnings, infos int) {
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityBlocker:
			blockers++
		case SeverityWarning:
			warnings++
		default:
			infos++
		}
	}
	return blockers, warnings, infos
}

// Summary строит сводку отчёта.
func (r *Report) Summary() Summary {
	b, w, i := r.Counts()
	nc := 0
	for _, f := range r.Findings {
		if f.Status == StatusNotChecked {
			nc++
		}
	}
	for _, s := range r.Coverage.Sources {
		if !s.Checked || s.Partial {
			nc++
		}
	}
	return Summary{
		ID: r.ID, ClusterID: r.ClusterID, CreatedAt: r.CreatedAt, Initiator: r.Initiator,
		CurrentVersion: r.Current.Minor.String(), TargetVersion: r.Target.String(),
		Blockers: b, Warnings: w, Infos: i, NotChecked: nc,
	}
}

// MatrixInfo и CatalogInfo — псевдонимы сведений о матрицах для coverage.
type (
	MatrixInfo  = apimatrix.Info
	CatalogInfo = catalog.Info
)

func sortSummaries(s []Summary) {
	slices.SortFunc(s, func(a, b Summary) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
}
