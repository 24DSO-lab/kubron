// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/24DSO-lab/kubron/internal/drift"
	"github.com/24DSO-lab/kubron/internal/secretbox"
)

// previewKeyInfo — метка вывода ключа токенов предпросмотра baseline (ADR-0027) из мастер-ключа. Версия в метке
// позволяет сменить схему вывода: старые токены просто перестанут приниматься (они живут не дольше drift.PreviewTTL).
const previewKeyInfo = "kubron/baseline/preview-key/v1"

// previewKeySize — длина выводимого ключа (HMAC-SHA256).
const previewKeySize = 32

var (
	// derivedPreviewKey — ключ, выведенный из KUBRON_SECRET_KEY при старте server; nil — не задан.
	derivedPreviewKey atomic.Pointer[[]byte]
	// randomPreviewKey — запасной случайный ключ процесса: десктоп и server без мастер-ключа (одна реплика).
	randomPreviewKey = sync.OnceValue(drift.NewPreviewKey)
)

// derivePreviewKey выводит ключ токенов предпросмотра из мастер-ключа (HKDF-SHA256). Сам мастер-ключ токены не
// подписывает: утечка выводимого ключа не раскрывает ключ шифрования данных.
func derivePreviewKey(master []byte) ([]byte, error) {
	if len(master) == 0 {
		return nil, secretbox.ErrNoKey
	}
	return hkdf.Key(sha256.New, master, nil, previewKeyInfo, previewKeySize)
}

// SetBaselinePreviewKey задаёт ключ токенов предпросмотра baseline из мастер-ключа server (KUBRON_SECRET_KEY,
// формат «id:base64»). Мастер-ключ один на все реплики, поэтому предпросмотр, выданный одной репликой, принимает любая
// другая (ADR-0016): запрос подтверждения может попасть не на ту реплику. Пустой ключ оставляет случайный ключ
// процесса: так работают десктоп и server без базы (реплика одна). Вызывать до обработки запросов.
//
// При ротации мастер-ключа ключ предпросмотра меняется; предпросмотры, выданные до смены, отклоняются как устаревшие
// (повтор предпросмотра — один клик), потому что живут не дольше drift.PreviewTTL.
func SetBaselinePreviewKey(masterKey string) error {
	if strings.TrimSpace(masterKey) == "" {
		return nil
	}
	k, err := secretbox.ParseKey(masterKey)
	if err != nil {
		return fmt.Errorf("baseline preview key: %w", err)
	}
	d, err := derivePreviewKey(k.Bytes[:])
	if err != nil {
		return fmt.Errorf("baseline preview key: %w", err)
	}
	derivedPreviewKey.Store(&d)
	return nil
}

// baselinePreviewKey — ключ токенов предпросмотра для сервиса drift.
func baselinePreviewKey() []byte {
	if p := derivedPreviewKey.Load(); p != nil {
		return *p
	}
	return randomPreviewKey()
}
