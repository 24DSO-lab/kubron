// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"strconv"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/internal/teams"
)

// Управление командами существует только в сервисном режиме (PostgreSQL). В десктопе и в личном режиме
// команд нет, методы возвращают понятную ошибку.
var errNoTeams = teams.ErrNoTeams

var errNotSharedAccount = errors.New("the cluster does not use a shared team account")

// TeamView — команда в списке пользователя: общие сведения и его роль в ней.
type TeamView struct {
	auth.Team
	// MyRole — роль текущего пользователя в команде; пусто, если он не член (администратор организации видит все команды).
	MyRole string `json:"myRole"`
}

// recordTeam записывает в журнал результат уже разрешённого действия над командами.
func (a *App) recordTeam(action, result, teamID, detail string) {
	if a.audit == nil || a.auth == nil {
		return
	}
	act := a.auth.Actor()
	a.audit.Record(audit.Event{User: act.Username, Role: string(act.Role), Action: action, Result: result, Detail: detail, Source: act.Source, TeamID: teamID})
}

// teamSvc — управление командами от имени сессии этого вида. Создаётся на вызов: у каждого вида своя сессия.
func (a *App) teamSvc() *teams.Service {
	var acc teams.Access
	if a.auth != nil { // не передаём типизированный nil в интерфейс
		acc = a.auth
	}
	var rec teams.Recorder
	if a.audit != nil {
		rec = a.audit
	}
	return teams.New(a.dir, acc, rec, a.revokeTeamClusters)
}

// ListTeams — команды пользователя; администратор организации видит все.
func (a *App) ListTeams() ([]TeamView, error) {
	if err := a.authzAny(auth.PermRead, auth.PermUsersManage); err != nil {
		return nil, err
	}
	entries, err := a.teamSvc().List()
	if err != nil {
		return nil, err
	}
	out := make([]TeamView, 0, len(entries))
	for _, e := range entries {
		out = append(out, TeamView{Team: e.Team, MyRole: e.MyRole})
	}
	return out, nil
}

// isOrgAdmin — текущий пользователь администрирует организацию (право users.manage вне команд).
func (a *App) isOrgAdmin() bool {
	return a.teamSvc().IsOrgAdmin()
}

// CreateTeam создаёт команду (администратор организации).
func (a *App) CreateTeam(name string) (*auth.Team, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return nil, err
	}
	return a.teamSvc().Create(name)
}

// RenameTeam переименовывает команду (её администратор или администратор организации).
func (a *App) RenameTeam(teamID, name string) error {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return err
	}
	return a.teamSvc().Rename(teamID, name)
}

// DeleteTeam удаляет пустую команду без кластеров (администратор организации).
func (a *App) DeleteTeam(teamID string) error {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return err
	}
	return a.teamSvc().Delete(teamID)
}

// ListTeamMembers — участники команды.
func (a *App) ListTeamMembers(teamID string) ([]auth.Member, error) {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return nil, err
	}
	return a.teamSvc().Members(teamID)
}

// SetTeamMember добавляет пользователя в команду или меняет его роль в ней.
func (a *App) SetTeamMember(teamID, userID, role string) error {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return err
	}
	return a.teamSvc().SetMember(teamID, userID, role)
}

// RemoveTeamMember исключает пользователя из команды.
func (a *App) RemoveTeamMember(teamID, userID string) error {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return err
	}
	return a.teamSvc().RemoveMember(teamID, userID)
}

// AddClusterToTeam добавляет кластер (по тексту kubeconfig) в команду; право — администратор команды.
// Общая учётная запись (settings.SharedAccount) выбирается здесь же: право то же, вход вводит администратор кластера
// при первом подключении, а не в этом вызове.
func (a *App) AddClusterToTeam(teamID, kubeconfigText string, settings k8s.ClusterSettings) (*k8s.ClusterMeta, error) {
	if err := a.authzTeam(auth.PermClusterManage, teamID, "team", teamID, "displayName", settings.DisplayName,
		"sharedAccount", strconv.FormatBool(settings.SharedAccount)); err != nil {
		return nil, err
	}
	if a.dir == nil {
		return nil, errNoTeams
	}
	return k8s.AddClusterFromTextToTeam(kubeconfigText, settings, teamID)
}

// AddInClusterToTeam добавляет кластер, в котором работает сам сервер (под его ServiceAccount). Это доступ с правами
// пода, поэтому, кроме администратора команды, нужен администратор организации.
func (a *App) AddInClusterToTeam(teamID string, settings k8s.ClusterSettings) (*k8s.ClusterMeta, error) {
	if err := a.authzTeam(auth.PermClusterManage, teamID, "team", teamID, "displayName", settings.DisplayName, "mode", k8s.ModeInCluster); err != nil {
		return nil, err
	}
	if a.dir == nil {
		return nil, errNoTeams
	}
	if !a.isOrgAdmin() {
		return nil, auth.ErrForbidden
	}
	return k8s.AddInClusterToTeam(settings, teamID)
}

// ResetClusterSharedAccount забывает сохранённый вход общей учётной записи команды (например, сменился пароль в
// провайдере входа). Следующее подключение попросит администратора кластера войти заново; остальные участники до
// тех пор увидят просьбу обратиться к администратору. Пароль нигде не вводится: вход идёт через брокер.
func (a *App) ResetClusterSharedAccount(clusterID string) error {
	if err := a.authzCluster(auth.PermClusterManage, clusterID, "id", clusterID, "mode", k8s.ModeTeamAccount); err != nil {
		return err
	}
	meta, err := k8s.LoadClusterMeta(clusterID)
	if err != nil {
		return err
	}
	if meta.Mode != k8s.ModeTeamAccount {
		return errNotSharedAccount
	}
	if err := a.dropTeamSignIn(clusterID); err != nil {
		return err
	}
	if a.k8sMgr.RemoveConnection(clusterID) {
		a.pfMgr.StopAll()
	}
	return nil
}

// MoveCluster передаёт кластер другой команде. Нужны права администратора и у текущей, и у новой команды.
func (a *App) MoveCluster(clusterID, teamID string) error {
	if err := a.authzCluster(auth.PermClusterManage, clusterID, "id", clusterID, "team", teamID); err != nil {
		return err
	}
	if a.dir == nil {
		return errNoTeams
	}
	if err := a.auth.AuthorizeTeam(auth.PermClusterManage, teamID); err != nil {
		return err
	}
	if err := a.dir.MoveCluster(clusterID, teamID); err != nil {
		return err
	}
	// Подключения и проброшенные порты у тех, кто потерял доступ, закроются при следующей проверке прав;
	// у владельца этого вида кластер остаётся активным только если он всё ещё видит его.
	return nil
}

// ListTeamUsers — пользователи, которых можно добавить в команду (только имена): для администратора команды,
// у которого нет права users.manage на всю организацию.
func (a *App) ListTeamUsers(teamID string) ([]auth.UserBrief, error) {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return nil, err
	}
	if a.dir == nil {
		return nil, errNoTeams
	}
	return a.auth.UserBriefs(), nil
}
