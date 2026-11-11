// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package readiness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/readiness/kver"
)

// run — состояние одной проверки: накапливает findings и покрытие, пока читает кластер.
type run struct {
	s      *Service
	rd     Reader
	cur    kver.Minor
	target kver.Minor // нулевая — не указана
	eff    kver.Minor // версия, относительно которой оцениваются API и компоненты
	now    time.Time

	findings []Finding
	live     SourceCoverage
	scanned  []string
	notCover []string
	// readFailed — части живого API, прочитанные не полностью: источник «частично».
	partial bool
}

func (r *run) add(f Finding) { r.findings = append(r.findings, f) }

func (r *run) method(m string) {
	if !slices.Contains(r.live.Methods, m) {
		r.live.Methods = append(r.live.Methods, m)
	}
}

// sourceFinding — предупреждение «не проверено» или «частично»: источник недоступен или прочитан не целиком.
// Это отдельный finding, а не пустой успех.
func (r *run) sourceFinding(key string, status Status, sev Severity, code string, params map[string]string, rec string) {
	f := Finding{
		Key: key, Type: TypeSource, Status: status, Severity: sev,
		Explanation: Message{Code: code, Params: params},
		Source:      SourceLiveAPI, ObservedAt: r.now,
	}
	if rec != "" {
		f.Recommendation = &Message{Code: rec}
	}
	r.add(f)
	r.partial = true
}

// Check собирает отчёт, не сохраняя его.
func (s *Service) Check(ctx context.Context, req Request) (*Report, error) {
	if req.Reader == nil {
		return nil, errors.New("cluster reader is required")
	}
	if err := ValidateScope(req.Scope); err != nil {
		return nil, err
	}
	target, err := s.ParseTarget(req.Target)
	if err != nil {
		return nil, err
	}
	info, err := req.Reader.ServerVersion(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrCurrentVersion, err)
	}
	cur, err := kver.ParseInfo(info.GitVersion, info.Major, info.Minor)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCurrentVersion, err)
	}
	if !target.IsZero() && target.Less(cur) {
		return nil, fmt.Errorf("%w: %s < %s", ErrTargetOlder, target, cur)
	}

	now := s.now().UTC()
	r := &run{s: s, rd: req.Reader, cur: cur, target: target, eff: target, now: now}
	if target.IsZero() {
		r.eff = cur
	}
	r.live = SourceCoverage{Kind: SourceLiveAPI, Checked: true, ObservedAt: now}
	r.method(MethodServerVersion)

	r.checkPlatform()
	r.checkAPIs(ctx)
	r.checkComponents(ctx)
	// Отмена пользователем — не «частичный отчёт»: результат никому не нужен. Истёкший deadline, напротив, даёт
	// отчёт с пометками «не проверено» у непрочитанных источников.
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}

	rep := &Report{
		SchemaVersion: SchemaVersion, ID: s.newID(), ClusterID: req.Scope.ClusterID, CreatedAt: now,
		Initiator: req.Initiator, CorrelationID: req.CorrelationID,
		Current: ClusterVersion{GitVersion: info.GitVersion, Minor: cur, Platform: info.Platform},
		Target:  target,
	}
	rep.Findings = r.findings
	rep.Coverage = r.coverage()
	finalize(rep)
	if err := rep.Validate(); err != nil {
		return nil, err // сервис не отдаёт невалидный отчёт: это ошибка программы, а не данных
	}
	return rep, nil
}

// coverage строит обязательное поле покрытия по тому, что проверка реально сделала.
func (r *run) coverage() Coverage {
	m := r.s.matrix
	live := r.live
	live.Partial = r.partial
	slices.Sort(r.scanned)
	slices.Sort(r.notCover)
	live.Scanned = slices.Compact(r.scanned)
	live.NotCovered = slices.Compact(r.notCover)
	live.Limitations = []Message{
		{Code: LimitLiveOnly},
		{Code: LimitLiveManagedFields},
		{Code: LimitLiveReadOnlyClients},
		{Code: LimitLiveKindsScanned},
		{Code: LimitLiveCustomResources},
		{Code: LimitLiveNoHelmReleases},
		{Code: LimitLiveComponents},
	}
	limits := []Message{{Code: LimitProviderNotChecked}, {Code: LimitNotAGuarantee}}
	if r.target.IsZero() {
		limits = append(limits, Message{Code: LimitNoTarget})
	}
	if r.cur.Compare(m.Latest()) > 0 {
		limits = append(limits, Message{Code: LimitStaleMatrix, Params: map[string]string{"latest": m.Latest().String()}})
	}
	if r.cur.Less(m.Since) {
		limits = append(limits, Message{Code: LimitClusterOlderThanMatrix, Params: map[string]string{"since": m.Since.String()}})
	}
	not := func(k SourceKind) SourceCoverage {
		return SourceCoverage{Kind: k, Reason: &Message{Code: LimitNotAnalyzed, Params: map[string]string{"source": string(k)}}}
	}
	return Coverage{
		Mode: ModeGenericKubernetes, TargetSpecified: !r.target.IsZero(), EvaluatedAgainst: r.eff,
		Sources:   []SourceCoverage{live, not(SourceGitOps), not(SourceLocalFiles)},
		APIMatrix: m.Info(), Catalog: r.s.catalog.Info(),
		Limitations: limits,
	}
}

// checkPlatform — проверки пути обновления: шаги версий и окно поддержки апстрима.
func (r *run) checkPlatform() {
	if !r.target.IsZero() {
		steps, _ := r.target.Distance(r.cur)
		p := map[string]string{"current": r.cur.String(), "target": r.target.String(), "steps": strconv.Itoa(steps)}
		f := Finding{
			Key: "platform/upgrade-path", Type: TypePlatform, Source: SourceLiveAPI, ObservedAt: r.now,
			Status: StatusOK, Severity: SeverityInfo, Explanation: Message{Code: CodePlatformPathOK, Params: p},
		}
		switch {
		case steps == 0:
			f.Explanation.Code = CodePlatformSameVersion
		case steps > 1:
			f.Status, f.Severity = StatusAttention, SeverityWarning
			f.Explanation.Code = CodePlatformPathSkips
			f.Recommendation = &Message{Code: CodeRecStepwise}
		}
		r.add(f)
	}
	if w := r.s.matrix.Window; len(w) > 0 && r.cur.Less(w[0]) {
		r.add(Finding{
			Key: "platform/current-support", Type: TypePlatform, Source: SourceLiveAPI, ObservedAt: r.now,
			Status: StatusAttention, Severity: SeverityWarning,
			Explanation:    Message{Code: CodePlatformUnsupported, Params: map[string]string{"current": r.cur.String(), "window": joinMinors(w)}},
			Recommendation: &Message{Code: CodeRecSupportedVersion},
		})
	}
}

// finalize упорядочивает findings (важность, вид, ключ), назначает приоритеты и строит действия.
func finalize(rep *Report) {
	order := map[FindingType]int{TypeAPI: 0, TypePlatform: 1, TypeComponent: 2, TypeSource: 3}
	slices.SortStableFunc(rep.Findings, func(a, b Finding) int {
		if c := a.Severity.Rank() - b.Severity.Rank(); c != 0 {
			return c
		}
		if c := order[a.Type] - order[b.Type]; c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	for i := range rep.Findings {
		rep.Findings[i].Priority = i + 1
	}
	rep.Actions = buildActions(rep)
}
