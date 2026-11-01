// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package pgstore

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/24DSO-labs/kubron/internal/auth"
)

// Directory — auth.Directory: команды, участники, передача кластеров. Права проверяет вызывающий.
func (s *Store) Directory() auth.Directory { return directory{s} }

type directory struct{ s *Store }

func (d directory) DefaultTeam() string { return d.s.teamID }

func cleanTeamName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return "", errors.New("team name must be 1 to 64 characters")
	}
	return name, nil
}

func (d directory) Teams() ([]auth.Team, error) {
	if err := d.s.ready(); err != nil {
		return nil, err
	}
	ctx, cancel := opCtx()
	defer cancel()
	rows, err := d.s.pool.Query(ctx, `
		SELECT t.id, t.name,
		       (SELECT count(*) FROM memberships m WHERE m.team_id = t.id),
		       (SELECT count(*) FROM clusters c WHERE c.team_id = t.id)
		FROM teams t WHERE t.org_id = $1 ORDER BY lower(t.name), t.id`, d.s.orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auth.Team
	for rows.Next() {
		var t auth.Team
		if err := rows.Scan(&t.ID, &t.Name, &t.Members, &t.Clusters); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d directory) CreateTeam(name string) (*auth.Team, error) {
	if err := d.s.ready(); err != nil {
		return nil, err
	}
	name, err := cleanTeamName(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := opCtx()
	defer cancel()
	t := &auth.Team{Name: name}
	err = d.s.pool.QueryRow(ctx, `INSERT INTO teams (org_id, name) VALUES ($1, $2) RETURNING id`, d.s.orgID, name).Scan(&t.ID)
	if isUnique(err) {
		return nil, fmt.Errorf("%w: team %q", ErrConflict, name)
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (d directory) RenameTeam(id, name string) error {
	name, err := cleanTeamName(name)
	if err != nil {
		return err
	}
	ctx, cancel := opCtx()
	defer cancel()
	tag, err := d.s.pool.Exec(ctx, `UPDATE teams SET name = $2 WHERE id = $1 AND org_id = $3`, id, name, d.s.orgID)
	if isUnique(err) {
		return fmt.Errorf("%w: team %q", ErrConflict, name)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrTeamNotFound
	}
	return nil
}

func (d directory) DeleteTeam(id string) error {
	if id == d.s.teamID {
		return ErrDefaultTeam
	}
	ctx, cancel := opCtx()
	defer cancel()
	defer d.s.invalidateAccess()
	tag, err := d.s.pool.Exec(ctx, `DELETE FROM teams WHERE id = $1 AND org_id = $2`, id, d.s.orgID)
	if pgCode(err) == "23503" { // clusters.team_id RESTRICT
		return ErrTeamNotEmpty
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrTeamNotFound
	}
	return nil
}

func (d directory) Members(teamID string) ([]auth.Member, error) {
	ctx, cancel := opCtx()
	defer cancel()
	var exists bool
	if err := d.s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teams WHERE id = $1 AND org_id = $2)`, teamID, d.s.orgID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, auth.ErrTeamNotFound
	}
	rows, err := d.s.pool.Query(ctx, `
		SELECT u.id, u.username, m.role FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.team_id = $1 ORDER BY lower(u.username), u.id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auth.Member
	for rows.Next() {
		var m auth.Member
		var role string
		if err := rows.Scan(&m.UserID, &m.Username, &role); err != nil {
			return nil, err
		}
		m.Role = auth.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d directory) SetMember(teamID, userID string, role auth.Role) error {
	if !role.Valid() {
		return auth.ErrUnknownRole
	}
	ctx, cancel := opCtx()
	defer cancel()
	defer d.s.invalidateAccess()
	// Команда и пользователь — одной организации (внешние ключи этого не гарантируют).
	tag, err := d.s.pool.Exec(ctx, `
		INSERT INTO memberships (user_id, team_id, role)
		SELECT u.id, t.id, $3 FROM users u, teams t
		WHERE u.id = $1 AND t.id = $2 AND u.org_id = $4 AND t.org_id = $4
		ON CONFLICT (user_id, team_id) DO UPDATE SET role = EXCLUDED.role`, userID, teamID, string(role), d.s.orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrTeamNotFound
	}
	return nil
}

func (d directory) RemoveMember(teamID, userID string) error {
	ctx, cancel := opCtx()
	defer cancel()
	defer d.s.invalidateAccess()
	_, err := d.s.pool.Exec(ctx, `DELETE FROM memberships WHERE team_id = $1 AND user_id = $2`, teamID, userID)
	return err
}

func (d directory) MoveCluster(clusterID, teamID string) error {
	ctx, cancel := opCtx()
	defer cancel()
	defer d.s.invalidateAccess()
	err := pgx.BeginFunc(ctx, d.s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teams WHERE id = $1 AND org_id = $2)`, teamID, d.s.orgID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return auth.ErrTeamNotFound
		}
		tag, err := tx.Exec(ctx, `UPDATE clusters SET team_id = $2, updated_at = now() WHERE id = $1`, clusterID, teamID)
		if isUnique(err) { // в целевой команде уже есть кластер с таким именем
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrClusterNotFound
		}
		return nil
	})
	return err
}
