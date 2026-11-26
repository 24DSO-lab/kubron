// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/24DSO-lab/kubron/internal/depgraph"
)

// Цепочка Pod → ConfigMap (ADR-0031 §6, этап 2). Существование ConfigMap, на который ссылается неготовый Pod,
// проверяет граф (только по имени, содержимое не читается). Текстовое правило config.missing_reference работает по
// сообщению kubelet; здесь его подтверждает или не подтверждает структура:
//
//   - ребро missing → гипотеза config.missing_reference на ConfigMap (high), цепочка Pod > ConfigMap; текстовая
//     гипотеза того же Pod'а объясняется ею и теряет уверенность;
//   - ConfigMap существует, а kubelet жалуется на ключ → уверенность не растёт (ключи не читаются), шаг для человека;
//   - прочитать ConfigMap не удалось (запрет, предел чтения) → «не знаем», а не «нет».

const ruleConfigRefsID = "chain.config_refs"

func init() { registerChainRule(ruleConfigRefsID, 1, ruleConfigRefs) }

// maxConfigSteps — для скольких существующих ConfigMap одного Pod'а даётся шаг «проверить ключ»: остальные видны в графе.
const maxConfigSteps = 3

const (
	hypConfigMissing = "config.missing_reference"
	hypVolumeMount   = "startup.volume_mount_failed"
	stepCheckObject  = "step.check_referenced_object"
)

// configUse — отсутствующий ConfigMap и неготовые Pod'ы, которые на него ссылаются.
type configUse struct {
	node depgraph.Node
	pods []*corev1.Pod
}

func ruleConfigRefs(b *builder) {
	g := b.c.Graph
	if g == nil {
		return
	}
	textHyps := len(b.rep.Hypotheses) // гипотезы текстовых правил: правило идёт после них
	byKey := map[string]*configUse{}
	for _, p := range b.c.Pods {
		if podReady(p) {
			continue
		}
		for _, e := range configEdges(g, p) {
			if e.State != depgraph.EdgeMissing {
				continue
			}
			n, ok := g.Node(e.To)
			if !ok {
				continue
			}
			u := byKey[n.Key()]
			if u == nil {
				u = &configUse{node: n}
				byKey[n.Key()] = u
			}
			u.pods = append(u.pods, p)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		missingConfigMap(b, byKey[k], textHyps)
	}
	for i := range textHyps {
		configTextFindings(b, g, i)
	}
}

// configEdges — рёбра mounts от Pod'а к ConfigMap, по Key() вершины.
func configEdges(g *depgraph.Graph, p *corev1.Pod) []depgraph.Edge {
	var out []depgraph.Edge
	for _, e := range g.EdgesFrom(depgraph.Ref{Kind: "Pod", Namespace: p.Namespace, Name: p.Name}, depgraph.EdgeMounts) {
		if e.To.Kind == "ConfigMap" {
			out = append(out, e)
		}
	}
	return out
}

// isConfigText — текстовая гипотеза о ссылке Pod'а на ConfigMap или неопределённый объект (не Secret).
func isConfigText(h Hypothesis) bool {
	return h.Code == hypConfigMissing && h.Resource.Kind == "Pod" && h.Params["kind"] != "secret"
}

// missingConfigMap: Pod ссылается на ConfigMap, которого нет (кластер ответил NotFound).
func missingConfigMap(b *builder, u *configUse, textHyps int) {
	cm := fromGraphRef(u.node.Ref)
	first := u.pods[0]
	field := configField(first, cm.Name)
	b.symptom(SymptomStartupFailure)
	sup := []string{b.fact(cm, "config.reference_missing", Source{Kind: SourceDerived, Resource: podRef(first), Field: field},
		map[string]string{"configmap": cm.Name})}
	for i, p := range u.pods {
		if i == maxConsumerFacts {
			break
		}
		sup = append(sup, b.fact(podRef(p), "config.referenced_by", Source{Kind: SourceDerived, Resource: podRef(p), Field: configField(p, cm.Name)},
			map[string]string{"pod": p.Name, "configmap": cm.Name}))
	}
	params := map[string]string{"container": configContainer(first, cm.Name), "kind": "ConfigMap", "configmap": cm.Name}
	id := b.hypothesis(cm, hypConfigMissing, ConfidenceHigh, params, sup, nil)
	b.recommend(cm, "rec.fix_config_reference", params, id)
	self := len(b.rep.Hypotheses) - 1

	// Текстовые гипотезы Pod'ов-потребителей — следствие: ConfigMap их объясняет, а прежняя уверенность по тексту
	// сообщения заменена структурой (как у PVC: lowerTextPath).
	for _, p := range u.pods {
		for i := range textHyps {
			h := &b.rep.Hypotheses[i]
			if h.Resource.Kind != "Pod" || h.Resource.Name != p.Name || h.Resource.Namespace != p.Namespace {
				continue
			}
			switch {
			case isConfigText(*h):
				h.Confidence = ConfidenceLow
				hid := h.ID
				b.rep.Steps = slices.DeleteFunc(b.rep.Steps, func(s Step) bool {
					return s.Code == stepCheckObject && slices.Contains(s.Hypotheses, hid)
				})
			case h.Code == hypVolumeMount && mentionsConfigMap(b, *h):
				// Совет про PVC к ConfigMap не относится: его место занимает рекомендация цепочки.
				hid := h.ID
				b.rep.Steps = slices.DeleteFunc(b.rep.Steps, func(s Step) bool {
					return s.Code == "step.check_pvc" && slices.Contains(s.Hypotheses, hid)
				})
				b.rep.Recommendations = slices.DeleteFunc(b.rep.Recommendations, func(r Recommendation) bool {
					return r.Code == "rec.fix_volume_claim" && len(r.Hypotheses) == 1 && r.Hypotheses[0] == hid
				})
			default:
				continue
			}
			if !slices.Contains(b.rep.Hypotheses[self].Explains, h.ID) {
				b.rep.Hypotheses[self].Explains = append(b.rep.Hypotheses[self].Explains, h.ID)
			}
		}
	}
}

// mentionsConfigMap — сообщение события в доказательствах гипотезы называет ConfigMap.
func mentionsConfigMap(b *builder, h Hypothesis) bool {
	for _, id := range h.Supporting {
		for _, f := range b.rep.Facts {
			if f.ID == id && f.Code == "event" && contains(f.Params["message"], "configmap") {
				return true
			}
		}
	}
	return false
}

// configTextFindings разбирает текстовую гипотезу i, которую граф не опроверг и не заменил отсутствующим ConfigMap.
func configTextFindings(b *builder, g *depgraph.Graph, i int) {
	h := b.rep.Hypotheses[i]
	if !isConfigText(h) {
		return
	}
	var present []depgraph.Edge
	var unknown *depgraph.Edge
	for _, p := range b.c.Pods {
		if p.Name != h.Resource.Name || p.Namespace != h.Resource.Namespace {
			continue
		}
		for _, e := range configEdges(g, p) {
			switch e.State {
			case depgraph.EdgeMissing:
				return // объяснено отсутствующим ConfigMap (missingConfigMap)
			case depgraph.EdgeUnknown:
				if unknown == nil {
					unknown = &e
				}
			case depgraph.EdgeOK:
				if !e.Optional { // optional-ссылка на отсутствующий ConfigMap — ребро ok, но объекта нет: ключ проверять не у кого
					present = append(present, e)
				}
			}
		}
	}
	if unknown != nil {
		// Не удалось проверить: граф не подтверждает и не опровергает сообщение kubelet.
		b.missing("config.unverified", fromGraphRef(unknown.To), map[string]string{"reason": unknown.Reason})
		return
	}
	// ConfigMap'ы есть, а kubelet жалуется: речь о ключе. Ключи не читаются, поэтому уверенность не растёт.
	if h.Params["kind"] != "configmap" {
		return
	}
	for j, e := range present {
		if j == maxConfigSteps {
			break
		}
		cm := fromGraphRef(e.To)
		f := b.fact(cm, "config.reference_present", Source{Kind: SourceDerived, Resource: h.Resource, Field: "spec"},
			map[string]string{"configmap": cm.Name})
		b.rep.Hypotheses[i].Supporting = append(b.rep.Hypotheses[i].Supporting, f)
		b.step(cm, "step.check_configmap_key", map[string]string{"configmap": cm.Name}, h.ID)
	}
	if len(present) > 0 { // существование уже проверено графом: общий шаг «проверить, что объект есть» не нужен
		b.rep.Steps = slices.DeleteFunc(b.rep.Steps, func(s Step) bool {
			return s.Code == stepCheckObject && slices.Contains(s.Hypotheses, h.ID)
		})
	}
}

// configField — где в Pod'е стоит ссылка: в томах или в окружении контейнеров.
func configField(p *corev1.Pod, name string) string {
	for _, v := range p.Spec.Volumes {
		if volumeRefsConfigMap(v, name) {
			return "spec.volumes"
		}
	}
	return "spec.containers"
}

func volumeRefsConfigMap(v corev1.Volume, name string) bool {
	if v.ConfigMap != nil && v.ConfigMap.Name == name {
		return true
	}
	if v.Projected != nil {
		for _, s := range v.Projected.Sources {
			if s.ConfigMap != nil && s.ConfigMap.Name == name {
				return true
			}
		}
	}
	return false
}

// configContainer — первый контейнер Pod'а (сначала init), который использует ConfigMap; если связь не нашлась,
// первый контейнер Pod'а.
func configContainer(p *corev1.Pod, name string) string {
	vols := map[string]bool{}
	for _, v := range p.Spec.Volumes {
		if volumeRefsConfigMap(v, name) {
			vols[v.Name] = true
		}
	}
	uses := func(c corev1.Container) bool {
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil && ef.ConfigMapRef.Name == name {
				return true
			}
		}
		for _, e := range c.Env {
			if e.ValueFrom != nil && e.ValueFrom.ConfigMapKeyRef != nil && e.ValueFrom.ConfigMapKeyRef.Name == name {
				return true
			}
		}
		return slices.ContainsFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return vols[m.Name] })
	}
	all := slices.Concat(p.Spec.InitContainers, p.Spec.Containers)
	if i := slices.IndexFunc(all, uses); i >= 0 {
		return all[i].Name
	}
	if len(all) > 0 {
		return all[0].Name
	}
	return strings.TrimSpace(p.Name)
}
