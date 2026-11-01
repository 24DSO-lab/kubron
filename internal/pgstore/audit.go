// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"encoding/json"
	"time"

	"github.com/24DSO-labs/kubron/pkg/ext"
)

// auditLine — формат строки журнала (совпадает с audit.Event); team/cluster — задача 3.5.
type auditLine struct {
	Time      time.Time `json:"time"`
	User      string    `json:"user"`
	Role      string    `json:"role,omitempty"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
	Detail    string    `json:"detail,omitempty"`
	Source    string    `json:"source,omitempty"`
	UserID    string    `json:"userId,omitempty"`
	TeamID    string    `json:"teamId,omitempty"`
	ClusterID string    `json:"clusterId,omitempty"`
}

// Audit — ext.AuditBackend поверх таблицы audit_events. Записи не удаляются и не меняются.
func (s *Store) Audit() ext.AuditBackend { return auditBackend{s} }

type auditBackend struct{ s *Store }

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (b auditBackend) Append(line []byte) error {
	var e auditLine
	if err := json.Unmarshal(line, &e); err != nil {
		return err
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	switch e.Result {
	case "ok", "failed", "denied":
	default:
		e.Result = "failed"
	}
	ctx, cancel := opCtx()
	defer cancel()
	_, err := b.s.pool.Exec(ctx, `
		INSERT INTO audit_events (ts, username, role, action, result, detail, source, user_id, team_id, cluster_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.Time, e.User, e.Role, e.Action, e.Result, e.Detail, e.Source,
		nullable(e.UserID), nullable(e.TeamID), nullable(e.ClusterID))
	return err
}

func (b auditBackend) ScanNewestFirst(fn func(line []byte) bool) error {
	ctx, cancel := opCtx()
	defer cancel()
	rows, err := b.s.pool.Query(ctx, `
		SELECT ts, username, role, action, result, detail, source, COALESCE(user_id,''), COALESCE(team_id,''), COALESCE(cluster_id,'')
		FROM audit_events ORDER BY ts DESC, id DESC`)
	if err != nil {
		return err
	}
	defer rows.Close() // досрочный выход закрывает курсор
	for rows.Next() {
		var e auditLine
		if err := rows.Scan(&e.Time, &e.User, &e.Role, &e.Action, &e.Result, &e.Detail, &e.Source, &e.UserID, &e.TeamID, &e.ClusterID); err != nil {
			return err
		}
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if !fn(raw) {
			return nil
		}
	}
	return rows.Err()
}
