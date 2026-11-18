// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package correlation

import (
	"time"

	"github.com/24DSO-lab/kubron/internal/diagnose"
)

// Version — версия правил корреляции. Поднимается при любом изменении правил или кодов фактов: снимок в
// расследовании хранит версию, которой построен (ADR-0033 §2).
const Version = 1

// Пределы корреляции. Значения зафиксированы в коде (ADR-0033 §3–§4); изменение — только новым ADR.
const (
	// CorrelationWindow — окно временной связи между изменением источника и первыми сбоями (ADR-0033 §4).
	CorrelationWindow = 30 * time.Minute
	// MaxCandidates — сколько кандидатов общего источника попадает в снимок (ADR-0033 §3).
	MaxCandidates = 5
	// MaxAffected — сколько нагрузок перечисляется в Affected и Potential одного кандидата (ADR-0033 §3).
	MaxAffected = 50
	// MaxExtraReads — бюджет дополнительных чтений кластера за один запуск (ADR-0033 §3).
	MaxExtraReads = 10
	// MinDeadlineReserve — минимальный остаток до deadline, при котором корреляция запускается (ADR-0033 §3).
	MinDeadlineReserve = 5 * time.Second
)

// Kind — вид общего источника. Этап A реализует первые два, остальные зарезервированы под этап C.
type Kind string

const (
	KindSharedNode       Kind = "shared_node"
	KindSharedStorage    Kind = "shared_storage"
	KindSharedDependency Kind = "shared_dependency" // этап C
	KindSharedRoot       Kind = "shared_root"       // этап C
	KindSharedSymptom    Kind = "shared_symptom"    // этап C
)

// WorkloadRef — затронутая нагрузка верхнего уровня (Deployment, StatefulSet, DaemonSet, CronJob, Job или
// одиночный Pod). Хранит только коды и числа: метки, аннотации и поля объектов в снимок не попадают (ADR-0022).
type WorkloadRef struct {
	Kind        string    `json:"kind"`
	Namespace   string    `json:"namespace,omitempty"`
	Name        string    `json:"name"`
	ProblemPods int       `json:"problemPods"`
	Symptoms    []string  `json:"symptoms,omitempty"` // коды симптомов, отсортированы
	Since       time.Time `json:"since,omitzero"`     // время первого сбоя по меткам объектов; нет у «потенциальных»
}

// Candidate — возможный общий источник сбоев. Это гипотеза, а не установленная причина.
type Candidate struct {
	ID         string               `json:"id"`
	Kind       Kind                 `json:"kind"`
	Source     diagnose.ResourceRef `json:"source"` // общий источник
	Confidence diagnose.Confidence  `json:"confidence"`
	Since      time.Time            `json:"since"`
	Affected   []WorkloadRef        `json:"affected,omitempty"`   // до MaxAffected, отсортировано
	Total      int                  `json:"total"`                // всего затронуто (точно в пределах прочитанного)
	Truncated  bool                 `json:"truncated,omitempty"`  // список затронутых усечён лимитом чтения
	Potential  []WorkloadRef        `json:"potential,omitempty"`  // зависимые, но пока без симптомов (из графа)
	Supporting []string             `json:"supporting,omitempty"` // ID фактов в Result.Facts
}

// Key — стабильный ключ кандидата «<вид>:<группа/вид/namespace/имя источника>». По нему автоприсоединение
// ищет открытую проблему с тем же источником (ADR-0033 §6); семантику не менять без уведомления D5.
func (c Candidate) Key() string {
	return string(c.Kind) + ":" + sourceKey(c.Source)
}

// Result — снимок корреляции, который лежит в расследовании (Incident.Correlation).
type Result struct {
	Version    int                       `json:"version"`
	ObservedAt time.Time                 `json:"observedAt"`
	Candidates []Candidate               `json:"candidates,omitempty"` // до MaxCandidates, по убыванию уверенности, затем Total
	Facts      []diagnose.Fact           `json:"facts,omitempty"`
	Missing    []diagnose.MissingContext `json:"missing,omitempty"`
	Partial    []string                  `json:"partial,omitempty"`
}

// Best возвращает лучшего кандидата или nil. Кандидаты уже упорядочены Run, поэтому это первый из них.
// Безопасен для nil-получателя: старые расследования без снимка читаются без ошибок.
func (r *Result) Best() *Candidate {
	if r == nil || len(r.Candidates) == 0 {
		return nil
	}
	return &r.Candidates[0]
}

// sourceKey строит часть ключа по ссылке источника в формате depgraph.Ref.Key(): «группа/вид/namespace/имя»,
// пустая группа — core. Для StorageClass без заполненной группы (старые данные) группа восстанавливается по виду.
func sourceKey(ref diagnose.ResourceRef) string {
	group := ref.Group
	if group == "" {
		group = "core"
		if ref.Kind == "StorageClass" {
			group = "storage.k8s.io"
		}
	}
	return group + "/" + ref.Kind + "/" + ref.Namespace + "/" + ref.Name
}
