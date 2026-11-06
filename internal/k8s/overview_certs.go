// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"math"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	certWarnDays = 30
	// cert-manager обновляет сертификат заранее (обычно за треть срока, у Let's Encrypt — за 30 дней).
	// Для таких секретов окно в 30 дней — штатная работа, а не проблема: тревожим, только если
	// до конца осталось ≤ certManagedWarnDays, то есть автообновление, похоже, не сработало.
	certManagedWarnDays   = 14
	certManagerAnnotation = "cert-manager.io/certificate-name"
)

// CertExpiry — TLS-сертификат, срок которого истекает (или уже истёк). Содержимое секрета наружу не отдаётся.
type CertExpiry struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Subject   string `json:"subject"`
	NotAfter  string `json:"notAfter"` // RFC3339
	DaysLeft  int    `json:"daysLeft"` // отрицательное — уже истёк
	Managed   bool   `json:"managed"`  // выпущен cert-manager
}

// CertScan — результат проверки: сколько сертификатов прочитано и какие из них требуют внимания.
type CertScan struct {
	Checked  int          `json:"checked"`
	Expiring []CertExpiry `json:"expiring"`
}

// GetCertificateExpiry читает TLS-секреты (kubernetes.io/tls) и возвращает только имя, субъект и дату окончания.
// Нужно право secrets.read: вызывающий метод App обязан проверять его.
func (m *Manager) GetCertificateExpiry() (*CertScan, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), 20*time.Second)
	defer cancel()

	list, err := conn.Client.CoreV1().Secrets("").List(ctx, metav1.ListOptions{
		FieldSelector: "type=" + string(corev1.SecretTypeTLS),
	})
	if err != nil {
		return nil, err
	}
	scan := scanTLSSecrets(list.Items, time.Now())
	return &scan, nil
}

func scanTLSSecrets(secrets []corev1.Secret, now time.Time) CertScan {
	scan := CertScan{Expiring: []CertExpiry{}}
	for i := range secrets {
		s := &secrets[i]
		leaf := firstCertificate(s.Data[corev1.TLSCertKey])
		if leaf == nil {
			continue
		}
		scan.Checked++

		days := int(math.Floor(leaf.NotAfter.Sub(now).Hours() / 24))
		_, managed := s.Annotations[certManagerAnnotation]
		limit := certWarnDays
		if managed {
			limit = certManagedWarnDays
		}
		if days > limit {
			continue
		}
		subject := leaf.Subject.CommonName
		if subject == "" && len(leaf.DNSNames) > 0 {
			subject = leaf.DNSNames[0]
		}
		scan.Expiring = append(scan.Expiring, CertExpiry{
			Namespace: s.Namespace,
			Name:      s.Name,
			Subject:   subject,
			NotAfter:  leaf.NotAfter.UTC().Format(time.RFC3339),
			DaysLeft:  days,
			Managed:   managed,
		})
	}
	sort.SliceStable(scan.Expiring, func(i, j int) bool {
		a, b := scan.Expiring[i], scan.Expiring[j]
		if a.DaysLeft != b.DaysLeft {
			return a.DaysLeft < b.DaysLeft
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	return scan
}

// firstCertificate возвращает первый (листовой) сертификат из PEM-цепочки; nil, если разобрать нельзя.
func firstCertificate(data []byte) *x509.Certificate {
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return nil
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(block.Bytes); err == nil {
			return c
		}
		return nil
	}
	return nil
}
