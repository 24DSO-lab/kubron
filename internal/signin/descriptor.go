// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package signin

import (
	"errors"
	"net/url"
	"strings"

	"github.com/24DSO-labs/kubron/internal/credential"
)

// События сессии, которой принадлежит вход (ADR-0018). В данных только идентификаторы.
const (
	EventAuthCompleted = "cluster:auth-completed"
	EventAuthRequired  = "cluster:auth-required"
)

// ErrUnavailable возвращают методы входа, когда брокер не настроен.
var ErrUnavailable = errors.New("sign-in is not available")

// SharedAccountNotSignedIn: у общей учётной записи команды нет сохранённого входа, а ввести его может только
// администратор кластера. Для вызывающих, проверяющих ErrChallengeRequired, это по-прежнему он.
type SharedAccountNotSignedIn struct{}

func (SharedAccountNotSignedIn) Error() string {
	return "the shared account of this cluster is not signed in: ask a cluster administrator"
}

// Is делает ошибку равной credential.ErrChallengeRequired.
func (SharedAccountNotSignedIn) Is(target error) bool {
	return target == credential.ErrChallengeRequired
}

// IDPKey идентифицирует провайдера входа для лимитера: хост издателя или, для видов без него (облачные плагины),
// вид и кластер. Ключ используется только внутри лимитера, не в метриках и журналах.
func IDPKey(d credential.Descriptor, clusterID string) string {
	if u, err := url.Parse(d.Params["issuer-url"]); err == nil && u.Host != "" {
		return "idp:" + strings.ToLower(u.Host)
	}
	return "kind:" + string(d.Kind) + ":" + clusterID
}

// WithWebOIDCParams убирает веб-переопределения, которые могли прийти из данных кластера, и подставляет значения,
// заданные администратором, для этого веб-запроса.
func WithWebOIDCParams(d credential.Descriptor, clientID, redirectURL string) credential.Descriptor {
	p := make(map[string]string, len(d.Params)+2)
	for k, v := range d.Params {
		if k != "web.client-id" && k != "web.redirect-url" {
			p[k] = v
		}
	}
	// Колбэк пригоден, только когда есть и публичный клиент, заданный администратором, и проверенный публичный URL
	// сервера. Иначе остаётся прежний сценарий с вставкой адреса перенаправления.
	if clientID != "" && redirectURL != "" {
		p["web.client-id"] = clientID
		p["web.redirect-url"] = redirectURL
	}
	d.Params = p
	return d
}

// PrincipalOf — субъект Kubernetes завершённого входа; пусто, если провайдер его не дал.
func PrincipalOf(res credential.Result) string {
	if res.Token != nil {
		return res.Token.Principal
	}
	return ""
}
