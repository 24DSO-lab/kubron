// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph

import "k8s.io/apimachinery/pkg/runtime/schema"

// kindSpec — вид, который граф умеет читать. Secret здесь намеренно нет: он не читается никогда.
type kindSpec struct {
	gvk        schema.GroupVersionKind
	namespaced bool
}

func (s kindSpec) ref(ns, name string) Ref {
	if !s.namespaced {
		ns = ""
	}
	return Ref{Group: s.gvk.Group, Kind: s.gvk.Kind, Namespace: ns, Name: name}
}

var (
	specPod         = kindSpec{schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, true}
	specNode        = kindSpec{schema.GroupVersionKind{Version: "v1", Kind: "Node"}, false}
	specService     = kindSpec{schema.GroupVersionKind{Version: "v1", Kind: "Service"}, true}
	specPVC         = kindSpec{schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolumeClaim"}, true}
	specPV          = kindSpec{schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolume"}, false}
	specConfigMap   = kindSpec{schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, true}
	specDeployment  = kindSpec{schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, true}
	specReplicaSet  = kindSpec{schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}, true}
	specStatefulSet = kindSpec{schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}, true}
	specDaemonSet   = kindSpec{schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DaemonSet"}, true}
	specJob         = kindSpec{schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}, true}
	specCronJob     = kindSpec{schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "CronJob"}, true}
	specStorageCls  = kindSpec{schema.GroupVersionKind{Group: "storage.k8s.io", Version: "v1", Kind: "StorageClass"}, false}
	specSlice       = kindSpec{schema.GroupVersionKind{Group: "discovery.k8s.io", Version: "v1", Kind: "EndpointSlice"}, true}
)

var allSpecs = []kindSpec{
	specPod, specNode, specService, specPVC, specPV, specConfigMap, specDeployment, specReplicaSet,
	specStatefulSet, specDaemonSet, specJob, specCronJob, specStorageCls, specSlice,
}

// lookupBuiltin ищет вид среди встроенных. Пустая группа у не-core вида допускается, если вид однозначен.
func lookupBuiltin(group, kind string) (kindSpec, bool) {
	for _, s := range allSpecs {
		if s.gvk.Kind == kind && s.gvk.Group == group {
			return s, true
		}
	}
	if group == "" {
		for _, s := range allSpecs {
			if s.gvk.Kind == kind {
				return s, true
			}
		}
	}
	return kindSpec{}, false
}

// specFor ищет вид среди встроенных, затем среди расширений (kind_registry.go).
func specFor(group, kind string) (kindSpec, bool) {
	if s, ok := lookupBuiltin(group, kind); ok {
		return s, true
	}
	if e, ok := lookupExt(group, kind); ok {
		return e.Spec, true
	}
	return kindSpec{}, false
}

// seedable — виды, от которых разрешено строить граф.
func seedable(s kindSpec) bool {
	switch s.gvk.Kind {
	case "Pod", "Deployment", "ReplicaSet", "Service", "PersistentVolumeClaim", "Node":
		return true
	}
	e, ok := lookupExt(s.gvk.Group, s.gvk.Kind)
	return ok && e.Seedable
}
