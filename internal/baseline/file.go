// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package baseline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Раскладка файловой реализации (desktop). Подкаталог на область «организация/команда/кластер» и на baseline:
//
//	<root>/<org>/<team>/<cluster>/<baselineID>/baseline.json
//	                                           versions/<seq, 10 цифр>_<versionID>.json
//	                                           exceptions/<versionID>.json
//	                                           results/<микросекунды, 20 цифр>_<resultID>.json
//
// Запись атомарна (временный файл в том же каталоге и переименование), права 0600 для файлов и 0700 для каталогов —
// не шире, чем у users.json и audit.log. Один процесс, межпроцессных блокировок нет; сериализация записи — мьютекс
// корня, общий для всех экземпляров на один каталог.
const (
	fileBaselineName = "baseline.json"
	dirVersions      = "versions"
	dirExceptions    = "exceptions"
	dirResults       = "results"

	// maxFileResults — предел числа результатов одного baseline в desktop: старейшие удаляются при добавлении.
	// В server предела по числу нет (только срок). Допустимое различие объёма истории (ADR-0026 §4), см. NOTES.md.
	maxFileResults = 500
)

var fileLocks sync.Map // корень → *sync.Mutex

func lockFor(root string) *sync.Mutex {
	m, _ := fileLocks.LoadOrStore(root, new(sync.Mutex))
	return m.(*sync.Mutex)
}

type fileBackend struct {
	root string
	mu   *sync.Mutex
}

// NewFileBackend возвращает файловое хранилище baseline в каталоге root (создаётся при первой записи).
func NewFileBackend(root string) Backend {
	root = filepath.Clean(root)
	return &fileBackend{root: root, mu: lockFor(root)}
}

func (b *fileBackend) clusterDir(s tenant.Scope) string {
	return filepath.Join(b.root, s.OrgID, s.TeamID, s.ClusterID)
}

func (b *fileBackend) baselineDir(s tenant.Scope, id string) string {
	return filepath.Join(b.clusterDir(s), id)
}

// check проверяет область и контекст.
func check(ctx context.Context, s tenant.Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ValidateScope(s)
}

func (b *fileBackend) PutBaseline(ctx context.Context, bl Baseline) error {
	if err := check(ctx, bl.Scope); err != nil {
		return err
	}
	if err := bl.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return writeJSON(filepath.Join(b.baselineDir(bl.Scope, bl.ID), fileBaselineName), bl.Normalized())
}

func (b *fileBackend) GetBaseline(ctx context.Context, s tenant.Scope, id string) (Baseline, error) {
	if err := check(ctx, s); err != nil {
		return Baseline{}, err
	}
	if err := ValidateID(id); err != nil {
		return Baseline{}, ErrNotFound
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.getBaseline(s, id)
}

func (b *fileBackend) getBaseline(s tenant.Scope, id string) (Baseline, error) {
	var bl Baseline
	if err := readJSON(filepath.Join(b.baselineDir(s, id), fileBaselineName), &bl); err != nil {
		return Baseline{}, err
	}
	if bl.Scope != s { // защита в глубину: документ в чужом каталоге не отдаётся
		return Baseline{}, ErrNotFound
	}
	return bl.Normalized(), nil
}

func (b *fileBackend) ListBaselines(ctx context.Context, s tenant.Scope) ([]Baseline, error) {
	if err := check(ctx, s); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	entries, err := os.ReadDir(b.clusterDir(s))
	if errors.Is(err, fs.ErrNotExist) {
		return []Baseline{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read baselines: %w", err)
	}
	out := []Baseline{}
	for _, e := range entries {
		if !e.IsDir() || ValidateID(e.Name()) != nil {
			continue
		}
		bl, err := b.getBaseline(s, e.Name())
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, bl)
	}
	slices.SortFunc(out, func(x, y Baseline) int {
		if c := x.CreatedAt.Compare(y.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	return out, nil
}

func (b *fileBackend) DeleteBaseline(ctx context.Context, s tenant.Scope, id string) error {
	if err := check(ctx, s); err != nil {
		return err
	}
	if err := ValidateID(id); err != nil {
		return ErrNotFound
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.getBaseline(s, id); err != nil {
		return err
	}
	if err := os.RemoveAll(b.baselineDir(s, id)); err != nil {
		return fmt.Errorf("failed to delete the baseline: %w", err)
	}
	return nil
}

func versionFileName(seq int, id string) string { return fmt.Sprintf("%010d_%s.json", seq, id) }

// parseVersionFile разбирает имя файла версии: номер и идентификатор.
func parseVersionFile(name string) (seq int, id string, ok bool) {
	base, found := strings.CutSuffix(name, ".json")
	if !found {
		return 0, "", false
	}
	num, id, found := strings.Cut(base, "_")
	if !found || len(num) != 10 || ValidateID(id) != nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0, "", false
	}
	return n, id, true
}

type versionFile struct {
	seq  int
	id   string
	path string
}

// versionFiles возвращает файлы версий baseline по возрастанию номера.
func (b *fileBackend) versionFiles(s tenant.Scope, baselineID string) ([]versionFile, error) {
	dir := filepath.Join(b.baselineDir(s, baselineID), dirVersions)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read versions: %w", err)
	}
	var out []versionFile
	for _, e := range entries {
		if seq, id, ok := parseVersionFile(e.Name()); ok && !e.IsDir() {
			out = append(out, versionFile{seq: seq, id: id, path: filepath.Join(dir, e.Name())})
		}
	}
	return out, nil // ReadDir отдаёт имена по возрастанию, номера дополнены нулями
}

func (b *fileBackend) PutVersion(ctx context.Context, s tenant.Scope, v Version) error {
	if err := check(ctx, s); err != nil {
		return err
	}
	if err := v.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.getBaseline(s, v.BaselineID); err != nil {
		return err
	}
	files, err := b.versionFiles(s, v.BaselineID)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.id == v.ID || f.seq == v.Seq {
			return ErrExists
		}
	}
	return writeJSON(filepath.Join(b.baselineDir(s, v.BaselineID), dirVersions, versionFileName(v.Seq, v.ID)), v.Normalized())
}

func (b *fileBackend) findVersion(s tenant.Scope, baselineID, versionID string) (versionFile, error) {
	if ValidateID(baselineID) != nil || ValidateID(versionID) != nil {
		return versionFile{}, ErrNotFound
	}
	if _, err := b.getBaseline(s, baselineID); err != nil {
		return versionFile{}, err
	}
	files, err := b.versionFiles(s, baselineID)
	if err != nil {
		return versionFile{}, err
	}
	for _, f := range files {
		if f.id == versionID {
			return f, nil
		}
	}
	return versionFile{}, ErrNotFound
}

func (b *fileBackend) GetVersion(ctx context.Context, s tenant.Scope, baselineID, versionID string) (Version, error) {
	if err := check(ctx, s); err != nil {
		return Version{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	f, err := b.findVersion(s, baselineID, versionID)
	if err != nil {
		return Version{}, err
	}
	var v Version
	if err := readJSON(f.path, &v); err != nil {
		return Version{}, err
	}
	return v.Normalized(), nil
}

func (b *fileBackend) ListVersions(ctx context.Context, s tenant.Scope, baselineID string, page Page) (VersionPage, error) {
	if err := check(ctx, s); err != nil {
		return VersionPage{}, err
	}
	if ValidateID(baselineID) != nil {
		return VersionPage{}, ErrNotFound
	}
	before := int(^uint(0) >> 1)
	if page.Cursor != "" {
		n, err := decodeSeqCursor(page.Cursor)
		if err != nil {
			return VersionPage{}, err
		}
		before = n
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.getBaseline(s, baselineID); err != nil {
		return VersionPage{}, err
	}
	files, err := b.versionFiles(s, baselineID)
	if err != nil {
		return VersionPage{}, err
	}
	size := page.Size()
	var out VersionPage
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		if f.seq >= before {
			continue
		}
		if len(out.Items) == size {
			out.Next = encodeSeqCursor(out.Items[len(out.Items)-1].Seq)
			break
		}
		var v Version
		if err := readJSON(f.path, &v); err != nil {
			return VersionPage{}, err
		}
		out.Items = append(out.Items, v.Normalized().Summary())
	}
	if out.Items == nil {
		out.Items = []VersionSummary{}
	}
	return out, nil
}

func (b *fileBackend) exceptionsPath(s tenant.Scope, baselineID, versionID string) string {
	return filepath.Join(b.baselineDir(s, baselineID), dirExceptions, versionID+".json")
}

func (b *fileBackend) PutExceptions(ctx context.Context, s tenant.Scope, baselineID, versionID string, list []Exception) error {
	if err := check(ctx, s); err != nil {
		return err
	}
	for _, e := range list {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.findVersion(s, baselineID, versionID); err != nil {
		return err
	}
	path := b.exceptionsPath(s, baselineID, versionID)
	if len(list) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("failed to clear exceptions: %w", err)
		}
		return nil
	}
	return writeJSON(path, NormalizedExceptions(list))
}

func (b *fileBackend) GetExceptions(ctx context.Context, s tenant.Scope, baselineID, versionID string) ([]Exception, error) {
	if err := check(ctx, s); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.findVersion(s, baselineID, versionID); err != nil {
		return nil, err
	}
	var list []Exception
	err := readJSON(b.exceptionsPath(s, baselineID, versionID), &list)
	if errors.Is(err, ErrNotFound) {
		return []Exception{}, nil
	}
	if err != nil {
		return nil, err
	}
	return NormalizedExceptions(list), nil
}

func resultFileName(at time.Time, id string) string {
	return fmt.Sprintf("%020d_%s.json", at.UnixMicro(), id)
}

// parseResultFile разбирает имя файла результата: микросекунды и идентификатор.
func parseResultFile(name string) (micro int64, id string, ok bool) {
	base, found := strings.CutSuffix(name, ".json")
	if !found {
		return 0, "", false
	}
	num, id, found := strings.Cut(base, "_")
	if !found || len(num) != 20 || ValidateID(id) != nil {
		return 0, "", false
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return n, id, true
}

// resultNames возвращает имена файлов результатов по возрастанию (время, затем ID).
func (b *fileBackend) resultNames(s tenant.Scope, baselineID string) (string, []string, error) {
	dir := filepath.Join(b.baselineDir(s, baselineID), dirResults)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return dir, nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("failed to read results: %w", err)
	}
	var names []string
	for _, e := range entries {
		if _, _, ok := parseResultFile(e.Name()); ok && !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return dir, names, nil
}

func (b *fileBackend) AppendResult(ctx context.Context, s tenant.Scope, r CheckResult) error {
	if err := check(ctx, s); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	r = r.Normalized()
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.getBaseline(s, r.BaselineID); err != nil {
		return err
	}
	dir, names, err := b.resultNames(s, r.BaselineID)
	if err != nil {
		return err
	}
	for _, n := range names {
		if _, id, _ := parseResultFile(n); id == r.ID {
			return ErrExists
		}
	}
	if err := writeJSON(filepath.Join(dir, resultFileName(r.CheckedAt, r.ID)), r); err != nil {
		return err
	}
	// Предел desktop: старейшие результаты сверх maxFileResults удаляются.
	names = append(names, resultFileName(r.CheckedAt, r.ID))
	slices.Sort(names)
	for len(names) > maxFileResults {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
	return nil
}

func (b *fileBackend) findResult(s tenant.Scope, baselineID, resultID string) (string, error) {
	if ValidateID(baselineID) != nil || ValidateID(resultID) != nil {
		return "", ErrNotFound
	}
	if _, err := b.getBaseline(s, baselineID); err != nil {
		return "", err
	}
	dir, names, err := b.resultNames(s, baselineID)
	if err != nil {
		return "", err
	}
	for _, n := range names {
		if _, id, _ := parseResultFile(n); id == resultID {
			return filepath.Join(dir, n), nil
		}
	}
	return "", ErrNotFound
}

func (b *fileBackend) GetResult(ctx context.Context, s tenant.Scope, baselineID, resultID string) (CheckResult, error) {
	if err := check(ctx, s); err != nil {
		return CheckResult{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	path, err := b.findResult(s, baselineID, resultID)
	if err != nil {
		return CheckResult{}, err
	}
	var r CheckResult
	if err := readJSON(path, &r); err != nil {
		return CheckResult{}, err
	}
	return r.Normalized(), nil
}

func (b *fileBackend) ListResults(ctx context.Context, s tenant.Scope, baselineID string, page Page) (ResultPage, error) {
	if err := check(ctx, s); err != nil {
		return ResultPage{}, err
	}
	if ValidateID(baselineID) != nil {
		return ResultPage{}, ErrNotFound
	}
	var before string
	if page.Cursor != "" {
		c, err := decodeResultCursor(page.Cursor)
		if err != nil {
			return ResultPage{}, err
		}
		before = c
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.getBaseline(s, baselineID); err != nil {
		return ResultPage{}, err
	}
	dir, names, err := b.resultNames(s, baselineID)
	if err != nil {
		return ResultPage{}, err
	}
	size := page.Size()
	out := ResultPage{Items: []ResultSummary{}}
	for i := len(names) - 1; i >= 0; i-- {
		n := names[i]
		if before != "" && n >= before {
			continue
		}
		if len(out.Items) == size {
			out.Next = encodeResultCursor(resultNameOf(out.Items[len(out.Items)-1]))
			break
		}
		var r CheckResult
		if err := readJSON(filepath.Join(dir, n), &r); err != nil {
			return ResultPage{}, err
		}
		out.Items = append(out.Items, r.Normalized().Summarize())
	}
	return out, nil
}

// resultNameOf восстанавливает имя файла результата по строке истории (курсор строится из него).
func resultNameOf(r ResultSummary) string { return resultFileName(r.CheckedAt, r.ID) }

func (b *fileBackend) DeleteResultsBefore(ctx context.Context, t time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cut := t.UnixMicro()
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	err := b.eachBaselineDir(ctx, func(dir string) error {
		resDir := filepath.Join(dir, dirResults)
		entries, err := os.ReadDir(resDir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read results: %w", err)
		}
		for _, e := range entries {
			micro, _, ok := parseResultFile(e.Name())
			if !ok || micro >= cut {
				continue
			}
			if err := os.Remove(filepath.Join(resDir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("failed to delete a result: %w", err)
			}
			n++
		}
		return nil
	})
	return n, err
}

func (b *fileBackend) PruneVersions(ctx context.Context, keep int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	keep = max(keep, MinVersionsKept)
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	err := b.eachBaselineDir(ctx, func(dir string) error {
		var bl Baseline
		if err := readJSON(filepath.Join(dir, fileBaselineName), &bl); err != nil {
			return err
		}
		vdir := filepath.Join(dir, dirVersions)
		entries, err := os.ReadDir(vdir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read versions: %w", err)
		}
		var files []versionFile
		for _, e := range entries {
			if seq, id, ok := parseVersionFile(e.Name()); ok && !e.IsDir() {
				files = append(files, versionFile{seq: seq, id: id, path: filepath.Join(vdir, e.Name())})
			}
		}
		if len(files) <= keep {
			return nil
		}
		for _, f := range files[:len(files)-keep] { // старейшие сверх keep
			if f.id == bl.ActiveVersionID {
				continue // активную версию не удаляем никогда
			}
			if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("failed to delete a version: %w", err)
			}
			_ = os.Remove(filepath.Join(dir, dirExceptions, f.id+".json"))
			n++
		}
		return nil
	})
	return n, err
}

// eachBaselineDir обходит каталоги всех baseline всех областей (retention не привязан к области).
func (b *fileBackend) eachBaselineDir(ctx context.Context, fn func(dir string) error) error {
	// корень → организация → команда → кластер (глубина 3), внутри каталога кластера лежат каталоги baseline
	return walkLevels(ctx, b.root, 3, func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("failed to read baselines: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() || ValidateID(e.Name()) != nil {
				continue
			}
			bdir := filepath.Join(dir, e.Name())
			if _, err := os.Stat(filepath.Join(bdir, fileBaselineName)); err != nil {
				continue
			}
			if err := fn(bdir); err != nil {
				return err
			}
		}
		return nil
	})
}

// walkLevels вызывает fn для каталогов на глубине depth под root. Отсутствие root — не ошибка.
func walkLevels(ctx context.Context, root string, depth int, fn func(dir string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth == 0 {
		return fn(root)
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read baselines: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := walkLevels(ctx, filepath.Join(root, e.Name()), depth-1, fn); err != nil {
			return err
		}
	}
	return nil
}

// --- файлы -----------------------------------------------------------------------------------------------------

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to read baseline data: %w", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("baseline data is corrupted: %w", err)
	}
	return nil
}

// writeJSON пишет файл атомарно: временный файл в том же каталоге, fsync и переименование.
func writeJSON(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to encode baseline data: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to write baseline data: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*") // CreateTemp создаёт файл с правами 0600
	if err != nil {
		return fmt.Errorf("failed to write baseline data: %w", err)
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("failed to write baseline data: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("failed to write baseline data: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("failed to write baseline data: %w", err)
	}
	return nil
}

// --- курсоры ---------------------------------------------------------------------------------------------------

// Курсоры непрозрачны для клиента: base64 от внутреннего значения. Испорченный курсор — ErrInvalid.

func encodeSeqCursor(seq int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("v:" + strconv.Itoa(seq)))
}

func decodeSeqCursor(c string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, fmt.Errorf("%w: malformed cursor", ErrInvalid)
	}
	num, ok := strings.CutPrefix(string(raw), "v:")
	if !ok {
		return 0, fmt.Errorf("%w: malformed cursor", ErrInvalid)
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: malformed cursor", ErrInvalid)
	}
	return n, nil
}

func encodeResultCursor(name string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("r:" + name))
}

func decodeResultCursor(c string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", fmt.Errorf("%w: malformed cursor", ErrInvalid)
	}
	name, ok := strings.CutPrefix(string(raw), "r:")
	if !ok {
		return "", fmt.Errorf("%w: malformed cursor", ErrInvalid)
	}
	if _, _, ok := parseResultFile(name); !ok {
		return "", fmt.Errorf("%w: malformed cursor", ErrInvalid)
	}
	return name, nil
}
