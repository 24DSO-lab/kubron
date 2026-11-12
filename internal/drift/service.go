// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Сервис не импортирует платформу операционных модулей (internal/opsplatform): проверка прав, аудит и чтение кластера
// подключаются снаружи, в wire-слое (ADR-0026). Сервис работает с узкими интерфейсами этого пакета и хранилищем
// baseline.Backend, поэтому проверяется тестами на двойниках без кластера и без платформы.

// Ошибки сервиса.
var (
	// ErrConfirmationRequired — не получено подтверждение «фиксируется текущее состояние, включая существующие
	// отклонения». Версия без явного подтверждения не создаётся.
	ErrConfirmationRequired = fmt.Errorf("%w: confirmation is required", baseline.ErrInvalid)
	// ErrCommentRequired — у версии и исключения должен быть комментарий.
	ErrCommentRequired = fmt.Errorf("%w: a comment is required", baseline.ErrInvalid)
	// ErrEmptySnapshot — в выборке нет ни одного ресурса: пустую версию создавать нельзя.
	ErrEmptySnapshot = fmt.Errorf("%w: no resources were selected", baseline.ErrInvalid)
	// ErrNoActiveVersion — у baseline нет активной версии, проверять нечего.
	ErrNoActiveVersion = fmt.Errorf("%w: the baseline has no active version", baseline.ErrInvalid)
)

const (
	// MaxExceptions — предел исключений одной версии.
	MaxExceptions = 200
	// maxPutRetries — повторы при одновременном создании версии с тем же номером.
	maxPutRetries = 3
)

// Session — контекст одного вызова: область, действующее лицо и чтение кластера. Собирается wire-слоем из операции
// платформы; прав сервис не проверяет — это делает вызывающий до обращения (Op.Require).
type Session struct {
	Scope         tenant.Scope
	Actor         string
	Reader        ClusterReader
	CorrelationID string
}

// Service — прикладной слой baseline: создание версий, активная версия, исключения, проверки, история.
type Service struct {
	store baseline.Backend
	audit AuditLookup
	key   []byte
	now   func() time.Time
	log   *slog.Logger
}

// ServiceOptions — необязательные зависимости сервиса.
type ServiceOptions struct {
	Audit AuditLookup
	Log   *slog.Logger
	// PreviewKey — общий для процесса ключ токенов предпросмотра (иначе предпросмотр и подтверждение, попавшие в
	// разные экземпляры, не совпадут).
	PreviewKey []byte
	// Now — источник времени (в тестах); по умолчанию time.Now.
	Now func() time.Time
}

// NewService создаёт сервис над хранилищем.
func NewService(store baseline.Backend, o ServiceOptions) *Service {
	s := &Service{store: store, audit: o.Audit, key: o.PreviewKey, now: o.Now, log: o.Log}
	if len(s.key) == 0 {
		s.key = NewPreviewKey()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	return s
}

func (s *Service) engine(sess Session) *Engine {
	return NewEngine(sess.Reader, Options{Audit: s.audit, Log: s.log, PreviewKey: s.key})
}

// CreateInput — создание baseline вместе с первой версией.
type CreateInput struct {
	Name    string
	Spec    Spec
	Comment string
	// Token — токен предпросмотра; Confirmed — пользователь подтвердил фиксацию текущего состояния.
	Token     string
	Confirmed bool
}

// PreviewNew показывает, что будет зафиксировано первой версией. Ничего не сохраняет.
func (s *Service) PreviewNew(ctx context.Context, sess Session, spec Spec) (Preview, error) {
	if err := baseline.ValidateScope(sess.Scope); err != nil {
		return Preview{}, err
	}
	return s.engine(sess).Preview(ctx, spec, nil, "new", s.now())
}

// PreviewVersion показывает, что изменится относительно активной версии baseline.
func (s *Service) PreviewVersion(ctx context.Context, sess Session, baselineID string) (Preview, error) {
	b, err := s.store.GetBaseline(ctx, sess.Scope, baselineID)
	if err != nil {
		return Preview{}, err
	}
	var prev *baseline.Version
	if b.ActiveVersionID != "" {
		v, err := s.store.GetVersion(ctx, sess.Scope, b.ID, b.ActiveVersionID)
		if err != nil {
			return Preview{}, err
		}
		prev = &v
	}
	return s.engine(sess).Preview(ctx, specOf(b), prev, b.ID, s.now())
}

func specOf(b baseline.Baseline) Spec {
	return Spec{Namespaces: b.Namespaces, Selector: b.Selector, Kinds: b.Kinds, Refs: b.Refs}
}

func checkIntent(comment string, confirmed bool) (string, error) {
	c := strings.TrimSpace(comment)
	if c == "" {
		return "", ErrCommentRequired
	}
	if len(c) > baseline.MaxCommentLength {
		return "", fmt.Errorf("%w: the comment is too long", baseline.ErrInvalid)
	}
	if !confirmed {
		return "", ErrConfirmationRequired
	}
	return c, nil
}

// Create создаёт baseline и его первую версию (она же активная). Без комментария, подтверждения и актуального
// токена предпросмотра не создаёт ничего.
func (s *Service) Create(ctx context.Context, sess Session, in CreateInput) (baseline.Baseline, baseline.Version, error) {
	if err := baseline.ValidateScope(sess.Scope); err != nil {
		return baseline.Baseline{}, baseline.Version{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return baseline.Baseline{}, baseline.Version{}, fmt.Errorf("%w: a baseline needs a name", baseline.ErrInvalid)
	}
	comment, err := checkIntent(in.Comment, in.Confirmed)
	if err != nil {
		return baseline.Baseline{}, baseline.Version{}, err
	}
	snap, err := s.engine(sess).SnapshotConfirmed(ctx, in.Spec, in.Token, "new", s.now())
	if err != nil {
		return baseline.Baseline{}, baseline.Version{}, err
	}
	if len(snap.Objects) == 0 {
		return baseline.Baseline{}, baseline.Version{}, ErrEmptySnapshot
	}
	now := baseline.Normalize(s.now())
	b := baseline.Baseline{
		ID: baseline.NewID(), Scope: sess.Scope, Name: strings.TrimSpace(in.Name),
		Namespaces: in.Spec.Namespaces, Selector: in.Spec.Selector, Kinds: in.Spec.Kinds, Refs: in.Spec.Refs,
		CreatedBy: sess.Actor, CreatedAt: now,
	}
	if err := b.Validate(); err != nil {
		return baseline.Baseline{}, baseline.Version{}, err
	}
	if err := s.store.PutBaseline(ctx, b); err != nil {
		return baseline.Baseline{}, baseline.Version{}, err
	}
	v := newVersion(b.ID, 1, snap, sess.Actor, comment, now)
	if err := s.store.PutVersion(ctx, sess.Scope, v); err != nil {
		s.rollback(sess, b.ID)
		return baseline.Baseline{}, baseline.Version{}, err
	}
	b.ActiveVersionID = v.ID
	if err := s.store.PutBaseline(ctx, b); err != nil {
		s.rollback(sess, b.ID)
		return baseline.Baseline{}, baseline.Version{}, err
	}
	return b, v, nil
}

// rollback убирает недосозданный baseline: без версии он не должен остаться в списке.
func (s *Service) rollback(sess Session, id string) {
	// Контекст вызова мог быть отменён — именно из-за этого и откатываем.
	if err := s.store.DeleteBaseline(context.WithoutCancel(context.Background()), sess.Scope, id); err != nil && !errors.Is(err, baseline.ErrNotFound) {
		s.log.Warn("baseline rollback failed", "baseline", id, "err", err)
	}
}

func newVersion(baselineID string, seq int, snap Snapshot, actor, comment string, at time.Time) baseline.Version {
	return baseline.Version{
		ID: baseline.NewID(), BaselineID: baselineID, Seq: seq, RulesVersion: snap.RulesVersion,
		Source: baseline.SourceLiveSnapshot, Author: actor, Comment: comment, CreatedAt: at,
		Salt: snap.Salt, Objects: snap.Objects,
	}
}

// VersionInput — создание новой версии существующего baseline.
type VersionInput struct {
	BaselineID string
	Comment    string
	Token      string
	Confirmed  bool
}

// AddVersion снимает текущее состояние в новую версию и делает её активной. Исключения активной версии переносятся
// на новую: решение «это отклонение допустимо» не должно пропадать от пересъёмки.
func (s *Service) AddVersion(ctx context.Context, sess Session, in VersionInput) (baseline.Version, error) {
	comment, err := checkIntent(in.Comment, in.Confirmed)
	if err != nil {
		return baseline.Version{}, err
	}
	b, err := s.store.GetBaseline(ctx, sess.Scope, in.BaselineID)
	if err != nil {
		return baseline.Version{}, err
	}
	snap, err := s.engine(sess).SnapshotConfirmed(ctx, specOf(b), in.Token, b.ID, s.now())
	if err != nil {
		return baseline.Version{}, err
	}
	if len(snap.Objects) == 0 {
		return baseline.Version{}, ErrEmptySnapshot
	}
	var carried []baseline.Exception
	if b.ActiveVersionID != "" {
		if carried, err = s.store.GetExceptions(ctx, sess.Scope, b.ID, b.ActiveVersionID); err != nil {
			return baseline.Version{}, err
		}
	}
	var v baseline.Version
	for attempt := 0; ; attempt++ {
		seq, err := s.nextSeq(ctx, sess.Scope, b.ID)
		if err != nil {
			return baseline.Version{}, err
		}
		v = newVersion(b.ID, seq, snap, sess.Actor, comment, baseline.Normalize(s.now()))
		err = s.store.PutVersion(ctx, sess.Scope, v)
		if err == nil {
			break
		}
		if !errors.Is(err, baseline.ErrExists) || attempt >= maxPutRetries {
			return baseline.Version{}, err
		}
	}
	if len(carried) > 0 {
		if err := s.store.PutExceptions(ctx, sess.Scope, b.ID, v.ID, carried); err != nil {
			return baseline.Version{}, err
		}
	}
	b.ActiveVersionID = v.ID
	if err := s.store.PutBaseline(ctx, b); err != nil {
		return baseline.Version{}, err
	}
	return v, nil
}

func (s *Service) nextSeq(ctx context.Context, scope tenant.Scope, id string) (int, error) {
	p, err := s.store.ListVersions(ctx, scope, id, baseline.Page{Limit: 1})
	if err != nil {
		return 0, err
	}
	if len(p.Items) == 0 {
		return 1, nil
	}
	return p.Items[0].Seq + 1, nil // страница от новых к старым
}

// SetActive делает активной одну из существующих версий. Содержимое версий не меняется.
func (s *Service) SetActive(ctx context.Context, sess Session, baselineID, versionID string) (baseline.Baseline, error) {
	b, err := s.store.GetBaseline(ctx, sess.Scope, baselineID)
	if err != nil {
		return baseline.Baseline{}, err
	}
	if _, err := s.store.GetVersion(ctx, sess.Scope, baselineID, versionID); err != nil {
		return baseline.Baseline{}, err
	}
	b.ActiveVersionID = versionID
	if err := s.store.PutBaseline(ctx, b); err != nil {
		return baseline.Baseline{}, err
	}
	return b, nil
}

// ExceptionInput — новое исключение.
type ExceptionInput struct {
	Scope     baseline.ExceptionScope
	Ref       baseline.Ref
	Path      string
	Comment   string
	ExpiresAt *time.Time
}

// AddException добавляет исключение к активной версии. Хранится отдельно от неизменяемого содержимого версии.
func (s *Service) AddException(ctx context.Context, sess Session, baselineID string, in ExceptionInput) (baseline.Exception, error) {
	b, err := s.store.GetBaseline(ctx, sess.Scope, baselineID)
	if err != nil {
		return baseline.Exception{}, err
	}
	if b.ActiveVersionID == "" {
		return baseline.Exception{}, ErrNoActiveVersion
	}
	ex := baseline.Exception{
		ID: baseline.NewID(), Scope: in.Scope, Ref: in.Ref, Path: strings.TrimSpace(in.Path),
		Comment: strings.TrimSpace(in.Comment), ExpiresAt: in.ExpiresAt, CreatedBy: sess.Actor, CreatedAt: baseline.Normalize(s.now()),
	}
	if ex.Scope == baseline.ExceptionKind {
		ex.Ref = baseline.Ref{Kind: in.Ref.Kind} // исключение по типу не привязано к ресурсу
	}
	if err := ex.Validate(); err != nil {
		return baseline.Exception{}, err
	}
	list, err := s.store.GetExceptions(ctx, sess.Scope, b.ID, b.ActiveVersionID)
	if err != nil {
		return baseline.Exception{}, err
	}
	if len(list) >= MaxExceptions {
		return baseline.Exception{}, fmt.Errorf("%w: too many exceptions", baseline.ErrInvalid)
	}
	if err := s.store.PutExceptions(ctx, sess.Scope, b.ID, b.ActiveVersionID, append(list, ex)); err != nil {
		return baseline.Exception{}, err
	}
	return ex, nil
}

// RemoveException удаляет исключение активной версии.
func (s *Service) RemoveException(ctx context.Context, sess Session, baselineID, exceptionID string) error {
	b, err := s.store.GetBaseline(ctx, sess.Scope, baselineID)
	if err != nil {
		return err
	}
	if b.ActiveVersionID == "" {
		return baseline.ErrNotFound
	}
	list, err := s.store.GetExceptions(ctx, sess.Scope, b.ID, b.ActiveVersionID)
	if err != nil {
		return err
	}
	kept := make([]baseline.Exception, 0, len(list))
	for _, e := range list {
		if e.ID != exceptionID {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(list) {
		return baseline.ErrNotFound
	}
	return s.store.PutExceptions(ctx, sess.Scope, b.ID, b.ActiveVersionID, kept)
}

// Exceptions возвращает исключения активной версии.
func (s *Service) Exceptions(ctx context.Context, sess Session, baselineID string) ([]baseline.Exception, error) {
	b, err := s.store.GetBaseline(ctx, sess.Scope, baselineID)
	if err != nil {
		return nil, err
	}
	if b.ActiveVersionID == "" {
		return nil, nil
	}
	return s.store.GetExceptions(ctx, sess.Scope, b.ID, b.ActiveVersionID)
}

// Delete удаляет baseline каскадом: версии, исключения, историю проверок.
func (s *Service) Delete(ctx context.Context, sess Session, baselineID string) error {
	return s.store.DeleteBaseline(ctx, sess.Scope, baselineID)
}

// Check проверяет кластер по активной версии и записывает результат в историю. Кластер не меняется.
func (s *Service) Check(ctx context.Context, sess Session, baselineID string) (baseline.CheckResult, error) {
	b, err := s.store.GetBaseline(ctx, sess.Scope, baselineID)
	if err != nil {
		return baseline.CheckResult{}, err
	}
	if b.ActiveVersionID == "" {
		return baseline.CheckResult{}, ErrNoActiveVersion
	}
	v, err := s.store.GetVersion(ctx, sess.Scope, b.ID, b.ActiveVersionID)
	if err != nil {
		return baseline.CheckResult{}, err
	}
	ex, err := s.store.GetExceptions(ctx, sess.Scope, b.ID, v.ID)
	if err != nil {
		return baseline.CheckResult{}, err
	}
	res, err := s.engine(sess).Check(ctx, CheckInput{Version: v, Exceptions: ex, Now: s.now()})
	if err != nil {
		return baseline.CheckResult{}, err
	}
	res.ID, res.BaselineID, res.VersionID, res.Scope = baseline.NewID(), b.ID, v.ID, sess.Scope
	res.Initiator, res.CorrelationID = sess.Actor, sess.CorrelationID
	res = res.Normalized()
	if err := s.store.AppendResult(ctx, sess.Scope, res); err != nil {
		return baseline.CheckResult{}, err
	}
	return res, nil
}

// List и просмотр: версии отдаются без соли и содержимого.

// Baselines возвращает baseline кластера.
func (s *Service) Baselines(ctx context.Context, sess Session) ([]baseline.Baseline, error) {
	return s.store.ListBaselines(ctx, sess.Scope)
}

// Baseline возвращает один baseline.
func (s *Service) Baseline(ctx context.Context, sess Session, id string) (baseline.Baseline, error) {
	return s.store.GetBaseline(ctx, sess.Scope, id)
}

// Versions возвращает страницу истории версий.
func (s *Service) Versions(ctx context.Context, sess Session, id string, p baseline.Page) (baseline.VersionPage, error) {
	return s.store.ListVersions(ctx, sess.Scope, id, p)
}

// Results возвращает страницу истории проверок.
func (s *Service) Results(ctx context.Context, sess Session, id string, p baseline.Page) (baseline.ResultPage, error) {
	return s.store.ListResults(ctx, sess.Scope, id, p)
}

// Result возвращает полный результат проверки.
func (s *Service) Result(ctx context.Context, sess Session, baselineID, resultID string) (baseline.CheckResult, error) {
	return s.store.GetResult(ctx, sess.Scope, baselineID, resultID)
}
