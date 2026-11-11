// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Форматы экспорта.
const (
	FormatJSON     = "json"
	FormatMarkdown = "markdown"
)

// ExportFormatID — значение поля format в JSON-экспорте.
const ExportFormatID = "kubron.readiness-report"

// ExportedReport — результат экспорта: содержимое и подсказки для сохранения файла.
type ExportedReport struct {
	Format   string `json:"format"`
	Filename string `json:"filename"`
	MIME     string `json:"mime"`
	Content  string `json:"content"`
}

// exportEnvelope — JSON-экспорт: метка формата и сам отчёт со всеми разделами, включая coverage.
type exportEnvelope struct {
	Format string  `json:"format"`
	Report *Report `json:"report"`
}

// ExportJSON возвращает отчёт в JSON. Экспорт без заполненного coverage невозможен: невалидный отчёт — ошибка
// (ErrInvalidReport), а не файл, который выглядит полным.
func ExportJSON(r *Report) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil report", ErrInvalidReport)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(exportEnvelope{Format: ExportFormatID, Report: r}, "", "  ")
}

// ExportMarkdown возвращает человекочитаемый отчёт. Раздел Coverage идёт сразу за сводкой, до findings: читатель
// сначала видит, что не проверено. Текст английский: бэкенд язык интерфейса не знает, а экспорт — документ,
// который уходит за пределы приложения.
func ExportMarkdown(r *Report) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil report", ErrInvalidReport)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# Kubernetes upgrade readiness report\n\n")
	w("- Report: `%s`\n- Cluster: `%s`\n- Created: %s\n", md(r.ID), md(r.ClusterID), r.CreatedAt.UTC().Format(time.RFC3339))
	if r.Initiator != "" {
		w("- Initiated by: %s\n", md(r.Initiator))
	}
	w("- Current version: %s (`%s`)\n", r.Current.Minor, md(r.Current.GitVersion))
	if r.Target.IsZero() {
		w("- Target version: not specified (APIs evaluated against %s)\n", r.Coverage.EvaluatedAgainst)
	} else {
		w("- Target version: %s\n", r.Target)
	}
	bl, wa, in := r.Counts()
	w("\n**%d blocker(s), %d warning(s), %d informational.** This report is an assessment, not a guarantee that the upgrade will succeed.\n\n", bl, wa, in)

	writeCoverage(&b, &r.Coverage)

	for _, sev := range []Severity{SeverityBlocker, SeverityWarning, SeverityInfo} {
		var list []*Finding
		for i := range r.Findings {
			if r.Findings[i].Severity == sev {
				list = append(list, &r.Findings[i])
			}
		}
		w("## %s\n\n", map[Severity]string{SeverityBlocker: "Blockers", SeverityWarning: "Warnings", SeverityInfo: "Informational"}[sev])
		if len(list) == 0 {
			w("None.\n\n")
			continue
		}
		for _, f := range list {
			writeFinding(&b, f)
		}
	}

	for _, phase := range []string{PhaseBefore, PhaseAfter} {
		title := "Preparation before the upgrade"
		if phase == PhaseAfter {
			title = "Checks after the upgrade"
		}
		w("## %s\n\n", title)
		var acts []Action
		for _, a := range r.Actions {
			if a.Phase == phase {
				acts = append(acts, a)
			}
		}
		slices.SortStableFunc(acts, func(a, b Action) int { return a.Priority - b.Priority })
		for _, a := range acts {
			w("%d. %s", a.Priority, md(EnglishText(a.Message)))
			if a.URL != "" {
				w(" (%s)", a.URL)
			}
			w("\n")
		}
		w("\n")
	}
	return b.Bytes(), nil
}

func writeCoverage(b *bytes.Buffer, c *Coverage) {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	w("## Coverage — what was and was not checked\n\n")
	w("- Mode: %s\n", map[string]string{ModeGenericKubernetes: "generic Kubernetes (no provider-specific checks)"}[c.Mode])
	w("- APIs evaluated against Kubernetes: %s\n", c.EvaluatedAgainst)
	w("- API matrix: `%s`, generated %s, supported targets %s, data since %s (%d entries)\n",
		md(c.APIMatrix.Version), md(c.APIMatrix.GeneratedAt), joinMinors(c.APIMatrix.Window), c.APIMatrix.Since, c.APIMatrix.Entries)
	w("- Component catalog: `%s`, checked %s (oldest entry check %s, %d components)\n\n",
		md(c.Catalog.Version), md(c.Catalog.CheckedAt), md(c.Catalog.OldestCheck), c.Catalog.Entries)
	for _, s := range c.Sources {
		switch {
		case !s.Checked:
			w("- **%s: NOT CHECKED.** %s\n", sourceTitle(s.Kind), md(EnglishText(*s.Reason)))
		default:
			state := "checked"
			if s.Partial {
				state = "checked partially"
			}
			w("- **%s: %s** (%s; methods: %s)\n", sourceTitle(s.Kind), state, s.ObservedAt.UTC().Format(time.RFC3339), strings.Join(s.Methods, ", "))
			for _, l := range s.Limitations {
				w("  - %s\n", md(EnglishText(l)))
			}
			if len(s.Scanned) > 0 {
				w("  - Scanned kinds: %s\n", md(strings.Join(s.Scanned, ", ")))
			}
			if len(s.NotCovered) > 0 {
				w("  - Not covered (not checked): %s\n", md(strings.Join(s.NotCovered, ", ")))
			}
		}
	}
	w("\nGeneral limitations:\n\n")
	for _, l := range c.Limitations {
		w("- %s\n", md(EnglishText(l)))
	}
	w("\n")
}

func sourceTitle(k SourceKind) string {
	switch k {
	case SourceLiveAPI:
		return "Live Kubernetes API objects"
	case SourceGitOps:
		return "GitOps manifests (Argo CD, Flux, Git)"
	case SourceLocalFiles:
		return "Local manifest files"
	}
	return string(k)
}

func writeFinding(b *bytes.Buffer, f *Finding) {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	w("### %d. %s — %s\n\n", f.Priority, md(findingTitle(f)), md(string(f.Status)))
	w("%s\n\n", md(EnglishText(f.Explanation)))
	switch {
	case f.API != nil:
		d := f.API
		w("- API: `%s`, kind `%s`\n", md(d.APIVersion), md(d.Kind))
		if d.DeprecatedIn != "" {
			w("- Deprecated in: %s\n", d.DeprecatedIn)
		}
		if d.RemovedIn != "" {
			w("- Removed in: %s\n", d.RemovedIn)
		}
		w("- Objects found: %d (source: live API, observed %s)\n", d.ResourceCount, f.ObservedAt.UTC().Format(time.RFC3339))
		for _, r := range d.Resources {
			name := r.Name
			if r.Namespace != "" {
				name = r.Namespace + "/" + r.Name
			}
			w("  - `%s` (%s)\n", md(name), strings.Join(r.Evidence, ", "))
		}
		if d.ResourcesTruncated {
			w("  - … and %d more\n", d.ResourceCount-len(d.Resources))
		}
	case f.Component != nil:
		d := f.Component
		if d.Version != "" {
			w("- Detected version: %s\n", md(d.Version))
		}
		if d.Kind != "" {
			w("- Found in: %s %s\n", md(d.Kind), md(strings.Trim(d.Namespace+"/"+d.Workload, "/")))
		}
		if d.State != "" {
			w("- State: %s (%d of %d ready)\n", d.State, d.Ready, d.Desired)
		}
		w("- Evaluated for Kubernetes: %s\n", md(d.TargetVersion))
		if d.DocsURL != "" {
			w("- Compatibility source: %s (checked %s)\n", d.DocsURL, md(d.CheckedAt))
		}
		if d.NoteCode != "" || d.Note != "" {
			note := d.Note
			if KnownCode(d.NoteCode) {
				note = EnglishText(Message{Code: d.NoteCode})
			}
			if note != "" {
				w("- Note: %s\n", md(note))
			}
		}
	}
	if f.Recommendation != nil {
		w("- Recommendation: %s\n", md(EnglishText(*f.Recommendation)))
	}
	w("\n")
}

func findingTitle(f *Finding) string {
	switch {
	case f.API != nil:
		return f.API.Kind + " " + f.API.APIVersion
	case f.Component != nil && f.Component.Name != "":
		return f.Component.Name
	}
	return f.Key
}

// md делает значение безопасным для строки Markdown: без переводов строк и служебных символов таблиц и кода.
func md(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "`", "'", "|", "/").Replace(s)
	return strings.TrimSpace(s)
}

// Export возвращает сохранённый отчёт области в выбранном формате.
func (s *Service) Export(ctx context.Context, scope tenant.Scope, id, format string) (ExportedReport, error) {
	r, err := s.backend.Get(ctx, scope, id)
	if err != nil {
		return ExportedReport{}, err
	}
	return ExportReport(r, format)
}

// ExportReport выгружает готовый отчёт.
func ExportReport(r *Report, format string) (ExportedReport, error) {
	base := "readiness-" + r.ClusterID + "-" + r.CreatedAt.UTC().Format("20060102T150405Z")
	base = strings.Map(func(c rune) rune {
		if c == '.' || c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			return c
		}
		return '_'
	}, base)
	switch format {
	case FormatJSON:
		raw, err := ExportJSON(r)
		if err != nil {
			return ExportedReport{}, err
		}
		return ExportedReport{Format: format, Filename: base + ".json", MIME: "application/json", Content: string(raw)}, nil
	case FormatMarkdown:
		raw, err := ExportMarkdown(r)
		if err != nil {
			return ExportedReport{}, err
		}
		return ExportedReport{Format: format, Filename: base + ".md", MIME: "text/markdown", Content: string(raw)}, nil
	}
	return ExportedReport{}, fmt.Errorf("%w: %q", ErrUnknownFormat, format)
}
