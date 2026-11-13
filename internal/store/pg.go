// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/baseline"
	"github.com/24DSO-lab/kubron/internal/clusterhealth"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/pgstore"
	"github.com/24DSO-lab/kubron/internal/websession"
	"github.com/24DSO-lab/kubron/pkg/ext"
)

// FromPostgres адаптирует хранилище сервиса на PostgreSQL (internal/pgstore) к Store.
func FromPostgres(s *pgstore.Store) Store { return pgBackend{s} }

type pgBackend struct{ s *pgstore.Store }

func (p pgBackend) Users() auth.Backend {
	return pgUsers{Backend: auth.FromExt(p.s.Users()), d: p.s.UserDelta(), f: p.s.LoginFailures()}
}
func (p pgBackend) Audit() audit.Backend        { return p.s.Audit() }
func (p pgBackend) Baselines() baseline.Backend { return p.s.Baselines() }
func (p pgBackend) ArgoTokens() k8s.TokenStore  { return p.s.ArgoTokens() }

func (p pgBackend) WebOIDC() (WebOIDC, error) {
	v, err := p.s.WebOIDC()
	return WebOIDC{ClientID: v.ClientID, IssuerURL: v.IssuerURL}, err
}

func (p pgBackend) SetWebOIDC(v WebOIDC) error {
	return p.s.SetWebOIDC(pgstore.WebOIDC{ClientID: v.ClientID, IssuerURL: v.IssuerURL})
}

// Access — права по членству в командах (задача 3.2); app подключает их к сервису аутентификации.
func (p pgBackend) Access() auth.Access { return p.s.Access() }

// HealthSink и HealthLeader — общее хранилище состояний кластеров и выбор реплики-проверяющего (3.6).
func (p pgBackend) HealthSink() clusterhealth.Sink     { return p.s.Health() }
func (p pgBackend) HealthLeader() clusterhealth.Leader { return p.s.Leader(pgstore.LeaderKey) }

// Sessions — общее хранилище веб-сессий (ADR-0016): сессия, выданная одной репликой, действует на всех.
func (p pgBackend) Sessions() websession.Backend { return p.s.Sessions() }

// Directory — команды и участники (задача 3.3).
func (p pgBackend) Directory() auth.Directory  { return p.s.Directory() }
func (p pgBackend) Clusters() k8s.ClusterStore { return pgClusters{p.s.Clusters()} }

// pgClusters приводит pgstore.Clusters к k8s.ClusterStore. Файла kubeconfig у кластера в БД нет
// (kubeconfigPather не реализован): клиент получает байты.
type pgClusters struct{ c *pgstore.Clusters }

func (p pgClusters) List() ([]k8s.ClusterMeta, error) {
	list, err := p.c.List()
	if err != nil {
		return nil, err
	}
	out := make([]k8s.ClusterMeta, 0, len(list))
	for i := range list {
		out = append(out, toMeta(&list[i]))
	}
	return out, nil
}

func (p pgClusters) Get(id string) (*k8s.ClusterMeta, error) {
	c, err := p.c.Get(id)
	if err != nil {
		return nil, err
	}
	m := toMeta(c)
	return &m, nil
}

func (p pgClusters) Kubeconfig(id string) ([]byte, error) { return p.c.Kubeconfig(id) }

func (p pgClusters) Save(m *k8s.ClusterMeta, kubeconfig []byte) error {
	if m == nil {
		return errors.New("cluster is not set")
	}
	return p.c.Save(toPG(m), kubeconfig)
}

// SaveWithSecret и Secret — режимы in-cluster и oidc-password (задача 3.7): секрет шифруется хранилищем.
func (p pgClusters) SaveWithSecret(m *k8s.ClusterMeta, kubeconfig, secret []byte) error {
	if m == nil {
		return errors.New("cluster is not set")
	}
	return p.c.SaveWithSecret(toPG(m), kubeconfig, secret)
}

func (p pgClusters) Secret(id string) ([]byte, error) { return p.c.Secret(id) }

func toPG(m *k8s.ClusterMeta) *pgstore.Cluster {
	return &pgstore.Cluster{
		ID: m.ID, DisplayName: m.DisplayName, Icon: m.Icon, Color: m.Color, Environment: m.Environment,
		DefaultNamespace: m.DefaultNamespace, ContextName: m.ContextName, AddedAt: m.AddedAt, TeamID: m.TeamID,
		Mode: m.Mode, ArgoURL: m.ArgoURL,
	}
}

func (p pgClusters) Delete(id string) error { return p.c.Delete(id) }

func toMeta(c *pgstore.Cluster) k8s.ClusterMeta {
	return k8s.ClusterMeta{
		ID: c.ID, DisplayName: c.DisplayName, Icon: c.Icon, Color: c.Color, Environment: c.Environment,
		DefaultNamespace: c.DefaultNamespace, ContextName: c.ContextName, AddedAt: c.AddedAt, TeamID: c.TeamID,
		Mode: c.Mode, ArgoURL: c.ArgoURL,
	}
}

// pgUsers — пользователи в PostgreSQL: помимо Load/Save (заменяют набор целиком) реализует auth.DeltaBackend,
// чтобы несколько реплик не затирали изменения друг друга.
type pgUsers struct {
	auth.Backend
	d pgstore.UserDelta
	f *pgstore.LoginFailures
}

var (
	_ auth.DeltaBackend     = pgUsers{}
	_ auth.FirstUserBackend = pgUsers{}
	_ auth.FailureBackend   = pgUsers{}
)

// Блокировки входа по логину общие для реплик (ADR-0016, раздел 5).
func (u pgUsers) LockRemaining(ctx context.Context, key string) (time.Duration, error) {
	return u.f.LockRemaining(ctx, key)
}
func (u pgUsers) RecordFailure(ctx context.Context, key string, free int, base, max time.Duration) error {
	return u.f.RecordFailure(ctx, key, free, base, max)
}
func (u pgUsers) ClearFailures(ctx context.Context, key string) error {
	return u.f.ClearFailures(ctx, key)
}

func (u pgUsers) UpsertUser(r auth.UserRecord) error { return u.d.UpsertUser(toExtUser(r)) }
func (u pgUsers) InsertFirstUser(r auth.UserRecord) (bool, error) {
	return u.d.InsertFirstUser(toExtUser(r))
}
func (u pgUsers) TouchLogin(id string, at time.Time, passwordHash string) error {
	return u.d.TouchLogin(id, at, passwordHash)
}
func (u pgUsers) DeleteUser(id string) error { return u.d.DeleteUser(id) }
func (u pgUsers) SaveSettings(s auth.SecuritySettings) error {
	return u.d.SaveSettings(ext.SecuritySettings{IdleTimeoutMinutes: s.IdleTimeoutMinutes})
}

func toExtUser(r auth.UserRecord) ext.UserRecord {
	return ext.UserRecord{
		ID: r.ID, Username: r.Username, Role: string(r.Role), PasswordHash: r.PasswordHash,
		Disabled: r.Disabled, MustChangePassword: r.MustChangePassword,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, LastLoginAt: r.LastLoginAt,
	}
}

// HelmState — состояние Helm в БД по командам (ADR-0015). Подключается так же, как Access: сервер проверяет
// наличие метода и передаёт хранилище в k8s.SetHelmStateStore; в десктопе его нет, и Helm остаётся на файлах.
func (p pgBackend) HelmState() k8s.HelmStateStore { return pgHelm{p.s.Helm()} }

// pgHelm приводит pgstore.Helm к k8s.HelmStateStore.
type pgHelm struct{ h *pgstore.Helm }

func (pgHelm) CredentialsInStore() bool { return true }

func (p pgHelm) ListRepos(team string) ([]k8s.HelmRepoRecord, error) {
	list, err := p.h.List(team)
	if err != nil {
		return nil, err
	}
	out := make([]k8s.HelmRepoRecord, 0, len(list))
	for _, r := range list {
		out = append(out, k8s.HelmRepoRecord{
			Name: r.Name, Kind: r.Kind, URL: r.URL, Username: r.Username, Password: r.Secret,
			InsecureTLS: r.Options.InsecureTLS, PlainHTTP: r.Options.PlainHTTP, Charts: r.Options.Charts,
			CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

func (p pgHelm) CreateRepo(team string, r *k8s.HelmRepoRecord) error {
	err := p.h.Create(team, pgstore.HelmRepo{
		Name: r.Name, Kind: r.Kind, URL: r.URL, Username: r.Username, Secret: r.Password,
		Options:   pgstore.HelmRepoOptions{InsecureTLS: r.InsecureTLS, PlainHTTP: r.PlainHTTP, Charts: r.Charts},
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
	})
	return mapHelmErr(err)
}

func (p pgHelm) SetOCICharts(team, name string, charts []string) error {
	return mapHelmErr(p.h.SetCharts(team, name, charts))
}

func (p pgHelm) DeleteRepo(team, name string) (*k8s.HelmRepoRecord, error) {
	r, err := p.h.Delete(team, name)
	if err != nil {
		return nil, mapHelmErr(err)
	}
	return &k8s.HelmRepoRecord{Name: r.Name, Kind: r.Kind, URL: r.URL, Username: r.Username,
		InsecureTLS: r.Options.InsecureTLS, PlainHTTP: r.Options.PlainHTTP, Charts: r.Options.Charts}, nil
}

func (p pgHelm) AddOperation(team, clusterID string, r k8s.HelmOperationRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	finished, err := time.Parse(time.RFC3339, r.FinishedAt)
	if err != nil {
		finished = time.Now()
	}
	return p.h.AddOperation(team, clusterID, r.ID, finished, data)
}

func (p pgHelm) ListOperations(team string) ([]k8s.HelmOperationRecord, error) {
	raw, err := p.h.Operations(team)
	if err != nil {
		return nil, err
	}
	out := make([]k8s.HelmOperationRecord, 0, len(raw))
	for _, b := range raw {
		var r k8s.HelmOperationRecord
		if err := json.Unmarshal(b, &r); err != nil {
			continue // повреждённая запись не должна скрывать остальной журнал
		}
		out = append(out, r)
	}
	return out, nil
}

func (p pgHelm) GetOperation(team, id string) (*k8s.HelmOperationRecord, error) {
	b, err := p.h.Operation(team, id)
	if err != nil {
		return nil, mapHelmErr(err)
	}
	var r k8s.HelmOperationRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (p pgHelm) ClearOperations(team string) error { return p.h.ClearOperations(team) }

func mapHelmErr(err error) error {
	switch {
	case errors.Is(err, pgstore.ErrHelmExists):
		return k8s.ErrHelmRepoExists
	case errors.Is(err, pgstore.ErrHelmNotFound):
		return k8s.ErrHelmNotFound
	}
	return err
}
