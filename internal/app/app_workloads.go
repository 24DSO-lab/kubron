// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/24DSO-labs/kubron/internal/auth"
	"github.com/24DSO-labs/kubron/internal/k8s"
)

func (a *App) GetNamespaces() ([]k8s.NamespaceInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetNamespaces()
}

func (a *App) GetPods(namespaces []string) ([]k8s.PodInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPods(namespaces)
}

func (a *App) GetPodDetail(namespace, name string) (*k8s.PodDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetPodDetail(namespace, name)
}

func (a *App) GetPodLogs(namespace, name, container string, lines int64) (string, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return "", err
	}
	return a.k8sMgr.GetPodLogs(namespace, name, container, lines)
}

func (a *App) EvictPod(namespace, name string) error {
	if err := a.authz(auth.PermWrite, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.EvictPod(namespace, name)
}

func (a *App) GetDeployments(namespaces []string) ([]k8s.DeploymentInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetDeployments(namespaces)
}

func (a *App) ScaleDeployment(namespace, name string, replicas int32) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "replicas", replicas); err != nil {
		return err
	}
	return a.k8sMgr.ScaleDeployment(namespace, name, replicas)
}

func (a *App) RestartDeployment(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RestartDeployment(namespace, name)
}

func (a *App) GetDeploymentRevisions(namespace, name string) ([]k8s.RevisionInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetDeploymentRevisions(namespace, name)
}

func (a *App) RollbackDeployment(namespace, name string, revision int64) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "revision", revision); err != nil {
		return err
	}
	return a.k8sMgr.RollbackDeployment(namespace, name, revision)
}

func (a *App) GetStatefulSets(namespaces []string) ([]k8s.StatefulSetInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetStatefulSets(namespaces)
}

func (a *App) ScaleStatefulSet(namespace, name string, replicas int32) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "replicas", replicas); err != nil {
		return err
	}
	return a.k8sMgr.ScaleStatefulSet(namespace, name, replicas)
}

func (a *App) GetReplicaSets(namespaces []string) ([]k8s.ReplicaSetInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetReplicaSets(namespaces)
}

func (a *App) ScaleReplicaSet(namespace, name string, replicas int32) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name, "replicas", replicas); err != nil {
		return err
	}
	return a.k8sMgr.ScaleReplicaSet(namespace, name, replicas)
}

func (a *App) GetDaemonSets(namespaces []string) ([]k8s.DaemonSetInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetDaemonSets(namespaces)
}

func (a *App) GetJobs(namespaces []string) ([]k8s.JobInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetJobs(namespaces)
}

func (a *App) RestartDaemonSet(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RestartDaemonSet(namespace, name)
}

func (a *App) RetryJob(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RetryJob(namespace, name)
}

func (a *App) GetCronJobs(namespaces []string) ([]k8s.CronJobInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCronJobs(namespaces)
}

func (a *App) GetCronJobJobs(namespace, cronJobName string) ([]k8s.JobInfo, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCronJobJobs(namespace, cronJobName)
}

func (a *App) GetCronJobDetail(namespace, name string) (*k8s.CronJobDetail, error) {
	if err := a.authz(auth.PermRead); err != nil {
		return nil, err
	}
	return a.k8sMgr.GetCronJobDetail(namespace, name)
}

func (a *App) TriggerCronJob(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.TriggerCronJob(namespace, name)
}

func (a *App) ToggleCronJobSuspend(namespace, name string) (bool, error) {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return false, err
	}
	return a.k8sMgr.ToggleCronJobSuspend(namespace, name)
}

func (a *App) RestartStatefulSet(namespace, name string) error {
	if err := a.authz(auth.PermOperate, "namespace", namespace, "name", name); err != nil {
		return err
	}
	return a.k8sMgr.RestartStatefulSet(namespace, name)
}
