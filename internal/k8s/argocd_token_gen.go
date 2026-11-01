// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

const (
	argoTokenDefaultDays = 30
	argoTokenMaxDays     = 365
	argoCredMaxLen       = 512
)

// Имя локального аккаунта Argo CD; встаёт в путь запроса, поэтому набор символов строго ограничен.
var argoAccountRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// БЕЗОПАСНОСТЬ. Пароль нужен только на время входа в Argo CD: он уходит в тело одного запроса через
// loopback-туннель, не сохраняется, не попадает в ответ, ошибки, журнал аудита и логи. Для создания токена
// используется короткоживущий сессионный токен, который тоже нигде не хранится. Сохраняется только
// выданный токен аккаунта (см. argocd_token.go). Обходить Argo (подписывать JWT ключом server.secretkey) нельзя.

// postJSON отправляет JSON POST-запросом к Argo CD API и возвращает тело ответа (с ограничением размера) и статус.
// bearer пустой — запрос без Authorization (вход по паролю).
func (c *argoAPIClient) postJSON(ctx context.Context, path, bearer string, in any) ([]byte, int, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, 0, err
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		// Ошибка транспорта содержит метод и URL, но не тело и не заголовки.
		return nil, 0, fmt.Errorf("Argo CD API request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, argoAPIMaxBody+1))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read Argo CD API response: %w", err)
	}
	if len(body) > argoAPIMaxBody {
		return nil, 0, errors.New("Argo CD API response is too large")
	}
	return body, resp.StatusCode, nil
}

// scrub убирает секреты из тела ответа перед тем, как его текст может попасть в сообщение об ошибке.
func scrub(body []byte, secrets ...string) []byte {
	for _, s := range secrets {
		if s == "" {
			continue
		}
		body = bytes.ReplaceAll(body, []byte(s), []byte("***"))
		if q, err := json.Marshal(s); err == nil && len(q) > 2 {
			body = bytes.ReplaceAll(body, q[1:len(q)-1], []byte("***"))
		}
	}
	return body
}

func tokenFromBody(body []byte) string {
	var out struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(body, &out) != nil {
		return ""
	}
	return out.Token
}

// validateTokenRequest проверяет вход до обращения к сети.
func validateTokenRequest(account, username, password string, days int) (int, error) {
	if !argoAccountRe.MatchString(account) {
		return 0, errors.New("invalid Argo CD account name (letters, digits and . _ @ - are allowed, up to 64 characters)")
	}
	if username == "" || password == "" {
		return 0, errors.New("enter the Argo CD username and password")
	}
	if len(username) > argoCredMaxLen || len(password) > argoCredMaxLen {
		return 0, errors.New("username or password is too long")
	}
	if days == 0 {
		days = argoTokenDefaultDays
	}
	if days < 1 || days > argoTokenMaxDays {
		return 0, fmt.Errorf("token validity must be between 1 and %d days", argoTokenMaxDays)
	}
	return days, nil
}

// generateArgoToken входит в Argo CD по логину и паролю и просит у него токен для аккаунта account.
// Возвращает только выданный токен.
func generateArgoToken(ctx context.Context, c *argoAPIClient, account, username, password string, days int) (string, error) {
	days, err := validateTokenRequest(account, username, password, days)
	if err != nil {
		return "", err
	}

	// 1. Сессия (вход по паролю)
	body, status, err := c.postJSON(ctx, "/api/v1/session", "", map[string]string{"username": username, "password": password})
	if err != nil {
		return "", err
	}
	switch {
	case status == http.StatusOK:
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "", errors.New("Argo CD rejected the username or password (check that login is enabled for the account in argocd-cm)")
	default:
		return "", argoAPIStatusError(status, scrub(body, password))
	}
	session := tokenFromBody(body)
	if session == "" {
		return "", errors.New("Argo CD returned no session token (check the server version and server.rootpath)")
	}

	// 2. Токен аккаунта
	body, status, err = c.postJSON(ctx, "/api/v1/account/"+account+"/token", session,
		map[string]any{"name": account, "expiresIn": int64(days) * 24 * 3600})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		body = scrub(body, password, session)
		if strings.Contains(string(body), "apiKey") {
			return "", fmt.Errorf("account %q does not have the apiKey capability enabled: add accounts.%s: apiKey to argocd-cm", account, account)
		}
		if status == http.StatusForbidden {
			return "", fmt.Errorf("user %q is not allowed to create tokens for account %q (Argo CD RBAC: accounts, update)", username, account)
		}
		return "", argoAPIStatusError(status, body)
	}
	token, err := normalizeArgoToken(tokenFromBody(body))
	if err != nil {
		return "", errors.New("Argo CD returned an empty or invalid token")
	}
	return token, nil
}

// GenerateArgoAPIToken получает токен Argo CD API для аккаунта account и сохраняет его для активного кластера.
// Токен фронтенду не возвращается. Нужен доступ к argocd-server через туннель (port-forward).
func (pfm *PortForwardManager) GenerateArgoAPIToken(account, username, password string, days int) error {
	// Проверка до подъёма туннеля: ошибки ввода не должны ждать сети.
	if _, err := validateTokenRequest(account, username, password, days); err != nil {
		return err
	}
	clusterID := pfm.k8sMgr.GetActiveContext()
	if clusterID == "" {
		return errors.New("no active connection")
	}
	client, release, err := pfm.argoClient("")
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), argoAPITimeout)
	defer cancel()
	token, err := generateArgoToken(ctx, client, account, username, password, days)
	if err != nil {
		return err
	}
	return saveArgoToken(clusterID, token)
}
