// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Таблица видов Flux (ADR-0023, разделы 3 и 6): группа, plural, допустимые версии API и разрешённые действия.
// Произвольный GVR от клиента не принимается: вид из FluxRef сначала ищется здесь. Тест flux_kinds_test.go сверяет
// таблицу с ADR. Версии Flux в kindTable не зашиваются: YAML и правка идут через cr:group/version/plural.

const (
	// fluxMaxActionObjects — предел объектов за один вызов reconcile, suspend и resume.
	fluxMaxActionObjects = 200
	// fluxActionTimeout — таймаут одного вызова действия.
	fluxActionTimeout = 60 * time.Second //nolint:unused // контракт: используется исполнителями действий (B.8, B.9)
)

// Группы API Flux.
const (
	fluxGroupKustomize    = "kustomize.toolkit.fluxcd.io"
	fluxGroupHelm         = "helm.toolkit.fluxcd.io"
	fluxGroupSource       = "source.toolkit.fluxcd.io"
	fluxGroupNotification = "notification.toolkit.fluxcd.io"
	fluxGroupImage        = "image.toolkit.fluxcd.io"
)

// Виды Flux (значения FluxRef.Kind).
const (
	FluxKindKustomization         = "Kustomization"
	FluxKindHelmRelease           = "HelmRelease"
	FluxKindGitRepository         = "GitRepository"
	FluxKindOCIRepository         = "OCIRepository"
	FluxKindHelmRepository        = "HelmRepository"
	FluxKindHelmChart             = "HelmChart"
	FluxKindBucket                = "Bucket"
	FluxKindAlert                 = "Alert"
	FluxKindProvider              = "Provider"
	FluxKindReceiver              = "Receiver"
	FluxKindImageRepository       = "ImageRepository"
	FluxKindImagePolicy           = "ImagePolicy"
	FluxKindImageUpdateAutomation = "ImageUpdateAutomation"
)

// fluxController — контроллер Flux, которому принадлежит вид (соответствует группе API).
type fluxController string

const (
	fluxCtrlKustomize    fluxController = "kustomize"
	fluxCtrlHelm         fluxController = "helm"
	fluxCtrlSource       fluxController = "source"
	fluxCtrlNotification fluxController = "notification"
	fluxCtrlImage        fluxController = "image"
)

// fluxActions — набор действий, разрешённых над видом.
type fluxActions uint8

const (
	fluxActReconcile fluxActions = 1 << iota // ReconcileFlux
	fluxActSuspend                           // SuspendFlux и ResumeFlux (spec.suspend)
	fluxActDelete                            // DeleteFlux

	fluxActReadOnly fluxActions = 0
	// fluxActToggle — reconcile, suspend и resume: всё, кроме удаления.
	fluxActToggle = fluxActReconcile | fluxActSuspend
)

func (a fluxActions) has(want fluxActions) bool { return a&want == want }

// fluxKind — описание вида Flux.
type fluxKind struct {
	Kind       string
	Group      string
	Plural     string
	Namespaced bool
	// Versions — допустимые версии API по порядку предпочтения. Версия вне списка считается неподдерживаемой.
	Versions   []string
	Controller fluxController
	Actions    fluxActions
}

// fluxKinds — все виды Flux, которые читает Kubron. Порядок — порядок отображения.
var fluxKinds = []fluxKind{
	{FluxKindKustomization, fluxGroupKustomize, "kustomizations", true, []string{"v1"}, fluxCtrlKustomize, fluxActToggle | fluxActDelete},
	{FluxKindHelmRelease, fluxGroupHelm, "helmreleases", true, []string{"v2"}, fluxCtrlHelm, fluxActToggle | fluxActDelete},

	{FluxKindGitRepository, fluxGroupSource, "gitrepositories", true, []string{"v1"}, fluxCtrlSource, fluxActToggle},
	{FluxKindOCIRepository, fluxGroupSource, "ocirepositories", true, []string{"v1", "v1beta2"}, fluxCtrlSource, fluxActToggle},
	{FluxKindHelmRepository, fluxGroupSource, "helmrepositories", true, []string{"v1"}, fluxCtrlSource, fluxActToggle},
	{FluxKindHelmChart, fluxGroupSource, "helmcharts", true, []string{"v1"}, fluxCtrlSource, fluxActToggle},
	{FluxKindBucket, fluxGroupSource, "buckets", true, []string{"v1", "v1beta2"}, fluxCtrlSource, fluxActToggle},

	// Alert и Provider пока без стабильной версии (v1beta3): смена версии в Flux потребует правки этой таблицы и фикстур.
	{FluxKindAlert, fluxGroupNotification, "alerts", true, []string{"v1beta3"}, fluxCtrlNotification, fluxActReadOnly},
	{FluxKindProvider, fluxGroupNotification, "providers", true, []string{"v1beta3"}, fluxCtrlNotification, fluxActReadOnly},
	{FluxKindReceiver, fluxGroupNotification, "receivers", true, []string{"v1", "v1beta2"}, fluxCtrlNotification, fluxActReadOnly},

	{FluxKindImageRepository, fluxGroupImage, "imagerepositories", true, []string{"v1", "v1beta2"}, fluxCtrlImage, fluxActToggle},
	{FluxKindImagePolicy, fluxGroupImage, "imagepolicies", true, []string{"v1", "v1beta2"}, fluxCtrlImage, fluxActReadOnly},
	{FluxKindImageUpdateAutomation, fluxGroupImage, "imageupdateautomations", true, []string{"v1", "v1beta2"}, fluxCtrlImage, fluxActToggle},
}

// fluxKindByName ищет вид по FluxRef.Kind (регистр важен: вид приходит от клиента, нормализации нет).
func fluxKindByName(kind string) (fluxKind, bool) {
	for _, k := range fluxKinds {
		if k.Kind == kind {
			return k, true
		}
	}
	return fluxKind{}, false
}

// fluxKindsOf возвращает виды контроллера в порядке таблицы.
func fluxKindsOf(c fluxController) []fluxKind {
	var out []fluxKind
	for _, k := range fluxKinds {
		if k.Controller == c {
			out = append(out, k)
		}
	}
	return out
}

// GVR строит GroupVersionResource для версии, которую обслуживает кластер (fluxServedVersions).
func (k fluxKind) GVR(version string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: k.Group, Version: version, Resource: k.Plural}
}

// supports сообщает, входит ли версия в допустимые для вида.
func (k fluxKind) supports(version string) bool { return slices.Contains(k.Versions, version) }

// checkFluxRef проверяет ссылку от клиента: вид разрешён для действия, namespace и имя заданы.
// Ошибки — errFluxInvalidRef и errFluxKindUnsupported (оборачиваются исполнителями с подробностями).
func checkFluxRef(ref FluxRef, action fluxActions) (fluxKind, error) {
	if ref.Kind == "" || ref.Namespace == "" || ref.Name == "" {
		return fluxKind{}, errFluxInvalidRef
	}
	k, ok := fluxKindByName(ref.Kind)
	if !ok || !k.Actions.has(action) {
		return fluxKind{}, errFluxKindUnsupported
	}
	return k, nil
}
