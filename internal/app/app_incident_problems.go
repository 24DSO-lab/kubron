// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"

	"github.com/24DSO-lab/kubron/internal/auth"
)

// errIncidentNotImplemented — заглушка скелета D7: права уже проверяются, логика появится в следующих PR.
var errIncidentNotImplemented = errors.New("not implemented")

// IncidentProblemList возвращает страницу проблем кластера, новые первыми.
func (a *App) IncidentProblemList(clusterID string, q IncidentProblemListQuery) (ProblemListDTO, error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return ProblemListDTO{}, err
	}
	_ = q
	return ProblemListDTO{}, errIncidentNotImplemented
}

// IncidentProblemGet возвращает проблему с составом и затронутыми workload'ами.
func (a *App) IncidentProblemGet(clusterID, problemID string) (ProblemDetailDTO, error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return ProblemDetailDTO{}, err
	}
	_ = problemID
	return ProblemDetailDTO{}, errIncidentNotImplemented
}

// IncidentProblemAttach добавляет расследование в проблему вручную.
func (a *App) IncidentProblemAttach(clusterID, problemID, incidentID string) (ProblemDetailDTO, error) {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return ProblemDetailDTO{}, err
	}
	_, _ = problemID, incidentID
	return ProblemDetailDTO{}, errIncidentNotImplemented
}

// IncidentProblemDetach выводит расследование из проблемы; автоприсоединение его больше не вернёт.
func (a *App) IncidentProblemDetach(clusterID, incidentID string) error {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return err
	}
	_ = incidentID
	return errIncidentNotImplemented
}

// IncidentProblemMerge объединяет проблему fromID в toID.
func (a *App) IncidentProblemMerge(clusterID, fromID, toID string) (ProblemDetailDTO, error) {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return ProblemDetailDTO{}, err
	}
	_, _ = fromID, toID
	return ProblemDetailDTO{}, errIncidentNotImplemented
}

// IncidentProblemCreate создаёт проблему вручную из выбранных расследований.
func (a *App) IncidentProblemCreate(clusterID string, incidentIDs []string) (ProblemDetailDTO, error) {
	if err := a.authzCluster(auth.PermOperate, clusterID); err != nil {
		return ProblemDetailDTO{}, err
	}
	_ = incidentIDs
	return ProblemDetailDTO{}, errIncidentNotImplemented
}

// IncidentRuleStats возвращает статистику правил по закрытым расследованиям кластера.
func (a *App) IncidentRuleStats(clusterID string) (RuleStatsDTO, error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return RuleStatsDTO{}, err
	}
	return RuleStatsDTO{}, errIncidentNotImplemented
}

// IncidentRuleStatsExport выгружает анонимную статистику правил файлом. Право — то же, что у настроек хранения
// расследований (users.manage).
func (a *App) IncidentRuleStatsExport(clusterID string) (ExportFileDTO, error) {
	if err := a.authzCluster(auth.PermRead, clusterID); err != nil {
		return ExportFileDTO{}, err
	}
	if err := a.authz(auth.PermUsersManage); err != nil {
		return ExportFileDTO{}, err
	}
	return ExportFileDTO{}, errIncidentNotImplemented
}
