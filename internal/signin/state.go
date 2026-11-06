// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package signin — части сценария входа в кластер, которые не зависят от App: учёт ожидающих вызовов сессии,
// лимит попыток ответа, ключ провайдера входа, параметры веб-OIDC и ошибки.
//
// Сам сценарий (ConnectCluster, AnswerChallenge, PollChallenge, ...) остаётся в App: каждый его шаг начинается с
// проверки прав, а имя действия для журнала аудита берётся из стека вызовов App.
package signin

import (
	"sync"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// AuthRequiredEvery — не чаще, чем раз в этот срок сессии отправляется событие «нужен вход» по одному кластеру:
// умерший вход ломает каждый запрос кластера, а клиенту событие нужно один раз, а не на каждый запрос.
const AuthRequiredEvery = 5 * time.Second

// Pending — что сессия помнит об открытом вызове: его кластер, ключ провайдера входа (для лимита попыток) и субъект,
// для которого вызов выдан (команда или пользователь).
type Pending struct {
	Cluster string
	IdP     string
	Sub     credential.Subject
}

// State — учёт входа одной сессии (вида). Хранится указателем, поэтому виды сессий не копируют мьютекс и не делят
// состояние.
type State struct {
	mu       sync.Mutex
	pending  map[string]Pending   // id вызова → кому принадлежит; только вызовы, начатые этой сессией
	notified map[string]time.Time // id кластера → когда последний раз отправлено cluster:auth-required
	seenSeq  uint64               // до какого отзыва реплики этот вид применил отзывы
}

// NewState создаёт пустое состояние.
func NewState() *State {
	return &State{pending: map[string]Pending{}, notified: map[string]time.Time{}}
}

// Seen — до какого номера отзывов реплики вид их уже применил.
func (s *State) Seen() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seenSeq
}

// MarkSeen запоминает номер применённых отзывов; назад он не идёт.
func (s *State) MarkSeen(n uint64) {
	s.mu.Lock()
	if n > s.seenSeq {
		s.seenSeq = n
	}
	s.mu.Unlock()
}

// AuthRequiredDue сообщает, можно ли сейчас отправить cluster:auth-required, и отмечает отправку.
func (s *State) AuthRequiredDue(clusterID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.notified[clusterID]; ok && now.Sub(last) < AuthRequiredEvery {
		return false
	}
	s.notified[clusterID] = now
	return true
}

// Remember запоминает открытый вызов этой сессии.
func (s *State) Remember(challengeID string, p Pending) {
	s.mu.Lock()
	s.pending[challengeID] = p
	s.mu.Unlock()
}

// Lookup возвращает вызов, начатый этой сессией.
func (s *State) Lookup(challengeID string) (Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[challengeID]
	return p, ok
}

// Forget забывает вызов (завершён, отменён или истёк).
func (s *State) Forget(challengeID string) {
	s.mu.Lock()
	delete(s.pending, challengeID)
	s.mu.Unlock()
}
