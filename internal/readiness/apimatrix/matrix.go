// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package apimatrix — встроенная матрица устаревших и удалённых Kubernetes API (ADR-0025 §3).
//
// Матрицу генерирует scripts/gen-apimatrix из данных апстрима (файлы zz_generated.prerelease-lifecycle.go
// репозитория kubernetes/kubernetes по тегам релизов) и коммитит в matrix.json. Во время проверки сеть не нужна:
// файл встроен в поставку. Версия матрицы содержит отпечаток её содержимого, поэтому ручная правка файла без
// перегенерации обнаруживается при загрузке; дата генерации видна в отчёте.
//
// Матрица говорит только о том, что заявлено апстримом. Отсутствие записи не означает «API безопасно»:
// для этого в отчёте есть coverage.
package apimatrix

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

//go:embed matrix.json
var embedded []byte

// SchemaVersion — версия формата matrix.json.
const SchemaVersion = 1

// MaxWindow — сколько последних минорных версий апстрим поддерживает одновременно.
const MaxWindow = 3

// ErrInvalid — матрица повреждена или не прошла проверку.
var ErrInvalid = errors.New("API matrix is invalid")

// GVK — группа, версия и вид API.
type GVK struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// APIVersion возвращает «группа/версия» или «версия» для core.
func (g GVK) APIVersion() string {
	if g.Group == "" {
		return g.Version
	}
	return g.Group + "/" + g.Version
}

// String возвращает «группа/версия, Kind».
func (g GVK) String() string { return g.APIVersion() + ", " + g.Kind }

// Entry — одна версия вида API с заявленным апстримом жизненным циклом. Нулевая версия — «не заявлено».
type Entry struct {
	GVK
	Introduced kver.Minor `json:"introduced,omitzero"`
	Deprecated kver.Minor `json:"deprecated,omitzero"`
	Removed    kver.Minor `json:"removed,omitzero"`
	// Replacement — вид, на который апстрим рекомендует перейти (APILifecycleReplacement); nil — не указан.
	Replacement *GVK `json:"replacement,omitempty"`
}

// State — характер проблемы для выбранной версии Kubernetes.
type State string

const (
	// StateNone — для этой версии API не объявлен ни устаревшим, ни удалённым.
	StateNone State = ""
	// StateDeprecated — API объявлен устаревшим к целевой версии, но ещё обслуживается.
	StateDeprecated State = "deprecated"
	// StateRemoved — API не обслуживается в целевой версии.
	StateRemoved State = "removed"
)

// StateAt возвращает состояние API в версии v.
func (e Entry) StateAt(v kver.Minor) State {
	switch {
	case !e.Removed.IsZero() && e.Removed.Compare(v) <= 0:
		return StateRemoved
	case !e.Deprecated.IsZero() && e.Deprecated.Compare(v) <= 0:
		return StateDeprecated
	}
	return StateNone
}

// Source — откуда взяты данные.
type Source struct {
	Repository string   `json:"repository"`
	Tags       []string `json:"tags"`
	Guide      string   `json:"guide"`
}

// Matrix — загруженная матрица.
type Matrix struct {
	SchemaVersion int    `json:"schemaVersion"`
	Version       string `json:"version"`
	// GeneratedAt — дата генерации (YYYY-MM-DD).
	GeneratedAt string `json:"generatedAt"`
	Source      Source `json:"source"`
	// Window — три последние минорные версии, поддерживаемые апстримом на момент генерации, по возрастанию.
	Window []kver.Minor `json:"window"`
	// Since — старейший релиз, чьи данные учтены: удаления раньше него в матрице отсутствуют.
	Since   kver.Minor `json:"since"`
	Entries []Entry    `json:"entries"`

	byGVK map[GVK]int
}

// Latest возвращает старшую версию окна.
func (m *Matrix) Latest() kver.Minor { return m.Window[len(m.Window)-1] }

// InWindow сообщает, что v — одна из целевых версий окна.
func (m *Matrix) InWindow(v kver.Minor) bool { return slices.Contains(m.Window, v) }

// Lookup возвращает запись по виду API.
func (m *Matrix) Lookup(g GVK) (Entry, bool) {
	i, ok := m.byGVK[g]
	if !ok {
		return Entry{}, false
	}
	return m.Entries[i], true
}

// Relevant перебирает записи, важные для перехода с версии current на eff: ещё не удалённые в current
// (удалённое раньше уже не обслуживается и обновлению не мешает) и устаревшие или удалённые к eff.
func (m *Matrix) Relevant(current, eff kver.Minor) iter.Seq[Entry] {
	return func(yield func(Entry) bool) {
		for _, e := range m.Entries {
			if !e.Removed.IsZero() && e.Removed.Compare(current) <= 0 {
				continue
			}
			if e.StateAt(eff) == StateNone {
				continue
			}
			if !yield(e) {
				return
			}
		}
	}
}

// Info — краткие сведения о матрице для отчёта.
type Info struct {
	Version     string       `json:"version"`
	GeneratedAt string       `json:"generatedAt"`
	Window      []kver.Minor `json:"window"`
	Since       kver.Minor   `json:"since"`
	Source      string       `json:"source"`
	Entries     int          `json:"entries"`
}

// Info возвращает сведения для coverage.
func (m *Matrix) Info() Info {
	return Info{
		Version:     m.Version,
		GeneratedAt: m.GeneratedAt,
		Window:      slices.Clone(m.Window),
		Since:       m.Since,
		Source:      m.Source.Repository,
		Entries:     len(m.Entries),
	}
}

// ComputeVersion возвращает версию матрицы: старшая версия окна и отпечаток записей. Один и тот же набор записей
// всегда даёт одну версию; генератор и загрузчик используют эту функцию.
func ComputeVersion(latest kver.Minor, entries []Entry) (string, error) {
	b, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("failed to encode API matrix entries: %w", err)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("k8s-%s-%s", latest, hex.EncodeToString(sum[:4])), nil
}

// Parse разбирает и проверяет matrix.json. Строгий разбор: неизвестные поля — ошибка, как и нарушение порядка
// или дубли записей: файл генерируется, и любое отличие от генератора означает ручную правку.
func Parse(data []byte) (*Matrix, error) {
	var m Matrix
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	m.byGVK = make(map[GVK]int, len(m.Entries))
	for i, e := range m.Entries {
		m.byGVK[e.GVK] = i
	}
	return &m, nil
}

func (m *Matrix) validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", m.SchemaVersion)
	}
	if _, err := time.Parse(time.DateOnly, m.GeneratedAt); err != nil {
		return fmt.Errorf("generatedAt must be a date: %w", err)
	}
	if m.Source.Repository == "" || m.Source.Guide == "" || len(m.Source.Tags) == 0 {
		return errors.New("source is incomplete")
	}
	if n := len(m.Window); n < 1 || n > MaxWindow {
		return fmt.Errorf("window must hold 1..%d versions, got %d", MaxWindow, n)
	}
	for i, w := range m.Window {
		if w.IsZero() {
			return errors.New("window holds an empty version")
		}
		if i > 0 {
			if d, ok := w.Distance(m.Window[i-1]); !ok || d != 1 {
				return errors.New("window must be consecutive minor versions in ascending order")
			}
		}
	}
	if m.Since.IsZero() || !m.Since.Less(m.Window[0]) {
		return errors.New("since must precede the window")
	}
	if len(m.Entries) == 0 {
		return errors.New("no entries")
	}
	seen := make(map[GVK]struct{}, len(m.Entries))
	for i, e := range m.Entries {
		if e.Version == "" || e.Kind == "" {
			return fmt.Errorf("entry %d has no version or kind", i)
		}
		if e.Deprecated.IsZero() && e.Removed.IsZero() {
			return fmt.Errorf("entry %s declares neither deprecation nor removal", e.GVK)
		}
		if !e.Deprecated.IsZero() && !e.Removed.IsZero() && e.Removed.Less(e.Deprecated) {
			return fmt.Errorf("entry %s is removed before it is deprecated", e.GVK)
		}
		if _, dup := seen[e.GVK]; dup {
			return fmt.Errorf("duplicate entry %s", e.GVK)
		}
		seen[e.GVK] = struct{}{}
		if i > 0 && compareGVK(m.Entries[i-1].GVK, e.GVK) >= 0 {
			return fmt.Errorf("entries are not sorted at %s", e.GVK)
		}
	}
	want, err := ComputeVersion(m.Window[len(m.Window)-1], m.Entries)
	if err != nil {
		return err
	}
	if m.Version != want {
		return fmt.Errorf("version %q does not match the content (%q): regenerate the file with scripts/gen-apimatrix", m.Version, want)
	}
	return nil
}

func compareGVK(a, b GVK) int {
	if c := cmpString(a.Group, b.Group); c != 0 {
		return c
	}
	if c := cmpString(a.Kind, b.Kind); c != 0 {
		return c
	}
	return cmpString(a.Version, b.Version)
}

// SortEntries упорядочивает записи так, как их хранит matrix.json.
func SortEntries(entries []Entry) {
	slices.SortFunc(entries, func(a, b Entry) int { return compareGVK(a.GVK, b.GVK) })
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

var loadDefault = sync.OnceValues(func() (*Matrix, error) { return Parse(embedded) })

// Default возвращает встроенную матрицу. Ошибка означает дефект сборки (тест TestEmbeddedMatrix его ловит).
func Default() (*Matrix, error) { return loadDefault() }
