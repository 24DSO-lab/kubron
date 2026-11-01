// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/24DSO-labs/kubron/internal/audit"
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

// Управление командами существует только в сервисном режиме (PostgreSQL). В десктопе и в личном режиме
// команд нет, методы возвращают понятную ошибку.
var errNoTeams = errors.New("teams are available only in service mode")

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

func resultOf(err error) string {
	if err != nil {
		return audit.ResultFailed
	}
	return audit.ResultOK
}

// ListTeams — команды пользователя; администратор организации видит все.
func (a *App) ListTeams() ([]TeamView, error) {
	if err := a.authzAny(auth.PermRead, auth.PermUsersManage); err != nil {
		return nil, err
	}
	if a.dir == nil {
		return nil, errNoTeams
	}
	teams, err := a.dir.Teams()
	if err != nil {
		return nil, err
	}
	org := a.isOrgAdmin()
	out := make([]TeamView, 0, len(teams))
	for _, t := range teams {
		v := TeamView{Team: t}
		if actor, err := a.auth.CheckTeam(auth.PermRead, t.ID); err == nil {
			v.MyRole = string(actor.Role)
		}
		if v.MyRole != "" || org { // чужие команды видит только администратор организации
			out = append(out, v)
		}
	}
	return out, nil
}

// isOrgAdmin — текущий пользователь администрирует организацию (право users.manage вне команд).
func (a *App) isOrgAdmin() bool {
	return a.auth != nil && a.auth.Authorize(auth.PermUsersManage) == nil
}

// CreateTeam создаёт команду (администратор организации).
func (a *App) CreateTeam(name string) (*auth.Team, error) {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return nil, err
	}
	if a.dir == nil {
		return nil, errNoTeams
	}
	t, err := a.dir.CreateTeam(name)
	id := ""
	if t != nil {
		id = t.ID
	}
	a.recordTeam("CreateTeam", resultOf(err), id, "name="+strings.TrimSpace(name))
	return t, err
}

// RenameTeam переименовывает команду (её администратор или администратор организации).
func (a *App) RenameTeam(teamID, name string) error {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return err
	}
	if a.dir == nil {
		return errNoTeams
	}
	err := a.dir.RenameTeam(teamID, name)
	a.recordTeam("RenameTeam", resultOf(err), teamID, "name="+strings.TrimSpace(name))
	return err
}

// DeleteTeam удаляет пустую команду без кластеров (администратор организации).
func (a *App) DeleteTeam(teamID string) error {
	if err := a.authz(auth.PermUsersManage); err != nil {
		return err
	}
	if a.dir == nil {
		return errNoTeams
	}
	err := a.dir.DeleteTeam(teamID)
	a.recordTeam("DeleteTeam", resultOf(err), teamID, "")
	return err
}

// ListTeamMembers — участники команды.
func (a *App) ListTeamMembers(teamID string) ([]auth.Member, error) {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return nil, err
	}
	if a.dir == nil {
		return nil, errNoTeams
	}
	return a.dir.Members(teamID)
}

// SetTeamMember добавляет пользователя в команду или меняет его роль в ней.
func (a *App) SetTeamMember(teamID, userID, role string) error {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return err
	}
	if a.dir == nil {
		return errNoTeams
	}
	err := a.dir.SetMember(teamID, userID, auth.Role(strings.TrimSpace(role)))
	a.recordTeam("SetTeamMember", resultOf(err), teamID, fmt.Sprintf("user=%s role=%s", userID, role))
	return err
}

// RemoveTeamMember исключает пользователя из команды.
func (a *App) RemoveTeamMember(teamID, userID string) error {
	if err := a.authzTeam(auth.PermUsersManage, teamID); err != nil {
		return err
	}
	if a.dir == nil {
		return errNoTeams
	}
	err := a.dir.RemoveMember(teamID, userID)
	a.recordTeam("RemoveTeamMember", resultOf(err), teamID, "user="+userID)
	return err
}

// AddClusterToTeam добавляет кластер (по тексту kubeconfig) в команду; право — администратор команды.
func (a *App) AddClusterToTeam(teamID, kubeconfigText string, settings k8s.ClusterSettings) (*k8s.ClusterMeta, error) {
	if err := a.authzTeam(auth.PermClusterManage, teamID, "team", teamID, "displayName", settings.DisplayName); err != nil {
		return nil, err
	}
	if a.dir == nil {
		return nil, errNoTeams
	}
	return k8s.AddClusterFromTextToTeam(kubeconfigText, settings, teamID)
}

// AddOIDCClusterToTeam добавляет кластер, kubeconfig которого использует oidc-login с паролем (grant password):
// сервер сам получает и обновляет токен, а логин и пароль хранит зашифрованными. Пароль в журнал не попадает.
func (a *App) AddOIDCClusterToTeam(teamID, kubeconfigText string, settings k8s.ClusterSettings, cred k8s.OIDCCredentials) (*k8s.ClusterMeta, error) {
	if err := a.authzTeam(auth.PermClusterManage, teamID, "team", teamID, "displayName", settings.DisplayName, "mode", k8s.ModeOIDCPassword); err != nil {
		return nil, err
	}
	if a.dir == nil {
		return nil, errNoTeams
	}
	return k8s.AddClusterOIDCToTeam(kubeconfigText, settings, teamID, cred)
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

// SetClusterOIDCCredentials меняет логин и пароль кластера в режиме oidc-password (например, сменился пароль в
// провайдере входа). Вход проверяется до сохранения; открытое подключение пересоздаётся при следующем обращении.
func (a *App) SetClusterOIDCCredentials(clusterID string, cred k8s.OIDCCredentials) error {
	if err := a.authzCluster(auth.PermClusterManage, clusterID, "id", clusterID, "mode", k8s.ModeOIDCPassword); err != nil {
		return err
	}
	if err := k8s.SetClusterOIDCCredentials(clusterID, cred); err != nil {
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
