// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// argoAPIMaxBody — предел размера ответа: managed-resources у больших приложений тяжёлые, но не бесконечные.
	argoAPIMaxBody = 64 << 20
	argoAPITimeout = 45 * time.Second
)

// argoAPIClient — минимальный клиент Argo CD REST API (только то, что нужно для diff).
// Адрес — локальный туннель к argocd-server (port-forward), токен — токен аккаунта Argo CD.
type argoAPIClient struct {
	base  *url.URL
	token string
	hc    *http.Client
}

func newArgoAPIClient(baseURL, token string) (*argoAPIClient, error) {
	return newArgoAPIClientVia(baseURL, token, nil)
}

// newArgoAPIClientVia создаёт клиент, который соединяется через dial (PodDialer), а не по адресу из baseURL.
// Сертификат argocd-server по умолчанию самоподписанный. Проверку отключаем только для loopback-туннеля и для
// dial: трафик идёт через port-forward, аутентифицированный Kubernetes API, а не через сеть.
func newArgoAPIClientVia(baseURL, token string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) (*argoAPIClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("invalid Argo CD address")
	}
	tr := &http.Transport{DialContext: dial}
	if dial != nil || isLoopbackHost(u.Hostname()) {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // port-forward через Kubernetes API
	}
	return &argoAPIClient{
		base:  u,
		token: token,
		hc: &http.Client{
			Transport: tr,
			Timeout:   argoAPITimeout,
			// Редиректы не выполняем: заголовок Authorization не должен уходить на чужой адрес.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// argoManagedResource — элемент ответа GET /api/v1/applications/{name}/managed-resources.
// *State — JSON-манифесты, закодированные строкой; отсутствующее состояние приходит как "" или "null".
type argoManagedResource struct {
	Group               string `json:"group"`
	Kind                string `json:"kind"`
	Namespace           string `json:"namespace"`
	Name                string `json:"name"`
	Hook                bool   `json:"hook"`
	TargetState         string `json:"targetState"`
	LiveState           string `json:"liveState"`
	NormalizedLiveState string `json:"normalizedLiveState"`
	PredictedLiveState  string `json:"predictedLiveState"`
}

func (c *argoAPIClient) managedResources(ctx context.Context, appNamespace, name string) ([]argoManagedResource, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/applications/" + url.PathEscape(name) + "/managed-resources"
	q := url.Values{}
	if appNamespace != "" {
		q.Set("appNamespace", appNamespace)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		// Ошибка транспорта содержит URL, но не заголовки: токен в неё не попадает.
		return nil, fmt.Errorf("Argo CD API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, argoAPIMaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read Argo CD API response: %w", err)
	}
	if len(body) > argoAPIMaxBody {
		return nil, errors.New("Argo CD API response is too large")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, argoAPIStatusError(resp.StatusCode, body)
	}

	var out struct {
		Items []argoManagedResource `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, errors.New("Argo CD API returned an unexpected response (check the server version and server.rootpath)")
	}
	return out.Items, nil
}

// argoAPIStatusError переводит HTTP-статус в понятную ошибку; текст из тела ограничен и берётся только из поля message.
func argoAPIStatusError(status int, body []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return errors.New("Argo CD token is invalid or expired")
	case http.StatusForbidden:
		return errors.New("Argo CD token has no permission for this application")
	case http.StatusNotFound:
		return errors.New("application not found in Argo CD (check that it is managed by this Argo CD)")
	}
	var e struct {
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal(body, &e) == nil {
		msg = e.Message
	}
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	if msg == "" {
		return fmt.Errorf("Argo CD API: HTTP %d", status)
	}
	return fmt.Errorf("Argo CD API: HTTP %d: %s", status, msg)
}
