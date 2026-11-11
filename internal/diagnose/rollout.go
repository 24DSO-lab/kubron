// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func desiredReplicas(n *int32) int32 {
	if n == nil {
		return 1 // значение по умолчанию Kubernetes
	}
	return *n
}

func srcDeployment(d *appsv1.Deployment, field string) Source {
	return Source{Kind: SourceResource, Resource: depRef(d), Field: field}
}

func srcRS(r *appsv1.ReplicaSet, field string) Source {
	return Source{Kind: SourceResource, Resource: rsRef(r), Field: field}
}

// ruleRollout — проблемы выката Deployment и ReplicaSet: остановка по дедлайну, ошибки создания подов, пауза,
// неготовые реплики нового ReplicaSet.
func ruleRollout(b *builder) {
	c := b.c
	var facts []string
	problem := false
	dep := c.Deployment
	var depRefV ResourceRef
	if dep != nil {
		depRefV = depRef(dep)
		desired := desiredReplicas(dep.Spec.Replicas)
		st := dep.Status
		lag := dep.Generation > 0 && st.ObservedGeneration > 0 && st.ObservedGeneration < dep.Generation
		stalled := false
		for _, cd := range st.Conditions {
			if (cd.Type == appsv1.DeploymentProgressing && cd.Status == corev1.ConditionFalse) ||
				(cd.Type == appsv1.DeploymentReplicaFailure && cd.Status == corev1.ConditionTrue) {
				stalled = true
				facts = append(facts, b.fact(depRefV, "deployment.condition", srcDeployment(dep, "status.conditions["+string(cd.Type)+"]"),
					map[string]string{"type": string(cd.Type), "status": string(cd.Status), "reason": cd.Reason, "message": cd.Message}))
			}
		}
		unhealthy := st.ReadyReplicas < desired || st.UnavailableReplicas > 0
		if unhealthy || stalled || dep.Spec.Paused || lag {
			problem = unhealthy || stalled || dep.Spec.Paused
			facts = append(facts, b.fact(depRefV, "deployment.replicas", srcDeployment(dep, "status"), map[string]string{
				"desired": itoa(desired), "updated": itoa(st.UpdatedReplicas), "ready": itoa(st.ReadyReplicas),
				"available": itoa(st.AvailableReplicas), "unavailable": itoa(st.UnavailableReplicas)}))
		}
		if dep.Spec.Paused {
			f := b.fact(depRefV, "deployment.paused", srcDeployment(dep, "spec.paused"), nil)
			b.symptom(SymptomRollout)
			h := b.hypothesis(depRefV, "rollout.paused", ConfidenceHigh, nil, []string{f}, nil)
			b.recommend(depRefV, "rec.resume_rollout", nil, h)
		}
		if lag {
			f := b.fact(depRefV, "deployment.generation_lag", srcDeployment(dep, "status.observedGeneration"),
				map[string]string{"generation": strconv.FormatInt(dep.Generation, 10), "observed": strconv.FormatInt(st.ObservedGeneration, 10)})
			b.symptom(SymptomRollout)
			b.hypothesis(depRefV, "rollout.controller_lag", ConfidenceLow, nil, []string{f}, nil)
		}
	}

	newest := newestReplicaSet(c.ReplicaSets)
	var rsFacts []string
	for _, rs := range c.ReplicaSets {
		want := desiredReplicas(rs.Spec.Replicas)
		for _, cd := range rs.Status.Conditions {
			if cd.Type == appsv1.ReplicaSetReplicaFailure && cd.Status == corev1.ConditionTrue {
				problem = true
				rsFacts = append(rsFacts, b.fact(rsRef(rs), "replicaset.condition", srcRS(rs, "status.conditions[ReplicaFailure]"),
					map[string]string{"reason": cd.Reason, "message": cd.Message}))
			}
		}
		if rs == newest && want > 0 && rs.Status.ReadyReplicas < want {
			problem = true
			rsFacts = append(rsFacts, b.fact(rsRef(rs), "replicaset.replicas", srcRS(rs, "status"), map[string]string{
				"name": rs.Name, "desired": itoa(want), "ready": itoa(rs.Status.ReadyReplicas), "replicas": itoa(rs.Status.Replicas)}))
		}
	}
	if !problem {
		return
	}
	b.symptom(SymptomRollout)
	subject := depRefV
	if dep == nil {
		subject = c.Target
		if newest != nil {
			subject = rsRef(newest)
		}
	}
	facts = append(facts, rsFacts...)

	// События создания подов: квота и запреты admission.
	var quotaEv, denyEv []string
	for _, e := range c.Events {
		if e.Reason != "FailedCreate" && e.Reason != "FailedCreatePodSandBox" {
			continue
		}
		if e.Object.Kind != "ReplicaSet" && e.Object.Kind != "Deployment" {
			continue
		}
		switch {
		case contains(e.Message, "exceeded quota"):
			quotaEv = append(quotaEv, b.factFromEvent(subject, e))
		case contains(e.Message, "violates podsecurity", "admission webhook", "forbidden"):
			denyEv = append(denyEv, b.factFromEvent(subject, e))
		}
	}
	if len(quotaEv) > 0 {
		h := b.hypothesis(subject, "rollout.quota_exceeded", ConfidenceHigh, nil, quotaEv[len(quotaEv)-1:], nil)
		b.recommend(subject, "rec.raise_quota_or_reduce_requests", nil, h)
		b.step(subject, "step.check_quota", nil, h)
	}
	if len(denyEv) > 0 {
		h := b.hypothesis(subject, "rollout.pod_creation_denied", ConfidenceMedium, nil, denyEv[len(denyEv)-1:], nil)
		b.recommend(subject, "rec.fix_admission_denial", nil, h)
	}

	// Поды нового ReplicaSet: их собственные проблемы объясняют остановку выката.
	if newest != nil {
		var sup, contra []string
		ready := 0
		for _, p := range c.Pods {
			if o := controllerOf(p.OwnerReferences, "ReplicaSet"); o == nil || o.Name != newest.Name {
				continue
			}
			if podReady(p) {
				ready++
				continue
			}
			ids := b.podFacts[p.Name]
			sup = append(sup, ids[:min(len(ids), 3)]...)
		}
		if ready > 0 && len(sup) == 0 {
			contra = append(contra, b.fact(rsRef(newest), "replicaset.pods_ready", srcRS(newest, "status.readyReplicas"),
				map[string]string{"name": newest.Name, "ready": strconv.Itoa(ready)}))
		}
		if len(sup) > 0 {
			sup = uniq(append(sup, facts...))
			h := b.hypothesis(subject, "rollout.new_pods_unhealthy", ConfidenceMedium, map[string]string{"replicaset": newest.Name}, sup, contra)
			b.recommend(subject, "rec.fix_new_pods", map[string]string{"replicaset": newest.Name}, h)
			b.step(subject, "step.rollout_history", nil, h)
		} else if len(c.Pods) == 0 && len(quotaEv) == 0 && len(denyEv) == 0 {
			b.missing("rollout.no_pods", subject, nil)
			b.step(subject, "step.check_events", nil)
		}
	}
}

// newestReplicaSet — самый новый ReplicaSet по времени создания (revision-аннотация не читается: она не обязательна).
func newestReplicaSet(all []*appsv1.ReplicaSet) *appsv1.ReplicaSet {
	var best *appsv1.ReplicaSet
	for _, rs := range all {
		if best == nil || rs.CreationTimestamp.After(best.CreationTimestamp.Time) {
			best = rs
		}
	}
	return best
}

// uniq убирает повторы, сохраняя порядок.
func uniq(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0:0]
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
