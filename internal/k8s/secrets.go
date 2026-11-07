// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SecretInfo struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Type        string            `json:"type"`
	DataCount   int               `json:"dataCount"`
	Age         string            `json:"age"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Keys        []string          `json:"keys"`
	// ControlledBy — "Kind/name" владельца (ownerReferences), ManagedBy — "Kind/name" системы деплоя
	ControlledBy string `json:"controlledBy"`
	ManagedBy    string `json:"managedBy"`
}

func (m *Manager) GetSecrets(namespaces []string) ([]SecretInfo, error) {
	return scopedList(m, "GetSecrets", "", "secrets", namespaces, listSecrets,
		func(it SecretInfo) string { return it.Namespace })
}

// listSecrets читает ресурсы одного namespace ("" — все) для GetSecrets.
func listSecrets(ctx context.Context, conn *ClusterConnection, namespace string) ([]SecretInfo, error) {
	secretList, err := conn.Client.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var secrets []SecretInfo
	for _, s := range secretList.Items {
		var keys []string
		for k := range s.Data {
			keys = append(keys, k)
		}

		secrets = append(secrets, SecretInfo{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Type:        string(s.Type),
			DataCount:   len(s.Data),
			Age:         formatAge(s.CreationTimestamp.Time),
			Labels:      s.Labels,
			Annotations: s.Annotations,
			Keys:        keys,

			ControlledBy: controlledByOf(s.OwnerReferences),
			ManagedBy:    managedByOf(s.ObjectMeta),
		})
	}

	return secrets, nil
}

// GetSecretValue возвращает расшифрованное значение одного ключа секрета.
// client-go уже декодирует base64 в sec.Data.
func (m *Manager) GetSecretValue(namespace, name, key string) (string, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	sec, err := conn.Client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	val, ok := sec.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %s", key, name)
	}
	return string(val), nil
}

// GetSecretBase64Value возвращает base64-кодированное значение ключа секрета
// (как оно хранится в Kubernetes).
func (m *Manager) GetSecretBase64Value(namespace, name, key string) (string, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	sec, err := conn.Client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	val, ok := sec.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %s", key, name)
	}
	return base64.StdEncoding.EncodeToString(val), nil
}

// UpdateSecret обновляет данные секрета. Значения передаются в base64 (в том же виде,
// в каком их отдаёт GetSecretBase64Value) и декодируются в сырые байты: client-go сам
// кодирует Data в base64 при отправке, поэтому без декодирования значения
// записывались бы дважды закодированными.
func (m *Manager) UpdateSecret(namespace, name string, data map[string]string) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	sec, err := conn.Client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Secret: %w", err)
	}

	decoded := make(map[string][]byte, len(data))
	for k, v := range data {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("value of key %q is not valid base64: %w", k, err)
		}
		decoded[k] = raw
	}
	sec.Data = decoded

	_, err = conn.Client.CoreV1().Secrets(namespace).Update(ctx, sec, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Secret: %w", err)
	}
	return nil
}
