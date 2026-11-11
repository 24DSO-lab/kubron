// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package incident

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/24DSO-lab/kubron/internal/diagnose"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// ReasonDeleted — фрагмент удалён пользователем.
const ReasonDeleted = "deleted"

// Размер запроса лога при явном сохранении — как при диагностике.
const (
	logTailLines       = 400
	logLimitBytes      = 512 << 10
	maxConflictRetries = 3
)

// Caller — вызывающий: область доступа и инициатор приходят от платформы (Op), Reader — чтение кластера.
type Caller struct {
	Scope  tenant.Scope
	Actor  string
	Reader diagnose.Reader
}

// LogsAuthorizer повторно проверяет право diagnostics.logs на сервере; вызывается при КАЖДОМ чтении содержимого.
type LogsAuthorizer func() error

// Service — сервис расследований. Не знает про платформу, Store и приложение.
type Service struct {
	b        Backend
	red      Redactor
	defaults Settings
	now      func() time.Time
	newID    func() string
}

// Option настраивает Service.
type Option func(*Service)

// WithClock подменяет источник времени (тесты).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithIDs подменяет генератор идентификаторов (тесты).
func WithIDs(f func() string) Option { return func(s *Service) { s.newID = f } }

// WithDefaults задаёт настройки по умолчанию (из KUBRON_INCIDENT_*) для организаций без сохранённых.
func WithDefaults(d Settings) Option { return func(s *Service) { s.defaults = d.Normalize() } }

// NewService создаёт сервис. red обязателен: без редактора ничего, что пришло из кластера, не сохраняется.
func NewService(b Backend, red Redactor, opts ...Option) *Service {
	s := &Service{b: b, red: red, defaults: DefaultSettings(), now: time.Now,
		newID: func() string { return strings.ToLower(rand.Text()) }}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) clock() time.Time { return Micro(s.now()) }

// Settings возвращает действующие настройки организации.
func (s *Service) Settings(ctx context.Context, orgID string) (Settings, error) {
	st, ok, err := s.b.Settings(ctx, orgID)
	if err != nil {
		return Settings{}, err
	}
	if !ok {
		return s.defaults, nil
	}
	return st, nil
}

// SetSettings проверяет и сохраняет настройки. Срок фрагментов выше 15 дней отвергается.
func (s *Service) SetSettings(ctx context.Context, orgID string, st Settings) (Settings, error) {
	st = st.Normalize()
	if err := st.Validate(); err != nil {
		return Settings{}, err
	}
	if err := s.b.PutSettings(ctx, orgID, st); err != nil {
		return Settings{}, err
	}
	return st, nil
}

// OpenRequest — запрос на открытие расследования.
type OpenRequest struct {
	Target diagnose.ResourceRef
	// DisableLogs — не сохранять фрагменты логов в этом расследовании.
	DisableLogs bool
}

// Open открывает расследование: собирает контекст, строит отчёт, редактирует тексты кластера, сохраняет снимок
// Events и (если разрешено) фрагменты, на которые ссылаются findings.
func (s *Service) Open(ctx context.Context, c Caller, req OpenRequest) (*Incident, error) {
	if err := CheckScope(c.Scope); err != nil {
		return nil, err
	}
	st, err := s.Settings(ctx, c.Scope.OrgID)
	if err != nil {
		return nil, err
	}
	allowed, _ := st.LogsAllowed(req.Target.Namespace)
	allowed = allowed && !req.DisableLogs
	now := s.clock()
	dc, err := diagnose.Collect(ctx, c.Reader, req.Target, diagnose.CollectOptions{Logs: allowed, Now: s.now})
	if err != nil {
		return nil, err
	}
	rep := s.sanitizeReport(diagnose.Analyze(dc))
	in := &Incident{
		ID: s.newID(), Scope: c.Scope, Target: dc.Target, Status: StatusOpen,
		OpenedAt: now, UpdatedAt: now, OpenedBy: c.Actor, Report: rep,
		Events: s.sanitizeEvents(dc.Events), EventsUpdatedAt: now,
		SaveLogs: !req.DisableLogs, Evidence: []Evidence{},
	}
	var saved []string
	var existing []FragmentMeta
	for _, ref := range logRefs(rep) {
		ev := Evidence{Pod: ref.pod, Container: ref.container, Previous: ref.previous, UpdatedAt: now}
		reason := s.autoEvidence(ctx, in, st, dc, ref, &existing, &ev)
		if reason != "" {
			ev.Status, ev.Reason = EvidenceNotSaved, reason
		} else {
			saved = append(saved, ev.FragmentID)
		}
		in.Evidence = append(in.Evidence, ev)
	}
	if err := s.b.PutIncident(ctx, in); err != nil {
		for _, id := range saved { // не оставляем сирот: фрагмент без инцидента никому не виден
			_ = s.b.DeleteFragment(context.WithoutCancel(ctx), c.Scope, in.ID, id)
		}
		return nil, err
	}
	return in, nil
}

type logRef struct {
	ns, pod, container string
	previous           bool
}

// logRefs — уникальные ссылки на логи из фактов отчёта.
func logRefs(rep diagnose.Report) []logRef {
	var out []logRef
	for _, f := range rep.Facts {
		if f.Log == nil {
			continue
		}
		r := logRef{f.Resource.Namespace, f.Resource.Name, f.Log.Container, f.Log.Previous}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// autoEvidence сохраняет фрагмент для finding. Возвращает код причины, если не сохранён; иначе "" и заполненный ev.
func (s *Service) autoEvidence(ctx context.Context, in *Incident, st Settings, dc *diagnose.Context, ref logRef, existing *[]FragmentMeta, ev *Evidence) string {
	if !in.SaveLogs {
		return ReasonDisabledIncident
	}
	if ok, reason := st.LogsAllowed(ref.ns); !ok {
		return reason
	}
	idx := slices.IndexFunc(dc.Logs, func(l diagnose.LogCapture) bool {
		return l.Pod.Name == ref.pod && l.Container == ref.container && l.Previous == ref.previous
	})
	if idx < 0 {
		return ReasonLogUnavailable
	}
	meta, err := s.storeFragment(ctx, in, dc.Logs[idx].Raw, 0, ref, true, *existing, "")
	if err != nil {
		if reason, ok := NotSavedReason(err); ok {
			return reason
		}
		return ReasonRedactionFailed // сбой хранилища: фрагмент не сохранён, расследование продолжается
	}
	*existing = append(*existing, meta)
	ev.Status, ev.FragmentID = EvidenceSaved, meta.ID
	return ""
}

// storeFragment строит фрагмент (редакция, лимиты) и записывает его. replacing — ID заменяемого фрагмента.
func (s *Service) storeFragment(ctx context.Context, in *Incident, raw []byte, anchor int, ref logRef, auto bool, existing []FragmentMeta, replacing string) (FragmentMeta, error) {
	f, err := buildFragment(raw, anchor, s.red, s.now())
	if err != nil {
		return FragmentMeta{}, err
	}
	if err := quotaCheck(existing, replacing, f.Bytes); err != nil {
		return FragmentMeta{}, err
	}
	f.ID, f.IncidentID, f.Scope = s.newID(), in.ID, in.Scope
	f.Namespace, f.Pod, f.Container, f.Previous = ref.ns, ref.pod, ref.container, ref.previous
	f.Auto, f.CreatedAt, f.CreatedBy = auto, s.clock(), in.OpenedBy
	if err := s.b.PutFragment(ctx, &f); err != nil {
		return FragmentMeta{}, err
	}
	return f.FragmentMeta, nil
}

// mutate читает инцидент, применяет fn и записывает; при конфликте версий повторяет (запись одного документа).
func (s *Service) mutate(ctx context.Context, scope tenant.Scope, id string, fn func(*Incident) error) (*Incident, error) {
	for range maxConflictRetries {
		in, err := s.b.GetIncident(ctx, scope, id)
		if err != nil {
			return nil, err
		}
		if err := fn(in); err != nil {
			return nil, err
		}
		in.UpdatedAt = s.clock()
		switch err := s.b.PutIncident(ctx, in); {
		case err == nil:
			return in, nil
		case errors.Is(err, ErrConflict):
			continue
		default:
			return nil, err
		}
	}
	return nil, ErrConflict
}

// Refresh обновляет снимок Events открытого расследования (фонового наблюдателя нет). Отчёт и фрагменты не
// меняются: это состояние на момент открытия.
func (s *Service) Refresh(ctx context.Context, c Caller, id string) (*Incident, error) {
	cur, err := s.b.GetIncident(ctx, c.Scope, id)
	if err != nil {
		return nil, err
	}
	if cur.Status != StatusOpen {
		return nil, ErrClosed
	}
	dc, err := diagnose.Collect(ctx, c.Reader, cur.Target, diagnose.CollectOptions{Logs: false, Now: s.now})
	if err != nil {
		return nil, err
	}
	fresh := s.sanitizeEvents(dc.Events)
	return s.mutate(ctx, c.Scope, id, func(in *Incident) error {
		if in.Status != StatusOpen {
			return ErrClosed
		}
		in.Events = mergeEvents(in.Events, fresh)
		in.EventsUpdatedAt = s.clock()
		return nil
	})
}

// FragmentView — метаданные фрагмента для интерфейса; ContentHidden — у пользователя нет права diagnostics.logs.
type FragmentView struct {
	FragmentMeta
	ContentHidden bool `json:"contentHidden"`
}

// Detail — инцидент с метаданными фрагментов.
type Detail struct {
	Incident  *Incident      `json:"incident"`
	Fragments []FragmentView `json:"fragments"`
}

// Get возвращает инцидент и метаданные фрагментов. Содержимое здесь не отдаётся никогда: canReadLogs лишь
// сообщает интерфейсу, скрыто ли оно (само право проверяет ReadFragment при каждом чтении).
func (s *Service) Get(ctx context.Context, scope tenant.Scope, id string, canReadLogs bool) (*Detail, error) {
	in, err := s.b.GetIncident(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	metas, err := s.b.ListFragments(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	d := &Detail{Incident: in, Fragments: make([]FragmentView, len(metas))}
	for i, m := range metas {
		d.Fragments[i] = FragmentView{FragmentMeta: m, ContentHidden: !canReadLogs}
	}
	return d, nil
}

// List возвращает страницу истории кластера.
func (s *Service) List(ctx context.Context, scope tenant.Scope, q ListQuery) (Page, error) {
	return s.b.ListIncidents(ctx, scope, q)
}

// Delete удаляет инцидент вместе с фрагментами.
func (s *Service) Delete(ctx context.Context, scope tenant.Scope, id string) error {
	return s.b.DeleteIncident(ctx, scope, id)
}

// MarkOutcome отмечает результат расследования и закрывает его. Подтвердить гипотезу может только человек, и
// только ту, что есть в отчёте.
func (s *Service) MarkOutcome(ctx context.Context, c Caller, id string, o Outcome) (*Incident, error) {
	switch o.Result {
	case ResultResolved, ResultUnresolved, ResultInconclusive:
	default:
		return nil, fmt.Errorf("%w: unknown result", ErrInvalidOutcome)
	}
	if utf8.RuneCountInString(o.Note) > MaxNoteRunes {
		return nil, fmt.Errorf("%w: note is too long", ErrInvalidOutcome)
	}
	o.Note = s.redactText(o.Note)
	o.MarkedBy, o.MarkedAt = c.Actor, s.clock()
	return s.mutate(ctx, c.Scope, id, func(in *Incident) error {
		if o.ConfirmedHypothesisID != "" && !slices.ContainsFunc(in.Report.Hypotheses,
			func(h diagnose.Hypothesis) bool { return h.ID == o.ConfirmedHypothesisID }) {
			return fmt.Errorf("%w: unknown hypothesis", ErrInvalidOutcome)
		}
		in.Outcome, in.Status = &o, StatusClosed
		return nil
	})
}

// SaveFragmentRequest — явное действие «сохранить как доказательство».
type SaveFragmentRequest struct {
	Pod, Container string
	Previous       bool
	AnchorLine     int // строка якоря с 1; 0 — выбрать автоматически
}

// SaveFragment сохраняет фрагмент по явному действию пользователя. Под должен быть среди затронутых ресурсов
// расследования; выключатели (организация, namespace, расследование) действуют и здесь. Отказ — ошибка с кодом
// причины (NotSavedReason).
func (s *Service) SaveFragment(ctx context.Context, c Caller, id string, req SaveFragmentRequest) (FragmentMeta, error) {
	in, err := s.b.GetIncident(ctx, c.Scope, id)
	if err != nil {
		return FragmentMeta{}, err
	}
	i := slices.IndexFunc(in.Report.Resources, func(r diagnose.ResourceRef) bool { return r.Kind == "Pod" && r.Name == req.Pod })
	if i < 0 {
		return FragmentMeta{}, ErrNotAffected
	}
	ref := logRef{in.Report.Resources[i].Namespace, req.Pod, req.Container, req.Previous}
	st, err := s.Settings(ctx, c.Scope.OrgID)
	if err != nil {
		return FragmentMeta{}, err
	}
	if !in.SaveLogs {
		return FragmentMeta{}, ErrLogsDisabled
	}
	if ok, _ := st.LogsAllowed(ref.ns); !ok {
		return FragmentMeta{}, ErrLogsDisabled
	}
	raw, err := c.Reader.PodLogs(ctx, diagnose.LogRequest{Namespace: ref.ns, Pod: ref.pod, Container: ref.container,
		Previous: ref.previous, TailLines: logTailLines, LimitBytes: logLimitBytes})
	if err != nil {
		return FragmentMeta{}, fmt.Errorf("reading the log: %w", err)
	}
	existing, err := s.b.ListFragments(ctx, c.Scope, id)
	if err != nil {
		return FragmentMeta{}, err
	}
	key := Evidence{Pod: ref.pod, Container: ref.container, Previous: ref.previous}.key()
	var replacing string
	for _, e := range in.Evidence {
		if e.key() == key && e.FragmentID != "" {
			replacing = e.FragmentID
		}
	}
	meta, err := s.storeFragment(ctx, &Incident{ID: in.ID, Scope: in.Scope, OpenedBy: c.Actor}, raw, req.AnchorLine, ref, false, existing, replacing)
	if err != nil {
		return FragmentMeta{}, err
	}
	_, err = s.mutate(ctx, c.Scope, id, func(in *Incident) error {
		setEvidence(in, Evidence{Pod: ref.pod, Container: ref.container, Previous: ref.previous,
			Status: EvidenceSaved, FragmentID: meta.ID, UpdatedAt: s.clock()})
		return nil
	})
	if err != nil {
		_ = s.b.DeleteFragment(context.WithoutCancel(ctx), c.Scope, id, meta.ID)
		return FragmentMeta{}, err
	}
	if replacing != "" {
		_ = s.b.DeleteFragment(ctx, c.Scope, id, replacing)
	}
	return meta, nil
}

func setEvidence(in *Incident, ev Evidence) {
	if i := slices.IndexFunc(in.Evidence, func(e Evidence) bool { return e.key() == ev.key() }); i >= 0 {
		in.Evidence[i] = ev
		return
	}
	in.Evidence = append(in.Evidence, ev)
}

// DeleteFragment удаляет фрагмент; finding получает пометку «не сохранено: удалено».
func (s *Service) DeleteFragment(ctx context.Context, scope tenant.Scope, id, fragmentID string) error {
	if err := s.b.DeleteFragment(ctx, scope, id, fragmentID); err != nil {
		return err
	}
	_, err := s.mutate(ctx, scope, id, func(in *Incident) error {
		for i := range in.Evidence {
			if in.Evidence[i].FragmentID == fragmentID {
				in.Evidence[i].Status, in.Evidence[i].Reason = EvidenceNotSaved, ReasonDeleted
				in.Evidence[i].FragmentID, in.Evidence[i].UpdatedAt = "", s.clock()
			}
		}
		return nil
	})
	return err
}

// SetSaveLogs включает или отключает сохранение фрагментов для расследования; deleteExisting удаляет и уже
// сохранённые.
func (s *Service) SetSaveLogs(ctx context.Context, scope tenant.Scope, id string, enabled, deleteExisting bool) (*Incident, error) {
	if deleteExisting {
		metas, err := s.b.ListFragments(ctx, scope, id)
		if err != nil {
			return nil, err
		}
		for _, m := range metas {
			if err := s.DeleteFragment(ctx, scope, id, m.ID); err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
		}
	}
	return s.mutate(ctx, scope, id, func(in *Incident) error {
		in.SaveLogs = enabled
		return nil
	})
}

// ReadFragment отдаёт содержимое фрагмента. Право проверяется authz при каждом вызове до чтения хранилища.
func (s *Service) ReadFragment(ctx context.Context, scope tenant.Scope, id, fragmentID string, authz LogsAuthorizer) (*Fragment, error) {
	if authz == nil {
		return nil, errors.New("log access check is required")
	}
	if err := authz(); err != nil {
		return nil, err
	}
	return s.b.GetFragment(ctx, scope, id, fragmentID)
}

// Export — выгрузка инцидента. Фрагменты входят только по явному выбору и при праве diagnostics.logs.
type Export struct {
	ExportedAt time.Time  `json:"exportedAt"`
	Incident   *Incident  `json:"incident"`
	Fragments  []Fragment `json:"fragments,omitempty"`
}

// ExportIncident готовит выгрузку. includeFragments=true требует authz (иначе ошибка права, без выгрузки).
func (s *Service) ExportIncident(ctx context.Context, scope tenant.Scope, id string, includeFragments bool, authz LogsAuthorizer) (*Export, error) {
	if includeFragments {
		if authz == nil {
			return nil, errors.New("log access check is required")
		}
		if err := authz(); err != nil {
			return nil, err
		}
	}
	in, err := s.b.GetIncident(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	ex := &Export{ExportedAt: s.clock(), Incident: in}
	if includeFragments {
		metas, err := s.b.ListFragments(ctx, scope, id)
		if err != nil {
			return nil, err
		}
		for _, m := range metas {
			f, err := s.b.GetFragment(ctx, scope, id, m.ID)
			if err != nil {
				return nil, err
			}
			ex.Fragments = append(ex.Fragments, *f)
		}
	}
	return ex, nil
}

// Purge применяет retention ко всем организациям: возвращает число удалённых записей. Для задачи internal/jobs.
func (s *Service) Purge(ctx context.Context) (int, error) {
	orgs, err := s.b.OrgIDs(ctx)
	if err != nil {
		return 0, err
	}
	now, total := s.now(), 0
	var errs []error
	for _, org := range orgs {
		st, err := s.Settings(ctx, org)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		res, err := s.b.Purge(ctx, org, st.FragmentCutoff(now), st.IncidentCutoff(now))
		total += res.Fragments + res.Incidents
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}
