// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/logging"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Env — всё, что платформе нужно от приложения (consumer-side интерфейс; реализует слой связки internal/app).
// Пакет не знает ни App, ни Manager: подключение, права и каталог приходят отсюда.
type Env interface {
	// Check — серверная проверка права p на кластер clusterID для действующей сессии (auth.Service.CheckCluster).
	// Чужой или неизвестный кластер: auth.ErrClusterNotFound; нет права: auth.ErrForbidden; нет входа: ошибка входа.
	// Реализация сама в аудит не пишет: отказ записывает Provider с именем операции.
	Check(p auth.Permission, clusterID string) (auth.Actor, error)
	// OrgID — организация каталога Kubron (десктоп: фиксированная локальная).
	OrgID() string
	// DefaultTeam — команда кластеров без владельца (десктоп и режим без команд).
	DefaultTeam() string
	// SessionID — веб-сессия владельца вида (пусто в десктопе).
	SessionID() string
	// Clients — клиенты подключения ЭТОЙ сессии к кластеру: только через существующий Manager и ConfigFor.
	Clients(ctx context.Context, clusterID string) (Clients, error)
	// RecordAudit пишет запись аудита. Отказ зеркала или хранилища аудита результат операции не меняет.
	RecordAudit(ev audit.Event)
	// Notify доставляет событие ТОЛЬКО владельцу сессии (Event Hub, ADR-0009); без широковещания.
	Notify(event string, data ...any)
}

// Observer принимает метрики операций (internal/metrics.Metrics). Лейблы — модуль, операция, статус; кластеров,
// пользователей и namespace в них нет (ADR-0022).
type Observer interface {
	ObserveOp(module, operation, status string, d time.Duration)
}

var observer atomic.Pointer[Observer]

// SetObserver подключает метрики; nil — отключает. Вызывается при старте сервера.
func SetObserver(o Observer) {
	if o == nil {
		observer.Store(nil)
		return
	}
	observer.Store(&o)
}

// Config — настройки Provider. Нулевые поля заменяются значениями по умолчанию.
type Config struct {
	// OpTimeout — предельная длительность операции целиком (первая версия: короткие синхронные операции).
	OpTimeout time.Duration
	// Limiter — общий лимитер чтений кластера; nil — SharedLimiter().
	Limiter *ClusterLimiter
	// Limits — ограничения чтения.
	Limits ReaderLimits
	// Logger — технический журнал; nil — slog.Default().
	Logger *slog.Logger
	// Now подменяется в тестах.
	Now func() time.Time
}

// DefaultOpTimeout — предельная длительность одной операции.
const DefaultOpTimeout = 60 * time.Second

// nameRE — допустимые имена модуля и операции: задаются кодом, попадают в метрики и в имя действия аудита.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// attrKeyRE — допустимые ключи атрибутов аудита.
var attrKeyRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,31}$`)

const (
	maxAttrValue = 128 // символов значения атрибута аудита
	maxLoggedErr = 300 // символов ошибки в техническом журнале
)

// NewProvider создаёт Provider поверх Env.
func NewProvider(env Env, cfg Config) Provider {
	if cfg.OpTimeout <= 0 {
		cfg.OpTimeout = DefaultOpTimeout
	}
	if cfg.Limiter == nil {
		cfg.Limiter = SharedLimiter()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &provider{env: env, cfg: cfg}
}

type provider struct {
	env Env
	cfg Config
}

// Begin — см. Provider. Порядок важен: сначала права и принадлежность кластера, и только потом любое чтение.
func (p *provider) Begin(ctx context.Context, req OpRequest) (context.Context, *Op, error) {
	if !nameRE.MatchString(req.Module) || !nameRE.MatchString(req.Operation) || req.ClusterID == "" ||
		!auth.KnownPermission(req.Permission) || !auth.ClusterScoped(req.Permission) {
		return ctx, nil, errors.New("invalid operation request")
	}
	actor, err := p.env.Check(req.Permission, req.ClusterID)
	if err != nil {
		if errors.Is(err, auth.ErrForbidden) {
			p.recordDenied(actor, req.Module, req.Operation, req.Permission)
		}
		return ctx, nil, err
	}
	scope := tenant.Scope{OrgID: p.env.OrgID(), TeamID: actor.TeamID, ClusterID: req.ClusterID}
	if scope.TeamID == "" {
		scope.TeamID = p.env.DefaultTeam()
	}
	if !scope.Valid() {
		// Каталог не назвал организацию или команду: чужой scope недопустим, а «пустой» означал бы «любой».
		return ctx, nil, auth.ErrClusterNotFound
	}

	corr := logging.RequestID(ctx)
	if corr == "" {
		corr = logging.NewRequestID()
		ctx = logging.WithRequestID(ctx, corr)
	}
	ctx, cancel := context.WithTimeout(ctx, p.cfg.OpTimeout)

	var reader Reader
	if req.StoredOnly {
		// Подключения нет, поэтому существование кластера проверяет окружение (если умеет): иначе на десктопе
		// несуществующий id выглядел бы как кластер с пустой историей.
		if k, ok := p.env.(interface{ ClusterKnown(clusterID string) error }); ok {
			if err := k.ClusterKnown(req.ClusterID); err != nil {
				cancel()
				return ctx, nil, err
			}
		}
	} else {
		clients, err := p.env.Clients(ctx, req.ClusterID)
		if err != nil {
			cancel()
			return ctx, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		reader = NewReader(req.ClusterID, clients, p.cfg.Limiter, p.cfg.Limits)
	}
	op := &opState{p: p, req: req, actor: actor, scope: scope, corr: corr, started: p.cfg.Now(), cancel: cancel}
	return ctx, NewOp(&Op{
		Scope:         scope,
		Initiator:     actor.Username,
		SessionID:     p.env.SessionID(),
		CorrelationID: corr,
		StartedAt:     op.started,
		Reader:        reader,
	}, Hooks{Require: op.require, Audit: op.audit, Notify: op.notify, End: op.end}), nil
}

// opState — состояние одной операции для хуков Op.
type opState struct {
	p       *provider
	req     OpRequest
	actor   auth.Actor
	scope   tenant.Scope
	corr    string
	started time.Time
	cancel  context.CancelFunc
}

func (s *opState) require(perm auth.Permission) error {
	actor, err := s.p.env.Check(perm, s.req.ClusterID)
	if err != nil && errors.Is(err, auth.ErrForbidden) {
		s.p.recordDenied(actor, s.req.Module, s.req.Operation, perm)
	}
	return err
}

func (s *opState) notify(event, id string) {
	if event == "" {
		return
	}
	s.p.env.Notify(event, id)
}

// audit пишет значимое действие. В запись попадают цель, идентификаторы и счётчики из attrs; содержимое объектов и
// логов не попадает: значения неподходящего вида отбрасываются, остальные редактируются и усекаются. Паника или отказ
// приёмника аудита результат операции не меняют.
func (s *opState) audit(action, target string, err error, attrs ...slog.Attr) {
	defer func() { _ = recover() }()
	result := audit.ResultOK
	if err != nil {
		result = audit.ResultFailed
	}
	parts := make([]string, 0, len(attrs)+3)
	if target != "" {
		parts = append(parts, "target="+safeValue(target))
	}
	parts = append(parts, "correlation_id="+s.corr)
	for _, a := range attrs {
		v, ok := attrText(a)
		if !ok || !attrKeyRE.MatchString(a.Key) {
			continue
		}
		parts = append(parts, a.Key+"="+v)
	}
	if err != nil {
		parts = append(parts, "error="+safeValue(err.Error()))
	}
	s.p.env.RecordAudit(audit.Event{
		User: s.actor.Username, Role: string(s.actor.Role), Action: action, Result: result,
		Detail: strings.Join(parts, " "), Source: s.actor.Source,
		TeamID: s.scope.TeamID, ClusterID: s.scope.ClusterID,
	})
}

// end пишет технический журнал и метрику; журнал отделён от аудита.
func (s *opState) end(err error, partial ...string) {
	defer s.cancel()
	d := s.p.cfg.Now().Sub(s.started)
	status := opStatus(err, partial)
	level := slog.LevelInfo
	if status == "failed" {
		level = slog.LevelWarn
	}
	attrs := []slog.Attr{
		slog.String("module", s.req.Module),
		slog.String("operation", s.req.Operation),
		slog.String("status", status),
		slog.Duration("duration", d),
		slog.String("correlation_id", s.corr),
		slog.String("org_id", s.scope.OrgID),
		slog.String("team_id", s.scope.TeamID),
		slog.String("cluster_id", s.scope.ClusterID),
		slog.String("initiator", s.actor.Username),
	}
	if len(partial) > 0 {
		attrs = append(attrs, slog.Int("partial_sources", len(partial)))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", clipRunes(logging.Redact(err.Error()), maxLoggedErr)))
	}
	s.p.cfg.Logger.LogAttrs(context.Background(), level, "ops operation finished", attrs...)
	if o := observer.Load(); o != nil {
		(*o).ObserveOp(s.req.Module, s.req.Operation, status, d)
	}
}

func opStatus(err error, partial []string) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case err != nil:
		return "failed"
	case len(partial) > 0:
		return "partial"
	}
	return "ok"
}

// recordDenied записывает отказ по праву: действие названо по модулю и операции (IncidentOpen, ReadinessRun).
func (p *provider) recordDenied(actor auth.Actor, module, operation string, perm auth.Permission) {
	defer func() { _ = recover() }()
	p.env.RecordAudit(audit.Event{
		User: actor.Username, Role: string(actor.Role), Action: actionName(module, operation),
		Result: audit.ResultDenied, Detail: "need=" + string(perm), Source: actor.Source,
		TeamID: actor.TeamID, ClusterID: actor.ClusterID,
	})
}

// actionName строит имя действия аудита из модуля и операции: incident, open → IncidentOpen.
func actionName(module, operation string) string {
	var b strings.Builder
	for _, w := range []string{module, operation} {
		for part := range strings.SplitSeq(w, "_") {
			if part != "" {
				b.WriteString(strings.ToUpper(part[:1]) + part[1:])
			}
		}
	}
	return b.String()
}

// attrText превращает значение атрибута в текст. Допустимы только скаляры: структуры, группы и произвольные значения
// отбрасываются, чтобы в аудит не попало содержимое объекта.
func attrText(a slog.Attr) (string, bool) {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return safeValue(v.String()), true
	case slog.KindInt64, slog.KindUint64, slog.KindBool, slog.KindFloat64, slog.KindDuration, slog.KindTime:
		return clipRunes(v.String(), maxAttrValue), true
	}
	return "", false
}

// safeValue редактирует чувствительное, усекает и убирает пробелы (значение остаётся одним токеном записи).
func safeValue(s string) string {
	s = clipRunes(logging.Redact(s), maxAttrValue)
	return strings.Map(func(r rune) rune {
		if r == ' ' || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, s)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
