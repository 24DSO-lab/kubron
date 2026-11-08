// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GetFluxImageAutomation возвращает ImageRepository, ImagePolicy и ImageUpdateAutomation из набора namespace
// (пусто — из всех). Читаются только объекты кластера через Kubernetes API: к реестрам образов Kubron не
// обращается (число тегов и последний образ берутся из status, который заполняет image-reflector-controller).
//
// Версия вида (v1 или v1beta2) берётся из allowlist и discovery (fluxServedVersions). Вид без CRD, без версии из
// allowlist или без права list пропускается без ошибки; сбой одного вида не отменяет остальные, ошибка
// возвращается, только если не прочитан ни один вид.
func (m *Manager) GetFluxImageAutomation(namespaces []string) (*FluxImageAutomation, error) {
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

	out := &FluxImageAutomation{
		Repositories: []FluxImageRepositoryInfo{},
		Policies:     []FluxImagePolicyInfo{},
		Automations:  []FluxImageUpdateAutomationInfo{},
	}

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
		case imageKindSkippable(err):
			// Нет CRD или права list на этот вид: его просто нет в ответе.
		default:
			failures = append(failures, fmt.Errorf("%s: %w", k.Kind, err))
		}
	}
	for _, k := range fluxImageKindsToRead(versions) {
		wg.Go(func() {
			switch k.Kind {
			case FluxKindImageRepository:
				items, err := fluxList(m, k, namespaces, parseFluxImageRepository, func(i FluxImageRepositoryInfo) string { return i.Namespace })
				if err == nil {
					out.Repositories = items
				}
				finish(k, err)
			case FluxKindImagePolicy:
				items, err := fluxList(m, k, namespaces, parseFluxImagePolicy, func(i FluxImagePolicyInfo) string { return i.Namespace })
				if err == nil {
					out.Policies = items
				}
				finish(k, err)
			case FluxKindImageUpdateAutomation:
				items, err := fluxList(m, k, namespaces, parseFluxImageUpdateAutomation, func(i FluxImageUpdateAutomationInfo) string { return i.Namespace })
				if err == nil {
					out.Automations = items
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
		slog.Warn("some Flux image automation kinds could not be read", "error", errors.Join(failures...))
	}

	// Стабильный порядок для интерфейса: namespace, затем имя.
	byName := func(ns1, n1, ns2, n2 string) int { return cmp.Or(cmp.Compare(ns1, ns2), cmp.Compare(n1, n2)) }
	slices.SortFunc(out.Repositories, func(a, b FluxImageRepositoryInfo) int { return byName(a.Namespace, a.Name, b.Namespace, b.Name) })
	slices.SortFunc(out.Policies, func(a, b FluxImagePolicyInfo) int { return byName(a.Namespace, a.Name, b.Namespace, b.Name) })
	slices.SortFunc(out.Automations, func(a, b FluxImageUpdateAutomationInfo) int { return byName(a.Namespace, a.Name, b.Namespace, b.Name) })
	return out, nil
}

// fluxImageKindsToRead выбирает виды image automation, которые есть в кластере в поддерживаемой версии: версия
// вида из карты discovery должна входить в allowlist вида (v1 или v1beta2).
func fluxImageKindsToRead(versions map[string]string) []fluxKind {
	var out []fluxKind
	for _, k := range fluxKindsOf(fluxCtrlImage) {
		if v, ok := versions[k.Kind]; ok && k.supports(v) {
			out = append(out, k)
		}
	}
	return out
}

// imageKindSkippable — ошибки, при которых вид молча пропускается: нет права list (RBAC в мультитенантной
// установке выдают по видам) или API-группа не обслуживается. Таймаут и прочие сбои — настоящие ошибки.
func imageKindSkippable(err error) bool {
	return apierrors.IsForbidden(err) || apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err)
}

// imageBase заполняет общую часть объектов image automation; lastAt — запасное время «последнего reconcile» вида
// (скан репозитория, запуск автоматизации), если контроллер ещё не выставил Ready.
func imageBase(kind string, u *unstructured.Unstructured, lastAt string) FluxObjectInfo {
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

		LastReconcileAt: imageReconcileAt(conds, lastAt),
		Message:         imageMessage(conds),

		Labels:      labels,
		Annotations: fluxAnnotations(u.GetAnnotations()),
	}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		info.Age = formatAge(ts.Time)
		info.Created = ts.Format(time.RFC3339)
	}
	return info
}

// imageReconcileAt — lastTransitionTime условия Ready (его ставит сам контроллер при значимом результате), иначе
// время, которое вид пишет сам (скан или запуск автоматизации).
func imageReconcileAt(conds []ConditionInfo, fallback string) string {
	for _, c := range conds {
		if c.Type == "Ready" && c.LastTransitionTime != "" {
			return c.LastTransitionTime
		}
	}
	return fallback
}

// imageMessage — сообщение условия Ready, при его отсутствии Stalled или Reconciling. Тексты в conds уже прошли
// redactFluxMessage.
func imageMessage(conds []ConditionInfo) string {
	for _, typ := range [...]string{"Ready", "Stalled", "Reconciling"} {
		for _, c := range conds {
			if c.Type == typ && c.Message != "" {
				return c.Message
			}
		}
	}
	return ""
}

// parseFluxImageRepository защитно разбирает ImageRepository: отсутствующее или неожиданного типа поле даёт нулевое
// значение. Адрес образа отдаётся без userinfo. Не отдаются: secretRef, certSecretRef, serviceAccountName, списки
// исключений и прочие ссылки, last-applied-configuration. Число тегов и время скана — из status.lastScanResult;
// сами теги (latestTags) в DTO не попадают.
func parseFluxImageRepository(u *unstructured.Unstructured) FluxImageRepositoryInfo {
	obj := u.Object
	scanAt := strAt(obj, "status", "lastScanResult", "scanTime")
	return FluxImageRepositoryInfo{
		FluxObjectInfo: imageBase(FluxKindImageRepository, u, scanAt),
		Image:          fluxImageRef(strAt(obj, "spec", "image")),
		TagCount:       imageCount(obj, "status", "lastScanResult", "tagCount"),
		LastScanAt:     scanAt,
	}
}

// parseFluxImagePolicy защитно разбирает ImagePolicy. Не отдаются: filterTags (шаблоны), digestReflection и прочее.
func parseFluxImagePolicy(u *unstructured.Unstructured) FluxImagePolicyInfo {
	obj := u.Object
	return FluxImagePolicyInfo{
		FluxObjectInfo:  imageBase(FluxKindImagePolicy, u, ""),
		ImageRepository: strAt(obj, "spec", "imageRepositoryRef", "name"),
		Policy:          imagePolicyRule(obj),
		LatestImage:     imageLatest(obj),
	}
}

// parseFluxImageUpdateAutomation защитно разбирает ImageUpdateAutomation; источник — SourceRef общей части
// (namespace по умолчанию — namespace объекта). Не отдаются: адрес электронной почты и имя автора коммитов, шаблоны
// сообщений, ключ подписи, refspec, путь и стратегия обновления (в DTO нет полей).
func parseFluxImageUpdateAutomation(u *unstructured.Unstructured) FluxImageUpdateAutomationInfo {
	obj := u.Object
	runAt := strAt(obj, "status", "lastAutomationRunTime")
	info := FluxImageUpdateAutomationInfo{
		FluxObjectInfo: imageBase(FluxKindImageUpdateAutomation, u, runAt),
		// Ветка push главнее: туда автоматизация реально пишет; без push — ветка checkout (коммит в ту же ветку).
		Branch:         cmp.Or(strAt(obj, "spec", "git", "push", "branch"), strAt(obj, "spec", "git", "checkout", "ref", "branch")),
		LastRunAt:      runAt,
		LastPushCommit: strAt(obj, "status", "lastPushCommit"),
	}
	if ref, ok := mapAt(obj, "spec", "sourceRef"); ok {
		if name := strAt(ref, "name"); name != "" {
			info.SourceRef = &FluxRef{Kind: strAt(ref, "kind"), Namespace: cmp.Or(strAt(ref, "namespace"), u.GetNamespace()), Name: name}
		}
	}
	return info
}

// imagePolicyRule описывает правило выбора тега: «semver: >=1.0.0», «alphabetical: asc», «numerical: desc».
// Правил по документации одно из трёх; если заданы несколько, берётся первое в порядке semver, alphabetical,
// numerical. Порядок alphabetical и numerical по умолчанию asc. Незнакомое правило — пустая строка.
func imagePolicyRule(obj map[string]any) string {
	policy, ok := mapAt(obj, "spec", "policy")
	if !ok {
		return ""
	}
	if _, ok := policy["semver"]; ok {
		if r := strAt(policy, "semver", "range"); r != "" {
			return "semver: " + r
		}
		return "semver"
	}
	for _, kind := range [...]string{"alphabetical", "numerical"} {
		if _, ok := policy[kind]; ok {
			return kind + ": " + cmp.Or(strAt(policy, kind, "order"), "asc")
		}
	}
	return ""
}

// imageLatest собирает последний образ политики: из status.latestRef («имя:тег@дайджест», дайджест при наличии),
// а у v1beta2 — из строки status.latestImage. Имя образа отдаётся без userinfo.
func imageLatest(obj map[string]any) string {
	if ref, ok := mapAt(obj, "status", "latestRef"); ok {
		name := fluxImageRef(strAt(ref, "name"))
		if name == "" {
			return ""
		}
		if tag := strAt(ref, "tag"); tag != "" {
			name += ":" + tag
		}
		if digest := strAt(ref, "digest"); digest != "" {
			name += "@" + digest
		}
		return name
	}
	return fluxImageRef(strAt(obj, "status", "latestImage"))
}

// imageCount читает неотрицательное целое из status: после разбора YAML это int64, после JSON — float64.
// Строки, дробные, отрицательные и недопустимо большие значения дают ноль.
func imageCount(obj map[string]any, path ...string) int {
	v, ok := nestedValue(obj, path...)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return max(n, 0)
	case int64:
		if n < 0 || n > math.MaxInt32 {
			return 0
		}
		return int(n)
	case float64:
		if n < 0 || n > math.MaxInt32 || n != math.Trunc(n) {
			return 0
		}
		return int(n)
	}
	return 0
}

// nestedValue идёт по вложенным словарям и возвращает значение по пути.
func nestedValue(obj map[string]any, path ...string) (any, bool) {
	var cur any = obj
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[p]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// reImageDigest — дайджест в конце ссылки на образ («sha256:<hex>»): символ «@» перед ним не userinfo.
var reImageDigest = regexp.MustCompile(`@[A-Za-z][A-Za-z0-9+._-]*:[0-9a-fA-F]{32,}$`)

// fluxImageRef готовит ссылку на образ к показу без userinfo, query и fragment. Схема убирается («https://host/app»
// → «host/app»). Если адрес со схемой не разбирается и в нём есть «@», он не отдаётся вовсе: по обрывку нельзя
// поручиться, что в нём нет пароля.
func fluxImageRef(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			if strings.Contains(raw, "@") {
				return ""
			}
			return raw
		}
		return u.Host + u.Path
	}

	// Дайджест отделяется заранее: «@» перед ним — часть ссылки, а любой «@» раньше — это userinfo.
	digest := ""
	if loc := reImageDigest.FindStringIndex(raw); loc != nil {
		raw, digest = raw[:loc[0]], raw[loc[0]:]
	}
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		raw = raw[i+1:]
	}
	return raw + digest
}
