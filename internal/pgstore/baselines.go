// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/tenant"
)

// Baselines возвращает хранилище baseline конфигурации (ADR-0027) на PostgreSQL. Область входит в ключ каждой
// строки; содержимое версий (соль HMAC и нормализованные объекты) шифруется secretbox, как и прочие секреты.
func (s *Store) Baselines() baseline.Backend { return &baselineBackend{s: s} }

type baselineBackend struct{ s *Store }

var _ baseline.Backend = (*baselineBackend)(nil)

func bctx(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c, cancel := context.WithTimeout(ctx, opTimeout)
	return c, cancel, nil
}

// begin проверяет область и накладывает таймаут.
func begin(ctx context.Context, sc tenant.Scope) (context.Context, context.CancelFunc, error) {
	if err := baseline.ValidateScope(sc); err != nil {
		return nil, nil, err
	}
	return bctx(ctx)
}

// versionAAD привязывает шифротекст версии к области, baseline и версии: подмена строки другой областью не расшифруется.
func versionAAD(org, team, cluster, baselineID, versionID string) []byte {
	return []byte("baseline-version/" + org + "/" + team + "/" + cluster + "/" + baselineID + "/" + versionID)
}

// versionContent — зашифрованная часть версии.
type versionContent struct {
	Salt    []byte            `json:"salt"`
	Objects []baseline.Object `json:"objects"`
}

func mapWriteErr(err error) error {
	switch pgCode(err) {
	case "23505":
		return baseline.ErrExists
	case "23503":
		return baseline.ErrNotFound
	}
	return err
}

func jsonOrEmpty(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil { // типы документа всегда сериализуются
		return []byte("[]")
	}
	return b
}

// --- baseline -----------------------------------------------------------------------------------------------------

func (b *baselineBackend) PutBaseline(ctx context.Context, bl baseline.Baseline) error {
	ctx, cancel, err := begin(ctx, bl.Scope)
	if err != nil {
		return err
	}
	defer cancel()
	if err := bl.Validate(); err != nil {
		return err
	}
	bl = bl.Normalized()
	_, err = b.s.pool.Exec(ctx, `
		INSERT INTO baselines (org_id, team_id, cluster_id, id, name, namespaces, selector, kinds, refs, active_version_id, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (org_id, team_id, cluster_id, id) DO UPDATE SET
			name=EXCLUDED.name, namespaces=EXCLUDED.namespaces, selector=EXCLUDED.selector, kinds=EXCLUDED.kinds,
			refs=EXCLUDED.refs, active_version_id=EXCLUDED.active_version_id, created_by=EXCLUDED.created_by, created_at=EXCLUDED.created_at`,
		bl.Scope.OrgID, bl.Scope.TeamID, bl.Scope.ClusterID, bl.ID, bl.Name,
		jsonOrEmpty(bl.Namespaces), bl.Selector, jsonOrEmpty(bl.Kinds), jsonOrEmpty(bl.Refs),
		bl.ActiveVersionID, bl.CreatedBy, bl.CreatedAt)
	return err
}

const baselineCols = `id, name, namespaces, selector, kinds, refs, active_version_id, created_by, created_at`

func scanBaseline(sc tenant.Scope, row pgx.Row) (baseline.Baseline, error) {
	var bl baseline.Baseline
	var ns, kinds, refs []byte
	if err := row.Scan(&bl.ID, &bl.Name, &ns, &bl.Selector, &kinds, &refs, &bl.ActiveVersionID, &bl.CreatedBy, &bl.CreatedAt); err != nil {
		return baseline.Baseline{}, err
	}
	for _, f := range []struct {
		raw []byte
		to  any
	}{{ns, &bl.Namespaces}, {kinds, &bl.Kinds}, {refs, &bl.Refs}} {
		if err := json.Unmarshal(f.raw, f.to); err != nil {
			return baseline.Baseline{}, fmt.Errorf("failed to read a baseline document: %w", err)
		}
	}
	bl.Scope = sc
	return bl.Normalized(), nil
}

func (b *baselineBackend) GetBaseline(ctx context.Context, sc tenant.Scope, id string) (baseline.Baseline, error) {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return baseline.Baseline{}, err
	}
	defer cancel()
	if baseline.ValidateID(id) != nil {
		return baseline.Baseline{}, baseline.ErrNotFound
	}
	bl, err := scanBaseline(sc, b.s.pool.QueryRow(ctx,
		`SELECT `+baselineCols+` FROM baselines WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4`,
		sc.OrgID, sc.TeamID, sc.ClusterID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return baseline.Baseline{}, baseline.ErrNotFound
	}
	return bl, err
}

func (b *baselineBackend) ListBaselines(ctx context.Context, sc tenant.Scope) ([]baseline.Baseline, error) {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return nil, err
	}
	defer cancel()
	rows, err := b.s.pool.Query(ctx,
		`SELECT `+baselineCols+` FROM baselines WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 ORDER BY created_at, id`,
		sc.OrgID, sc.TeamID, sc.ClusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []baseline.Baseline{}
	for rows.Next() {
		bl, err := scanBaseline(sc, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, bl)
	}
	return out, rows.Err()
}

func (b *baselineBackend) DeleteBaseline(ctx context.Context, sc tenant.Scope, id string) error {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return err
	}
	defer cancel()
	if baseline.ValidateID(id) != nil {
		return baseline.ErrNotFound
	}
	tag, err := b.s.pool.Exec(ctx, `DELETE FROM baselines WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4`,
		sc.OrgID, sc.TeamID, sc.ClusterID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return baseline.ErrNotFound
	}
	return nil // версии, исключения и результаты удаляет каскад внешних ключей
}

// baselineExists отвечает, есть ли baseline в области.
func (b *baselineBackend) baselineExists(ctx context.Context, sc tenant.Scope, id string) error {
	if baseline.ValidateID(id) != nil {
		return baseline.ErrNotFound
	}
	var one int
	err := b.s.pool.QueryRow(ctx, `SELECT 1 FROM baselines WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND id=$4`,
		sc.OrgID, sc.TeamID, sc.ClusterID, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return baseline.ErrNotFound
	}
	return err
}

// --- versions -----------------------------------------------------------------------------------------------------

func (b *baselineBackend) PutVersion(ctx context.Context, sc tenant.Scope, v baseline.Version) error {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return err
	}
	defer cancel()
	if err := v.Validate(); err != nil {
		return err
	}
	v = v.Normalized()
	plain, err := json.Marshal(versionContent{Salt: v.Salt, Objects: v.Objects})
	if err != nil {
		return err
	}
	enc, keyID, err := b.s.keys.Seal(plain, versionAAD(sc.OrgID, sc.TeamID, sc.ClusterID, v.BaselineID, v.ID))
	if err != nil {
		return fmt.Errorf("failed to encrypt a baseline version: %w", err)
	}
	_, err = b.s.pool.Exec(ctx, `
		INSERT INTO baseline_versions (org_id, team_id, cluster_id, baseline_id, id, seq, rules_version, source, author, comment, created_at, object_count, content_enc, key_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		sc.OrgID, sc.TeamID, sc.ClusterID, v.BaselineID, v.ID, v.Seq, v.RulesVersion, v.Source, v.Author, v.Comment,
		v.CreatedAt, len(v.Objects), enc, keyID)
	return mapWriteErr(err)
}

func (b *baselineBackend) GetVersion(ctx context.Context, sc tenant.Scope, baselineID, versionID string) (baseline.Version, error) {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return baseline.Version{}, err
	}
	defer cancel()
	if baseline.ValidateID(baselineID) != nil || baseline.ValidateID(versionID) != nil {
		return baseline.Version{}, baseline.ErrNotFound
	}
	v := baseline.Version{ID: versionID, BaselineID: baselineID}
	var enc []byte
	var keyID string
	err = b.s.pool.QueryRow(ctx, `
		SELECT seq, rules_version, source, author, comment, created_at, content_enc, key_id
		FROM baseline_versions WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND baseline_id=$4 AND id=$5`,
		sc.OrgID, sc.TeamID, sc.ClusterID, baselineID, versionID).
		Scan(&v.Seq, &v.RulesVersion, &v.Source, &v.Author, &v.Comment, &v.CreatedAt, &enc, &keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return baseline.Version{}, baseline.ErrNotFound
	}
	if err != nil {
		return baseline.Version{}, err
	}
	plain, err := b.s.keys.Open(enc, keyID, versionAAD(sc.OrgID, sc.TeamID, sc.ClusterID, baselineID, versionID))
	if err != nil {
		return baseline.Version{}, fmt.Errorf("failed to decrypt a baseline version: %w", err)
	}
	var c versionContent
	if err := json.Unmarshal(plain, &c); err != nil {
		return baseline.Version{}, fmt.Errorf("failed to read a baseline version: %w", err)
	}
	v.Salt, v.Objects = c.Salt, c.Objects
	return v.Normalized(), nil
}

// Курсоры непрозрачны: base64 от внутреннего значения. Испорченный курсор — ErrInvalid.
func encodeCursor(kind, val string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + val))
}

func decodeCursor(kind, c string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", fmt.Errorf("%w: malformed cursor", baseline.ErrInvalid)
	}
	val, ok := strings.CutPrefix(string(raw), kind+":")
	if !ok {
		return "", fmt.Errorf("%w: malformed cursor", baseline.ErrInvalid)
	}
	return val, nil
}

func (b *baselineBackend) ListVersions(ctx context.Context, sc tenant.Scope, baselineID string, page baseline.Page) (baseline.VersionPage, error) {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return baseline.VersionPage{}, err
	}
	defer cancel()
	before := int(^uint32(0) >> 1)
	if page.Cursor != "" {
		val, err := decodeCursor("v", page.Cursor)
		if err != nil {
			return baseline.VersionPage{}, err
		}
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return baseline.VersionPage{}, fmt.Errorf("%w: malformed cursor", baseline.ErrInvalid)
		}
		before = n
	}
	if err := b.baselineExists(ctx, sc, baselineID); err != nil {
		return baseline.VersionPage{}, err
	}
	size := page.Size()
	rows, err := b.s.pool.Query(ctx, `
		SELECT id, seq, rules_version, source, author, comment, created_at, object_count
		FROM baseline_versions WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND baseline_id=$4 AND seq < $5
		ORDER BY seq DESC LIMIT $6`,
		sc.OrgID, sc.TeamID, sc.ClusterID, baselineID, before, size+1)
	if err != nil {
		return baseline.VersionPage{}, err
	}
	defer rows.Close()
	out := baseline.VersionPage{Items: []baseline.VersionSummary{}}
	for rows.Next() {
		s := baseline.VersionSummary{BaselineID: baselineID}
		if err := rows.Scan(&s.ID, &s.Seq, &s.RulesVersion, &s.Source, &s.Author, &s.Comment, &s.CreatedAt, &s.ObjectCount); err != nil {
			return baseline.VersionPage{}, err
		}
		s.CreatedAt = baseline.Normalize(s.CreatedAt)
		out.Items = append(out.Items, s)
	}
	if err := rows.Err(); err != nil {
		return baseline.VersionPage{}, err
	}
	if len(out.Items) > size {
		out.Items = out.Items[:size]
		out.Next = encodeCursor("v", strconv.Itoa(out.Items[size-1].Seq))
	}
	return out, nil
}

// --- exceptions ---------------------------------------------------------------------------------------------------

func (b *baselineBackend) versionExists(ctx context.Context, sc tenant.Scope, baselineID, versionID string) error {
	if baseline.ValidateID(baselineID) != nil || baseline.ValidateID(versionID) != nil {
		return baseline.ErrNotFound
	}
	var one int
	err := b.s.pool.QueryRow(ctx, `SELECT 1 FROM baseline_versions WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND baseline_id=$4 AND id=$5`,
		sc.OrgID, sc.TeamID, sc.ClusterID, baselineID, versionID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return baseline.ErrNotFound
	}
	return err
}

func (b *baselineBackend) PutExceptions(ctx context.Context, sc tenant.Scope, baselineID, versionID string, list []baseline.Exception) error {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return err
	}
	defer cancel()
	for _, e := range list {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	if err := b.versionExists(ctx, sc, baselineID, versionID); err != nil {
		return err
	}
	list = baseline.NormalizedExceptions(list)
	if list == nil {
		list = []baseline.Exception{}
	}
	_, err = b.s.pool.Exec(ctx, `
		INSERT INTO baseline_exceptions (org_id, team_id, cluster_id, baseline_id, version_id, items)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, team_id, cluster_id, baseline_id, version_id) DO UPDATE SET items=EXCLUDED.items`,
		sc.OrgID, sc.TeamID, sc.ClusterID, baselineID, versionID, jsonOrEmpty(list))
	return mapWriteErr(err)
}

func (b *baselineBackend) GetExceptions(ctx context.Context, sc tenant.Scope, baselineID, versionID string) ([]baseline.Exception, error) {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if err := b.versionExists(ctx, sc, baselineID, versionID); err != nil {
		return nil, err
	}
	var raw []byte
	err = b.s.pool.QueryRow(ctx, `SELECT items FROM baseline_exceptions WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND baseline_id=$4 AND version_id=$5`,
		sc.OrgID, sc.TeamID, sc.ClusterID, baselineID, versionID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []baseline.Exception{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []baseline.Exception
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("failed to read baseline exceptions: %w", err)
	}
	list = baseline.NormalizedExceptions(list)
	if list == nil {
		list = []baseline.Exception{}
	}
	return list, nil
}

// --- results ------------------------------------------------------------------------------------------------------

func (b *baselineBackend) AppendResult(ctx context.Context, sc tenant.Scope, r baseline.CheckResult) error {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return err
	}
	defer cancel()
	if err := r.Validate(); err != nil {
		return err
	}
	r = r.Normalized()
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = b.s.pool.Exec(ctx, `
		INSERT INTO baseline_results (org_id, team_id, cluster_id, baseline_id, id, checked_at, version_id, initiator, rules_version, summary, body)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		sc.OrgID, sc.TeamID, sc.ClusterID, r.BaselineID, r.ID, r.CheckedAt, r.VersionID, r.Initiator, r.RulesVersion,
		jsonOrEmpty(r.Summary), body)
	return mapWriteErr(err)
}

func (b *baselineBackend) GetResult(ctx context.Context, sc tenant.Scope, baselineID, resultID string) (baseline.CheckResult, error) {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return baseline.CheckResult{}, err
	}
	defer cancel()
	if baseline.ValidateID(baselineID) != nil || baseline.ValidateID(resultID) != nil {
		return baseline.CheckResult{}, baseline.ErrNotFound
	}
	var raw []byte
	err = b.s.pool.QueryRow(ctx, `SELECT body FROM baseline_results WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND baseline_id=$4 AND id=$5`,
		sc.OrgID, sc.TeamID, sc.ClusterID, baselineID, resultID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return baseline.CheckResult{}, baseline.ErrNotFound
	}
	if err != nil {
		return baseline.CheckResult{}, err
	}
	var r baseline.CheckResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return baseline.CheckResult{}, fmt.Errorf("failed to read a check result: %w", err)
	}
	return r.Normalized(), nil
}

func (b *baselineBackend) ListResults(ctx context.Context, sc tenant.Scope, baselineID string, page baseline.Page) (baseline.ResultPage, error) {
	ctx, cancel, err := begin(ctx, sc)
	if err != nil {
		return baseline.ResultPage{}, err
	}
	defer cancel()
	// Курсор: «микросекунды:идентификатор» последней строки страницы. Порядок: время, затем идентификатор, по убыванию.
	cursorAt, cursorID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), "\U0010FFFF"
	if page.Cursor != "" {
		val, err := decodeCursor("r", page.Cursor)
		if err != nil {
			return baseline.ResultPage{}, err
		}
		micro, id, ok := strings.Cut(val, ":")
		n, perr := strconv.ParseInt(micro, 10, 64)
		if !ok || perr != nil || baseline.ValidateID(id) != nil {
			return baseline.ResultPage{}, fmt.Errorf("%w: malformed cursor", baseline.ErrInvalid)
		}
		cursorAt, cursorID = time.UnixMicro(n).UTC(), id
	}
	if err := b.baselineExists(ctx, sc, baselineID); err != nil {
		return baseline.ResultPage{}, err
	}
	size := page.Size()
	rows, err := b.s.pool.Query(ctx, `
		SELECT id, version_id, checked_at, initiator, rules_version, summary
		FROM baseline_results
		WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND baseline_id=$4 AND (checked_at, id) < ($5, $6)
		ORDER BY checked_at DESC, id DESC LIMIT $7`,
		sc.OrgID, sc.TeamID, sc.ClusterID, baselineID, cursorAt, cursorID, size+1)
	if err != nil {
		return baseline.ResultPage{}, err
	}
	defer rows.Close()
	out := baseline.ResultPage{Items: []baseline.ResultSummary{}}
	for rows.Next() {
		rs := baseline.ResultSummary{BaselineID: baselineID}
		var sum []byte
		if err := rows.Scan(&rs.ID, &rs.VersionID, &rs.CheckedAt, &rs.Initiator, &rs.RulesVersion, &sum); err != nil {
			return baseline.ResultPage{}, err
		}
		if err := json.Unmarshal(sum, &rs.Summary); err != nil {
			return baseline.ResultPage{}, fmt.Errorf("failed to read a check result: %w", err)
		}
		rs.CheckedAt = baseline.Normalize(rs.CheckedAt)
		out.Items = append(out.Items, rs)
	}
	if err := rows.Err(); err != nil {
		return baseline.ResultPage{}, err
	}
	if len(out.Items) > size {
		out.Items = out.Items[:size]
		last := out.Items[size-1]
		out.Next = encodeCursor("r", strconv.FormatInt(last.CheckedAt.UnixMicro(), 10)+":"+last.ID)
	}
	return out, nil
}

// --- retention ----------------------------------------------------------------------------------------------------

func (b *baselineBackend) DeleteResultsBefore(ctx context.Context, t time.Time) (int, error) {
	ctx, cancel, err := bctx(ctx)
	if err != nil {
		return 0, err
	}
	defer cancel()
	tag, err := b.s.pool.Exec(ctx, `DELETE FROM baseline_results WHERE checked_at < $1`, t)
	return int(tag.RowsAffected()), err
}

func (b *baselineBackend) PruneVersions(ctx context.Context, keep int) (int, error) {
	ctx, cancel, err := bctx(ctx)
	if err != nil {
		return 0, err
	}
	defer cancel()
	keep = max(keep, baseline.MinVersionsKept)
	// Остаются keep последних версий каждого baseline; активная версия не удаляется никогда. Исключения — каскадом.
	tag, err := b.s.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT org_id, team_id, cluster_id, baseline_id, id,
			       row_number() OVER (PARTITION BY org_id, team_id, cluster_id, baseline_id ORDER BY seq DESC) AS rn
			FROM baseline_versions
		)
		DELETE FROM baseline_versions v
		USING ranked r, baselines b
		WHERE v.org_id=r.org_id AND v.team_id=r.team_id AND v.cluster_id=r.cluster_id AND v.baseline_id=r.baseline_id AND v.id=r.id
		  AND b.org_id=v.org_id AND b.team_id=v.team_id AND b.cluster_id=v.cluster_id AND b.id=v.baseline_id
		  AND r.rn > $1 AND v.id <> b.active_version_id`, keep)
	return int(tag.RowsAffected()), err
}

// --- перешифрование и проверка ключа ------------------------------------------------------------------------------

// rekeyBaselines перешифровывает версии, зашифрованные не текущим ключом cur. Соль версии шифруется вместе с
// содержимым и не меняется, поэтому keyed hash чувствительных значений остаются сравнимыми после ротации ключа.
// Запись обновляется, только если её не успели изменить (условие по key_id и шифротексту).
func (s *Store) rekeyBaselines(ctx context.Context, cur string) (int, error) {
	type row struct {
		org, team, cluster, bid, id, keyID string
		ct                                 []byte
	}
	rows, err := s.pool.Query(ctx, `SELECT org_id, team_id, cluster_id, baseline_id, id, content_enc, key_id FROM baseline_versions WHERE key_id <> $1`, cur)
	if err != nil {
		return 0, err
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.org, &r.team, &r.cluster, &r.bid, &r.id, &r.ct, &r.keyID); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range todo {
		aad := versionAAD(r.org, r.team, r.cluster, r.bid, r.id)
		plain, err := s.keys.Open(r.ct, r.keyID, aad)
		if err != nil {
			return n, fmt.Errorf("failed to decrypt a baseline version: %w", err)
		}
		ct, keyID, err := s.keys.Seal(plain, aad)
		if err != nil {
			return n, err
		}
		tag, err := s.pool.Exec(ctx, `
			UPDATE baseline_versions SET content_enc=$6, key_id=$7
			WHERE org_id=$1 AND team_id=$2 AND cluster_id=$3 AND baseline_id=$4 AND id=$5 AND key_id=$8 AND content_enc=$9`,
			r.org, r.team, r.cluster, r.bid, r.id, ct, keyID, r.keyID, r.ct)
		if err != nil {
			return n, err
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

// checkBaselinesKey проверяет, что связка ключей читает версии baseline: по одной записи на идентификатор ключа.
func (s *Store) checkBaselinesKey(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (key_id) org_id, team_id, cluster_id, baseline_id, id, content_enc, key_id FROM baseline_versions ORDER BY key_id, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var org, team, cluster, bid, id, keyID string
		var ct []byte
		if err := rows.Scan(&org, &team, &cluster, &bid, &id, &ct, &keyID); err != nil {
			return err
		}
		if _, err := s.keys.Open(ct, keyID, versionAAD(org, team, cluster, bid, id)); err != nil {
			return fmt.Errorf("a baseline version stored with encryption key %q cannot be read with the configured keys: %w", keyID, err)
		}
	}
	return rows.Err()
}
