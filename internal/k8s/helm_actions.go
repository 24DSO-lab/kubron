// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/kube"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// Rollback и uninstall ждут готовности ресурсов; больше — считаем операцию зависшей.
const helmActionTimeout = 5 * time.Minute

// helmConfigForRelease — конфигурация Helm с тем драйвером хранилища, где лежит релиз (Secret или ConfigMap):
// иначе Helm не найдёт ревизии. Не найден релиз — Secret по умолчанию, ошибку «не найден» вернёт сам Helm.
func (m *Manager) helmConfigForRelease(ctx context.Context, namespace, name string, h slog.Handler) (*action.Configuration, error) {
	storage, err := m.helmStorageOf(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to determine the storage of release %s/%s: %w", namespace, name, err)
	}
	return m.helmConfigDriver(namespace, h, storage)
}

// helmConfigDriver создаёт action.Configuration для активного кластера и namespace релиза с
// собственным обработчиком журнала Helm (прогресс операций; nil — журнал по умолчанию) и
// выбором хранилища релизов (secret | configmap).
// Подключаемся к тому же кластеру, что и основное подключение приложения (см. helmRESTClientGetter).
// Обработчик нужно задать ДО Init: Init раздаёт логгер клиенту Kubernetes и ожидателю готовности.
func (m *Manager) helmConfigDriver(namespace string, h slog.Handler, storage string) (*action.Configuration, error) {
	driver, err := helmDriverName(storage)
	if err != nil {
		return nil, err
	}
	id := m.GetActiveContext()
	if id == "" {
		return nil, errors.New("no active connection")
	}
	meta, err := LoadClusterMeta(id)
	if err != nil {
		return nil, fmt.Errorf("cluster metadata not found: %w", err)
	}
	getter, err := m.helmRESTClientGetter(id, meta, namespace)
	if err != nil {
		return nil, err
	}

	cfg := new(action.Configuration)
	if h != nil {
		cfg.SetLogger(h)
	}
	if err := cfg.Init(getter, namespace, driver); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm: %w", err)
	}
	return cfg, nil
}

// helmRESTClientGetter выбирает, как Helm получает доступ к кластеру:
//   - личный режим (десктоп): у kubeconfig-кластера есть файл — Helm читает его, как и прежде;
//   - всё остальное (кластеры из PostgreSQL в любом режиме: kubeconfig, in-cluster, oidc-password): rest.Config
//     действующего подключения менеджера, без файлов и без выписывания учётных данных на диск.
func (m *Manager) helmRESTClientGetter(id string, meta *ClusterMeta, namespace string) (genericclioptions.RESTClientGetter, error) {
	if meta.Mode == "" || meta.Mode == ModeKubeconfig {
		if kubeconfig := GetClusterKubeconfigPath(id); kubeconfig != "" {
			flags := genericclioptions.NewConfigFlags(false)
			flags.KubeConfig = &kubeconfig
			flags.Namespace = &namespace
			if meta.ContextName != "" {
				ctxName := meta.ContextName
				flags.Context = &ctxName
			}
			return flags, nil
		}
	}
	conn, ok := m.pool.get(id)
	if !ok {
		return nil, errors.New("no active connection")
	}
	return newRESTConfigGetter(conn.helmRESTConfig(), namespace), nil
}

// RollbackHelmRelease откатывает релиз к ревизии. Helm создаёт НОВУЮ ревизию с содержимым старой.
func (m *Manager) RollbackHelmRelease(namespace, name string, revision int, opID string) (err error) {
	if err := validateHelmRef(namespace, name); err != nil {
		return err
	}
	if revision <= 0 {
		return fmt.Errorf("invalid revision %d", revision)
	}
	_, tr, err := m.beginHelmOp(context.Background(), opID, "rollback", namespace, name, false)
	if err != nil {
		return err
	}
	defer func() { tr.finish(err) }()

	tr.note(fmt.Sprintf("to revision %d", revision))
	tr.setRevision(revision)
	tr.setPhase(fmt.Sprintf("Rolling back to revision %d…", revision))
	cfg, err := m.helmConfigForRelease(context.Background(), namespace, name, tr.handler())
	if err != nil {
		return err
	}
	a := action.NewRollback(cfg)
	a.Version = revision
	a.Timeout = helmActionTimeout
	a.WaitStrategy = kube.StatusWatcherStrategy
	if err := a.Run(name); err != nil {
		return fmt.Errorf("rollback of %s/%s to revision %d: %w", namespace, name, revision, err)
	}
	return nil
}

// UninstallHelmRelease удаляет релиз и его ресурсы. keepHistory оставляет записи ревизий
// (тогда имя релиза можно переиспользовать только через upgrade --install).
func (m *Manager) UninstallHelmRelease(namespace, name string, keepHistory bool, opID string) (err error) {
	if err := validateHelmRef(namespace, name); err != nil {
		return err
	}
	_, tr, err := m.beginHelmOp(context.Background(), opID, "uninstall", namespace, name, false)
	if err != nil {
		return err
	}
	defer func() { tr.finish(err) }()

	if keepHistory {
		tr.note("keeping history")
	}
	tr.setPhase("Uninstalling release…")
	cfg, err := m.helmConfigForRelease(context.Background(), namespace, name, tr.handler())
	if err != nil {
		return err
	}
	a := action.NewUninstall(cfg)
	a.KeepHistory = keepHistory
	a.Timeout = helmActionTimeout
	a.WaitStrategy = kube.StatusWatcherStrategy
	if _, err := a.Run(name); err != nil {
		return fmt.Errorf("uninstall %s/%s: %w", namespace, name, err)
	}
	return nil
}

// HelmRevisionDiff — различия двух ревизий. Манифест уже с замаскированными Secret.
type HelmRevisionDiff struct {
	From     int    `json:"from"`
	To       int    `json:"to"`
	Values   string `json:"values"`   // user-supplied values, unified diff; "" — без различий
	Manifest string `json:"manifest"` // unified diff манифестов; "" — без различий
}

// GetHelmRevisionDiff сравнивает ревизии from → to. Требует права на секреты:
// в values могут быть пароли, поэтому метод защищён так же, как GetHelmReleaseContent.
func (m *Manager) GetHelmRevisionDiff(namespace, name string, from, to int) (*HelmRevisionDiff, error) {
	a, err := m.GetHelmReleaseContent(namespace, name, from)
	if err != nil {
		return nil, err
	}
	b, err := m.GetHelmReleaseContent(namespace, name, to)
	if err != nil {
		return nil, err
	}
	return &HelmRevisionDiff{
		From: from, To: to,
		Values:   textDiff(a.Values, b.Values),
		Manifest: textDiff(a.Manifest, b.Manifest),
	}, nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// textDiff — unified diff двух текстов с защитой от слишком больших входов (LCS квадратичен).
func textDiff(a, b string) string {
	la, lb := splitLines(a), splitLines(b)
	if len(la) > argoDiffMaxLines || len(lb) > argoDiffMaxLines {
		return "(text is too large for a line-by-line diff)"
	}
	d := unifiedDiff(la, lb, argoDiffContext)
	if len(d) > argoDiffMaxTextBytes {
		d = d[:argoDiffMaxTextBytes] + "\n… (diff truncated)"
	}
	return d
}
