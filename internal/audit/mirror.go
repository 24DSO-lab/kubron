// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/24DSO-lab/kubron/internal/logging"
)

// Зеркало: каждая запись, помимо хранилища, дублируется строкой JSON в поток (stdout сервера) для
// внешнего сборщика (Loki, Elastic, Splunk). Включается один раз при старте (KUBRON_AUDIT_STDOUT=1).
// Зеркало не заменяет журнал в БД и не влияет на него: ошибка или медленный приёмник записи не прерывает
// действие и не попадает в Page.WriteError.
var mirror struct {
	mu sync.Mutex
	w  io.Writer
}

// SetMirror включает дублирование записей в w; nil выключает.
func SetMirror(w io.Writer) {
	mirror.mu.Lock()
	mirror.w = w
	mirror.mu.Unlock()
}

// mirrorLine — запись в потоке: общие поля журнала процесса (time, level, msg) плюс type=audit, по
// которому сборщик отделяет аудит от остального вывода, и поля Event как в хранилище.
type mirrorLine struct {
	Type  string `json:"type"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Event
}

func mirrorEvent(e Event) {
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	if mirror.w == nil {
		return
	}
	// Подробности проходят тот же редактор секретов, что и журнал процесса: страховка от значения,
	// попавшего в detail по ошибке.
	e.Detail = logging.Redact(e.Detail)
	line, err := json.Marshal(mirrorLine{Type: "audit", Level: "INFO", Msg: "audit", Event: e})
	if err != nil {
		return
	}
	_, _ = mirror.w.Write(append(line, '\n')) // одним вызовом: строки не перемешиваются
}
