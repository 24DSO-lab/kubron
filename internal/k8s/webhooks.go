// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WebhookRule — правило, на какие запросы реагирует webhook.
type WebhookRule struct {
	Operations  []string `json:"operations"`
	APIGroups   []string `json:"apiGroups"`
	APIVersions []string `json:"apiVersions"`
	Resources   []string `json:"resources"`
	Scope       string   `json:"scope"`
}

// WebhookInfo — один webhook внутри Mutating/ValidatingWebhookConfiguration.
type WebhookInfo struct {
	Name                    string        `json:"name"`
	ClientURL               string        `json:"clientUrl"`
	ServiceNamespace        string        `json:"serviceNamespace"`
	ServiceName             string        `json:"serviceName"`
	ServicePath             string        `json:"servicePath"`
	ServicePort             int32         `json:"servicePort"`
	HasCABundle             bool          `json:"hasCaBundle"`
	FailurePolicy           string        `json:"failurePolicy"`
	MatchPolicy             string        `json:"matchPolicy"`
	SideEffects             string        `json:"sideEffects"`
	TimeoutSeconds          int32         `json:"timeoutSeconds"`
	ReinvocationPolicy      string        `json:"reinvocationPolicy"` // только Mutating
	AdmissionReviewVersions []string      `json:"admissionReviewVersions"`
	NamespaceSelector       string        `json:"namespaceSelector"`
	ObjectSelector          string        `json:"objectSelector"`
	MatchConditions         []string      `json:"matchConditions"` // "name: expression"
	Rules                   []WebhookRule `json:"rules"`
}

// WebhookConfigurationInfo — общий вид MutatingWebhookConfiguration и ValidatingWebhookConfiguration.
type WebhookConfigurationInfo struct {
	Name         string        `json:"name"`
	Age          string        `json:"age"`
	WebhookCount int           `json:"webhookCount"`
	Webhooks     []WebhookInfo `json:"webhooks"`

	Created     string            `json:"created"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Finalizers  []string          `json:"finalizers"`

	ControlledBy string `json:"controlledBy"`
	ManagedBy    string `json:"managedBy"`
}

func (m *Manager) GetMutatingWebhookConfigurations() ([]WebhookConfigurationInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	list, err := conn.Client.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	items := make([]WebhookConfigurationInfo, 0, len(list.Items))
	for _, cfg := range list.Items {
		hooks := make([]WebhookInfo, 0, len(cfg.Webhooks))
		for _, w := range cfg.Webhooks {
			hook := newWebhookInfo(w.Name, w.ClientConfig, w.Rules, w.FailurePolicy, w.MatchPolicy, w.SideEffects,
				w.TimeoutSeconds, w.AdmissionReviewVersions, w.NamespaceSelector, w.ObjectSelector, w.MatchConditions)
			if w.ReinvocationPolicy != nil {
				hook.ReinvocationPolicy = string(*w.ReinvocationPolicy)
			}
			hooks = append(hooks, hook)
		}
		items = append(items, newWebhookConfigurationInfo(cfg.ObjectMeta, hooks))
	}
	return items, nil
}

func (m *Manager) GetValidatingWebhookConfigurations() ([]WebhookConfigurationInfo, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 15*time.Second)
	defer cancel()

	list, err := conn.Client.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	items := make([]WebhookConfigurationInfo, 0, len(list.Items))
	for _, cfg := range list.Items {
		hooks := make([]WebhookInfo, 0, len(cfg.Webhooks))
		for _, w := range cfg.Webhooks {
			hooks = append(hooks, newWebhookInfo(w.Name, w.ClientConfig, w.Rules, w.FailurePolicy, w.MatchPolicy, w.SideEffects,
				w.TimeoutSeconds, w.AdmissionReviewVersions, w.NamespaceSelector, w.ObjectSelector, w.MatchConditions))
		}
		items = append(items, newWebhookConfigurationInfo(cfg.ObjectMeta, hooks))
	}
	return items, nil
}

func newWebhookConfigurationInfo(om metav1.ObjectMeta, hooks []WebhookInfo) WebhookConfigurationInfo {
	meta := metaOf(om)
	return WebhookConfigurationInfo{
		Name:         om.Name,
		Age:          meta.Age,
		WebhookCount: len(hooks),
		Webhooks:     hooks,
		Created:      meta.Created,
		Labels:       meta.Labels,
		Annotations:  meta.Annotations,
		Finalizers:   meta.Finalizers,

		ControlledBy: controlledByOf(om.OwnerReferences),
		ManagedBy:    managedByOf(om),
	}
}

// newWebhookInfo — общая часть Mutating и Validating webhook (их типы в client-go разные,
// но поля совпадают).
func newWebhookInfo(
	name string,
	cc admissionv1.WebhookClientConfig,
	rules []admissionv1.RuleWithOperations,
	failure *admissionv1.FailurePolicyType,
	match *admissionv1.MatchPolicyType,
	sideEffects *admissionv1.SideEffectClass,
	timeout *int32,
	reviewVersions []string,
	nsSelector, objSelector *metav1.LabelSelector,
	conditions []admissionv1.MatchCondition,
) WebhookInfo {
	w := WebhookInfo{
		Name:                    name,
		ClientURL:               derefStr(cc.URL),
		HasCABundle:             len(cc.CABundle) > 0,
		TimeoutSeconds:          derefInt32(timeout),
		AdmissionReviewVersions: reviewVersions,
		NamespaceSelector:       formatSelector(nsSelector),
		ObjectSelector:          formatSelector(objSelector),
		Rules:                   make([]WebhookRule, 0, len(rules)),
		MatchConditions:         make([]string, 0, len(conditions)),
	}
	if svc := cc.Service; svc != nil {
		w.ServiceNamespace, w.ServiceName = svc.Namespace, svc.Name
		w.ServicePath = derefStr(svc.Path)
		w.ServicePort = 443 // порт по умолчанию, если в конфигурации не задан
		if svc.Port != nil {
			w.ServicePort = *svc.Port
		}
	}
	if failure != nil {
		w.FailurePolicy = string(*failure)
	}
	if match != nil {
		w.MatchPolicy = string(*match)
	}
	if sideEffects != nil {
		w.SideEffects = string(*sideEffects)
	}
	for _, r := range rules {
		ops := make([]string, 0, len(r.Operations))
		for _, op := range r.Operations {
			ops = append(ops, string(op))
		}
		rule := WebhookRule{
			Operations:  ops,
			APIGroups:   r.APIGroups,
			APIVersions: r.APIVersions,
			Resources:   r.Resources,
		}
		if r.Scope != nil {
			rule.Scope = string(*r.Scope)
		}
		w.Rules = append(w.Rules, rule)
	}
	for _, c := range conditions {
		w.MatchConditions = append(w.MatchConditions, c.Name+": "+strings.TrimSpace(c.Expression))
	}
	return w
}
