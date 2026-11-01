// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Журнал Helm-операций (H7): история установок, апгрейдов, откатов и удалений с итогом и журналом Helm.
// Хранится локально в ~/.kubron/helm/operations.json (права 0600), общий для всех кластеров; в записи есть
// имя кластера. Хранятся последние helmJournalMax операций, у каждой — последние helmJournalLogLines строк.
// Это операционный журнал, а не аудит: кто выполнил действие, фиксирует журнал аудита.
const (
	helmJournalMax      = 200
	helmJournalLogLines = 80
)

// HelmOperationRecord — завершённая операция.
type HelmOperationRecord struct {
	ID         string   `json:"id"`
	Op         string   `json:"op"` // install | upgrade | rollback | uninstall
	Cluster    string   `json:"cluster"`
	Namespace  string   `json:"namespace"`
	Release    string   `json:"release"`
	Detail     string   `json:"detail"`   // чарт и версия, целевая ревизия и т. п.
	Revision   int      `json:"revision"` // созданная или целевая ревизия (0 — нет)
	Status     string   `json:"status"`   // success | failed | canceled
	Error      string   `json:"error"`
	StartedAt  string   `json:"startedAt"` // RFC3339
	FinishedAt string   `json:"finishedAt"`
	DurationMs int64    `json:"durationMs"`
	Log        []string `json:"log,omitempty"` // только в GetHelmOperation
}

// HelmOperationQuery — фильтр списка; пустые поля не фильтруют.
type HelmOperationQuery struct {
	Op     string `json:"op"`
	Status string `json:"status"`
	Search string `json:"search"` // подстрока: релиз, namespace, кластер, описание, ошибка
	Limit  int    `json:"limit"`  // по умолчанию 100, максимум helmJournalMax
}

var (
	helmJournalMu    sync.Mutex
	helmJournalOps   = map[string]bool{"install": true, "upgrade": true, "rollback": true, "uninstall": true}
	helmJournalState = map[string]bool{"success": true, "failed": true, "canceled": true}
)

func helmJournalPath() string { return filepath.Join(helmHomeDir(), "operations.json") }

// journalLoadLocked читает журнал (новые первыми). Нет файла — пусто; повреждённый файл — ошибка и пусто.
func journalLoadLocked() ([]HelmOperationRecord, error) {
	b, err := os.ReadFile(helmJournalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recs []HelmOperationRecord
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, fmt.Errorf("operation journal is corrupted: %w", err)
	}
	return recs, nil
}

func journalSaveLocked(recs []HelmOperationRecord) error {
	b, err := json.Marshal(recs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(helmHomeDir(), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(helmJournalPath(), b, 0o600)
}

func matchesJournal(r *HelmOperationRecord, q *HelmOperationQuery, search string) bool {
	if q.Op != "" && r.Op != q.Op {
		return false
	}
	if q.Status != "" && r.Status != q.Status {
		return false
	}
	if search == "" {
		return true
	}
	hay := strings.ToLower(r.Release + " " + r.Namespace + " " + r.Cluster + " " + r.Detail + " " + r.Error)
	for _, term := range strings.Fields(search) {
		if !strings.Contains(hay, term) {
			return false
		}
	}
	return true
}

// ListHelmOperations возвращает записи без журнала Helm (он тяжёлый и запрашивается отдельно).
// Это журнал команды активного кластера: операции других команд не видны.
func (m *Manager) ListHelmOperations(q HelmOperationQuery) ([]HelmOperationRecord, error) {
	if q.Op != "" && !helmJournalOps[q.Op] {
		return nil, fmt.Errorf("unknown operation %q", q.Op)
	}
	if q.Status != "" && !helmJournalState[q.Status] {
		return nil, fmt.Errorf("unknown status %q", q.Status)
	}
	limit := q.Limit
	switch {
	case limit <= 0:
		limit = 100
	case limit > helmJournalMax:
		limit = helmJournalMax
	}
	search := strings.ToLower(strings.TrimSpace(q.Search))

	sc, err := m.helmScope()
	if err != nil {
		return nil, err
	}
	recs, err := sc.store.ListOperations(sc.team)
	if err != nil {
		return nil, err
	}
	out := make([]HelmOperationRecord, 0, limit)
	for i := range recs {
		if !matchesJournal(&recs[i], &q, search) {
			continue
		}
		r := recs[i]
		r.Log = nil
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// GetHelmOperation возвращает запись целиком, вместе с журналом Helm. Запись другой команды — «не найдена».
func (m *Manager) GetHelmOperation(id string) (*HelmOperationRecord, error) {
	if !helmOpIDRe.MatchString(id) {
		return nil, errors.New("invalid operation ID")
	}
	sc, err := m.helmScope()
	if err != nil {
		return nil, err
	}
	r, err := sc.store.GetOperation(sc.team, id)
	if errors.Is(err, ErrHelmNotFound) {
		return nil, errors.New("entry not found (the journal keeps only recent operations)")
	}
	return r, err
}

// ClearHelmOperations стирает историю операций команды активного кластера.
func (m *Manager) ClearHelmOperations() error {
	sc, err := m.helmScope()
	if err != nil {
		return err
	}
	return sc.store.ClearOperations(sc.team)
}
