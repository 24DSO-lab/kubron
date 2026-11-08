// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GetFluxNotifications возвращает Provider, Alert и Receiver из набора namespace (пусто — из всех).
// Читается только через Kubernetes API. Версия вида берётся из allowlist и discovery (fluxServedVersions): Provider и
// Alert — v1beta3, Receiver — v1 или v1beta2. Вид без CRD, без версии из allowlist или без права list пропускается
// без ошибки; сбой одного вида не отменяет остальные, ошибка возвращается, только если не прочитан ни один вид.
func (m *Manager) GetFluxNotifications(namespaces []string) (*FluxNotifications, error) {
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

	out := &FluxNotifications{
		Providers: []FluxProviderInfo{},
		Alerts:    []FluxAlertInfo{},
		Receivers: []FluxReceiverInfo{},
	}
	kinds := fluxNotificationKindsToRead(versions)

	// Каждый вид читается своим запросом и кладёт результат в своё поле; учёт успехов и отказов общий.
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		failures []error
		read     int
	)
	finish := func(k fluxKind, err error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			read++
		case notificationKindSkippable(err):
			// Нет CRD или права list на этот вид: его просто нет в ответе.
		default:
			failures = append(failures, fmt.Errorf("%s: %w", k.Kind, err))
		}
	}
	for _, k := range kinds {
		wg.Go(func() {
			switch k.Kind {
			case FluxKindProvider:
				items, err := fluxList(m, k, namespaces, parseFluxProvider, func(i FluxProviderInfo) string { return i.Namespace })
				if err == nil {
					out.Providers = items
				}
				finish(k, err)
			case FluxKindAlert:
				items, err := fluxList(m, k, namespaces, parseFluxAlert, func(i FluxAlertInfo) string { return i.Namespace })
				if err == nil {
					out.Alerts = items
				}
				finish(k, err)
			case FluxKindReceiver:
				items, err := fluxList(m, k, namespaces, parseFluxReceiver, func(i FluxReceiverInfo) string { return i.Namespace })
				if err == nil {
					out.Receivers = items
				}
				finish(k, err)
			}
		})
	}
	wg.Wait()

	if len(failures) > 0 {
		if read == 0 {
			return nil, errors.Join(failures...)
		}
		slog.Warn("some Flux notification kinds could not be read", "error", errors.Join(failures...))
	}

	// Стабильный порядок для интерфейса: namespace, затем имя. Пустые списки остаются непустыми значениями
	// (в JSON — [], не null).
	byName := func(ns1, n1, ns2, n2 string) int { return cmp.Or(cmp.Compare(ns1, ns2), cmp.Compare(n1, n2)) }
	slices.SortFunc(out.Providers, func(a, b FluxProviderInfo) int { return byName(a.Namespace, a.Name, b.Namespace, b.Name) })
	slices.SortFunc(out.Alerts, func(a, b FluxAlertInfo) int { return byName(a.Namespace, a.Name, b.Namespace, b.Name) })
	slices.SortFunc(out.Receivers, func(a, b FluxReceiverInfo) int { return byName(a.Namespace, a.Name, b.Namespace, b.Name) })
	return out, nil
}

// fluxNotificationKindsToRead выбирает виды Notification, которые есть в кластере в поддерживаемой версии: версия
// вида из карты discovery должна входить в allowlist вида.
func fluxNotificationKindsToRead(versions map[string]string) []fluxKind {
	var out []fluxKind
	for _, k := range fluxKindsOf(fluxCtrlNotification) {
		if v, ok := versions[k.Kind]; ok && k.supports(v) {
			out = append(out, k)
		}
	}
	return out
}

// notificationKindSkippable — ошибки, при которых вид молча пропускается: нет права list (RBAC в мультитенантной
// установке выдают по видам) или API-группа не обслуживается. Таймаут и прочие сбои — настоящие ошибки.
func notificationKindSkippable(err error) bool {
	return apierrors.IsForbidden(err) || apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err)
}

// notificationBase заполняет общую часть Notification-объектов. У них нет sourceRef, dependsOn и ревизий.
func notificationBase(kind string, u *unstructured.Unstructured) FluxObjectInfo {
	conds := fluxConditions(u.Object)
	suspended := boolAt(u.Object, "spec", "suspend")

	labels := maps.Clone(u.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}
	info := FluxObjectInfo{
		Kind:      kind,
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),

		Interval:  strAt(u.Object, "spec", "interval"),
		Suspended: suspended,
		State:     FluxStateOf(conds, suspended),

		DependsOn:  []FluxRef{},
		Conditions: conds,

		LastReconcileAt: notificationReconcileAt(conds),
		Message:         notificationMessage(conds),

		Labels:      labels,
		Annotations: fluxAnnotations(u.GetAnnotations()),
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		info.Age = formatAge(ts.Time)
		info.Created = ts.Format(time.RFC3339)
	}
	return info
}

// notificationReconcileAt — lastTransitionTime условия Ready (его ставит сам контроллер при значимом результате).
// У Notification-объектов нет lastHandledReconcileAt и артефакта, запасного источника времени нет.
func notificationReconcileAt(conds []ConditionInfo) string {
	for _, c := range conds {
		if c.Type == "Ready" && c.LastTransitionTime != "" {
			return c.LastTransitionTime
		}
	}
	return ""
}

// notificationMessage — сообщение условия Ready, при его отсутствии Stalled. Тексты в conds уже прошли
// redactFluxMessage.
func notificationMessage(conds []ConditionInfo) string {
	for _, typ := range [...]string{"Ready", "Stalled"} {
		for _, c := range conds {
			if c.Type == typ && c.Message != "" {
				return c.Message
			}
		}
	}
	return ""
}

// notificationRefs разбирает список объектов (spec.eventSources у Alert, spec.resources у Receiver) в []FluxRef.
// Namespace по умолчанию — namespace самого объекта, как делает контроллер; имя может быть «*». Записи без вида и
// имени и неожиданного типа пропускаются. matchLabels не отдаются: в DTO для них нет поля. Никогда не nil.
func notificationRefs(obj map[string]any, defaultNS string, field string) []FluxRef {
	items, _ := sliceAt(obj, "spec", field)
	out := make([]FluxRef, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		kind, name := strAt(m, "kind"), strAt(m, "name")
		if kind == "" && name == "" {
			continue
		}
		out = append(out, FluxRef{Kind: kind, Namespace: cmp.Or(strAt(m, "namespace"), defaultNS), Name: name})
	}
	return out
}

// parseFluxProvider защитно разбирает Provider: отсутствующее или неожиданного типа поле даёт нулевое значение.
//
// Адрес отдаётся только как схема и хост (fluxAddressHost): в путях и query вебхуков бывают токены, userinfo
// отбрасывается. Из secretRef — только имя. Не отдаются: spec.username (в DTO нет поля), proxy (в нём бывает
// userinfo), certSecretRef и прочие ссылки, last-applied-configuration.
func parseFluxProvider(u *unstructured.Unstructured) FluxProviderInfo {
	obj := u.Object
	return FluxProviderInfo{
		FluxObjectInfo: notificationBase(FluxKindProvider, u),
		Type:           strAt(obj, "spec", "type"),
		Channel:        strAt(obj, "spec", "channel"),
		Address:        fluxAddressHost(strAt(obj, "spec", "address")),
		SecretRef:      strAt(obj, "spec", "secretRef", "name"),
	}
}

// parseFluxAlert защитно разбирает Alert. eventSeverity по документации по умолчанию info. Не отдаются:
// inclusionList, exclusionList, summary (шаблоны, не нужные интерфейсу), matchLabels.
func parseFluxAlert(u *unstructured.Unstructured) FluxAlertInfo {
	obj := u.Object
	return FluxAlertInfo{
		FluxObjectInfo: notificationBase(FluxKindAlert, u),
		ProviderRef:    strAt(obj, "spec", "providerRef", "name"),
		EventSeverity:  cmp.Or(strAt(obj, "spec", "eventSeverity"), "info"),
		EventSources:   notificationRefs(obj, u.GetNamespace(), "eventSources"),
	}
}

// reWebhookPath — путь вебхука Receiver в тексте: контроллер печатает его в сообщении условия Ready
// («Receiver initialized for path: /hook/<sha256>»).
var reWebhookPath = regexp.MustCompile(`/hook/[^\s'"]+`)

// scrubWebhookPath убирает путь вебхука из текста: и точное значение status.webhookPath (оно может не начинаться
// с /hook/), и любой фрагмент вида /hook/....
func scrubWebhookPath(s, path string) string {
	if path != "" {
		s = strings.ReplaceAll(s, path, "[redacted]")
	}
	return reWebhookPath.ReplaceAllString(s, "[redacted]")
}

// parseFluxReceiver защитно разбирает Receiver. status.webhookPath и любые производные значения НЕ отдаются: сам
// путь служит секретом (зная его, можно запустить reconcile), поэтому он вырезается и из сообщений условий.
// Из secretRef — только имя; events в DTO нет.
func parseFluxReceiver(u *unstructured.Unstructured) FluxReceiverInfo {
	obj := u.Object
	info := FluxReceiverInfo{
		FluxObjectInfo: notificationBase(FluxKindReceiver, u),
		Type:           strAt(obj, "spec", "type"),
		Resources:      notificationRefs(obj, u.GetNamespace(), "resources"),
		SecretRef:      strAt(obj, "spec", "secretRef", "name"),
	}
	path := strAt(obj, "status", "webhookPath")
	info.Message = scrubWebhookPath(info.Message, path)
	for i := range info.Conditions {
		info.Conditions[i].Message = scrubWebhookPath(info.Conditions[i].Message, path)
	}
	return info
}
