// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package catalog — встроенный каталог системных компонентов для проверки совместимости при обновлении Kubernetes
// (ADR-0025 §4).
//
// Каталог — данные репозитория (components.yaml): ограниченный список компонентов, у каждого ссылка на официальную
// документацию совместимости проекта и дата, когда документ был прочитан. Сеть при проверке не нужна.
//
// Правила несут только то, что документ проекта утверждает прямо. Статусы строго четыре:
//
//   - confirmed — совместимость подтверждена источником каталога;
//   - known_limitation — источник называет ограничение;
//   - insufficient_data — компонент распознан, но источник не покрывает эту комбинацию версий;
//   - not_recognized — компонента нет в каталоге.
//
// Неизвестная комбинация версий никогда не считается совместимой: «нет правила» — это insufficient_data.
package catalog

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

//go:embed components.yaml
var embedded []byte

// SchemaVersion — версия формата components.yaml.
const SchemaVersion = 1

// ErrInvalid — каталог повреждён или не прошёл проверку.
var ErrInvalid = errors.New("component catalog is invalid")

// Status — результат проверки совместимости компонента.
type Status string

const (
	StatusConfirmed       Status = "confirmed"
	StatusKnownLimitation Status = "known_limitation"
	StatusInsufficient    Status = "insufficient_data"
	StatusNotRecognized   Status = "not_recognized"
)

// Valid сообщает, что статус — один из четырёх.
func (s Status) Valid() bool {
	switch s {
	case StatusConfirmed, StatusKnownLimitation, StatusInsufficient, StatusNotRecognized:
		return true
	}
	return false
}

// Severity — важность finding.
type Severity string

const (
	SeverityBlocker Severity = "blocker"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Valid сообщает, что важность известна.
func (s Severity) Valid() bool {
	return s == SeverityBlocker || s == SeverityWarning || s == SeverityInfo
}

// Rank упорядочивает важность: меньше — важнее.
func (s Severity) Rank() int {
	switch s {
	case SeverityBlocker:
		return 0
	case SeverityWarning:
		return 1
	}
	return 2
}

// DefaultSeverity — важность по умолчанию для статуса. Правило может её повысить или понизить явно.
// «Данных недостаточно» — предупреждение: неподтверждённая совместимость не должна выглядеть безобидной.
func DefaultSeverity(s Status) Severity {
	switch s {
	case StatusKnownLimitation, StatusInsufficient:
		return SeverityWarning
	}
	return SeverityInfo
}

// Тип записи каталога.
const (
	// TypeWorkload — компонент находят среди Deployment, DaemonSet и StatefulSet по имени или образу.
	TypeWorkload = "workload"
	// TypeNodes — компонент (kubelet) находят по объектам Node.
	TypeNodes = "nodes"
)

// Catalog — загруженный каталог.
type Catalog struct {
	SchemaVersion    int         `json:"schemaVersion"`
	CheckedAt        string      `json:"checkedAt"`
	SystemNamespaces []string    `json:"systemNamespaces"`
	Components       []Component `json:"components"`

	// Version — «дата проверки-отпечаток содержимого»; считается при разборе.
	Version string `json:"-"`
}

// Component — запись каталога.
type Component struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Docs      string `json:"docs"`
	CheckedAt string `json:"checkedAt"`

	Detect     Detect     `json:"detect,omitzero"`
	Skew       *Skew      `json:"skew,omitempty"`
	Rules      []Rule     `json:"rules,omitempty"`
	Advisories []Advisory `json:"advisories,omitempty"`
}

// Detect — как найти компонент среди рабочих нагрузок.
type Detect struct {
	Workloads []WorkloadMatch `json:"workloads,omitempty"`
}

// WorkloadMatch — одно условие поиска: вид, пространства имён и (имя ИЛИ образ).
type WorkloadMatch struct {
	Kind       string   `json:"kind"`
	Namespaces []string `json:"namespaces"`
	Names      []string `json:"names,omitempty"`
	// Images — репозитории образов без тега. Совпадение — полное либо по суффиксу пути: зеркало реестра
	// («mirror.local/proxy/registry.k8s.io/…») не должно прятать компонент.
	Images []string `json:"images,omitempty"`
}

// Skew — правило совместимости по версиям Kubernetes (version skew policy): компонент не новее kube-apiserver и
// отстаёт не более чем на MaxBehind минорных версий (LegacyMaxBehind — для версий компонента старше LegacyBefore).
type Skew struct {
	MaxBehind       int        `json:"maxBehind"`
	LegacyBefore    kver.Minor `json:"legacyBefore,omitzero"`
	LegacyMaxBehind int        `json:"legacyMaxBehind,omitempty"`
}

// Rule — правило «версии компонента × версии Kubernetes → статус». Первое подошедшее правило решает.
type Rule struct {
	Versions   VersionRange `json:"versions"`
	Kubernetes MinorRange   `json:"kubernetes"`
	Status     Status       `json:"status"`
	Severity   Severity     `json:"severity,omitempty"`
	// Code — стабильный код пояснения: интерфейс переводит его по ключу, Note — запасной английский текст.
	Code string `json:"code,omitempty"`
	Note string `json:"note,omitempty"`
}

// Advisory — безусловное замечание к компоненту (например, проект закрыт). Показывается всегда, когда компонент найден.
type Advisory struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Note     string   `json:"note"`
	URL      string   `json:"url,omitempty"`
}

// Version — версия компонента «мажор.минор.патч».
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compare возвращает -1, 0 или 1.
func (v Version) Compare(o Version) int {
	for _, p := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		switch {
		case p[0] < p[1]:
			return -1
		case p[0] > p[1]:
			return 1
		}
	}
	return 0
}

// AsMinor возвращает «мажор.минор» компонента.
func (v Version) AsMinor() kver.Minor { return kver.Minor{Major: v.Major, Minor: v.Minor} }

var versionRe = regexp.MustCompile(`^v?(\d{1,6})\.(\d{1,6})(?:\.(\d{1,6}))?(?:[-+_].*)?$`)

// ParseVersion разбирает тег образа или версию: «v0.7.2», «1.11.1-eks-1-31-1», «0.7». Всё, что не похоже на
// версию («latest», digest, «1.2.3.4»), даёт false: версия неизвестна, и совместимость не выводится.
func ParseVersion(s string) (Version, bool) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, false
	}
	n := func(x string) int {
		if x == "" {
			return 0
		}
		v, _ := strconv.Atoi(x)
		return v
	}
	return Version{n(m[1]), n(m[2]), n(m[3])}, true
}

// VersionRange — полуинтервал версий компонента [From, Before). Пустая граница — без ограничения.
type VersionRange struct {
	From   string `json:"from,omitempty"`
	Before string `json:"before,omitempty"`

	from, before *Version
}

// Contains сообщает, что v входит в диапазон.
func (r VersionRange) Contains(v Version) bool {
	if r.from != nil && v.Compare(*r.from) < 0 {
		return false
	}
	if r.before != nil && v.Compare(*r.before) >= 0 {
		return false
	}
	return true
}

// MinorRange — отрезок версий Kubernetes [From, To], границы включены, пустая граница — без ограничения.
type MinorRange struct {
	From kver.Minor `json:"from,omitzero"`
	To   kver.Minor `json:"to,omitzero"`
}

// Contains сообщает, что m входит в отрезок.
func (r MinorRange) Contains(m kver.Minor) bool {
	return (r.From.IsZero() || r.From.Compare(m) <= 0) && (r.To.IsZero() || m.Compare(r.To) <= 0)
}

// Verdict — результат оценки компонента.
type Verdict struct {
	Status   Status
	Severity Severity
	Code     string
	Note     string
}

// Коды пояснений вердиктов без правила каталога.
const (
	CodeVersionUnknown  = "version.unknown"
	CodeNoRules         = "catalog.noRules"
	CodeNoMatchingRule  = "catalog.noMatchingRule"
	CodeSkewWithin      = "skew.within"
	CodeSkewTooOld      = "skew.tooOld"
	CodeSkewNewer       = "skew.newerThanAPIServer"
	CodeSkewUnavailable = "skew.targetUnknown"
)

// Evaluate оценивает компонент версии v (known=false — версию определить не удалось) для версии Kubernetes k8s.
// Нет подходящего правила — «данных недостаточно», а не «совместимо».
func (c *Component) Evaluate(v Version, known bool, k8s kver.Minor) Verdict {
	if !known {
		return Verdict{Status: StatusInsufficient, Severity: SeverityWarning, Code: CodeVersionUnknown}
	}
	for _, r := range c.Rules {
		if r.Versions.Contains(v) && r.Kubernetes.Contains(k8s) {
			sev := r.Severity
			if sev == "" {
				sev = DefaultSeverity(r.Status)
			}
			return Verdict{Status: r.Status, Severity: sev, Code: r.Code, Note: r.Note}
		}
	}
	code := CodeNoMatchingRule
	if len(c.Rules) == 0 {
		code = CodeNoRules
	}
	return Verdict{Status: StatusInsufficient, Severity: SeverityWarning, Code: code}
}

// EvaluateSkew оценивает компонент по правилу version skew: после обновления control plane до target компонент версии
// comp не должен быть новее kube-apiserver и отставать больше допустимого. Нарушение — блокирующая проблема.
func (c *Component) EvaluateSkew(comp, target kver.Minor) Verdict {
	if c.Skew == nil {
		return Verdict{Status: StatusInsufficient, Severity: SeverityWarning, Code: CodeNoRules}
	}
	d, ok := target.Distance(comp)
	if !ok || comp.IsZero() || target.IsZero() {
		return Verdict{Status: StatusInsufficient, Severity: SeverityWarning, Code: CodeSkewUnavailable}
	}
	limit := c.Skew.MaxBehind
	if !c.Skew.LegacyBefore.IsZero() && comp.Less(c.Skew.LegacyBefore) && c.Skew.LegacyMaxBehind > 0 {
		limit = c.Skew.LegacyMaxBehind
	}
	switch {
	case d < 0:
		return Verdict{Status: StatusKnownLimitation, Severity: SeverityBlocker, Code: CodeSkewNewer}
	case d > limit:
		return Verdict{Status: StatusKnownLimitation, Severity: SeverityBlocker, Code: CodeSkewTooOld}
	}
	return Verdict{Status: StatusConfirmed, Severity: SeverityInfo, Code: CodeSkewWithin}
}

// Workload — рабочая нагрузка, которую нужно сопоставить с каталогом.
type Workload struct {
	Kind, Namespace, Name string
	// Images — образы контейнеров нагрузки как в спецификации.
	Images []string
}

// SplitImage делит образ на репозиторий и тег; digest отбрасывается. Тег пуст, если образ задан только digest.
func SplitImage(image string) (repo, tag string) {
	image, _, _ = strings.Cut(image, "@")
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		return image[:i], image[i+1:]
	}
	return image, ""
}

func repoMatches(repo, want string) bool {
	return repo == want || strings.HasSuffix(repo, "/"+want)
}

// Match ищет компонент для нагрузки: вид и пространство имён должны подойти, и совпасть имя или репозиторий образа.
func (c *Catalog) Match(w Workload) (*Component, bool) {
	for i := range c.Components {
		comp := &c.Components[i]
		for _, m := range comp.Detect.Workloads {
			if m.Kind != w.Kind || !slices.Contains(m.Namespaces, w.Namespace) {
				continue
			}
			if slices.Contains(m.Names, w.Name) {
				return comp, true
			}
			for _, img := range w.Images {
				repo, _ := SplitImage(img)
				for _, want := range m.Images {
					if repoMatches(repo, want) {
						return comp, true
					}
				}
			}
		}
	}
	return nil, false
}

// ComponentVersion определяет версию компонента по образам нагрузки: первый образ из совпавших по репозиторию
// с условием поиска, тег которого разбирается как версия.
func (c *Component) ComponentVersion(w Workload) (Version, string, bool) {
	var fallback string
	for _, m := range c.Detect.Workloads {
		if m.Kind != w.Kind || !slices.Contains(m.Namespaces, w.Namespace) {
			continue
		}
		for _, img := range w.Images {
			repo, tag := SplitImage(img)
			for _, want := range m.Images {
				if !repoMatches(repo, want) {
					continue
				}
				if v, ok := ParseVersion(tag); ok {
					return v, tag, true
				}
				if fallback == "" {
					fallback = tag
				}
			}
		}
	}
	return Version{}, fallback, false
}

// Namespaces возвращает пространства имён, в которых каталог ищет рабочие нагрузки (по видам), без повторов.
func (c *Catalog) Namespaces() []string {
	var out []string
	for _, comp := range c.Components {
		for _, m := range comp.Detect.Workloads {
			for _, ns := range m.Namespaces {
				if !slices.Contains(out, ns) {
					out = append(out, ns)
				}
			}
		}
	}
	for _, ns := range c.SystemNamespaces {
		if !slices.Contains(out, ns) {
			out = append(out, ns)
		}
	}
	slices.Sort(out)
	return out
}

// IsSystemNamespace сообщает, что ns — системное пространство имён каталога.
func (c *Catalog) IsSystemNamespace(ns string) bool { return slices.Contains(c.SystemNamespaces, ns) }

// ByID возвращает запись по идентификатору.
func (c *Catalog) ByID(id string) (*Component, bool) {
	for i := range c.Components {
		if c.Components[i].ID == id {
			return &c.Components[i], true
		}
	}
	return nil, false
}

// Info — сведения о каталоге для отчёта.
type Info struct {
	Version   string `json:"version"`
	CheckedAt string `json:"checkedAt"`
	// OldestCheck — самая ранняя дата проверки среди записей: по ней видна устаревшая запись.
	OldestCheck string `json:"oldestCheck"`
	Entries     int    `json:"entries"`
}

// Info возвращает сведения для coverage.
func (c *Catalog) Info() Info {
	oldest := c.CheckedAt
	for _, comp := range c.Components {
		if comp.CheckedAt < oldest { // даты YYYY-MM-DD сравниваются как строки
			oldest = comp.CheckedAt
		}
	}
	return Info{Version: c.Version, CheckedAt: c.CheckedAt, OldestCheck: oldest, Entries: len(c.Components)}
}

// Parse разбирает и проверяет каталог. Разбор строгий: неизвестное поле — ошибка (опечатка в правиле не должна
// молча превращаться в «правила нет»).
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	if err := yaml.UnmarshalStrict(data, &c); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := c.prepare(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	sum := sha256.Sum256(data)
	c.Version = c.CheckedAt + "-" + hex.EncodeToString(sum[:4])
	return &c, nil
}

func isDate(s string) bool {
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

func (c *Catalog) prepare() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", c.SchemaVersion)
	}
	if !isDate(c.CheckedAt) {
		return errors.New("checkedAt must be a date")
	}
	if len(c.Components) == 0 {
		return errors.New("no components")
	}
	ids := map[string]bool{}
	for i := range c.Components {
		comp := &c.Components[i]
		if err := comp.prepare(c.CheckedAt); err != nil {
			return fmt.Errorf("component %q: %w", comp.ID, err)
		}
		if ids[comp.ID] {
			return fmt.Errorf("duplicate component %q", comp.ID)
		}
		ids[comp.ID] = true
	}
	return nil
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (comp *Component) prepare(catalogDate string) error {
	if !idRe.MatchString(comp.ID) || comp.Name == "" {
		return errors.New("id and name are required (id: lowercase letters, digits, dashes)")
	}
	u, err := url.Parse(comp.Docs)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("docs must be an https link to the project's compatibility documentation")
	}
	if !isDate(comp.CheckedAt) {
		return errors.New("checkedAt must be a date")
	}
	if comp.CheckedAt > catalogDate {
		return errors.New("checkedAt is later than the catalog date")
	}
	switch comp.Type {
	case TypeNodes:
		if len(comp.Detect.Workloads) > 0 {
			return errors.New("a nodes component has no workload conditions")
		}
	case TypeWorkload:
		if len(comp.Detect.Workloads) == 0 {
			return errors.New("a workload component needs detect.workloads")
		}
	default:
		return fmt.Errorf("unknown type %q", comp.Type)
	}
	for _, m := range comp.Detect.Workloads {
		switch m.Kind {
		case "Deployment", "DaemonSet", "StatefulSet":
		default:
			return fmt.Errorf("unsupported workload kind %q", m.Kind)
		}
		if len(m.Namespaces) == 0 || (len(m.Names) == 0 && len(m.Images) == 0) {
			return errors.New("a workload condition needs namespaces and names or images")
		}
	}
	if s := comp.Skew; s != nil {
		if s.MaxBehind < 0 || s.LegacyMaxBehind < 0 || s.LegacyMaxBehind > s.MaxBehind {
			return errors.New("skew limits are invalid")
		}
	}
	for i := range comp.Rules {
		r := &comp.Rules[i]
		if err := r.prepare(); err != nil {
			return fmt.Errorf("rule %d: %w", i+1, err)
		}
	}
	for _, a := range comp.Advisories {
		if a.Code == "" || a.Note == "" || !a.Severity.Valid() {
			return errors.New("an advisory needs code, note and severity")
		}
		if a.URL != "" {
			if u, err := url.Parse(a.URL); err != nil || u.Scheme != "https" {
				return errors.New("an advisory link must be https")
			}
		}
	}
	return nil
}

func (r *Rule) prepare() error {
	if r.Status != StatusConfirmed && r.Status != StatusKnownLimitation {
		return fmt.Errorf("a rule can only confirm compatibility or name a limitation, got %q", r.Status)
	}
	if r.Severity != "" && !r.Severity.Valid() {
		return fmt.Errorf("unknown severity %q", r.Severity)
	}
	if r.Status == StatusKnownLimitation && (r.Code == "" || r.Note == "") {
		return errors.New("a known limitation needs a code and a note")
	}
	if r.Status == StatusConfirmed && r.Severity != "" && r.Severity.Rank() < SeverityWarning.Rank() {
		return errors.New("confirmed compatibility cannot be a blocker")
	}
	for _, p := range []struct {
		s   string
		dst **Version
	}{{r.Versions.From, &r.Versions.from}, {r.Versions.Before, &r.Versions.before}} {
		if p.s == "" {
			continue
		}
		v, ok := ParseVersion(p.s)
		if !ok {
			return fmt.Errorf("invalid component version %q", p.s)
		}
		*p.dst = &v
	}
	if r.Versions.from == nil && r.Versions.before == nil {
		return errors.New("a rule must bound the component versions")
	}
	if r.Versions.from != nil && r.Versions.before != nil && r.Versions.from.Compare(*r.Versions.before) >= 0 {
		return errors.New("versions.from must be lower than versions.before")
	}
	if r.Kubernetes.From.IsZero() && r.Kubernetes.To.IsZero() {
		return errors.New("a rule must bound the Kubernetes versions")
	}
	if !r.Kubernetes.From.IsZero() && !r.Kubernetes.To.IsZero() && r.Kubernetes.To.Less(r.Kubernetes.From) {
		return errors.New("kubernetes.to must not precede kubernetes.from")
	}
	return nil
}

var loadDefault = sync.OnceValues(func() (*Catalog, error) { return Parse(embedded) })

// Default возвращает встроенный каталог. Ошибка означает дефект сборки (тест TestEmbeddedCatalog его ловит).
func Default() (*Catalog, error) { return loadDefault() }
