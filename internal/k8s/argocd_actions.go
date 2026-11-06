// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"
)

const (
	argoRefreshAnnotation = "argocd.argoproj.io/refresh"
	argoInitiator         = "kubron"
	argoActionTimeout     = 60 * time.Second
	argoMaxBulk           = 200
	argoMaxRevisionLen    = 256

	argoPhaseRunning     = "Running"
	argoPhaseTerminating = "Terminating"
)

// ArgoAppRef — ссылка на Application (для массовых действий).
type ArgoAppRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ArgoSyncOptions — параметры синхронизации. Revision допустим только для одного приложения.
type ArgoSyncOptions struct {
	Revision string `json:"revision"`
	Prune    bool   `json:"prune"`
	Force    bool   `json:"force"`
	DryRun   bool   `json:"dryRun"`
}

// ArgoActionResult — итог действия над одним приложением; Error пуст при успехе.
type ArgoActionResult struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Error     string `json:"error"`
}

var (
	errArgoBusy    = errors.New("an operation is already running for this application")
	errArgoDeleted = errors.New("application is being deleted")
)

// ─── Manager: тонкие обёртки над функциями, которые тестируются на fake dynamic client ───

func (m *Manager) SyncArgoApplication(apps []ArgoAppRef, opts ArgoSyncOptions) ([]ArgoActionResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), argoActionTimeout)
	defer cancel()
	return syncArgoApps(ctx, conn.DynamicClient, apps, opts)
}

// RefreshArgoApplication просит контроллер пересчитать приложение (hard — со сбросом кеша манифестов).
func (m *Manager) RefreshArgoApplication(apps []ArgoAppRef, hard bool) ([]ArgoActionResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), argoActionTimeout)
	defer cancel()
	return refreshArgoApps(ctx, conn.DynamicClient, apps, hard)
}

func (m *Manager) TerminateArgoOperation(apps []ArgoAppRef) ([]ArgoActionResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), argoActionTimeout)
	defer cancel()
	return terminateArgoApps(ctx, conn.DynamicClient, apps)
}

// RollbackArgoApplication откатывает приложение к записи истории id.
func (m *Manager) RollbackArgoApplication(namespace, name string, id int64, prune, dryRun bool) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), argoActionTimeout)
	defer cancel()
	ref := ArgoAppRef{Namespace: strings.TrimSpace(namespace), Name: strings.TrimSpace(name)}
	if ref.Namespace == "" || ref.Name == "" {
		return errors.New("no application specified")
	}
	ri := conn.DynamicClient.Resource(kindTable["application"].gvr).Namespace(ref.Namespace)
	return updateArgoApp(ctx, ri, ref.Name, func(obj *unstructured.Unstructured) error {
		return applyRollback(obj, id, prune, dryRun)
	})
}

// ─── Массовые операции ───

func syncArgoApps(ctx context.Context, dyn dynamic.Interface, apps []ArgoAppRef, opts ArgoSyncOptions) ([]ArgoActionResult, error) {
	if err := validateArgoSyncOptions(opts, len(apps)); err != nil {
		return nil, err
	}
	return forEachArgoApp(dyn, apps, func(ri dynamic.ResourceInterface, ref ArgoAppRef) error {
		return updateArgoApp(ctx, ri, ref.Name, func(obj *unstructured.Unstructured) error {
			return applySyncOperation(obj, opts)
		})
	})
}

func refreshArgoApps(ctx context.Context, dyn dynamic.Interface, apps []ArgoAppRef, hard bool) ([]ArgoActionResult, error) {
	mode := "normal"
	if hard {
		mode = "hard"
	}
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`, argoRefreshAnnotation, mode))
	return forEachArgoApp(dyn, apps, func(ri dynamic.ResourceInterface, ref ArgoAppRef) error {
		_, err := ri.Patch(ctx, ref.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	})
}

func terminateArgoApps(ctx context.Context, dyn dynamic.Interface, apps []ArgoAppRef) ([]ArgoActionResult, error) {
	return forEachArgoApp(dyn, apps, func(ri dynamic.ResourceInterface, ref ArgoAppRef) error {
		return updateArgoApp(ctx, ri, ref.Name, applyTerminate)
	})
}

// forEachArgoApp выполняет op для каждого приложения; ошибка одного не прерывает остальные.
func forEachArgoApp(dyn dynamic.Interface, apps []ArgoAppRef, op func(ri dynamic.ResourceInterface, ref ArgoAppRef) error) ([]ArgoActionResult, error) {
	refs, err := normalizeArgoRefs(apps)
	if err != nil {
		return nil, err
	}
	res := dyn.Resource(kindTable["application"].gvr)
	out := make([]ArgoActionResult, 0, len(refs))
	for _, ref := range refs {
		r := ArgoActionResult{Namespace: ref.Namespace, Name: ref.Name}
		if err := op(res.Namespace(ref.Namespace), ref); err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out, nil
}

func normalizeArgoRefs(apps []ArgoAppRef) ([]ArgoAppRef, error) {
	if len(apps) == 0 {
		return nil, errors.New("no applications selected")
	}
	if len(apps) > argoMaxBulk {
		return nil, fmt.Errorf("at most %d applications can be processed at once", argoMaxBulk)
	}
	seen := make(map[ArgoAppRef]bool, len(apps))
	out := make([]ArgoAppRef, 0, len(apps))
	for _, a := range apps {
		ref := ArgoAppRef{Namespace: strings.TrimSpace(a.Namespace), Name: strings.TrimSpace(a.Name)}
		if ref.Namespace == "" || ref.Name == "" {
			return nil, errors.New("application has no namespace or name")
		}
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out, nil
}

func validateArgoSyncOptions(o ArgoSyncOptions, appCount int) error {
	if o.Revision == "" {
		return nil
	}
	if appCount > 1 {
		return errors.New("a revision can only be specified when syncing a single application")
	}
	if len(o.Revision) > argoMaxRevisionLen ||
		strings.ContainsFunc(o.Revision, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return errors.New("invalid revision")
	}
	return nil
}

// updateArgoApp: get -> mutate -> update; при конфликте resourceVersion повторяет всё заново,
// поэтому проверки внутри mutate всегда выполняются над свежим объектом.
func updateArgoApp(ctx context.Context, ri dynamic.ResourceInterface, name string, mutate func(*unstructured.Unstructured) error) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := ri.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if obj.GetDeletionTimestamp() != nil {
			return errArgoDeleted
		}
		if err := mutate(obj); err != nil {
			return err
		}
		_, err = ri.Update(ctx, obj, metav1.UpdateOptions{})
		return err
	})
}

// ─── Чистые мутации unstructured (без сети, покрыты тестами) ───

// argoOperationBusy: операция уже запрошена (operation) или выполняется.
func argoOperationBusy(obj map[string]interface{}) bool {
	if v, found, _ := unstructured.NestedFieldNoCopy(obj, "operation"); found && v != nil {
		return true
	}
	phase := strAt(obj, "status", "operationState", "phase")
	return phase == argoPhaseRunning || phase == argoPhaseTerminating
}

func autoSyncEnabled(obj map[string]interface{}) bool {
	v, found, _ := unstructured.NestedFieldNoCopy(obj, "spec", "syncPolicy", "automated")
	return found && v != nil
}

// argoSyncOptionsOf копирует spec.syncPolicy.syncOptions (CreateNamespace=true и т.п.) в операцию.
func argoSyncOptionsOf(obj map[string]interface{}) []interface{} {
	items, ok := sliceAt(obj, "spec", "syncPolicy", "syncOptions")
	if !ok {
		return nil
	}
	out := make([]interface{}, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func setArgoOperation(obj *unstructured.Unstructured, sync map[string]interface{}) error {
	if opts := argoSyncOptionsOf(obj.Object); len(opts) > 0 {
		sync["syncOptions"] = opts
	}
	return unstructured.SetNestedField(obj.Object, map[string]interface{}{
		"initiatedBy": map[string]interface{}{"username": argoInitiator},
		"sync":        sync,
	}, "operation")
}

func applySyncOperation(obj *unstructured.Unstructured, o ArgoSyncOptions) error {
	if argoOperationBusy(obj.Object) {
		return errArgoBusy
	}
	if srcs, ok := sliceAt(obj.Object, "spec", "sources"); o.Revision != "" && ok && len(srcs) > 0 {
		return errors.New("a revision cannot be set for a multi-source application")
	}
	sync := map[string]interface{}{"prune": o.Prune, "dryRun": o.DryRun}
	if o.Revision != "" {
		sync["revision"] = o.Revision
	}
	if o.Force {
		sync["syncStrategy"] = map[string]interface{}{"hook": map[string]interface{}{"force": true}}
	}
	return setArgoOperation(obj, sync)
}

func applyTerminate(obj *unstructured.Unstructured) error {
	phase := strAt(obj.Object, "status", "operationState", "phase")
	switch {
	case phase == argoPhaseRunning:
		// так делает API-сервер Argo: контроллер подхватывает фазу Terminating
		if err := unstructured.SetNestedField(obj.Object, argoPhaseTerminating, "status", "operationState", "phase"); err != nil {
			return err
		}
		return unstructured.SetNestedField(obj.Object, "operation is terminating", "status", "operationState", "message")
	case phase == argoPhaseTerminating:
		return errors.New("operation is already being terminated")
	}
	// операция запрошена, но контроллер её ещё не взял: достаточно убрать поле
	if v, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "operation"); found && v != nil {
		unstructured.RemoveNestedField(obj.Object, "operation")
		return nil
	}
	return errors.New("no operation is running")
}

func applyRollback(obj *unstructured.Unstructured, id int64, prune, dryRun bool) error {
	if autoSyncEnabled(obj.Object) {
		return errors.New("rollback is unavailable while auto-sync is enabled: disable auto-sync first")
	}
	if argoOperationBusy(obj.Object) {
		return errArgoBusy
	}
	var entry map[string]interface{}
	history, _ := sliceAt(obj.Object, "status", "history")
	for _, it := range history {
		if hm, ok := it.(map[string]interface{}); ok && int64At(hm, "id") == id {
			entry = hm
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("history entry #%d not found", id)
	}

	sync := map[string]interface{}{"prune": prune, "dryRun": dryRun}
	// source/sources из истории копируем как есть: контроллер применяет именно их
	for _, key := range []string{"revision", "revisions", "source", "sources"} {
		if v, found := entry[key]; found && v != nil {
			sync[key] = runtime.DeepCopyJSONValue(v)
		}
	}
	if sync["revision"] == nil && sync["revisions"] == nil {
		return errors.New("history entry has no revision")
	}
	return setArgoOperation(obj, sync)
}

const (
	argoResourcesFinalizer  = "resources-finalizer.argocd.argoproj.io"
	argoSuspendedAnnotation = "kubron.io/suspended-automated"
)

// SuspendArgoAutoSync отключает автосинхронизацию, запоминая прежние параметры в аннотации.
func (m *Manager) SuspendArgoAutoSync(apps []ArgoAppRef) ([]ArgoActionResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), argoActionTimeout)
	defer cancel()
	return forEachArgoApp(conn.DynamicClient, apps, func(ri dynamic.ResourceInterface, ref ArgoAppRef) error {
		return updateArgoApp(ctx, ri, ref.Name, applySuspendAutoSync)
	})
}

// ResumeArgoAutoSync включает автосинхронизацию с параметрами, сохранёнными при отключении.
func (m *Manager) ResumeArgoAutoSync(apps []ArgoAppRef) ([]ArgoActionResult, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), argoActionTimeout)
	defer cancel()
	return forEachArgoApp(conn.DynamicClient, apps, func(ri dynamic.ResourceInterface, ref ArgoAppRef) error {
		return updateArgoApp(ctx, ri, ref.Name, applyResumeAutoSync)
	})
}

// DeleteArgoApplication удаляет Application. cascade=true: вместе с управляемыми ресурсами
// (финализатор Argo CD), false: ресурсы остаются в кластере.
func (m *Manager) DeleteArgoApplication(namespace, name string, cascade bool) error {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return err
	}
	namespace, name = strings.TrimSpace(namespace), strings.TrimSpace(name)
	if namespace == "" || name == "" {
		return errors.New("no application specified")
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), argoActionTimeout)
	defer cancel()
	ri := conn.DynamicClient.Resource(kindTable["application"].gvr).Namespace(namespace)
	if err := updateArgoApp(ctx, ri, name, func(obj *unstructured.Unstructured) error {
		applyDeletePolicy(obj, cascade)
		return nil
	}); err != nil {
		return err
	}
	policy := metav1.DeletePropagationBackground
	return ri.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy})
}

func applySuspendAutoSync(obj *unstructured.Unstructured) error {
	cur, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", "syncPolicy", "automated")
	if !found || cur == nil {
		return errors.New("auto-sync is already disabled")
	}
	raw, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	ann := obj.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	ann[argoSuspendedAnnotation] = string(raw)
	obj.SetAnnotations(ann)
	unstructured.RemoveNestedField(obj.Object, "spec", "syncPolicy", "automated")
	return nil
}

func applyResumeAutoSync(obj *unstructured.Unstructured) error {
	if autoSyncEnabled(obj.Object) {
		return errors.New("auto-sync is already enabled")
	}
	automated := map[string]interface{}{}
	ann := obj.GetAnnotations()
	if raw, ok := ann[argoSuspendedAnnotation]; ok {
		var saved map[string]interface{}
		if json.Unmarshal([]byte(raw), &saved) == nil && saved != nil {
			automated = saved
		}
		delete(ann, argoSuspendedAnnotation)
		obj.SetAnnotations(ann)
	}
	return unstructured.SetNestedField(obj.Object, automated, "spec", "syncPolicy", "automated")
}

// applyDeletePolicy ставит (cascade) или снимает финализатор каскадного удаления Argo CD,
// включая варианты с суффиксом /foreground и /background.
func applyDeletePolicy(obj *unstructured.Unstructured, cascade bool) {
	out := make([]string, 0, len(obj.GetFinalizers())+1)
	has := false
	for _, f := range obj.GetFinalizers() {
		if strings.HasPrefix(f, argoResourcesFinalizer) {
			if !cascade {
				continue
			}
			has = true
		}
		out = append(out, f)
	}
	if cascade && !has {
		out = append(out, argoResourcesFinalizer)
	}
	obj.SetFinalizers(out)
}
