// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"github.com/24DSO-lab/kubron/internal/audit"
)

// Recorder принимает события журнала аудита. Реализация (audit.Logger) не должна
// вызывать методы Service: события пишутся под его блокировкой.
type Recorder interface{ Record(audit.Event) }

// Actor — кто выполняет действие (для журнала аудита).
type Actor struct {
	Username string
	Role     Role
	Source   string // адрес клиента сессии (веб); пусто в десктопе
	// Контекст проверки в сервисном режиме: кластер и команда, к которым относится действие (для журнала).
	ClusterID string
	TeamID    string
}

// SetAuditor подключает журнал аудита.
func (s *Service) SetAuditor(r Recorder) {
	s.mu.Lock()
	s.rec = r
	s.mu.Unlock()
}

// record пишет событие в журнал. Вызывается под s.mu; actor может быть nil.
func (s *Service) record(actor *UserRecord, action, result, detail string) {
	s.recordFrom(s.source, actor, action, result, detail)
}

// recordFrom — то же, что record, но с адресом клиента той сессии, о которой событие.
func (s *Service) recordFrom(source string, actor *UserRecord, action, result, detail string) {
	if s.rec == nil {
		return
	}
	e := audit.Event{Time: s.now(), Action: action, Result: result, Detail: detail, Source: source}
	if actor != nil {
		e.User, e.Role = actor.Username, string(actor.Role)
	}
	s.rec.Record(e)
}
