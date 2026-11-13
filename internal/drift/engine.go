// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/24DSO-lab/kubron/internal/baseline"
)

// Ошибки движка.
var (
	// ErrStalePreview — состояние кластера изменилось после предпросмотра: подтверждать нужно то, что было показано.
	ErrStalePreview = errors.New("cluster state changed since the preview")
	// ErrPreviewExpired — токен предпросмотра просрочен или повреждён.
	ErrPreviewExpired = errors.New("preview token is expired or invalid")
	// ErrTooManyObjects — выборка больше допустимого размера версии.
	ErrTooManyObjects = fmt.Errorf("%w: too many resources selected", baseline.ErrInvalid)
)

const (
	// MaxObjects — предел числа ресурсов в одной версии.
	MaxObjects = 1000
	// PreviewTTL — срок действия токена предпросмотра.
	PreviewTTL = 10 * time.Minute
	// checkConcurrency — число одновременных чтений при проверке.
	checkConcurrency = 8
)

var hpaGVK = schema.GroupVersionKind{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"}

// Options — необязательные зависимости движка.
type Options struct {
	// Audit — поиск записей аудита Kubron; без него источник «kubron-audit» не устанавливается.
	Audit AuditLookup
	// Log — журнал; в него не попадает содержимое объектов.
	Log *slog.Logger
	// PreviewKey — ключ токенов предпросмотра (процесс-вайд). Пустой — случайный ключ на движок.
	PreviewKey []byte
}

// Engine — движок снимков и проверок. Не хранит состояния между вызовами и не пишет в кластер.
type Engine struct {
	reader ClusterReader
	attr   attributor
	log    *slog.Logger
	key    []byte
}

// NewPreviewKey возвращает случайный ключ токенов предпросмотра.
func NewPreviewKey() []byte { return NewSalt() }

// NewEngine создаёт движок поверх читателя кластера (read-only).
func NewEngine(r ClusterReader, o Options) *Engine {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	key := o.PreviewKey
	if len(key) == 0 {
		key = NewPreviewKey()
	}
	return &Engine{reader: r, attr: attributor{audit: o.Audit}, log: log, key: key}
}

// Spec — что снимать в версию: явный список ресурсов или выборка по пространствам имён, типам и селектору меток.
type Spec struct {
	Namespaces []string
	Selector   string
	Kinds      []string
	Refs       []baseline.Ref
}

// Snapshot — содержимое новой версии. ID, Seq, автор и комментарий — дело сервиса.
type Snapshot struct {
	RulesVersion int
	Salt         []byte
	Objects      []baseline.Object
	Skipped      []Skip
	// Hashed — число значений, сохранённых только как keyed hash.
	Hashed int
	// Fingerprint — отпечаток снятого состояния (не зависит от соли версии), для сверки с предпросмотром.
	Fingerprint string
}

// collected — живое состояние выборки.
type collected struct {
	objs          []*unstructured.Unstructured
	skipped       []Skip
	hpa           map[string]bool
	hpaUnreadable bool
}

func classify(err error) baseline.Reason {
	switch {
	case errors.Is(err, ErrForbidden):
		return baseline.ReasonForbidden
	case errors.Is(err, ErrTooLarge):
		return baseline.ReasonTooLarge
	case errors.Is(err, ErrKindUnavailable):
		return baseline.ReasonKindUnavailable
	default:
		return baseline.ReasonUnavailable
	}
}

func kindRef(ks *kindSpec, ns string) baseline.Ref {
	return baseline.Ref{Group: ks.GVK.Group, Version: ks.GVK.Version, Kind: ks.GVK.Kind, Namespace: ns}
}

func (e *Engine) collect(ctx context.Context, spec Spec) (*collected, error) {
	c := &collected{}
	seen := map[string]struct{}{}
	add := func(u *unstructured.Unstructured) error {
		if _, dup := seen[refOf(u).Key()]; dup {
			return nil
		}
		seen[refOf(u).Key()] = struct{}{}
		if len(c.objs) >= MaxObjects {
			return ErrTooManyObjects
		}
		c.objs = append(c.objs, u)
		return nil
	}
	if len(spec.Refs) > 0 {
		for _, r := range spec.Refs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ks, err := specFor(r.Group, r.Kind)
			if err != nil {
				c.skipped = append(c.skipped, Skip{Ref: r, Reason: skipReason(err)})
				continue
			}
			u, err := e.reader.Get(ctx, ks.GVK, r.Namespace, r.Name)
			if err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return nil, cerr
				}
				reason := classify(err)
				if errors.Is(err, ErrNotFound) {
					reason = baseline.ReasonNotFound
				}
				c.skipped = append(c.skipped, Skip{Ref: r, Reason: reason})
				continue
			}
			if err := add(u); err != nil {
				return nil, err
			}
		}
	} else {
		if len(spec.Namespaces) == 0 {
			return nil, fmt.Errorf("%w: select namespaces or resources", baseline.ErrInvalid)
		}
		kinds := spec.Kinds
		if len(kinds) == 0 {
			kinds = SupportedKinds()
		}
		for _, kind := range slices.Compact(slices.Sorted(slices.Values(kinds))) {
			ks, err := kindByName(kind)
			if err != nil {
				c.skipped = append(c.skipped, Skip{Ref: baseline.Ref{Kind: kind}, Reason: skipReason(err)})
				continue
			}
			for _, ns := range spec.Namespaces {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				res, err := e.reader.List(ctx, ks.GVK, ns, spec.Selector, listLimit)
				if err != nil {
					if cerr := ctx.Err(); cerr != nil {
						return nil, cerr
					}
					if errors.Is(err, ErrNotFound) {
						err = ErrKindUnavailable
					}
					c.skipped = append(c.skipped, Skip{Ref: kindRef(ks, ns), Reason: classify(err)})
					continue
				}
				if res.Truncated {
					c.skipped = append(c.skipped, Skip{Ref: kindRef(ks, ns), Reason: baseline.ReasonTruncated})
				}
				for i := range res.Items {
					if err := add(&res.Items[i]); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	e.loadHPA(ctx, c)
	return c, ctx.Err()
}

// kindByName ищет тип по названию (в списке выбранных типов группа не указывается).
func kindByName(kind string) (*kindSpec, error) {
	if strings.EqualFold(kind, "Secret") {
		return nil, ErrSecretUnsupported
	}
	for i := range supportedKinds {
		if supportedKinds[i].GVK.Kind == kind {
			return &supportedKinds[i], nil
		}
	}
	return nil, ErrUnsupportedKind
}

func skipReason(err error) baseline.Reason {
	if errors.Is(err, ErrSecretUnsupported) {
		return baseline.ReasonSecretUnsupported
	}
	return baseline.ReasonUnsupportedKind
}

// loadHPA находит нагрузки под HorizontalPodAutoscaler. Не прочитался HPA — оговорка в результате, а не «не проверено».
func (e *Engine) loadHPA(ctx context.Context, c *collected) {
	c.hpa = map[string]bool{}
	nss := map[string]struct{}{}
	for _, u := range c.objs {
		if k := u.GetKind(); k == "Deployment" || k == "StatefulSet" {
			nss[u.GetNamespace()] = struct{}{}
		}
	}
	for _, ns := range slices.Sorted(maps.Keys(nss)) {
		res, err := e.reader.List(ctx, hpaGVK, ns, "", listLimit)
		if err != nil {
			if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrKindUnavailable) {
				c.hpaUnreadable = true
			}
			continue
		}
		if res.Truncated {
			c.hpaUnreadable = true
		}
		for _, h := range res.Items {
			kind, _, _ := unstructured.NestedString(h.Object, "spec", "scaleTargetRef", "kind")
			name, _, _ := unstructured.NestedString(h.Object, "spec", "scaleTargetRef", "name")
			c.hpa[hpaKey(kind, ns, name)] = true
		}
	}
}

func hpaKey(kind, ns, name string) string { return kind + "/" + ns + "/" + name }

func (c *collected) excludeReplicas(u *unstructured.Unstructured) bool {
	return c.hpa[hpaKey(u.GetKind(), u.GetNamespace(), u.GetName())]
}

// normalizeAll нормализует выборку с солью salt.
func (c *collected) normalizeAll(salt []byte) ([]baseline.Object, int, []Skip) {
	objs := make([]baseline.Object, 0, len(c.objs))
	var skipped []Skip
	total := 0
	for _, u := range c.objs {
		o, n, err := normalizeObject(u, salt, normOptions{excludeReplicas: c.excludeReplicas(u)})
		if err != nil {
			skipped = append(skipped, Skip{Ref: refOf(u), Reason: skipReason(err)})
			continue
		}
		objs = append(objs, o)
		total += n
	}
	slices.SortFunc(objs, func(a, b baseline.Object) int { return strings.Compare(a.Ref.Key(), b.Ref.Key()) })
	return objs, total, skipped
}

// fingerprint — отпечаток состояния: не зависит от соли версии (хеши считаются ключом движка).
func (e *Engine) fingerprint(c *collected) string {
	objs, _, _ := c.normalizeAll(e.key)
	h := sha256.New()
	for _, o := range objs {
		fmt.Fprintf(h, "%s\x00", o.Ref.Key())
		for _, p := range slices.Sorted(maps.Keys(o.Fields)) {
			f := o.Fields[p]
			if f.Hash != nil {
				fmt.Fprintf(h, "%s=#%x\x00", p, f.Hash.HMAC)
			} else {
				fmt.Fprintf(h, "%s=%s\x00", p, f.Plain)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Snapshot снимает текущее состояние выборки для новой версии. Ничего не пишет.
func (e *Engine) Snapshot(ctx context.Context, spec Spec) (Snapshot, error) {
	c, err := e.collect(ctx, spec)
	if err != nil {
		return Snapshot{}, err
	}
	return e.snapshotOf(c), nil
}

func (e *Engine) snapshotOf(c *collected) Snapshot {
	salt := NewSalt()
	objs, hashed, extra := c.normalizeAll(salt)
	return Snapshot{
		RulesVersion: RulesVersion, Salt: salt, Objects: objs, Hashed: hashed,
		Skipped: append(slices.Clone(c.skipped), extra...), Fingerprint: e.fingerprint(c),
	}
}

// Preview — предпросмотр новой версии: что будет зафиксировано и чем это отличается от предыдущей версии.
type Preview struct {
	Token       string    `json:"token"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Objects     int       `json:"objects"`
	Hashed      int       `json:"hashed"`
	Skipped     []Skip    `json:"skipped,omitempty"`
	HasPrevious bool      `json:"hasPrevious"`
	// RulesChanged — предыдущая версия снята по другим правилам: сравнение с ней невозможно.
	RulesChanged bool                  `json:"rulesChanged"`
	Added        []baseline.Ref        `json:"added,omitempty"`
	Removed      []baseline.Ref        `json:"removed,omitempty"`
	Changed      []baseline.ItemResult `json:"changed,omitempty"`
	Unchanged    int                   `json:"unchanged"`
}

func (e *Engine) tokenMAC(fp, bind string, exp int64) string {
	m := hmac.New(sha256.New, e.key)
	fmt.Fprintf(m, "kubron/baseline/preview/v1\x00%s\x00%s\x00%d", fp, bind, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// VerifyToken проверяет токен предпросмотра для отпечатка fp и привязки bind (идентификатор baseline).
func (e *Engine) VerifyToken(token, fp, bind string, now time.Time) error {
	macHex, expStr, ok := strings.Cut(token, ".")
	if !ok {
		return ErrPreviewExpired
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > exp {
		return ErrPreviewExpired
	}
	if !hmac.Equal([]byte(macHex), []byte(e.tokenMAC(fp, bind, exp))) {
		return ErrStalePreview
	}
	return nil
}

// Preview снимает состояние и сравнивает его с предыдущей версией (prev; nil — первая версия).
func (e *Engine) Preview(ctx context.Context, spec Spec, prev *baseline.Version, bind string, now time.Time) (Preview, error) {
	c, err := e.collect(ctx, spec)
	if err != nil {
		return Preview{}, err
	}
	snap := e.snapshotOf(c)
	exp := now.Add(PreviewTTL)
	p := Preview{
		Token:     e.tokenMAC(snap.Fingerprint, bind, exp.Unix()) + "." + strconv.FormatInt(exp.Unix(), 10),
		ExpiresAt: exp, Objects: len(snap.Objects), Hashed: snap.Hashed, Skipped: snap.Skipped,
	}
	if prev == nil {
		return p, nil
	}
	p.HasPrevious = true
	if prev.RulesVersion != RulesVersion {
		p.RulesChanged = true
		return p, nil
	}
	cur, _, _ := c.normalizeAll(prev.Salt) // та же соль: значения сравнимы с сохранёнными
	old := make(map[string]baseline.Object, len(prev.Objects))
	for _, o := range prev.Objects {
		old[o.Ref.Key()] = o
	}
	for _, o := range cur {
		prevObj, ok := old[o.Ref.Key()]
		if !ok {
			p.Added = append(p.Added, o.Ref)
			continue
		}
		delete(old, o.Ref.Key())
		if ch := diffFields(o.Ref.Kind, prevObj.Fields, o.Fields); len(ch) > 0 {
			p.Changed = append(p.Changed, baseline.ItemResult{Ref: o.Ref, Status: baseline.StatusChanged, Priority: maxPriority(ch), Changes: ch})
		} else {
			p.Unchanged++
		}
	}
	for _, o := range old {
		p.Removed = append(p.Removed, o.Ref)
	}
	byKey := func(a, b baseline.Ref) int { return strings.Compare(a.Key(), b.Key()) }
	slices.SortFunc(p.Removed, byKey)
	slices.SortFunc(p.Added, byKey)
	sortItems(p.Changed)
	return p, nil
}

// SnapshotConfirmed снимает состояние и сверяет его с токеном предпросмотра: если кластер изменился, возвращает
// ErrStalePreview, и версия не создаётся.
func (e *Engine) SnapshotConfirmed(ctx context.Context, spec Spec, token, bind string, now time.Time) (Snapshot, error) {
	c, err := e.collect(ctx, spec)
	if err != nil {
		return Snapshot{}, err
	}
	snap := e.snapshotOf(c)
	if err := e.VerifyToken(token, snap.Fingerprint, bind, now); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// CheckInput — входные данные проверки.
type CheckInput struct {
	Version    baseline.Version
	Exceptions []baseline.Exception
	Now        time.Time
}

// Check сравнивает активную версию с кластером. Идентификаторы, инициатор и корреляцию заполняет сервис.
// «Не проверено» — отдельный статус и никогда не «соответствует».
func (e *Engine) Check(ctx context.Context, in CheckInput) (baseline.CheckResult, error) {
	v := in.Version
	res := baseline.CheckResult{
		RulesVersion: RulesVersion, VersionRulesVersion: v.RulesVersion, CheckedAt: in.Now.UTC(),
	}
	items := make([]baseline.ItemResult, len(v.Objects))

	if v.RulesVersion != RulesVersion {
		for i, o := range v.Objects {
			items[i] = baseline.ItemResult{Ref: o.Ref, Status: baseline.StatusUnchecked, Reason: baseline.ReasonRulesMismatch}
		}
		return e.finish(res, items, newExceptionIndex(in.Exceptions, in.Now)), nil
	}

	// HPA нужен до чтения нагрузок: пространства имён берутся из версии.
	pre := &collected{}
	for _, o := range v.Objects {
		pre.objs = append(pre.objs, &unstructured.Unstructured{Object: map[string]any{
			"kind": o.Ref.Kind, "metadata": map[string]any{"namespace": o.Ref.Namespace, "name": o.Ref.Name},
		}})
	}
	e.loadHPA(ctx, pre)
	if err := ctx.Err(); err != nil {
		return baseline.CheckResult{}, err
	}
	if pre.hpaUnreadable {
		res.Caveats = append(res.Caveats, baseline.CaveatHPAUnreadable)
	}

	sem := make(chan struct{}, checkConcurrency)
	var wg sync.WaitGroup
	for i := range v.Objects {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			items[i] = e.checkOne(ctx, v, v.Objects[i], pre)
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return baseline.CheckResult{}, err
	}

	idx := newExceptionIndex(in.Exceptions, in.Now)
	for i := range items {
		it := &items[i]
		if it.Status != baseline.StatusChanged {
			continue
		}
		kept, used := idx.filter(it.Ref, it.Changes)
		it.Suppressed = used
		it.Changes = kept
		if len(kept) == 0 {
			it.Status = baseline.StatusMatch
			it.Priority = ""
		} else {
			it.Priority = maxPriority(kept)
		}
	}
	return e.finish(res, items, idx), nil
}

func (e *Engine) finish(res baseline.CheckResult, items []baseline.ItemResult, idx *exceptionIndex) baseline.CheckResult {
	sortItems(items)
	res.Items = items
	res.Summary = summarize(items)
	res.Exceptions = idx.report()
	for _, a := range res.Exceptions.Applied {
		res.Summary.SuppressedChanges += a.Hits
	}
	return res
}

func (e *Engine) checkOne(ctx context.Context, v baseline.Version, o baseline.Object, pre *collected) baseline.ItemResult {
	item := baseline.ItemResult{Ref: o.Ref}
	unchecked := func(r baseline.Reason) baseline.ItemResult {
		item.Status, item.Reason = baseline.StatusUnchecked, r
		return item
	}
	ks, err := specFor(o.Ref.Group, o.Ref.Kind)
	if err != nil {
		return unchecked(skipReason(err)) // Secret не читается ни при каких условиях
	}
	live, err := e.reader.Get(ctx, ks.GVK, o.Ref.Namespace, o.Ref.Name)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			item.Status = baseline.StatusMissing
			return item
		default:
			r := classify(err)
			e.log.Debug("baseline check: resource not checked", "resource", o.Ref.String(), "reason", string(r))
			return unchecked(r)
		}
	}
	excl := pre.excludeReplicas(live)
	cur, _, nerr := normalizeObject(live, v.Salt, normOptions{excludeReplicas: excl})
	if nerr != nil {
		return unchecked(skipReason(nerr))
	}
	stored := o.Fields
	if excl {
		stored = maps.Clone(stored)
		delete(stored, "spec.replicas")
	}
	changes := diffFields(o.Ref.Kind, stored, cur.Fields)
	if len(changes) == 0 {
		item.Status = baseline.StatusMatch
		return item
	}
	e.attr.attribute(ctx, live, o.Ref, changes)
	item.Status = baseline.StatusChanged
	item.Changes = changes
	return item
}
