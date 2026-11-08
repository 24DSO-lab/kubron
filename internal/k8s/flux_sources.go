// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GetFluxSources возвращает источники (GitRepository, OCIRepository, HelmRepository, HelmChart, Bucket)
// из набора namespace (пусто — из всех). Читается только через Kubernetes API.
//
// Версия каждого вида берётся из allowlist и результата discovery (fluxServedVersions); вид без CRD, без версии
// из allowlist или без права list пропускается без ошибки. Сбой одного вида не отменяет остальные: ошибка
// возвращается, только если не удалось прочитать ни один вид (а хотя бы один отказал не по причине «нет CRD/прав»).
func (m *Manager) GetFluxSources(namespaces []string) ([]FluxSourceInfo, error) {
	// Неверная область — одна понятная ошибка, а не по одной на каждый вид.
	if _, err := parseScope(namespaces); err != nil {
		return nil, err
	}
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	versions, err := fluxServedVersions(conn)
	if err != nil {
		return nil, err
	}

	kinds := fluxSourceKindsToRead(versions)
	type result struct {
		items []FluxSourceInfo
		err   error
	}
	results := make([]result, len(kinds))
	var wg sync.WaitGroup
	for i, k := range kinds {
		wg.Go(func() {
			items, err := fluxList(m, k, namespaces,
				func(u *unstructured.Unstructured) FluxSourceInfo { return parseFluxSource(k.Kind, u) },
				func(s FluxSourceInfo) string { return s.Namespace })
			results[i] = result{items, err}
		})
	}
	wg.Wait()

	out := []FluxSourceInfo{}
	var failures []error
	read := 0
	for i, r := range results {
		switch {
		case r.err == nil:
			read++
			out = append(out, r.items...)
		case fluxKindSkippable(r.err):
			// Нет CRD или права list на этот вид: его просто нет в ответе.
		default:
			failures = append(failures, fmt.Errorf("%s: %w", kinds[i].Kind, r.err))
		}
	}
	if len(failures) > 0 {
		if read == 0 {
			return nil, errors.Join(failures...)
		}
		slog.Warn("some Flux source kinds could not be read", "error", errors.Join(failures...))
	}

	// Стабильный порядок для интерфейса: вид в порядке таблицы, затем namespace и имя.
	rank := make(map[string]int, len(fluxKinds))
	for i, k := range fluxKinds {
		rank[k.Kind] = i
	}
	slices.SortStableFunc(out, func(a, b FluxSourceInfo) int {
		return cmp.Or(cmp.Compare(rank[a.Kind], rank[b.Kind]), cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// fluxSourceKindsToRead выбирает виды источников, которые есть в кластере в поддерживаемой версии: версия вида
// из карты discovery должна входить в allowlist вида (так OCIRepository и Bucket во Flux 2.3 читаются как v1beta2).
func fluxSourceKindsToRead(versions map[string]string) []fluxKind {
	var out []fluxKind
	for _, k := range fluxKindsOf(fluxCtrlSource) {
		if v, ok := versions[k.Kind]; ok && k.supports(v) {
			out = append(out, k)
		}
	}
	return out
}

// fluxKindSkippable — ошибки, при которых вид молча пропускается: нет права list (RBAC в мультитенантной
// установке выдают по видам) или API-группа не обслуживается. Таймаут и прочие сбои — настоящие ошибки.
func fluxKindSkippable(err error) bool {
	return apierrors.IsForbidden(err) || apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err)
}

// fluxRefPrecedence — порядок разрешения ссылки в source-controller: первая заданная выигрывает.
var fluxRefPrecedence = map[string][]string{
	FluxKindGitRepository: {"commit", "name", "semver", "tag", "branch"},
	FluxKindOCIRepository: {"digest", "semver", "tag"},
}

// parseFluxSource защитно превращает unstructured источник любого из пяти видов в контракт для UI: отсутствующее
// или неожиданного типа поле даёт нулевое значение, а не панику (CRD живёт в чужих версиях Flux).
//
// Наружу НЕ отдаются (ADR-0023, раздел 4): userinfo, query и fragment в адресах (в query бывают токены, например
// SAS у Azure), содержимое секретов (из spec.secretRef только имя), spec.ignore, verify, proxySecretRef,
// certSecretRef и прочие шаблоны и ссылки, не нужные интерфейсу, аннотация last-applied-configuration.
func parseFluxSource(kind string, u *unstructured.Unstructured) FluxSourceInfo {
	obj := u.Object
	ns := u.GetNamespace()
	conds := fluxConditions(obj)
	suspended := boolAt(obj, "spec", "suspend")

	labels := maps.Clone(u.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}

	info := FluxSourceInfo{
		FluxObjectInfo: FluxObjectInfo{
			Kind:      kind,
			Name:      u.GetName(),
			Namespace: ns,

			Interval:  strAt(obj, "spec", "interval"),
			Suspended: suspended,
			State:     FluxStateOf(conds, suspended),

			DependsOn:  []FluxRef{},
			Conditions: conds,

			// У источников Revision — ревизия артефакта.
			Revision:        strAt(obj, "status", "artifact", "revision"),
			LastReconcileAt: sourceReconcileAt(obj, conds),
			Message:         sourceMessage(conds),

			Labels:      labels,
			Annotations: fluxAnnotations(u.GetAnnotations()),
		},
		Digest:    strAt(obj, "status", "artifact", "digest"),
		SecretRef: strAt(obj, "spec", "secretRef", "name"), // только имя: значение секрета не читается
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		info.Age = formatAge(ts.Time)
		info.Created = ts.Format(time.RFC3339)
	}

	switch kind {
	case FluxKindGitRepository, FluxKindOCIRepository:
		info.URL = fluxSourceURL(strAt(obj, "spec", "url"))
		info.RefType, info.RefValue = fluxSourceRef(obj, kind)
	case FluxKindHelmRepository:
		// Тип (default или oci) виден по схеме адреса: у OCI-репозитория это oci://.
		info.URL = fluxSourceURL(strAt(obj, "spec", "url"))
	case FluxKindHelmChart:
		info.Chart = strAt(obj, "spec", "chart")
		info.ChartVersion = strAt(obj, "spec", "version")
		if ref, ok := mapAt(obj, "spec", "sourceRef"); ok {
			if name := strAt(ref, "name"); name != "" {
				info.SourceRef = &FluxRef{Kind: strAt(ref, "kind"), Namespace: cmp.Or(strAt(ref, "namespace"), ns), Name: name}
			}
		}
	case FluxKindBucket:
		info.URL = fluxSourceURL(strAt(obj, "spec", "endpoint"))
		info.BucketName = strAt(obj, "spec", "bucketName")
	}
	return info
}

// fluxSourceRef возвращает тип и значение ссылки на ревизию (spec.ref) по порядку разрешения source-controller.
// Пустые строки считаются незаданными. Нет ссылки — пустые значения.
func fluxSourceRef(obj map[string]any, kind string) (refType, refValue string) {
	for _, field := range fluxRefPrecedence[kind] {
		if v := strAt(obj, "spec", "ref", field); v != "" {
			return field, v
		}
	}
	return "", ""
}

// fluxSourceURL готовит адрес источника к показу: без userinfo, query и fragment. Адрес без схемы (endpoint
// корзины: «s3.amazonaws.com», «minio:9000») остаётся как есть. Если адрес не разбирается и в нём есть «@»,
// он не отдаётся вовсе: по обрывку нельзя поручиться, что в нём нет пароля.
func fluxSourceURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		if strings.Contains(raw, "@") {
			return ""
		}
		return raw
	}
	// «user:pass@host:9000» без схемы разбирается как схема «user» и непрозрачная часть: здесь userinfo не вырезать.
	if strings.Contains(u.Opaque, "@") {
		return ""
	}
	u.User = nil
	u.RawQuery, u.ForceQuery = "", false
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

// sourceReconcileAt выбирает момент «последнего reconcile» источника: lastTransitionTime условия Ready (его ставит
// сам контроллер при каждом значимом результате), затем время обновления артефакта, затем status.lastHandledReconcileAt
// — но только если он разбирается как время (это токен запроса, который пишет клиент, а не метка контроллера).
func sourceReconcileAt(obj map[string]any, conds []ConditionInfo) string {
	for _, c := range conds {
		if c.Type == "Ready" && c.LastTransitionTime != "" {
			return c.LastTransitionTime
		}
	}
	if t := strAt(obj, "status", "artifact", "lastUpdateTime"); t != "" {
		return t
	}
	if token := strAt(obj, "status", "lastHandledReconcileAt"); token != "" {
		if _, err := time.Parse(time.RFC3339Nano, token); err == nil {
			return token
		}
	}
	return ""
}

// sourceMessage — сообщение для списка: условие Ready, при его отсутствии Stalled или Reconciling.
// Тексты в conds уже прошли redactFluxMessage.
func sourceMessage(conds []ConditionInfo) string {
	for _, typ := range [...]string{"Ready", "Stalled", "Reconciling"} {
		for _, c := range conds {
			if c.Type == typ && c.Message != "" {
				return c.Message
			}
		}
	}
	return ""
}
