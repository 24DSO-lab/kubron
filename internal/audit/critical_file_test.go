// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var tBase = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

func mkIntent(org, id string) CriticalIntent {
	return CriticalIntent{
		EventID: "ev-" + id, MutationID: "mut-" + id, RequestDigest: "sha256:" + strings.Repeat("a", 64),
		ActorKind: "user", ActorID: "u-1", PrincipalKind: "technical", PrincipalID: "p-1",
		OrgID: org, TeamID: "t-1", ClusterID: "c-1", Action: "identity.create",
		ResourceType: "principal", ResourceID: "res-" + id, ExpectedRevision: 3, OccurredAt: tBase,
	}
}

func mkOutcome(id string, r CriticalResult) CriticalOutcome {
	o := CriticalOutcome{EventID: "ev-" + id, Result: r, ResourceRevision: 4, CompletedAt: tBase.Add(time.Second)}
	if r != CriticalSucceeded {
		o.ReasonCode = "cas_conflict"
	}
	return o
}

func mustOpen(t *testing.T, dir string) *FileCriticalRecorder {
	t.Helper()
	r, err := OpenCriticalFile(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func journalPath(dir string) string { return filepath.Join(dir, criticalJournalName) }

func readJournal(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(journalPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeJournal(t *testing.T, dir string, b []byte) {
	t.Helper()
	if err := os.WriteFile(journalPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// frameStarts — смещения начала кадров (после магии).
func frameStarts(t *testing.T, data []byte) []int {
	t.Helper()
	var out []int
	off := len(criticalMagic)
	for off < len(data) {
		out = append(out, off)
		off += criticalFrameHeader + int(binary.BigEndian.Uint32(data[off:off+4]))
	}
	if off != len(data) {
		t.Fatalf("journal is not frame-aligned: %d vs %d", off, len(data))
	}
	return out
}

// populate: 3 intent (a, a, b), outcome для первого.
func populate(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	r, err := OpenCriticalFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []CriticalIntent{mkIntent("org-a", "1"), mkIntent("org-a", "2"), mkIntent("org-b", "3")} {
		if err := r.AppendIntent(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AppendOutcome(ctx, mkOutcome("1", CriticalSucceeded)); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

// --- внедрение отказов ---

type faultFS struct {
	osCriticalFS
	openErr       error
	syncDirErr    error
	syncDirCalls  int
	writeErr      error // ошибка Write после partial байт
	writePartial  int   // сколько байт реально записать перед отказом/коротким write; -1 — не вмешиваться
	writeShortNil bool  // вернуть короткий n без ошибки
	syncErr       error
	truncErr      error
	armed         bool // отказы действуют только после arm()
}

func newFault() *faultFS { return &faultFS{writePartial: -1} }

func (f *faultFS) arm() { f.armed = true }

func (f *faultFS) SyncDir(d string) error {
	f.syncDirCalls++
	if f.syncDirErr != nil {
		return f.syncDirErr
	}
	return f.osCriticalFS.SyncDir(d)
}

func (f *faultFS) OpenFile(p string, flag int, perm os.FileMode) (criticalFile, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	cf, err := f.osCriticalFS.OpenFile(p, flag, perm)
	if err != nil {
		return nil, err
	}
	return &faultFile{criticalFile: cf, fs: f}, nil
}

type faultFile struct {
	criticalFile
	fs *faultFS
}

func (w *faultFile) Write(p []byte) (int, error) {
	if !w.fs.armed || w.fs.writePartial < 0 {
		return w.criticalFile.Write(p)
	}
	k := w.fs.writePartial
	if k > len(p) {
		k = len(p)
	}
	n, err := w.criticalFile.Write(p[:k])
	if err != nil {
		return n, err
	}
	if w.fs.writeShortNil {
		return n, nil
	}
	return n, w.fs.writeErr
}

func (w *faultFile) Sync() error {
	if w.fs.armed && w.fs.syncErr != nil {
		return w.fs.syncErr
	}
	return w.criticalFile.Sync()
}

func (w *faultFile) Truncate(n int64) error {
	if w.fs.armed && w.fs.truncErr != nil {
		return w.fs.truncErr
	}
	return w.criticalFile.Truncate(n)
}

func openFault(t *testing.T, dir string, f *faultFS) *FileCriticalRecorder {
	t.Helper()
	r, err := openCritical(f, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// --- защищённый use case (протокол §7.2) ---

type protectedUseCase struct {
	rec       CriticalRecorder
	mutations int
}

type protectedRequest struct {
	Secret string // никогда не попадает в аудит; в журнале только digest
	Target string
}

func (u *protectedUseCase) Run(ctx context.Context, eventID string, req protectedRequest, mutate func() error) error {
	sum := sha256.Sum256([]byte(req.Target + "\x00" + req.Secret))
	in := mkIntent("org-a", strings.TrimPrefix(eventID, "ev-"))
	in.RequestDigest = "sha256:" + hex.EncodeToString(sum[:])
	if err := u.rec.AppendIntent(ctx, in); err != nil {
		return fmt.Errorf("audit intent: %w", err) // мутации не будет
	}
	u.mutations++
	mErr := mutate()
	out := mkOutcome(strings.TrimPrefix(eventID, "ev-"), CriticalSucceeded)
	if mErr != nil {
		out = mkOutcome(strings.TrimPrefix(eventID, "ev-"), CriticalFailed)
	}
	if err := u.rec.AppendOutcome(ctx, out); err != nil {
		return fmt.Errorf("%w: %w", ErrOutcomeUnknown, err)
	}
	return mErr
}

func TestProtectedUseCase_IntentFailureBlocksMutation(t *testing.T) {
	for name, setup := range map[string]func(f *faultFS){
		"short write": func(f *faultFS) { f.writePartial, f.writeShortNil = 10, true },
		"disk full":   func(f *faultFS) { f.writePartial, f.writeErr = 10, syscall.ENOSPC },
		"sync":        func(f *faultFS) { f.syncErr = errors.New("EIO") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFault()
			r := openFault(t, t.TempDir(), f)
			setup(f)
			f.arm()
			uc := &protectedUseCase{rec: r}
			called := false
			err := uc.Run(context.Background(), "ev-1", protectedRequest{Secret: "s", Target: "x"}, func() error { called = true; return nil })
			if err == nil || !errors.Is(err, ErrCriticalDurability) {
				t.Fatalf("want durability error, got %v", err)
			}
			if called || uc.mutations != 0 {
				t.Fatal("domain mutation must not run when AppendIntent fails")
			}
		})
	}
	t.Run("closed", func(t *testing.T) {
		r := mustOpen(t, t.TempDir())
		r.Close()
		called := false
		err := (&protectedUseCase{rec: r}).Run(context.Background(), "ev-1", protectedRequest{}, func() error { called = true; return nil })
		if !errors.Is(err, ErrCriticalClosed) || called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	})
}

func TestProtectedUseCase_OutcomeFailureIsUnknownAndNotReplayed(t *testing.T) {
	f := newFault()
	dir := t.TempDir()
	r := openFault(t, dir, f)
	uc := &protectedUseCase{rec: r}
	calls := 0
	// Сбой наступает только на outcome: intent пишется нормально, затем включаем отказ внутри мутации.
	err := uc.Run(context.Background(), "ev-1", protectedRequest{Secret: "s3cr3t", Target: "x"}, func() error {
		calls++
		f.syncErr = errors.New("EIO")
		f.arm()
		return nil
	})
	if !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, ErrCriticalDurability) {
		t.Fatalf("want ErrOutcomeUnknown wrapping durability error, got %v", err)
	}
	if calls != 1 || uc.mutations != 1 {
		t.Fatalf("mutation must run exactly once, calls=%d", calls)
	}
	ctx := context.Background()
	// Неизвестный исход не равен успеху: пока recorder закрыт для работы, GetOutcome тоже отказывает.
	if _, err := r.GetOutcome(ctx, "org-a", "ev-1"); err == nil {
		t.Fatal("outcome must not be readable as success after failed AppendOutcome")
	}
	// Повторный Run того же события не выполняет мутацию вслепую.
	err = uc.Run(ctx, "ev-1", protectedRequest{Secret: "s3cr3t", Target: "x"}, func() error { calls++; return nil })
	if err == nil || calls != 1 {
		t.Fatalf("no blind replay: err=%v calls=%d", err, calls)
	}
	// После перезапуска исход подтверждает только реально сохранённый outcome (или его отсутствие = deny).
	r.Close()
	r2 := mustOpen(t, dir)
	if out, err := r2.GetOutcome(ctx, "org-a", "ev-1"); err == nil && out.Result != CriticalSucceeded {
		t.Fatalf("unexpected outcome %+v", out)
	}
	if bytes.Contains(readJournal(t, dir), []byte("s3cr3t")) {
		t.Fatal("secret leaked into journal")
	}
}

func TestCritical_SecretsAbsentFromJournalMirrorAndErrors(t *testing.T) {
	const secret = "tok-SuperSecret-123"
	var mirror bytes.Buffer
	SetMirror(&mirror)
	t.Cleanup(func() { SetMirror(nil) })

	dir := t.TempDir()
	r := mustOpen(t, dir)
	uc := &protectedUseCase{rec: r}
	if err := uc.Run(context.Background(), "ev-1", protectedRequest{Secret: secret, Target: "kubeconfig"}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	bad := mkIntent("org-a", "2")
	bad.ResourceID = secret + " space"
	err := r.AppendIntent(context.Background(), bad)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error: %v", err)
	}
	if bytes.Contains(readJournal(t, dir), []byte(secret)) {
		t.Fatal("secret in journal")
	}
	if mirror.Len() != 0 {
		t.Fatalf("critical audit must not write to the legacy stdout mirror: %q", mirror.String())
	}
}

// --- перезапуск и порядок ---

func TestCritical_RestartKeepsPendingAndOutcomes(t *testing.T) {
	dir := t.TempDir()
	populate(t, dir)
	r := mustOpen(t, dir)
	ctx := context.Background()

	pa, err := r.ListPending(ctx, "org-a", 10)
	if err != nil || len(pa) != 1 || pa[0].EventID != "ev-2" {
		t.Fatalf("org-a pending after restart: %+v %v", pa, err)
	}
	if got := pa[0]; !got.OccurredAt.Equal(tBase) || got.ExpectedRevision != 3 || got.PrincipalID != "p-1" {
		t.Fatalf("intent fields lost: %+v", got)
	}
	if pb, _ := r.ListPending(ctx, "org-b", 10); len(pb) != 1 || pb[0].EventID != "ev-3" {
		t.Fatalf("org-b pending: %+v", pb)
	}
	out, err := r.GetOutcome(ctx, "org-a", "ev-1")
	if err != nil || out.Result != CriticalSucceeded || out.ResourceRevision != 4 || !out.CompletedAt.Equal(tBase.Add(time.Second)) {
		t.Fatalf("outcome after restart: %+v %v", out, err)
	}
	// Идемпотентность и конфликты действуют и после перезапуска, без роста файла.
	size := len(readJournal(t, dir))
	if err := r.AppendIntent(ctx, mkIntent("org-a", "2")); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendOutcome(ctx, mkOutcome("1", CriticalSucceeded)); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendOutcome(ctx, mkOutcome("1", CriticalFailed)); !errors.Is(err, ErrCriticalConflict) {
		t.Fatal(err)
	}
	changed := mkIntent("org-a", "2")
	changed.ActorID = "u-2"
	if err := r.AppendIntent(ctx, changed); !errors.Is(err, ErrCriticalConflict) {
		t.Fatal(err)
	}
	if len(readJournal(t, dir)) != size {
		t.Fatal("idempotent repeats must not append")
	}
	// Дальнейшая запись продолжает seq.
	if err := r.AppendOutcome(ctx, mkOutcome("2", CriticalFailed)); err != nil {
		t.Fatal(err)
	}
	r.Close()
	r2 := mustOpen(t, dir)
	if pa, _ := r2.ListPending(ctx, "org-a", 10); len(pa) != 0 {
		t.Fatalf("pending: %+v", pa)
	}
}

func TestCritical_ConcurrentAppendOrderSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	r := mustOpen(t, dir)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := r.AppendIntent(ctx, mkIntent("org-a", fmt.Sprint(i))); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	before, _ := r.ListPending(ctx, "org-a", 100)
	if len(before) != 50 {
		t.Fatalf("got %d", len(before))
	}
	r.Close()
	r2 := mustOpen(t, dir) // загрузка проверяет непрерывность seq, CRC и рамки каждого кадра
	after, _ := r2.ListPending(ctx, "org-a", 100)
	for i := range before {
		if before[i].EventID != after[i].EventID {
			t.Fatalf("order changed at %d: %s vs %s", i, before[i].EventID, after[i].EventID)
		}
	}
	if n := len(frameStarts(t, readJournal(t, dir))); n != 50 {
		t.Fatalf("frames=%d", n)
	}
}

// --- повреждения и восстановление ---

func assertRecoveryRequired(t *testing.T, dir string, wantRepairable bool) *FileCriticalRecorder {
	t.Helper()
	before := readJournal(t, dir)
	r, err := OpenCriticalFile(dir)
	if !errors.Is(err, ErrCriticalRecoveryRequired) || r == nil {
		t.Fatalf("want recorder + ErrCriticalRecoveryRequired, got %v %v", r, err)
	}
	t.Cleanup(func() { r.Close() })
	rec := r.Recovery()
	if !rec.Required || rec.Repairable != wantRepairable {
		t.Fatalf("recovery=%+v want repairable=%v", rec, wantRepairable)
	}
	ctx := context.Background()
	// Fail closed: ни запись, ни чтение не работают, даже для данных до повреждения.
	if err := r.AppendIntent(ctx, mkIntent("org-a", "new")); !errors.Is(err, ErrCriticalRecoveryRequired) {
		t.Fatalf("append must fail closed: %v", err)
	}
	if err := r.AppendOutcome(ctx, mkOutcome("1", CriticalSucceeded)); !errors.Is(err, ErrCriticalRecoveryRequired) {
		t.Fatalf("outcome must fail closed: %v", err)
	}
	if _, err := r.GetOutcome(ctx, "org-a", "ev-1"); !errors.Is(err, ErrCriticalRecoveryRequired) {
		t.Fatalf("get must fail closed: %v", err)
	}
	if _, err := r.ListPending(ctx, "org-a", 5); !errors.Is(err, ErrCriticalRecoveryRequired) {
		t.Fatalf("list must fail closed: %v", err)
	}
	if !bytes.Equal(before, readJournal(t, dir)) {
		t.Fatal("startup must never modify a damaged journal")
	}
	return r
}

func TestCritical_TruncatedTailAtEveryOffsetFailsClosedAndIsRepairable(t *testing.T) {
	src := t.TempDir()
	populate(t, src)
	full := readJournal(t, src)
	starts := frameStarts(t, full)
	last := starts[len(starts)-1]
	prefix := full[:last]

	for cut := last + 1; cut < len(full); cut++ { // рваный последний кадр на каждом смещении
		dir := t.TempDir()
		writeJournal(t, dir, full[:cut])
		r := assertRecoveryRequired(t, dir, true)
		if got := r.Recovery().Offset; got != int64(last) {
			t.Fatalf("cut=%d offset=%d want %d", cut, got, last)
		}
		q, err := r.QuarantineCorruptTail()
		if err != nil {
			t.Fatalf("cut=%d quarantine: %v", cut, err)
		}
		saved, _ := os.ReadFile(q)
		if !bytes.Equal(saved, full[last:cut]) {
			t.Fatalf("cut=%d: quarantine must preserve the exact tail bytes", cut)
		}
		if !bytes.Equal(readJournal(t, dir), prefix) {
			t.Fatalf("cut=%d: journal must be truncated to the last whole frame", cut)
		}
		ctx := context.Background()
		// Данные до хвоста сохранены (последний кадр — outcome ev-1, он отброшен: ev-1 снова pending).
		if p, err := r.ListPending(ctx, "org-a", 10); err != nil || len(p) != 2 || p[0].EventID != "ev-1" {
			t.Fatalf("cut=%d: pending %+v %v", cut, p, err)
		}
		if p, _ := r.ListPending(ctx, "org-b", 10); len(p) != 1 {
			t.Fatalf("cut=%d: org-b %+v", cut, p)
		}
		if err := r.AppendIntent(ctx, mkIntent("org-a", "after")); err != nil {
			t.Fatalf("cut=%d: append after recovery: %v", cut, err)
		}
		if cut > last+40 {
			cut += 37 // дальше шагаем реже: достаточно покрыть заголовок, начало и середину payload
		}
	}
}

func TestCritical_CorruptTailVariants(t *testing.T) {
	src := t.TempDir()
	populate(t, src)
	full := readJournal(t, src)
	starts := frameStarts(t, full)
	last := starts[len(starts)-1]
	ctx := context.Background()

	t.Run("bit flip in last record is a repairable tail", func(t *testing.T) {
		d := t.TempDir()
		b := append([]byte(nil), full...)
		b[len(b)-3] ^= 0xFF
		writeJournal(t, d, b)
		r := assertRecoveryRequired(t, d, true)
		if _, err := r.QuarantineCorruptTail(); err != nil {
			t.Fatal(err)
		}
		if err := r.AppendIntent(ctx, mkIntent("org-a", "n")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("zero filled tail", func(t *testing.T) {
		d := t.TempDir()
		writeJournal(t, d, append(append([]byte(nil), full...), make([]byte, 4096)...))
		r := assertRecoveryRequired(t, d, true)
		if got := r.Recovery().Offset; got != int64(len(full)) {
			t.Fatalf("offset %d", got)
		}
		if _, err := r.QuarantineCorruptTail(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("torn header is repairable and reinitializes", func(t *testing.T) {
		d := t.TempDir()
		writeJournal(t, d, criticalMagic[:3])
		r := assertRecoveryRequired(t, d, true)
		if _, err := r.QuarantineCorruptTail(); err != nil {
			t.Fatal(err)
		}
		if err := r.AppendIntent(ctx, mkIntent("org-a", "n")); err != nil {
			t.Fatal(err)
		}
		r.Close()
		if p, _ := mustOpen(t, d).ListPending(ctx, "org-a", 5); len(p) != 1 {
			t.Fatal("reinitialized journal must persist")
		}
	})
	t.Run("corruption in the middle is never auto-repairable", func(t *testing.T) {
		d := t.TempDir()
		b := append([]byte(nil), full...)
		b[starts[1]+criticalFrameHeader+5] ^= 0xFF // второй кадр, дальше есть данные
		writeJournal(t, d, b)
		r := assertRecoveryRequired(t, d, false)
		if _, err := r.QuarantineCorruptTail(); !errors.Is(err, ErrCriticalInvalid) {
			t.Fatalf("quarantine must refuse: %v", err)
		}
		if !bytes.Equal(readJournal(t, d), b) {
			t.Fatal("journal must stay untouched")
		}
	})
	t.Run("foreign file", func(t *testing.T) {
		d := t.TempDir()
		writeJournal(t, d, []byte("this is not a journal at all"))
		assertRecoveryRequired(t, d, false)
	})
	t.Run("missing record (sequence gap)", func(t *testing.T) {
		d := t.TempDir()
		b := append(append([]byte(nil), full[:starts[1]]...), full[starts[2]:]...)
		writeJournal(t, d, b)
		assertRecoveryRequired(t, d, false)
	})
	t.Run("absurd record length", func(t *testing.T) {
		d := t.TempDir()
		b := append([]byte(nil), full[:last]...)
		hdr := make([]byte, criticalFrameHeader)
		binary.BigEndian.PutUint32(hdr, 0xFFFFFFFF)
		b = append(append(b, hdr...), []byte("junk")...)
		writeJournal(t, d, b)
		assertRecoveryRequired(t, d, false)
	})
	t.Run("outcome without intent in a checksum-valid frame", func(t *testing.T) {
		d := t.TempDir()
		r := mustOpen(t, d)
		if err := r.AppendIntent(ctx, mkIntent("org-a", "1")); err != nil {
			t.Fatal(err)
		}
		r.Close()
		b := readJournal(t, d)
		// Подмена EventID в outcome-кадре невозможна без пересчёта CRC; строим кадр вручную.
		r2 := mustOpen(t, t.TempDir())
		r2.mu.Lock()
		r2.seq = 1
		err := r2.appendLocked(journalRecord{Type: recTypeOutcome, Outcome: outcomeToWire(mkOutcome("ghost", CriticalSucceeded))})
		r2.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		frame := readJournal(t, filepathOf(r2))[len(criticalMagic):]
		writeJournal(t, d, append(b, frame...))
		assertRecoveryRequired(t, d, false)
	})
}

func filepathOf(r *FileCriticalRecorder) string { return filepath.Dir(r.path) }

func TestCritical_QuarantineRefusesWithoutRepairableTail(t *testing.T) {
	r := mustOpen(t, t.TempDir())
	if _, err := r.QuarantineCorruptTail(); !errors.Is(err, ErrCriticalInvalid) {
		t.Fatalf("healthy journal: %v", err)
	}
}

// --- отказы записи ---

func TestCritical_WriteFailuresFailClosedAndRollBack(t *testing.T) {
	ctx := context.Background()
	for name, setup := range map[string]func(f *faultFS){
		"short write without error": func(f *faultFS) { f.writePartial, f.writeShortNil = 11, true },
		"disk full mid-record":      func(f *faultFS) { f.writePartial, f.writeErr = 11, syscall.ENOSPC },
		"disk full before data":     func(f *faultFS) { f.writePartial, f.writeErr = 0, syscall.ENOSPC },
		"EIO":                       func(f *faultFS) { f.writePartial, f.writeErr = 5, syscall.EIO },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			f := newFault()
			r := openFault(t, dir, f)
			if err := r.AppendIntent(ctx, mkIntent("org-a", "1")); err != nil {
				t.Fatal(err)
			}
			good := readJournal(t, dir)

			setup(f)
			f.arm()
			err := r.AppendIntent(ctx, mkIntent("org-a", "2"))
			if !errors.Is(err, ErrCriticalDurability) {
				t.Fatalf("want ErrCriticalDurability, got %v", err)
			}
			if !bytes.Equal(readJournal(t, dir), good) {
				t.Fatal("partial record must be rolled back")
			}
			// Неподтверждённая запись не видна.
			if p, _ := r.ListPending(ctx, "org-a", 10); len(p) != 1 {
				t.Fatalf("failed intent must not be visible: %+v", p)
			}
			// Отказ снят — recorder продолжает работать, журнал остаётся валидным.
			f.writePartial = -1
			if err := r.AppendIntent(ctx, mkIntent("org-a", "2")); err != nil {
				t.Fatalf("append after rollback: %v", err)
			}
			r.Close()
			if p, _ := mustOpen(t, dir).ListPending(ctx, "org-a", 10); len(p) != 2 {
				t.Fatalf("restart: %+v", p)
			}
		})
	}
}

func TestCritical_FailedRollbackPoisonsUntilReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := newFault()
	r := openFault(t, dir, f)
	if err := r.AppendIntent(ctx, mkIntent("org-a", "1")); err != nil {
		t.Fatal(err)
	}
	f.writePartial, f.writeErr, f.truncErr = 9, syscall.ENOSPC, errors.New("EIO")
	f.arm()
	if err := r.AppendIntent(ctx, mkIntent("org-a", "2")); !errors.Is(err, ErrCriticalDurability) {
		t.Fatal(err)
	}
	f.writePartial = -1
	if err := r.AppendIntent(ctx, mkIntent("org-a", "3")); !errors.Is(err, ErrCriticalRecoveryRequired) {
		t.Fatalf("poisoned recorder must fail closed: %v", err)
	}
	if _, err := r.GetOutcome(ctx, "org-a", "ev-1"); !errors.Is(err, ErrCriticalRecoveryRequired) {
		t.Fatal(err)
	}
	if !r.Recovery().Required {
		t.Fatal("recovery state must be exposed")
	}
	r.Close()
	// Рваный хвост виден при открытии и разрешается только явно.
	r2 := assertRecoveryRequired(t, dir, true)
	if _, err := r2.QuarantineCorruptTail(); err != nil {
		t.Fatal(err)
	}
	if p, _ := r2.ListPending(ctx, "org-a", 10); len(p) != 1 || p[0].EventID != "ev-1" {
		t.Fatalf("%+v", p)
	}
}

func TestCritical_SyncFailurePoisonsAndRestartIsConsistent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := newFault()
	r := openFault(t, dir, f)
	if err := r.AppendIntent(ctx, mkIntent("org-a", "1")); err != nil {
		t.Fatal(err)
	}
	f.syncErr = errors.New("EIO")
	f.arm()
	if err := r.AppendOutcome(ctx, mkOutcome("1", CriticalSucceeded)); !errors.Is(err, ErrCriticalDurability) {
		t.Fatal(err)
	}
	if _, err := r.GetOutcome(ctx, "org-a", "ev-1"); !errors.Is(err, ErrCriticalRecoveryRequired) {
		t.Fatalf("outcome must not be trusted after a failed sync: %v", err)
	}
	if err := r.AppendIntent(ctx, mkIntent("org-a", "2")); !errors.Is(err, ErrCriticalRecoveryRequired) {
		t.Fatalf("no writes after failed sync: %v", err)
	}
	r.Close()
	// Журнал на диске валиден; outcome — либо отсутствует, либо целиком записан: промежуточного нет.
	r2 := mustOpen(t, dir)
	if out, err := r2.GetOutcome(ctx, "org-a", "ev-1"); err == nil && out.Result != CriticalSucceeded {
		t.Fatalf("%+v", out)
	}
}

func TestCritical_SyncFailureOnIntentDoesNotConfirm(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := newFault()
	r := openFault(t, dir, f)
	f.syncErr = errors.New("EIO")
	f.arm()
	if err := r.AppendIntent(ctx, mkIntent("org-a", "1")); !errors.Is(err, ErrCriticalDurability) {
		t.Fatal(err)
	}
	r.Close()
	// Запись могла дойти до файла: это безопасно — мутации не было, intent виден как pending.
	r2 := mustOpen(t, dir)
	if p, _ := r2.ListPending(ctx, "org-a", 10); len(p) > 1 {
		t.Fatalf("%+v", p)
	}
}

func TestCritical_OpenFailures(t *testing.T) {
	t.Run("permission denied on open", func(t *testing.T) {
		f := newFault()
		f.openErr = os.ErrPermission
		if r, err := openCritical(f, t.TempDir()); err == nil || r != nil || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("%v %v", r, err)
		}
	})
	t.Run("directory sync failure on creation", func(t *testing.T) {
		f := newFault()
		f.syncDirErr = errors.New("EIO")
		if r, err := openCritical(f, t.TempDir()); !errors.Is(err, ErrCriticalDurability) || r != nil {
			t.Fatalf("%v %v", r, err)
		}
	})
	t.Run("directory is synced when the journal is created", func(t *testing.T) {
		f := newFault()
		openFault(t, t.TempDir(), f)
		if f.syncDirCalls == 0 {
			t.Fatal("directory Sync was not called")
		}
	})
	t.Run("real read-only directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		d := t.TempDir()
		if err := os.Chmod(d, 0o500); err != nil {
			t.Skip(err)
		}
		t.Cleanup(func() { os.Chmod(d, 0o700) })
		if _, err := OpenCriticalFile(filepath.Join(d, "sub")); err == nil {
			t.Fatal("must fail")
		}
	})
	t.Run("journal file mode", func(t *testing.T) {
		d := t.TempDir()
		mustOpen(t, d)
		st, err := os.Stat(journalPath(d))
		if err != nil || st.Mode().Perm()&0o077 != 0 {
			t.Fatalf("journal must be private: %v %v", st, err)
		}
	})
}

func TestCritical_JournalFullFailsClosed(t *testing.T) {
	r := mustOpen(t, t.TempDir())
	r.mu.Lock()
	r.size = criticalMaxJournal - 1
	r.mu.Unlock()
	if err := r.AppendIntent(context.Background(), mkIntent("org-a", "1")); !errors.Is(err, ErrCriticalJournalFull) {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.size = int64(len(criticalMagic))
	r.mu.Unlock()
	if p, _ := r.ListPending(context.Background(), "org-a", 5); len(p) != 0 {
		t.Fatal("rejected record must not be indexed")
	}
}

func TestCritical_Close(t *testing.T) {
	r := mustOpen(t, t.TempDir())
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal("second Close must be harmless")
	}
	ctx := context.Background()
	if err := r.AppendIntent(ctx, mkIntent("org-a", "1")); !errors.Is(err, ErrCriticalClosed) {
		t.Fatal(err)
	}
	if _, err := r.ListPending(ctx, "org-a", 1); !errors.Is(err, ErrCriticalClosed) {
		t.Fatal(err)
	}
}

// --- legacy ---

func TestCritical_LegacyAuditLogIsIndependent(t *testing.T) {
	dir := t.TempDir()
	legacy := Open(dir)
	r := mustOpen(t, dir)

	legacy.Record(Event{User: "alice", Action: "Login", Result: ResultOK})
	if err := r.AppendIntent(context.Background(), mkIntent("org-a", "1")); err != nil {
		t.Fatal(err)
	}
	legacy.Record(Event{User: "bob", Action: "DeletePod", Result: ResultOK})

	page, err := legacy.Query(Filter{})
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("legacy query: %+v %v", page, err)
	}
	logBytes, _ := os.ReadFile(filepath.Join(dir, fileName))
	if bytes.Contains(logBytes, []byte("ev-1")) || bytes.Contains(logBytes, []byte("critical")) {
		t.Fatal("critical records must not land in the legacy audit.log")
	}
	if bytes.Contains(readJournal(t, dir), []byte("alice")) {
		t.Fatal("legacy events must not land in the critical journal")
	}
	// Legacy остаётся best-effort: Record не паникует и не возвращает ошибку, даже если каталог недоступен.
	Open(filepath.Join(dir, criticalJournalName, "nope")).Record(Event{Action: "X", Result: ResultOK})
}
