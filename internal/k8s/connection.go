// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Способы подключения к кластеру (ClusterMeta.Mode). Пусто означает kubeconfig.
const (
	// ModeKubeconfig — токен, сертификат или иные статические учётные данные из kubeconfig.
	ModeKubeconfig = "kubeconfig"
	// ModeInCluster — сервер сам работает в этом кластере и ходит под своим ServiceAccount.
	ModeInCluster = "in-cluster"
	// ModeTeamAccount — общая учётная запись команды: кластер входит под одной записью для всех участников. Вход
	// проверяет брокер (ADR-0018, ScopeTeam), результат хранится в его хранилище, и вводит его только
	// администратор кластера. Значение осталось прежним ("oidc-password"): так записаны и уже сохранённые
	// кластеры, и перенесённые MigrateLegacyOIDC.
	ModeTeamAccount = "oidc-password"
)

// ConnectionPolicy — что разрешено в kubeconfig там, где его присылает пользователь, а не читает владелец машины.
// Десктоп разрешает чтение файлов (как kubectl); веб-сервер его запрещает. Блок exec из kubeconfig не выполняется
// нигде: вход за него делает брокер (ADR-0018).
type ConnectionPolicy struct {
	// AllowLocalFiles — ссылки на файлы машины с ядром (certificate-authority, client-key, tokenFile): иначе
	// kubeconfig мог бы отправить файл сервера на чужой адрес.
	AllowLocalFiles bool
}

var connPolicy atomic.Pointer[ConnectionPolicy]

// SetConnectionPolicy задаёт политику; вызывается один раз при старте сервера, до открытия хранилищ.
// Десктоп её не вызывает: без неё разрешено всё.
func SetConnectionPolicy(p ConnectionPolicy) { connPolicy.Store(&p) }

func currentPolicy() ConnectionPolicy {
	if p := connPolicy.Load(); p != nil {
		return *p
	}
	return ConnectionPolicy{AllowLocalFiles: true}
}

// Ошибки политики подключений; тексты сопоставлены переводам в backend.json.
var (
	// ErrLocalFilesForbidden — kubeconfig ссылается на файлы машины с сервером.
	ErrLocalFilesForbidden = errors.New("the kubeconfig refers to files on the server: embed certificates and tokens in it (the -data fields)")
)

// checkLocalFiles отвергает ссылки на файлы машины с ядром в выбранном контексте.
func checkLocalFiles(cfg *clientcmdapi.Config, ctxName string) error {
	ctx := cfg.Contexts[ctxName]
	if ctx == nil {
		return nil
	}
	if cl := cfg.Clusters[ctx.Cluster]; cl != nil && cl.CertificateAuthority != "" {
		return ErrLocalFilesForbidden
	}
	if ai := cfg.AuthInfos[ctx.AuthInfo]; ai != nil && (ai.ClientCertificate != "" || ai.ClientKey != "" || ai.TokenFile != "") {
		return ErrLocalFilesForbidden
	}
	return nil
}

// CheckKubeconfigPolicy применяет к выбранному контексту проверки, не зависящие от способа входа: адрес сервера
// (политика адресов) и ссылки на файлы машины с сервером. Способ входа определяет credential.Broker.Describe
// (ADR-0018): oidc-login и облачные плагины не отклоняются при добавлении кластера, их блок exec не
// выполняется никогда (см. ConfigFor).
func CheckKubeconfigPolicy(cfg *clientcmdapi.Config, ctxName string) error {
	// Адрес API-сервера из kubeconfig пользователя проверяется политикой адресов (если она включена).
	if err := checkClusterAddress(cfg, ctxName); err != nil {
		return err
	}
	if !currentPolicy().AllowLocalFiles {
		if err := checkLocalFiles(cfg, ctxName); err != nil {
			return err
		}
	}
	return nil
}

// modeClusterStore — возможность хранилища, нужная режиму in-cluster: кластер без kubeconfig. Файловое хранилище
// десктопа её не имеет.
type modeClusterStore interface {
	// SaveWithSecret — Save с секретом: secret != nil заменяет прежний, nil оставляет.
	SaveWithSecret(meta *ClusterMeta, kubeconfig, secret []byte) error
}

var errModeUnsupported = errors.New("this storage cannot keep connection secrets: use the PostgreSQL storage")

func modeStore() (modeClusterStore, error) {
	ms, ok := clusterStore.(modeClusterStore)
	if !ok {
		return nil, errModeUnsupported
	}
	return ms, nil
}

// InClusterAvailable — процесс запущен внутри Kubernetes и может подключиться к его API.
func InClusterAvailable() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != "" && os.Getenv("KUBERNETES_SERVICE_PORT") != ""
}

// AddInClusterToTeam добавляет кластер, в котором работает сам сервер. Подключение идёт под ServiceAccount пода,
// поэтому право на это вызывающий проверяет отдельно (администратор организации).
func AddInClusterToTeam(settings ClusterSettings, teamID string) (*ClusterMeta, error) {
	if !InClusterAvailable() {
		return nil, errors.New("the server is not running inside a Kubernetes cluster")
	}
	ms, err := modeStore()
	if err != nil {
		return nil, err
	}
	settings, err = settings.normalize("")
	if err != nil {
		return nil, err
	}
	id := uniqueClusterID(settings.DisplayName)
	meta := &ClusterMeta{
		ID: id, DisplayName: settings.DisplayName, Icon: settings.Icon, Color: settings.Color,
		Environment: settings.Environment, DefaultNamespace: settings.DefaultNamespace, ArgoURL: settings.ArgoURL,
		AddedAt: time.Now().Format(time.RFC3339), TeamID: teamID, Mode: ModeInCluster,
	}
	if err := ms.SaveWithSecret(meta, nil, nil); err != nil {
		return nil, err
	}
	return meta, nil
}

// uniqueClusterID выбирает свободный идентификатор по названию.
func uniqueClusterID(displayName string) string {
	id := sanitizeID(displayName)
	if id == "" {
		id = fmt.Sprintf("cluster-%d", time.Now().Unix())
	}
	if existing, _ := LoadClusterMeta(id); existing != nil {
		id = fmt.Sprintf("%s-%d", id, time.Now().Unix())
	}
	return id
}
