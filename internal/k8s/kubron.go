// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/24DSO-labs/kubron/internal/clusterhealth"
)

type KubronConfig struct {
	LastActiveCluster   string `json:"lastActiveCluster"`
	Theme               string `json:"theme"`
	AutoRefreshInterval int    `json:"autoRefreshInterval"` // секунды, 0 = выключено
}

type ClusterMeta struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	Icon             string `json:"icon"`
	Color            string `json:"color"`
	KubeconfigFile   string `json:"kubeconfigFile"`
	DefaultNamespace string `json:"defaultNamespace"`
	AddedAt          string `json:"addedAt"`
	ContextName      string `json:"contextName"`
	// Environment — метка окружения (dev, test, stage, prod); пусто, если не задана.
	Environment string `json:"environment"`
	// TeamID — команда-владелец (только сервисное хранилище; в файловом пусто).
	TeamID string `json:"teamId,omitempty"`
	// Mode — способ подключения: kubeconfig (пусто), in-cluster, oidc-password (только сервисное хранилище).
	Mode string `json:"mode,omitempty"`
}

type ClusterStatus struct {
	ClusterMeta
	Connected bool `json:"connected"`
	Active    bool `json:"active"`
	// Error — причина недоступности (пусто, пока кластер в порядке или не проверялся).
	Error string `json:"error"`
	// Health — результат фоновой проверки соединения (3.6); nil, пока проверки не было
	// (десктоп монитор не запускает).
	Health *clusterhealth.Health `json:"health,omitempty"`
}

// dataDirOverride — каталог данных, заданный явно (kubron-server: --data-dir / KUBRON_DATA_DIR).
var dataDirOverride atomic.Pointer[string]

// SetKubronDir задаёт каталог данных вместо ~/.kubron; пустая строка возвращает умолчание.
// Вызывается один раз при старте сервера, до открытия хранилищ. Десктоп его не вызывает и
// переменные окружения для каталога данных не читает: общий терминал с выставленной
// переменной не должен подменять данные пользователя.
func SetKubronDir(dir string) {
	if dir == "" {
		dataDirOverride.Store(nil)
		return
	}
	dataDirOverride.Store(&dir)
}

// GetKubronDir — каталог данных Kubron: заданный через SetKubronDir, иначе ~/.kubron.
func GetKubronDir() string {
	if dir := dataDirOverride.Load(); dir != nil {
		return *dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kubron")
}

func getClustersDir() string {
	return filepath.Join(GetKubronDir(), "clusters")
}

func getConfigPath() string {
	return filepath.Join(GetKubronDir(), "config.json")
}

func InitKubronDir() error {
	dirs := []string{GetKubronDir(), getClustersDir()}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	return nil
}

func LoadKubronConfig() (*KubronConfig, error) {
	data, err := os.ReadFile(getConfigPath())
	if err != nil {
		return &KubronConfig{Theme: "dark"}, nil
	}
	var config KubronConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return &KubronConfig{Theme: "dark"}, nil
	}
	return &config, nil
}

func SaveKubronConfig(config *KubronConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(getConfigPath(), data, 0600)
}

// SaveClusterMeta сохраняет метаданные кластера (kubeconfig не меняется).
func SaveClusterMeta(meta *ClusterMeta) error {
	if !validClusterID(meta.ID) {
		return fmt.Errorf("invalid cluster ID %q", meta.ID)
	}
	return clusterStore.Save(meta, nil)
}

// validClusterID отсекает идентификаторы, которые могли бы выйти за пределы каталога кластеров.
func validClusterID(id string) bool {
	return clusterIDRe.MatchString(id)
}

func LoadClusterMeta(id string) (*ClusterMeta, error) {
	if !validClusterID(id) {
		return nil, fmt.Errorf("invalid cluster ID %q", id)
	}
	return clusterStore.Get(id)
}

func ListClusters() ([]ClusterMeta, error) {
	return clusterStore.List()
}

// AddClusterFromFile копирует kubeconfig из файла в каталог Kubron и регистрирует кластер.
func AddClusterFromFile(sourcePath string, settings ClusterSettings) (*ClusterMeta, error) {
	if err := InitKubronDir(); err != nil {
		return nil, err
	}

	data, err := readKubeconfigFile(sourcePath)
	if err != nil {
		return nil, err
	}

	return addClusterFromData(data, settings, "", nil)
}

// AddClusterFromText регистрирует кластер по тексту kubeconfig.
func AddClusterFromText(kubeconfigText string, settings ClusterSettings) (*ClusterMeta, error) {
	return AddClusterFromTextToTeam(kubeconfigText, settings, "")
}

// AddClusterFromTextToTeam — AddClusterFromText с владельцем-командой (пусто — команда по умолчанию).
// Файловое хранилище команды не знает и поле игнорирует.
func AddClusterFromTextToTeam(kubeconfigText string, settings ClusterSettings, teamID string) (*ClusterMeta, error) {
	if err := InitKubronDir(); err != nil {
		return nil, err
	}
	if len(kubeconfigText) > maxKubeconfigBytes {
		return nil, errKubeconfigTooLarge
	}

	return addClusterFromData([]byte(kubeconfigText), settings, teamID, nil)
}

// addClusterFromData регистрирует кластер. oidc != nil — режим oidc-password: kubeconfig должен использовать
// oidc-login с паролем, логин и пароль сохраняются зашифрованными.
func addClusterFromData(data []byte, settings ClusterSettings, teamID string, oidc *OIDCCredentials) (*ClusterMeta, error) {
	settings, err := settings.normalize("")
	if err != nil {
		return nil, err
	}

	// Сначала проверяем kubeconfig: с битым файлом кластер всё равно не подключится.
	cfg, err := parseKubeconfig(data)
	if err != nil {
		return nil, err
	}
	contextName, err := pickContext(cfg, settings.ContextName)
	if err != nil {
		return nil, err
	}

	if err := CheckKubeconfigPolicy(cfg, contextName, oidc != nil); err != nil {
		return nil, err
	}

	var secret []byte
	mode := ""
	if oidc != nil {
		kind, params := ClassifyAuth(cfg, contextName)
		if kind != AuthOIDCPassword {
			return nil, errors.New("the kubeconfig does not use oidc-login with a password grant")
		}
		sec, err := newOIDCSecret(*params, *oidc)
		if err != nil {
			return nil, err
		}
		if err := verifyOIDCLogin(sec); err != nil {
			return nil, err
		}
		if secret, err = json.Marshal(sec); err != nil {
			return nil, err
		}
		mode = ModeOIDCPassword
	}

	id := uniqueClusterID(settings.DisplayName)

	meta := &ClusterMeta{
		ID:               id,
		DisplayName:      settings.DisplayName,
		Icon:             settings.Icon,
		Color:            settings.Color,
		Environment:      settings.Environment,
		DefaultNamespace: settings.DefaultNamespace,
		KubeconfigFile:   id + ".kubeconfig",
		AddedAt:          time.Now().Format(time.RFC3339),
		ContextName:      contextName,
		TeamID:           teamID,
		Mode:             mode,
	}

	// Хранилище записывает kubeconfig и метаданные вместе: при сбое не остаётся полузаписанного кластера.
	if secret != nil {
		ms, err := modeStore()
		if err != nil {
			return nil, err
		}
		if err := ms.SaveWithSecret(meta, data, secret); err != nil {
			return nil, err
		}
		return meta, nil
	}
	if err := clusterStore.Save(meta, data); err != nil {
		return nil, err
	}

	return meta, nil
}

func DeleteCluster(id string) error {
	if !validClusterID(id) {
		return fmt.Errorf("invalid cluster ID %q", id)
	}
	if err := clusterStore.Delete(id); err != nil {
		return err
	}
	// Токен Argo CD API принадлежит кластеру: после удаления кластера он не должен оставаться на диске.
	if err := deleteArgoToken(id); err != nil {
		return err
	}
	return nil
}

// GetClusterKubeconfigPath — путь к файлу kubeconfig кластера; пусто, если кластера нет или хранилище
// не файловое (тогда kubeconfig читается через ClusterStore.Kubeconfig).
func GetClusterKubeconfigPath(id string) string {
	if p, ok := clusterStore.(kubeconfigPather); ok {
		return p.KubeconfigPath(id)
	}
	return ""
}

func sanitizeID(name string) string {
	result := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result += string(c)
		} else if c == ' ' {
			result += "-"
		}
	}
	if len(result) > 50 {
		result = result[:50]
	}
	return result
}
