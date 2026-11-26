// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package depgraph_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/24DSO-lab/kubron/internal/depgraph"
	"github.com/24DSO-lab/kubron/internal/depgraph/graphtest"
)

func TestHealthOf(t *testing.T) {
	cases := []struct {
		name   string
		yaml   string
		health depgraph.Health
		reason string
	}{
		{"pod ready", "kind: Pod\nstatus: {phase: Running, conditions: [{type: Ready, status: 'True'}]}", depgraph.Healthy, ""},
		{"pod running not ready", "kind: Pod\nstatus: {phase: Running, conditions: [{type: Ready, status: 'False'}]}", depgraph.Degraded, "not_ready"},
		{"pod terminating", "kind: Pod\nmetadata: {deletionTimestamp: '2026-01-01T00:00:00Z'}\nstatus: {phase: Running}", depgraph.Progressing, "terminating"},
		{"pod succeeded", "kind: Pod\nstatus: {phase: Succeeded}", depgraph.Healthy, "completed"},
		{"pod failed", "kind: Pod\nstatus: {phase: Failed}", depgraph.Degraded, "failed"},
		{"pod evicted", "kind: Pod\nstatus: {phase: Failed, reason: Evicted}", depgraph.Degraded, "evicted"},
		{"pod crash loop", "kind: Pod\nstatus: {phase: Running, containerStatuses: [{name: c, state: {waiting: {reason: CrashLoopBackOff}}}]}", depgraph.Degraded, "crash_loop"},
		{"pod crash loop after OOM", "kind: Pod\nstatus: {phase: Running, containerStatuses: [{name: c, state: {waiting: {reason: CrashLoopBackOff}}, lastState: {terminated: {reason: OOMKilled, exitCode: 137}}}]}", depgraph.Degraded, "oom_killed"},
		{"pod terminated OOM", "kind: Pod\nstatus: {phase: Running, containerStatuses: [{name: c, state: {terminated: {reason: OOMKilled, exitCode: 137}}}]}", depgraph.Degraded, "oom_killed"},
		{"pod image pull", "kind: Pod\nspec: {nodeName: node1}\nstatus: {phase: Pending, containerStatuses: [{name: c, state: {waiting: {reason: ImagePullBackOff}}}]}", depgraph.Degraded, "image_pull_failed"},
		{"pod config error", "kind: Pod\nspec: {nodeName: node1}\nstatus: {phase: Pending, containerStatuses: [{name: c, state: {waiting: {reason: CreateContainerConfigError}}}]}", depgraph.Degraded, "config_error"},
		{"pod init failed", "kind: Pod\nstatus: {phase: Pending, initContainerStatuses: [{name: i, state: {terminated: {exitCode: 1}}}]}", depgraph.Degraded, "init_failed"},
		{"pod container failed", "kind: Pod\nstatus: {phase: Running, containerStatuses: [{name: c, state: {terminated: {exitCode: 2}}}]}", depgraph.Degraded, "container_failed"},
		{"pod OOM beats crash loop and image pull", "kind: Pod\nstatus: {phase: Running, containerStatuses: [{name: a, state: {waiting: {reason: ErrImagePull}}}, {name: b, state: {terminated: {reason: OOMKilled}}}]}", depgraph.Degraded, "oom_killed"},
		{"pod unschedulable", "kind: Pod\nstatus: {phase: Pending, conditions: [{type: PodScheduled, status: 'False', reason: Unschedulable}]}", depgraph.Degraded, "pending_unscheduled"},
		{"pod pending no node", "kind: Pod\nstatus: {phase: Pending}", depgraph.Progressing, "pending"},
		{"pod creating", "kind: Pod\nspec: {nodeName: node1}\nstatus: {phase: Pending, containerStatuses: [{name: c, state: {waiting: {reason: ContainerCreating}}}]}", depgraph.Progressing, "container_creating"},
		{"pod pending on node", "kind: Pod\nspec: {nodeName: node1}\nstatus: {phase: Pending}", depgraph.Progressing, "pending"},

		{"rs ok", "kind: ReplicaSet\nspec: {replicas: 2}\nstatus: {readyReplicas: 2}", depgraph.Healthy, ""},
		{"rs zero", "kind: ReplicaSet\nspec: {replicas: 0}", depgraph.Suspended, "scaled_to_zero"},
		{"rs short", "kind: ReplicaSet\nspec: {replicas: 3}\nstatus: {readyReplicas: 1}", depgraph.Degraded, "replicas_unavailable"},
		{"rs failure", "kind: ReplicaSet\nspec: {replicas: 1}\nstatus: {conditions: [{type: ReplicaFailure, status: 'True'}]}", depgraph.Degraded, "replica_failure"},

		{"deployment ok", "kind: Deployment\nmetadata: {generation: 1}\nspec: {replicas: 1}\nstatus: {observedGeneration: 1, updatedReplicas: 1, availableReplicas: 1}", depgraph.Healthy, ""},
		{"deployment paused", "kind: Deployment\nspec: {paused: true, replicas: 1}", depgraph.Suspended, "paused"},
		{"deployment zero", "kind: Deployment\nspec: {replicas: 0}", depgraph.Suspended, "scaled_to_zero"},
		{"deployment deadline", "kind: Deployment\nspec: {replicas: 1}\nstatus: {conditions: [{type: Progressing, status: 'False', reason: ProgressDeadlineExceeded}]}", depgraph.Degraded, "progress_deadline_exceeded"},
		{"deployment replica failure", "kind: Deployment\nspec: {replicas: 1}\nstatus: {conditions: [{type: ReplicaFailure, status: 'True'}]}", depgraph.Degraded, "replica_failure"},
		{"deployment rolling", "kind: Deployment\nmetadata: {generation: 2}\nspec: {replicas: 2}\nstatus: {observedGeneration: 2, updatedReplicas: 1, availableReplicas: 2}", depgraph.Progressing, "rollout_in_progress"},
		{"deployment unavailable", "kind: Deployment\nspec: {replicas: 2}\nstatus: {updatedReplicas: 2, availableReplicas: 1}", depgraph.Degraded, "replicas_unavailable"},
		{"deployment not available", "kind: Deployment\nspec: {replicas: 2}\nstatus: {updatedReplicas: 2, availableReplicas: 0, conditions: [{type: Available, status: 'False'}]}", depgraph.Degraded, "not_available"},

		{"statefulset short", "kind: StatefulSet\nspec: {replicas: 3}\nstatus: {readyReplicas: 2}", depgraph.Degraded, "replicas_unavailable"},
		{"daemonset ok", "kind: DaemonSet\nstatus: {desiredNumberScheduled: 2, numberReady: 2}", depgraph.Healthy, ""},
		{"daemonset short", "kind: DaemonSet\nstatus: {desiredNumberScheduled: 2, numberReady: 1, numberUnavailable: 1}", depgraph.Degraded, "replicas_unavailable"},
		{"job failed", "kind: Job\nstatus: {conditions: [{type: Failed, status: 'True'}]}", depgraph.Degraded, "job_failed"},
		{"job complete", "kind: Job\nstatus: {conditions: [{type: Complete, status: 'True'}]}", depgraph.Healthy, "completed"},
		{"job suspended", "kind: Job\nstatus: {conditions: [{type: Suspended, status: 'True'}]}", depgraph.Suspended, "suspended"},
		{"job running", "kind: Job", depgraph.Progressing, "running"},
		{"cronjob ok", "kind: CronJob", depgraph.Healthy, ""},

		{"pvc bound", "kind: PersistentVolumeClaim\nstatus: {phase: Bound}", depgraph.Healthy, ""},
		{"pvc pending", "kind: PersistentVolumeClaim\nstatus: {phase: Pending}", depgraph.Degraded, "unbound"},
		{"pvc lost", "kind: PersistentVolumeClaim\nstatus: {phase: Lost}", depgraph.Degraded, "lost"},
		{"pvc terminating", "kind: PersistentVolumeClaim\nmetadata: {deletionTimestamp: '2026-01-01T00:00:00Z'}", depgraph.Progressing, "terminating"},
		{"pv bound", "kind: PersistentVolume\nstatus: {phase: Bound}", depgraph.Healthy, ""},
		{"pv available", "kind: PersistentVolume\nstatus: {phase: Available}", depgraph.Healthy, ""},
		{"pv released", "kind: PersistentVolume\nstatus: {phase: Released}", depgraph.Degraded, "released"},
		{"pv failed", "kind: PersistentVolume\nstatus: {phase: Failed}", depgraph.Degraded, "failed"},
		{"pv pending", "kind: PersistentVolume\nstatus: {phase: Pending}", depgraph.Progressing, "pending"},

		{"node ready", "kind: Node\nstatus: {conditions: [{type: Ready, status: 'True'}]}", depgraph.Healthy, ""},
		{"node not ready", "kind: Node\nstatus: {conditions: [{type: Ready, status: 'False'}]}", depgraph.Degraded, "node_not_ready"},
		{"node unreachable", "kind: Node\nstatus: {conditions: [{type: Ready, status: Unknown}]}", depgraph.Degraded, "node_unreachable"},
		{"node memory", "kind: Node\nstatus: {conditions: [{type: Ready, status: 'True'}, {type: MemoryPressure, status: 'True'}, {type: DiskPressure, status: 'True'}]}", depgraph.Degraded, "node_pressure_memory"},
		{"node disk", "kind: Node\nstatus: {conditions: [{type: DiskPressure, status: 'True'}]}", depgraph.Degraded, "node_pressure_disk"},
		{"node pid", "kind: Node\nstatus: {conditions: [{type: PIDPressure, status: 'True'}]}", depgraph.Degraded, "node_pressure_pid"},
		{"node network", "kind: Node\nstatus: {conditions: [{type: NetworkUnavailable, status: 'True'}]}", depgraph.Degraded, "node_network_unavailable"},
		{"node cordoned", "kind: Node\nspec: {unschedulable: true}\nstatus: {conditions: [{type: Ready, status: 'True'}]}", depgraph.Suspended, "node_unschedulable"},

		{"slice no endpoints", "kind: EndpointSlice\naddressType: IPv4", depgraph.Degraded, "no_endpoints"},
		{"slice none ready", "kind: EndpointSlice\nendpoints: [{addresses: ['1.1.1.1'], conditions: {ready: false}}]", depgraph.Degraded, "no_ready_endpoints"},
		{"slice ready unset means ready", "kind: EndpointSlice\nendpoints: [{addresses: ['1.1.1.1']}]", depgraph.Healthy, ""},

		{"service", "kind: Service", depgraph.Healthy, ""},
		{"unrecognised kind", "kind: Whatever", depgraph.Healthy, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := graphtest.NewFakeReader()
			g := gvkOf(c.yaml)
			av := g.Version
			if g.Group != "" {
				av = g.Group + "/" + g.Version
			}
			f.PutYAML(t, "apiVersion: "+av+"\nmetadata: {name: x}\n"+c.yaml)
			objs, _ := f.List(t.Context(), g, depgraph.ListOptions{})
			var obj *unstructured.Unstructured
			if len(objs.Items) > 0 {
				obj = &objs.Items[0]
			}
			if obj == nil {
				t.Fatal("fixture not stored")
			}
			h, r := depgraph.HealthOf(obj)
			if h != c.health || r != c.reason {
				t.Errorf("HealthOf = %s/%q, want %s/%q", h, r, c.health, c.reason)
			}
		})
	}
}

func TestHealthOfUnreadable(t *testing.T) {
	if h, r := depgraph.HealthOf(nil); h != depgraph.Unknown || r != "unreadable" {
		t.Errorf("nil: %s/%s", h, r)
	}
	for _, kind := range []string{"Pod", "ReplicaSet", "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "PersistentVolumeClaim", "PersistentVolume", "Node", "EndpointSlice"} {
		bad := &unstructured.Unstructured{Object: map[string]any{"kind": kind, "spec": "not-an-object", "status": "x", "endpoints": "x", "metadata": map[string]any{"name": "x"}}}
		if h, r := depgraph.HealthOf(bad); h != depgraph.Unknown || r != "unreadable" {
			t.Errorf("%s: %s/%s, want Unknown/unreadable", kind, h, r)
		}
	}
}

func TestSanitizeConfigMap(t *testing.T) {
	f := world(t)
	o, err := f.Get(t.Context(), gvkOf("kind: ConfigMap"), "app", "web-cfg")
	if err != nil {
		t.Fatal(err)
	}
	depgraph.SanitizeConfigMap(o)
	if s := jsonOf(o.Object); strings.Contains(strings.ToLower(s), "leak") || strings.Contains(s, "binaryData") || strings.Contains(s, "last-applied") {
		t.Errorf("sanitized ConfigMap still carries content: %s", s)
	}
	if o.GetName() != "web-cfg" || o.GetNamespace() != "app" {
		t.Error("identity must survive sanitising")
	}
	depgraph.SanitizeConfigMap(nil) // не паникует

	pod, _ := f.Get(t.Context(), gvkOf("kind: Pod"), "app", "web-7d-x")
	before := jsonOf(pod.Object)
	depgraph.SanitizeConfigMap(pod)
	if jsonOf(pod.Object) != before {
		t.Error("only ConfigMaps are touched")
	}
}

// depgraph не знает ни про платформу, ни про хранилище, ни про диагностику: чтение — только через Reader.
func TestPackageImportsStayNarrow(t *testing.T) {
	forbidden := []string{
		"internal/opsplatform", "internal/diagnose", "internal/incident", "internal/k8s", "internal/app",
		"internal/credential", "internal/pgstore", "internal/store", "internal/correlation", "internal/signals", "internal/feedback",
		"k8s.io/client-go",
	}
	fset := token.NewFileSet()
	n := 0
	_ = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Errorf("%s: %v", path, perr)
			return nil
		}
		n++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if strings.Contains(p, bad) {
					t.Errorf("%s imports %s", path, p)
				}
			}
		}
		return nil
	})
	if n < 5 {
		t.Fatalf("scanned only %d files: the check is vacuous", n)
	}
}
