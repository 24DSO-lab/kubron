// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// Состояние Helm — список репозиториев, OCI-реестры с учётными данными и журнал операций (ADR-0015).
// Десктоп и режим одного пользователя хранят его файлами в каталоге данных (fileHelmStore, прежнее поведение),
// сервис — в PostgreSQL по командам (pgstore, подключается через SetHelmStateStore).

const (
	helmKindHTTP = "http"
	helmKindOCI  = "oci"
)

// Ошибки хранилища состояния Helm. Хранилище отдаёт их для «чужой» и несуществующей записи одинаково.
var (
	ErrHelmRepoExists = errors.New("repository is already added")
	ErrHelmNotFound   = errors.New("record not found")
)

// HelmRepoRecord — репозиторий чартов любого вида. Password наружу (в UI и в логи) не уходит никогда: им
// пользуются только запросы к самому репозиторию.
type HelmRepoRecord struct {
	Name        string
	Kind        string // helmKindHTTP | helmKindOCI
	URL         string
	Username    string
	Password    string // файловое хранилище: пароль HTTP-репозитория; у OCI он лежит в registry-config.json
	InsecureTLS bool
	PlainHTTP   bool     // OCI: без TLS (loopback)
	Charts      []string // OCI: набор чартов
	CreatedBy   string   // идентификатор пользователя (сервис); пусто — неизвестно
	CreatedAt   time.Time
}

// HelmStateStore — постоянное состояние Helm. Все методы принимают команду: сервис изолирует данные по командам,
// файловое хранилище команду игнорирует. Чужая и несуществующая запись неразличимы (ErrHelmNotFound).
type HelmStateStore interface {
	// CredentialsInStore: учётные данные реестров хранятся в самом хранилище (и передаются клиенту из памяти),
	// а не в файле registry-config.json.
	CredentialsInStore() bool

	ListRepos(team string) ([]HelmRepoRecord, error)
	// CreateRepo добавляет репозиторий; имя уникально среди репозиториев любого вида (ErrHelmRepoExists).
	CreateRepo(team string, r *HelmRepoRecord) error
	// SetOCICharts заменяет набор чартов OCI-реестра (ErrHelmNotFound, если реестра нет).
	SetOCICharts(team, name string, charts []string) error
	// DeleteRepo удаляет репозиторий и возвращает его прежнее содержимое (ErrHelmNotFound, если его нет).
	DeleteRepo(team, name string) (*HelmRepoRecord, error)

	// AddOperation дописывает запись в журнал команды; хранятся последние helmJournalMax записей.
	AddOperation(team, clusterID string, r HelmOperationRecord) error
	// ListOperations отдаёт журнал команды, новые первыми.
	ListOperations(team string) ([]HelmOperationRecord, error)
	GetOperation(team, id string) (*HelmOperationRecord, error)
	ClearOperations(team string) error
}

var (
	helmStoreMu sync.RWMutex
	helmStore   HelmStateStore = fileHelmStore{}
)

// SetHelmStateStore подменяет хранилище состояния Helm (сервер вызывает при старте, как SetClusterStore);
// nil возвращает файловое.
func SetHelmStateStore(s HelmStateStore) {
	helmStoreMu.Lock()
	defer helmStoreMu.Unlock()
	if s == nil {
		s = fileHelmStore{}
	}
	helmStore = s
}

func currentHelmStore() HelmStateStore {
	helmStoreMu.RLock()
	defer helmStoreMu.RUnlock()
	return helmStore
}

// ─── область: команда активного кластера ───

var helmTeamRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// helmScope — то, к чему относится запрос Helm: команда активного кластера и хранилище. Состояние команды
// видят только участники команды, которой принадлежит активный кластер (права на него проверяет authz).
type helmScope struct {
	team      string
	clusterID string
	actor     string // пользователь для created_by
	store     HelmStateStore
}

// helmScope определяет область запроса. Файловое хранилище команд не знает: область одна на всех.
func (m *Manager) helmScope() (*helmScope, error) {
	sc := &helmScope{store: currentHelmStore(), actor: m.helmActorID(), clusterID: m.GetActiveContext()}
	if _, file := sc.store.(fileHelmStore); file {
		return sc, nil
	}
	if sc.clusterID == "" {
		return nil, errors.New("no active connection")
	}
	meta, err := LoadClusterMeta(sc.clusterID)
	if err != nil {
		return nil, fmt.Errorf("cluster metadata not found: %w", err)
	}
	if !helmTeamRe.MatchString(meta.TeamID) {
		return nil, errors.New("the cluster does not belong to a team")
	}
	sc.team = meta.TeamID
	return sc, nil
}

// SetHelmActor запоминает пользователя вида (сессии), от имени которого создаются репозитории (created_by).
func (m *Manager) SetHelmActor(userID string) { m.helmActor.Store(&userID) }

func (m *Manager) helmActorID() string {
	if p := m.helmActor.Load(); p != nil {
		return *p
	}
	return ""
}

// cacheDir — каталог кэша индексов и тегов: у каждой команды свой, чтобы одноимённые репозитории разных команд
// не пересекались. Кэш воспроизводим, поэтому ему достаточно emptyDir.
func (sc *helmScope) cacheDir() string {
	if sc.team == "" {
		return helmCacheDir()
	}
	return filepath.Join(helmCacheDir(), sc.team)
}

// key — ключ записи в общих кэшах памяти.
func (sc *helmScope) key(name string) string { return sc.team + "/" + name }

func (sc *helmScope) indexPath(name string) string {
	return filepath.Join(sc.cacheDir(), name+"-index.yaml")
}

func (sc *helmScope) tagsPath(name string) string {
	return filepath.Join(sc.cacheDir(), name+"-oci.json")
}

func (sc *helmScope) repos() ([]HelmRepoRecord, error) { return sc.store.ListRepos(sc.team) }

// repo возвращает репозиторий по имени; нет такого — ok == false.
func (sc *helmScope) repo(name string) (HelmRepoRecord, bool, error) {
	if validateHelmRepoName(name) != nil {
		return HelmRepoRecord{}, false, nil
	}
	list, err := sc.repos()
	if err != nil {
		return HelmRepoRecord{}, false, err
	}
	for _, r := range list {
		if r.Name == name {
			return r, true, nil
		}
	}
	return HelmRepoRecord{}, false, nil
}

func (sc *helmScope) create(r *HelmRepoRecord) error {
	if r.CreatedBy == "" {
		r.CreatedBy = sc.actor
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if err := sc.store.CreateRepo(sc.team, r); err != nil {
		if errors.Is(err, ErrHelmRepoExists) {
			return fmt.Errorf("repository %q is already added", r.Name)
		}
		return err
	}
	return nil
}

// ─── файловое хранилище (десктоп, режим одного пользователя) ───

// fileHelmStore хранит состояние файлами в helmHomeDir(): repositories.yaml, oci.yaml, operations.json.
// Учётные данные OCI-реестров — в registry-config.json (их ведёт клиент реестра Helm).
type fileHelmStore struct{}

func (fileHelmStore) CredentialsInStore() bool { return false }

func (fileHelmStore) ListRepos(string) ([]HelmRepoRecord, error) {
	helmRepoMu.Lock()
	defer helmRepoMu.Unlock()
	return fileListLocked()
}

func fileListLocked() ([]HelmRepoRecord, error) {
	hf, err := loadHelmRepoFile()
	if err != nil {
		return nil, err
	}
	of, err := ociLoadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]HelmRepoRecord, 0, len(hf.Repositories)+len(of.Repositories))
	for _, e := range hf.Repositories {
		if e == nil || validateHelmRepoName(e.Name) != nil {
			continue
		}
		out = append(out, HelmRepoRecord{Name: e.Name, Kind: helmKindHTTP, URL: e.URL, Username: e.Username,
			Password: e.Password, InsecureTLS: e.InsecureSkipTLSVerify})
	}
	for _, r := range of.Repositories {
		out = append(out, HelmRepoRecord{Name: r.Name, Kind: helmKindOCI, URL: r.URL, Username: r.Username,
			PlainHTTP: r.PlainHTTP, Charts: append([]string(nil), r.Charts...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (fileHelmStore) CreateRepo(_ string, r *HelmRepoRecord) error {
	helmRepoMu.Lock()
	defer helmRepoMu.Unlock()
	hf, err := loadHelmRepoFile()
	if err != nil {
		return err
	}
	of, err := ociLoadLocked()
	if err != nil {
		return err
	}
	if hf.Has(r.Name) || of.find(r.Name) != nil {
		return ErrHelmRepoExists
	}
	if r.Kind == helmKindOCI {
		of.Repositories = append(of.Repositories, ociRepo{Name: r.Name, URL: r.URL, Username: r.Username,
			PlainHTTP: r.PlainHTTP, Charts: append([]string(nil), r.Charts...)})
		if err := ociSaveLocked(of); err != nil {
			return fmt.Errorf("failed to save the OCI registry list: %w", err)
		}
		return nil
	}
	hf.Add(&repo.Entry{Name: r.Name, URL: r.URL, Username: r.Username, Password: r.Password, InsecureSkipTLSVerify: r.InsecureTLS})
	if err := saveHelmRepoFile(hf); err != nil {
		return fmt.Errorf("failed to save the repository list: %w", err)
	}
	return nil
}

func (fileHelmStore) SetOCICharts(_, name string, charts []string) error {
	helmRepoMu.Lock()
	defer helmRepoMu.Unlock()
	of, err := ociLoadLocked()
	if err != nil {
		return err
	}
	cur := of.find(name)
	if cur == nil {
		return ErrHelmNotFound
	}
	cur.Charts = append([]string(nil), charts...)
	return ociSaveLocked(of)
}

func (fileHelmStore) DeleteRepo(_, name string) (*HelmRepoRecord, error) {
	helmRepoMu.Lock()
	defer helmRepoMu.Unlock()
	of, err := ociLoadLocked()
	if err != nil {
		return nil, err
	}
	if r := of.find(name); r != nil {
		removed := HelmRepoRecord{Name: r.Name, Kind: helmKindOCI, URL: r.URL, Username: r.Username, PlainHTTP: r.PlainHTTP, Charts: r.Charts}
		kept := of.Repositories[:0]
		for _, x := range of.Repositories {
			if x.Name != name {
				kept = append(kept, x)
			}
		}
		of.Repositories = kept
		if err := ociSaveLocked(of); err != nil {
			return nil, err
		}
		return &removed, nil
	}
	hf, err := loadHelmRepoFile()
	if err != nil {
		return nil, err
	}
	e := hf.Get(name)
	if e == nil {
		return nil, ErrHelmNotFound
	}
	removed := HelmRepoRecord{Name: e.Name, Kind: helmKindHTTP, URL: e.URL, Username: e.Username, InsecureTLS: e.InsecureSkipTLSVerify}
	hf.Remove(name)
	if err := saveHelmRepoFile(hf); err != nil {
		return nil, err
	}
	return &removed, nil
}

func (fileHelmStore) AddOperation(_, _ string, r HelmOperationRecord) error {
	helmJournalMu.Lock()
	defer helmJournalMu.Unlock()
	recs, _ := journalLoadLocked() // повреждённый файл заменяется новым журналом
	recs = append([]HelmOperationRecord{r}, recs...)
	if len(recs) > helmJournalMax {
		recs = recs[:helmJournalMax]
	}
	return journalSaveLocked(recs)
}

func (fileHelmStore) ListOperations(string) ([]HelmOperationRecord, error) {
	helmJournalMu.Lock()
	defer helmJournalMu.Unlock()
	return journalLoadLocked()
}

func (fileHelmStore) GetOperation(_, id string) (*HelmOperationRecord, error) {
	helmJournalMu.Lock()
	recs, err := journalLoadLocked()
	helmJournalMu.Unlock()
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if recs[i].ID == id {
			r := recs[i]
			return &r, nil
		}
	}
	return nil, ErrHelmNotFound
}

func (fileHelmStore) ClearOperations(string) error {
	helmJournalMu.Lock()
	defer helmJournalMu.Unlock()
	return removeIfExists(helmJournalPath())
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
