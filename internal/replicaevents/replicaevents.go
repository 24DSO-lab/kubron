// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package replicaevents описывает сообщения между репликами сервиса (ADR-0016, раздел 2). Сообщение несёт только
// идентификаторы: получатель читает данные из БД с проверкой прав так же, как при обычном запросе. Секретов и
// содержимого кластеров в сообщениях нет. Транспорт (PostgreSQL LISTEN/NOTIFY) — в pgstore; пакет от него не зависит.
//
// Сообщения — ускорение, а не источник истины: потерянное сообщение не оставляет сессию живой или доступ открытым,
// корректность держится на сверке с БД (кэш сессий 2 с, фоновая проверка прав 10 с, сверка после переподключения).
package replicaevents

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Channel — канал NOTIFY.
const Channel = "kubron_events"

// Version — версия формата. Получатель игнорирует сообщения другой версии (обновление реплик по очереди).
const Version = 1

// MaxPayload — предел размера полезной нагрузки с запасом до 8000 байт PostgreSQL.
const MaxPayload = 7000

// Kind — вид события.
type Kind string

const (
	// SessionEnded — веб-сессия завершена (выход, отзыв): закрыть её WebSocket и потоки, освободить вид, сбросить кэш.
	SessionEnded Kind = "session.ended"
	// UserSessionsEnded — завершены сессии пользователя (отключение, удаление, сброс и смена пароля).
	// Session, если задан, — сессия-исключение (та, из которой сменили пароль).
	UserSessionsEnded Kind = "user.sessions_ended"
	// AccessChanged — изменились права: участники, роли, команды, привязка кластеров, пользователи.
	// Получатель немедленно перепроверяет доступ локальных видов вместо ожидания очередной сверки.
	AccessChanged Kind = "access.changed"
	// DirectoryChanged — изменились команды или кластеры: клиенты перечитывают список кластеров.
	DirectoryChanged Kind = "directory.changed"
	// HealthChanged — лидер проверок записал новые состояния кластеров: прочитать их из БД.
	HealthChanged Kind = "health.changed"
)

// Message — одно сообщение. Адресация по Session, User, Team или Cluster (как в ADR-0009); пустые поля опускаются.
// Сообщение без адресата («вся организация») допустимо для AccessChanged и DirectoryChanged: когда адресатов
// слишком много или они неизвестны, отправитель шлёт укрупнённое событие.
type Message struct {
	V       int    `json:"v"`
	Kind    Kind   `json:"kind"`
	Origin  string `json:"origin"`
	Session string `json:"session,omitempty"`
	User    string `json:"user,omitempty"`
	Team    string `json:"team,omitempty"`
	Cluster string `json:"cluster,omitempty"`
}

// Encode сериализует сообщение; origin проставляется отправителем (replica_id).
func (m Message) Encode(origin string) (string, error) {
	m.V, m.Origin = Version, origin
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	if len(b) > MaxPayload {
		return "", fmt.Errorf("replica event is too large: %d bytes", len(b))
	}
	return string(b), nil
}

// Decode разбирает полезную нагрузку. Сообщение другой версии — ошибка (получатель его пропускает).
func Decode(payload string) (Message, error) {
	var m Message
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return Message{}, fmt.Errorf("replica event cannot be decoded: %w", err)
	}
	if m.V != Version {
		return Message{}, fmt.Errorf("replica event has an unsupported version %d", m.V)
	}
	return m, nil
}

// Dispatcher раздаёт принятые сообщения обработчикам и отбрасывает собственные (по origin): локальное
// действие уже выполнено синхронно.
type Dispatcher struct {
	origin string
	logf   func(format string, args ...any)

	mu       sync.RWMutex
	handlers map[Kind]func(Message)
}

// NewDispatcher создаёт диспетчер реплики origin; logf — журнал (nil — молча).
func NewDispatcher(origin string, logf func(format string, args ...any)) *Dispatcher {
	return &Dispatcher{origin: origin, logf: logf, handlers: map[Kind]func(Message){}}
}

// Handle задаёт обработчик вида события (заменяет прежний).
func (d *Dispatcher) Handle(kind Kind, fn func(Message)) {
	d.mu.Lock()
	d.handlers[kind] = fn
	d.mu.Unlock()
}

// Dispatch обрабатывает одну принятую полезную нагрузку. Возвращает true, если вызван обработчик.
// Свои, неразборчивые, чужой версии и неизвестные сообщения пропускаются.
func (d *Dispatcher) Dispatch(payload string) bool {
	m, err := Decode(payload)
	if err != nil {
		if d.logf != nil {
			d.logf("replica events: %v", err)
		}
		return false
	}
	if m.Origin == d.origin {
		return false
	}
	d.mu.RLock()
	fn := d.handlers[m.Kind]
	d.mu.RUnlock()
	if fn == nil {
		return false
	}
	fn(m)
	return true
}
