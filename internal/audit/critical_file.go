// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package audit

// Файловый backend критичного аудита (Desktop).
//
// Durability-заметка
//
//   - Отдельный append-only файл critical-audit.journal; не связан с ротируемым legacy audit.log
//     и не удаляется/не ротируется (retention и архивация — отдельная операция, pending intent
//     она удалять не вправе). Предел размера — criticalMaxJournal: после него записи отклоняются
//     (ErrCriticalJournalFull), а не затираются.
//   - Формат: заголовок-магия, далее кадры [len u32 BE][crc32c u32 BE][JSON]. Каждая запись содержит
//     монотонный seq без пропусков. Запись в файл — один Write на кадр под мьютексом (сериализация
//     в одном процессе), затем проверка n == len(frame) и file Sync. Новый файл: Sync файла и Sync
//     каталога. Backend рассчитан на один процесс (внешней блокировки файла нет).
//   - Append* возвращает nil только после успешного Sync; в индекс запись попадает после этого.
//   - Сбой Write/short write (в т.ч. диск полон): неполный кадр откатывается Truncate+Sync; если откат
//     удался, recorder остаётся рабочим, вызывающий получил ErrCriticalDurability. Сбой отката или
//     Sync: состояние файла неизвестно → recorder переходит в recovery-required (fail closed), все
//     операции возвращают ErrCriticalRecoveryRequired, пока журнал не будет закрыт и открыт заново
//     (повторная валидация с диска). Запись, чей Sync не подтверждён, могла остаться в файле: после
//     перезапуска она будет видна как pending intent — это безопасно (мутация не выполнялась).
//   - Старт валидирует рамки/CRC/seq/семантику. Неполный или повреждённый хвост НЕ отбрасывается и
//     не трактуется как успех: OpenCriticalFile возвращает ошибку ErrCriticalRecoveryRequired, а
//     recorder остаётся закрытым для записи и чтения. Только явный QuarantineCorruptTail для
//     «рваного» хвоста (обрыв записи в конце файла) копирует хвост в отдельный файл и усекает журнал.
//     Повреждение в середине файла автоматически не исправляется.
//   - Известный неопределённый исход (AppendOutcome не удался) остаётся pending/unknown: см. critical.go.
//   - stdout-mirror не используется и не является durable-хранилищем.
//   - Индекс держится в памяти целиком; журнал рассчитан на десктопные объёмы.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	criticalJournalName = "critical-audit.journal"

	criticalMaxRecord  = 16 << 10  // предел JSON-payload одного кадра
	criticalMaxJournal = 256 << 20 // предел размера файла
	criticalMaxTail    = 4 << 20   // наибольший хвост, который разрешено карантинить

	criticalFrameHeader = 8
	recTypeIntent       = "intent"
	recTypeOutcome      = "outcome"
)

var (
	criticalMagic = []byte("KBRNCJ\x00\x01")
	crcTable      = crc32.MakeTable(crc32.Castagnoli)
)

// criticalFile и criticalFS — швы для внедрения отказов в тестах (disk full, short write, Sync…).
type criticalFile interface {
	io.Reader
	io.Writer
	io.Seeker
	Sync() error
	Truncate(size int64) error
	Close() error
	Stat() (os.FileInfo, error)
}

type criticalFS interface {
	MkdirAll(dir string, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	OpenFile(path string, flag int, perm os.FileMode) (criticalFile, error)
	SyncDir(dir string) error
}

type osCriticalFS struct{}

func (osCriticalFS) MkdirAll(d string, p os.FileMode) error { return os.MkdirAll(d, p) }
func (osCriticalFS) Stat(p string) (os.FileInfo, error)     { return os.Stat(p) }
func (osCriticalFS) SyncDir(d string) error                 { return syncCriticalDir(d) }
func (osCriticalFS) OpenFile(p string, flag int, perm os.FileMode) (criticalFile, error) {
	f, err := os.OpenFile(p, flag, perm)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// CriticalRecovery описывает состояние восстановления журнала.
type CriticalRecovery struct {
	Required   bool   // журнал закрыт для работы до восстановления
	Repairable bool   // QuarantineCorruptTail применим (рваный хвост)
	Offset     int64  // смещение первого невалидного байта; -1, если неприменимо
	Reason     string // безопасное описание без значений полей записей
}

type criticalEntry struct {
	in  CriticalIntent
	out *CriticalOutcome
}

// FileCriticalRecorder — CriticalRecorder поверх dedicated append-only файла.
type FileCriticalRecorder struct {
	mu   sync.Mutex
	fs   criticalFS
	dir  string
	path string
	f    criticalFile

	size    int64  // граница последнего подтверждённого кадра
	seq     uint64 // seq последней подтверждённой записи
	entries map[string]*criticalEntry
	order   []*criticalEntry

	closed bool
	rec    CriticalRecovery
}

var _ CriticalRecorder = (*FileCriticalRecorder)(nil)

// OpenCriticalFile открывает (или создаёт) критичный журнал в dir и валидирует его.
// Если журнал неполон или повреждён, возвращается непустой recorder (для Recovery и
// QuarantineCorruptTail) вместе с ошибкой, оборачивающей ErrCriticalRecoveryRequired.
func OpenCriticalFile(dir string) (*FileCriticalRecorder, error) {
	return openCritical(osCriticalFS{}, dir)
}

func openCritical(fsys criticalFS, dir string) (*FileCriticalRecorder, error) {
	_, statErr := fsys.Stat(dir)
	existed := statErr == nil
	if err := fsys.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("audit: critical journal dir: %w", err)
	}
	if !existed {
		if err := fsys.SyncDir(filepath.Dir(dir)); err != nil {
			return nil, fmt.Errorf("audit: critical journal dir sync: %w", err)
		}
	}
	r := &FileCriticalRecorder{fs: fsys, dir: dir, path: filepath.Join(dir, criticalJournalName)}
	f, err := fsys.OpenFile(r.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open critical journal: %w", err)
	}
	r.f = f
	if err := r.loadLocked(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if r.rec.Required {
		return r, r.recoveryErrLocked()
	}
	return r, nil
}

// Close закрывает журнал. Повторный вызов безопасен.
func (r *FileCriticalRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.f.Close()
}

// Recovery возвращает состояние восстановления.
func (r *FileCriticalRecorder) Recovery() CriticalRecovery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rec
}

func (r *FileCriticalRecorder) recoveryErrLocked() error {
	return fmt.Errorf("%w: %s", ErrCriticalRecoveryRequired, r.rec.Reason)
}

func (r *FileCriticalRecorder) readyLocked() error {
	if r.closed {
		return ErrCriticalClosed
	}
	if r.rec.Required {
		return r.recoveryErrLocked()
	}
	return nil
}

// poisonLocked переводит recorder в fail-closed: состояние файла после сбоя неизвестно.
func (r *FileCriticalRecorder) poisonLocked(reason string) {
	r.rec = CriticalRecovery{Required: true, Offset: -1, Reason: reason}
}

// --- запись ---

// AppendIntent реализует CriticalRecorder.
func (r *FileCriticalRecorder) AppendIntent(ctx context.Context, in CriticalIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := in.Validate(); err != nil {
		return err
	}
	in = in.normalized()

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.readyLocked(); err != nil {
		return err
	}
	if e, ok := r.entries[in.EventID]; ok {
		if sameIntent(e.in, in) {
			return nil // уже durable
		}
		return fmt.Errorf("%w: event id already used with different payload", ErrCriticalConflict)
	}
	if err := r.appendLocked(journalRecord{Type: recTypeIntent, Intent: intentToWire(in)}); err != nil {
		return err
	}
	e := &criticalEntry{in: in}
	r.entries[in.EventID] = e
	r.order = append(r.order, e)
	return nil
}

// AppendOutcome реализует CriticalRecorder.
func (r *FileCriticalRecorder) AppendOutcome(ctx context.Context, out CriticalOutcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := out.Validate(); err != nil {
		return err
	}
	out = out.normalized()

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.readyLocked(); err != nil {
		return err
	}
	e, ok := r.entries[out.EventID]
	if !ok {
		return fmt.Errorf("%w: intent for outcome", ErrCriticalNotFound)
	}
	if e.out != nil {
		if sameOutcome(*e.out, out) {
			return nil
		}
		return fmt.Errorf("%w: outcome already recorded with different payload", ErrCriticalConflict)
	}
	if err := r.appendLocked(journalRecord{Type: recTypeOutcome, Outcome: outcomeToWire(out)}); err != nil {
		return err
	}
	e.out = &out
	return nil
}

// appendLocked пишет кадр и подтверждает durability. nil — запись на диске.
func (r *FileCriticalRecorder) appendLocked(rec journalRecord) error {
	rec.Seq = r.seq + 1
	payload, err := json.Marshal(rec)
	if err != nil || len(payload) > criticalMaxRecord {
		return fmt.Errorf("%w: record encoding", ErrCriticalInvalid)
	}
	frame := make([]byte, criticalFrameHeader+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[4:8], crc32.Checksum(payload, crcTable))
	copy(frame[criticalFrameHeader:], payload)
	if r.size+int64(len(frame)) > criticalMaxJournal {
		return ErrCriticalJournalFull
	}

	n, werr := r.f.Write(frame)
	if werr == nil && n != len(frame) {
		werr = io.ErrShortWrite
	}
	if werr != nil {
		if rbErr := r.rollbackLocked(); rbErr != nil {
			r.poisonLocked("journal write failed and rollback failed; tail state is unknown")
			return fmt.Errorf("%w: write: %w", ErrCriticalDurability, errors.Join(werr, rbErr))
		}
		return fmt.Errorf("%w: write: %w", ErrCriticalDurability, werr)
	}
	if serr := r.f.Sync(); serr != nil {
		// После неудачного fsync содержимое страниц неопределённо: повторять нельзя.
		r.poisonLocked("journal sync failed; durability of the tail is unknown")
		return fmt.Errorf("%w: sync: %w", ErrCriticalDurability, serr)
	}
	r.size += int64(len(frame))
	r.seq = rec.Seq
	return nil
}

// rollbackLocked убирает возможный неполный кадр и возвращает позицию записи на границу.
func (r *FileCriticalRecorder) rollbackLocked() error {
	if err := r.f.Truncate(r.size); err != nil {
		return err
	}
	if _, err := r.f.Seek(r.size, io.SeekStart); err != nil {
		return err
	}
	return r.f.Sync()
}

// --- чтение ---

// GetOutcome реализует CriticalRecorder.
func (r *FileCriticalRecorder) GetOutcome(ctx context.Context, orgID, eventID string) (CriticalOutcome, error) {
	if err := ctx.Err(); err != nil {
		return CriticalOutcome{}, err
	}
	if !validToken(orgID, criticalMaxFieldLen) {
		return CriticalOutcome{}, invalidField("OrgID")
	}
	if !validToken(eventID, criticalMaxFieldLen) {
		return CriticalOutcome{}, invalidField("EventID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.readyLocked(); err != nil {
		return CriticalOutcome{}, err
	}
	e, ok := r.entries[eventID]
	if !ok || e.in.OrgID != orgID || e.out == nil {
		return CriticalOutcome{}, ErrCriticalNotFound
	}
	return *e.out, nil
}

// ListPending реализует CriticalRecorder.
func (r *FileCriticalRecorder) ListPending(ctx context.Context, orgID string, limit int) ([]CriticalIntent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validToken(orgID, criticalMaxFieldLen) {
		return nil, invalidField("OrgID")
	}
	switch {
	case limit <= 0:
		limit = CriticalDefaultPending
	case limit > CriticalMaxPending:
		limit = CriticalMaxPending
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.readyLocked(); err != nil {
		return nil, err
	}
	out := make([]CriticalIntent, 0, 8)
	for _, e := range r.order {
		if e.out == nil && e.in.OrgID == orgID {
			out = append(out, e.in)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// --- загрузка и валидация ---

type journalRecord struct {
	Seq     uint64       `json:"seq"`
	Type    string       `json:"type"`
	Intent  *intentWire  `json:"intent,omitempty"`
	Outcome *outcomeWire `json:"outcome,omitempty"`
}

type intentWire struct {
	EventID          string    `json:"event_id"`
	MutationID       string    `json:"mutation_id"`
	RequestDigest    string    `json:"request_digest"`
	ActorKind        string    `json:"actor_kind"`
	ActorID          string    `json:"actor_id"`
	PrincipalKind    string    `json:"principal_kind,omitempty"`
	PrincipalID      string    `json:"principal_id,omitempty"`
	OrgID            string    `json:"org_id"`
	TeamID           string    `json:"team_id,omitempty"`
	ClusterID        string    `json:"cluster_id,omitempty"`
	Action           string    `json:"action"`
	ResourceType     string    `json:"resource_type"`
	ResourceID       string    `json:"resource_id"`
	ExpectedRevision uint64    `json:"expected_revision"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type outcomeWire struct {
	EventID          string         `json:"event_id"`
	Result           CriticalResult `json:"result"`
	ResourceRevision uint64         `json:"resource_revision"`
	ReasonCode       string         `json:"reason_code,omitempty"`
	CompletedAt      time.Time      `json:"completed_at"`
}

func intentToWire(in CriticalIntent) *intentWire {
	w := intentWire(in)
	return &w
}

func outcomeToWire(o CriticalOutcome) *outcomeWire {
	w := outcomeWire(o)
	return &w
}

// loadLocked сбрасывает индекс и перечитывает журнал с диска. Ошибка — только сбой окружения
// (чтение/инициализация); повреждение журнала отражается в r.rec.
func (r *FileCriticalRecorder) loadLocked() error {
	r.entries = map[string]*criticalEntry{}
	r.order = nil
	r.seq, r.size = 0, 0
	r.rec = CriticalRecovery{Offset: -1}

	st, err := r.f.Stat()
	if err != nil {
		return fmt.Errorf("audit: stat critical journal: %w", err)
	}
	if st.Size() == 0 {
		return r.initLocked()
	}
	if _, err := r.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("audit: seek critical journal: %w", err)
	}
	total := st.Size()
	br := bufio.NewReaderSize(r.f, 64<<10)

	// Заголовок.
	head := make([]byte, len(criticalMagic))
	if n, err := io.ReadFull(br, head); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			if bytes.HasPrefix(criticalMagic, head[:n]) || allZero(head[:n]) {
				r.markLocked(true, 0, "incomplete journal header")
				return nil
			}
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			r.markLocked(false, 0, "unrecognized journal header")
			return nil
		}
		return fmt.Errorf("audit: read critical journal: %w", err)
	}
	if !bytes.Equal(head, criticalMagic) {
		r.markLocked(false, 0, "unrecognized journal header")
		return nil
	}

	offset := int64(len(criticalMagic))
	hdr := make([]byte, criticalFrameHeader)
	for {
		_, err := io.ReadFull(br, hdr)
		if err == io.EOF {
			break // чистая граница
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			r.markLocked(true, offset, "incomplete record header at tail")
			return nil
		}
		if err != nil {
			return fmt.Errorf("audit: read critical journal: %w", err)
		}
		length := binary.BigEndian.Uint32(hdr[0:4])
		sum := binary.BigEndian.Uint32(hdr[4:8])
		if length == 0 || length > criticalMaxRecord {
			if allZero(hdr) {
				rest, zerr := restIsZero(br)
				if zerr != nil {
					return fmt.Errorf("audit: read critical journal: %w", zerr)
				}
				if rest {
					r.markLocked(total-offset <= criticalMaxTail, offset, "zero-filled tail")
					return nil
				}
			}
			r.markLocked(false, offset, "invalid record length")
			return nil
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(br, payload); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				r.markLocked(true, offset, "incomplete record at tail")
				return nil
			}
			return fmt.Errorf("audit: read critical journal: %w", err)
		}
		end := offset + criticalFrameHeader + int64(length)
		if crc32.Checksum(payload, crcTable) != sum {
			// Битый последний кадр — «рваный» хвост; битый кадр с данными после — повреждение журнала.
			r.markLocked(end >= total, offset, "record checksum mismatch")
			return nil
		}
		if reason := r.applyLocked(payload); reason != "" {
			r.markLocked(false, offset, reason)
			return nil
		}
		offset = end
		r.size = end
	}
	r.size = offset
	if _, err := r.f.Seek(r.size, io.SeekStart); err != nil {
		return fmt.Errorf("audit: seek critical journal: %w", err)
	}
	return nil
}

// markLocked фиксирует recovery-состояние. repairable допустим только для хвоста сразу после
// последнего принятого кадра.
func (r *FileCriticalRecorder) markLocked(repairable bool, offset int64, reason string) {
	r.rec = CriticalRecovery{Required: true, Repairable: repairable && r.size == offset, Offset: offset, Reason: reason}
}

// initLocked записывает заголовок нового журнала: Sync файла, затем Sync каталога.
func (r *FileCriticalRecorder) initLocked() error {
	if _, err := r.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("audit: init critical journal: %w", err)
	}
	n, err := r.f.Write(criticalMagic)
	if err == nil && n != len(criticalMagic) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("audit: init critical journal: %w: %w", ErrCriticalDurability, err)
	}
	if err := r.f.Sync(); err != nil {
		return fmt.Errorf("audit: init critical journal: %w: %w", ErrCriticalDurability, err)
	}
	if err := r.fs.SyncDir(r.dir); err != nil {
		return fmt.Errorf("audit: init critical journal: %w: %w", ErrCriticalDurability, err)
	}
	r.size = int64(len(criticalMagic))
	return nil
}

// applyLocked применяет проверенный по CRC кадр к индексу. Непустая строка — причина повреждения.
func (r *FileCriticalRecorder) applyLocked(payload []byte) string {
	var rec journalRecord
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil || dec.More() {
		return "undecodable record"
	}
	if rec.Seq != r.seq+1 {
		return "record sequence gap"
	}
	switch rec.Type {
	case recTypeIntent:
		if rec.Intent == nil || rec.Outcome != nil {
			return "malformed intent record"
		}
		in := CriticalIntent(*rec.Intent)
		if in.Validate() != nil {
			return "invalid intent record"
		}
		if _, dup := r.entries[in.EventID]; dup {
			return "duplicate intent record"
		}
		in = in.normalized()
		e := &criticalEntry{in: in}
		r.entries[in.EventID] = e
		r.order = append(r.order, e)
	case recTypeOutcome:
		if rec.Outcome == nil || rec.Intent != nil {
			return "malformed outcome record"
		}
		out := CriticalOutcome(*rec.Outcome)
		if out.Validate() != nil {
			return "invalid outcome record"
		}
		e, ok := r.entries[out.EventID]
		if !ok {
			return "outcome without intent"
		}
		if e.out != nil {
			return "duplicate outcome record"
		}
		out = out.normalized()
		e.out = &out
	default:
		return "unknown record type"
	}
	r.seq = rec.Seq
	return ""
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func restIsZero(r io.Reader) (bool, error) {
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if !allZero(buf[:n]) {
			return false, nil
		}
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
}

// QuarantineCorruptTail — явное безопасное разрешение «рваного» хвоста (обрыв записи в конце файла).
// Хвост копируется в <journal>.corrupt-<time> (Sync файла и каталога), затем журнал усекается до
// последнего целого кадра, и состояние перечитывается. Если повреждение не является рваным хвостом
// (в середине файла, чужой формат, сбой записи) — отказ, данные не трогаются.
// Возвращает путь файла карантина.
func (r *FileCriticalRecorder) QuarantineCorruptTail() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", ErrCriticalClosed
	}
	if !r.rec.Required || !r.rec.Repairable {
		return "", fmt.Errorf("%w: no repairable tail", ErrCriticalInvalid)
	}
	offset := r.rec.Offset
	st, err := r.f.Stat()
	if err != nil {
		return "", fmt.Errorf("audit: stat critical journal: %w", err)
	}
	tailLen := st.Size() - offset
	if tailLen < 0 || tailLen > criticalMaxTail {
		return "", fmt.Errorf("%w: tail is not repairable", ErrCriticalInvalid)
	}
	tail := make([]byte, tailLen)
	if _, err := r.f.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("audit: seek critical journal: %w", err)
	}
	if _, err := io.ReadFull(r.f, tail); err != nil {
		return "", fmt.Errorf("audit: read critical tail: %w", err)
	}

	qpath := fmt.Sprintf("%s.corrupt-%d", r.path, time.Now().UTC().UnixNano())
	q, err := r.fs.OpenFile(qpath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("audit: create quarantine file: %w", err)
	}
	n, werr := q.Write(tail)
	if werr == nil && n != len(tail) {
		werr = io.ErrShortWrite
	}
	if werr == nil {
		werr = q.Sync()
	}
	if cerr := q.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = r.fs.SyncDir(r.dir)
	}
	if werr != nil {
		return "", fmt.Errorf("audit: quarantine tail: %w", werr) // журнал не тронут
	}

	if err := r.f.Truncate(offset); err != nil {
		return "", fmt.Errorf("audit: truncate critical journal: %w", err)
	}
	if err := r.f.Sync(); err != nil {
		r.poisonLocked("journal sync failed after quarantine; reopen to validate")
		return "", fmt.Errorf("audit: sync critical journal: %w", err)
	}
	if err := r.loadLocked(); err != nil {
		r.poisonLocked("journal reload failed after quarantine; reopen to validate")
		return "", err
	}
	if r.rec.Required {
		return qpath, r.recoveryErrLocked()
	}
	return qpath, nil
}
