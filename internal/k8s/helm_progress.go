// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Прогресс длинных Helm-операций (H6).
//
// Идентификатор операции (opID) создаёт фронтенд до вызова, поэтому события можно привязать к операции
// ещё до того, как блокирующий вызов вернётся. Бэкенд шлёт событие helm:progress:
//   - при смене фазы («Загрузка чарта», «Применение», «Ожидание готовности»);
//   - пачкой строк журнала Helm (его slog-сообщения: «waiting for resources count=7» и т. п.), не чаще
//     helmProgressTick, чтобы не засыпать UI;
//   - раз в секунду как heartbeat с прошедшим временем;
//   - итоговым событием done=true (с error при неудаче).
const (
	EventHelmProgress = "helm:progress"

	helmProgressTick   = 250 * time.Millisecond
	helmProgressBeat   = time.Second
	helmProgressBatch  = 40  // строк журнала в одном событии
	helmProgressLineMx = 300 // символов в строке
)

var helmOpIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// HelmProgress — полезная нагрузка события helm:progress.
type HelmProgress struct {
	OpID       string   `json:"opId"`
	Op         string   `json:"op"` // install | upgrade | rollback | uninstall
	Namespace  string   `json:"namespace"`
	Release    string   `json:"release"`
	Phase      string   `json:"phase"`
	Lines      []string `json:"lines"` // новые строки журнала Helm
	ElapsedMs  int64    `json:"elapsedMs"`
	Cancelable bool     `json:"cancelable"`
	Done       bool     `json:"done"`
	Error      string   `json:"error"`
}

type helmOps struct {
	mu  sync.Mutex
	ops map[string]*helmTracker
}

var activeHelmOps = &helmOps{ops: map[string]*helmTracker{}}

// helmTracker отслеживает одну операцию. Методы безопасны для nil-получателя (операция без opID).
type helmTracker struct {
	m       *Manager
	opID    string
	op      string
	ns, rel string
	started time.Time
	cancel  context.CancelFunc // nil — операцию нельзя отменить

	cluster string     // имя кластера: в записи журнала
	sc      *helmScope // область журнала (команда кластера); nil — журнал недоступен

	mu       sync.Mutex
	phase    string
	pending  []string
	log      []string // последние helmJournalLogLines строк: уходят в журнал операций
	detail   string
	revision int
	done     bool
	stop     chan struct{}
}

// beginHelmOp регистрирует операцию. Пустой opID — операция не отслеживается (возвращается nil-трекер).
// cancelable: операция принимает контекст, и CancelHelmOperation её прервёт.
func (m *Manager) beginHelmOp(ctx context.Context, opID, op, ns, rel string, cancelable bool) (context.Context, *helmTracker, error) {
	if opID == "" {
		return ctx, nil, nil
	}
	if !helmOpIDRe.MatchString(opID) {
		return ctx, nil, errors.New("invalid operation ID")
	}
	t := &helmTracker{m: m, opID: opID, op: op, ns: ns, rel: rel, started: time.Now(), stop: make(chan struct{})}
	if conn, err := m.GetActiveConnection(); err == nil {
		t.cluster = conn.Name
	}
	t.sc, _ = m.helmScope() // без области операция выполняется, но в журнал не попадает
	if cancelable {
		ctx, t.cancel = context.WithCancel(ctx)
	}

	activeHelmOps.mu.Lock()
	if _, busy := activeHelmOps.ops[opID]; busy {
		activeHelmOps.mu.Unlock()
		if t.cancel != nil {
			t.cancel()
		}
		return ctx, nil, errors.New("an operation with this ID is already running")
	}
	activeHelmOps.ops[opID] = t
	activeHelmOps.mu.Unlock()

	go t.loop()
	return ctx, t, nil
}

// CancelHelmOperation прерывает операцию, поддерживающую отмену (установка и апгрейд).
func (m *Manager) CancelHelmOperation(opID string) error {
	activeHelmOps.mu.Lock()
	t := activeHelmOps.ops[opID]
	activeHelmOps.mu.Unlock()
	// Чужую операцию (другой сессии) отменить нельзя; для неё она «не найдена», как и несуществующая.
	if t == nil || t.m != m {
		return errors.New("operation not found or already finished")
	}
	if t.cancel == nil {
		return errors.New("this operation cannot be canceled")
	}
	t.setPhase("Canceling…")
	t.cancel()
	return nil
}

func (t *helmTracker) snapshot(done bool, errMsg string) HelmProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.pending)
	if n > helmProgressBatch {
		n = helmProgressBatch
	}
	lines := append([]string(nil), t.pending[:n]...)
	t.pending = t.pending[n:]
	return HelmProgress{
		OpID: t.opID, Op: t.op, Namespace: t.ns, Release: t.rel, Phase: t.phase, Lines: lines,
		ElapsedMs: time.Since(t.started).Milliseconds(), Cancelable: t.cancel != nil, Done: done, Error: errMsg,
	}
}

func (t *helmTracker) send(p HelmProgress) {
	if t.m != nil {
		t.m.emit(EventHelmProgress, p)
	}
}

// loop периодически сбрасывает накопленные строки и шлёт heartbeat.
func (t *helmTracker) loop() {
	tick := time.NewTicker(helmProgressTick)
	defer tick.Stop()
	lastBeat := time.Now()
	for {
		select {
		case <-t.stop:
			return
		case <-tick.C:
			t.mu.Lock()
			has := len(t.pending) > 0
			t.mu.Unlock()
			if has || time.Since(lastBeat) >= helmProgressBeat {
				t.send(t.snapshot(false, ""))
				lastBeat = time.Now()
			}
		}
	}
}

func (t *helmTracker) setPhase(phase string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.phase = phase
	t.mu.Unlock()
	t.send(t.snapshot(false, ""))
}

// finish завершает операцию: итоговое событие и снятие с учёта. Вызывать ровно один раз (defer).
func (t *helmTracker) finish(err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return
	}
	t.done = true
	t.mu.Unlock()
	close(t.stop)

	activeHelmOps.mu.Lock()
	delete(activeHelmOps.ops, t.opID)
	activeHelmOps.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}

	rec := t.record(err)
	t.journal(rec) // раньше итогового события: страница журнала по нему перечитывает список
	msg := rec.Error
	for {
		p := t.snapshot(false, "")
		if len(p.Lines) == 0 {
			break
		}
		t.send(p)
	}
	t.send(t.snapshot(true, msg))
}

// addLine кладёт строку журнала в очередь (с обрезкой и защитой от разрастания).
func (t *helmTracker) addLine(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if len([]rune(s)) > helmProgressLineMx {
		s = string([]rune(s)[:helmProgressLineMx]) + "…"
	}
	t.mu.Lock()
	if len(t.pending) < 1000 {
		t.pending = append(t.pending, s)
	}
	t.log = append(t.log, s)
	if len(t.log) > helmJournalLogLines {
		t.log = t.log[len(t.log)-helmJournalLogLines:]
	}
	t.mu.Unlock()
}

// note задаёт краткое описание операции для журнала («к ревизии 3», «podinfo 6.5.0»).
func (t *helmTracker) note(detail string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.detail = detail
	t.mu.Unlock()
}

// setRevision запоминает ревизию, с которой связана операция (созданная или целевая).
func (t *helmTracker) setRevision(n int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.revision = n
	t.mu.Unlock()
}

// record собирает запись журнала по итогу операции.
func (t *helmTracker) record(err error) HelmOperationRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	status, msg := "success", ""
	if err != nil {
		status, msg = "failed", err.Error()
		if errors.Is(err, context.Canceled) {
			status, msg = "canceled", "operation canceled"
		}
	}
	now := time.Now()
	return HelmOperationRecord{
		ID: t.opID, Op: t.op, Cluster: t.cluster, Namespace: t.ns, Release: t.rel, Detail: t.detail, Revision: t.revision,
		Status: status, Error: msg,
		StartedAt: t.started.UTC().Format(time.RFC3339), FinishedAt: now.UTC().Format(time.RFC3339),
		DurationMs: now.Sub(t.started).Milliseconds(), Log: append([]string(nil), t.log...),
	}
}

// handler возвращает slog.Handler для action.Configuration: журнал Helm превращается в строки прогресса.
// nil-трекер даёт nil — тогда Helm пользуется логгером по умолчанию.
func (t *helmTracker) handler() slog.Handler {
	if t == nil {
		return nil
	}
	return &helmProgressHandler{t: t}
}

type helmProgressHandler struct {
	t     *helmTracker
	attrs []slog.Attr
}

func (h *helmProgressHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *helmProgressHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		b.WriteString(" ")
		b.WriteString(a.Key)
		b.WriteString("=")
		fmt.Fprint(&b, a.Value.Resolve().Any())
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	h.t.addLine(b.String())
	return nil
}

func (h *helmProgressHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &helmProgressHandler{t: h.t, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *helmProgressHandler) WithGroup(string) slog.Handler { return h }

// journal дописывает запись в журнал команды. Сбой записи не должен ломать саму операцию: он молча игнорируется.
func (t *helmTracker) journal(rec HelmOperationRecord) {
	if t.sc == nil {
		return
	}
	_ = t.sc.store.AddOperation(t.sc.team, t.sc.clusterID, rec)
}
