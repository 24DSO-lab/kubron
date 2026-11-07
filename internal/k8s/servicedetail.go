// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type ServiceDetail struct {
	Name                  string                 `json:"name"`
	Namespace             string                 `json:"namespace"`
	Type                  string                 `json:"type"`
	ClusterIP             string                 `json:"clusterIP"`
	ClusterIPs            []string               `json:"clusterIPs"`
	ExternalIPs           []string               `json:"externalIPs"`
	IPFamilies            string                 `json:"ipFamilies"`
	IPFamilyPolicy        string                 `json:"ipFamilyPolicy"`
	SessionAffinity       string                 `json:"sessionAffinity"`
	Ports                 []ServicePortInfo      `json:"ports"`
	Selector              map[string]string      `json:"selector"`
	Labels                map[string]string      `json:"labels"`
	Annotations           map[string]string      `json:"annotations"`
	Finalizers            []string               `json:"finalizers"`
	Age                   string                 `json:"age"`
	Endpoints             []EndpointInfo         `json:"endpoints"`
	EndpointSlices        []EndpointSliceInfo    `json:"endpointSlices"`
	ExternalName          string                 `json:"externalName"`
	LoadBalancer          []string               `json:"loadBalancer"`
	ExternalTrafficPolicy string                 `json:"externalTrafficPolicy"`
	InternalTrafficPolicy string                 `json:"internalTrafficPolicy"`
	NetworkPolicies       []ServiceNetworkPolicy `json:"networkPolicies"`
}

// ServiceNetworkPolicy — политика, выбирающая хотя бы один под сервиса.
type ServiceNetworkPolicy struct {
	Name        string   `json:"name"`
	Namespace   string   `json:"namespace"`
	PolicyTypes []string `json:"policyTypes"`
}

// serviceNetworkPolicies: политики namespace, чей podSelector подходит к подам сервиса.
// Ошибки (например, нет прав на NetworkPolicy) не ломают панель: возвращается пустой список.
func serviceNetworkPolicies(ctx context.Context, conn *ClusterConnection, svc *corev1.Service) []ServiceNetworkPolicy {
	out := []ServiceNetworkPolicy{}
	if len(svc.Spec.Selector) == 0 {
		return out
	}
	pods, err := conn.Client.CoreV1().Pods(svc.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
	})
	if err != nil || len(pods.Items) == 0 {
		return out
	}
	nps, err := conn.Client.NetworkingV1().NetworkPolicies(svc.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out
	}
	for i := range nps.Items {
		np := &nps.Items[i]
		sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector) // пустой селектор = все поды
		if err != nil {
			continue
		}
		for j := range pods.Items {
			if sel.Matches(labels.Set(pods.Items[j].Labels)) {
				out = append(out, ServiceNetworkPolicy{Name: np.Name, Namespace: np.Namespace, PolicyTypes: policyTypesOf(np)})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type ServicePortInfo struct {
	Name       string `json:"name"`
	Port       int32  `json:"port"`
	TargetPort string `json:"targetPort"`
	Protocol   string `json:"protocol"`
	NodePort   int32  `json:"nodePort"`
	Forwarded  bool   `json:"forwarded"`
	LocalPort  int    `json:"localPort"`
}

type EndpointInfo struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

func (m *Manager) GetServiceDetail(namespace, name string) (*ServiceDetail, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(m.baseCtx(), 10*time.Second)
	defer cancel()

	svc, err := conn.Client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var ports []ServicePortInfo
	for _, p := range svc.Spec.Ports {
		ports = append(ports, ServicePortInfo{
			Name:       p.Name,
			Port:       p.Port,
			TargetPort: p.TargetPort.String(),
			Protocol:   string(p.Protocol),
			NodePort:   p.NodePort,
		})
	}

	var clusterIPs []string
	clusterIPs = append(clusterIPs, svc.Spec.ClusterIPs...)

	ipFamilies := ""
	for i, f := range svc.Spec.IPFamilies {
		if i > 0 {
			ipFamilies += ", "
		}
		ipFamilies += string(f)
	}

	ipFamilyPolicy := ""
	if svc.Spec.IPFamilyPolicy != nil {
		ipFamilyPolicy = string(*svc.Spec.IPFamilyPolicy)
	}

	// Получаем endpoints
	var endpoints []EndpointInfo
	ep, err := conn.Client.CoreV1().Endpoints(namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		for _, subset := range ep.Subsets {
			var addrs []string
			for _, addr := range subset.Addresses {
				for _, port := range subset.Ports {
					addrs = append(addrs, fmt.Sprintf("%s:%d", addr.IP, port.Port))
				}
			}
			endpoints = append(endpoints, EndpointInfo{
				Name:      name,
				Addresses: addrs,
			})
		}
	}

	// Получаем endpoint slices
	var endpointSlices []EndpointSliceInfo
	esList, err := conn.Client.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kubernetes.io/service-name=" + name,
	})
	if err == nil {
		for _, es := range esList.Items {
			endpointSlices = append(endpointSlices, convertEndpointSlice(es))
		}
	}

	var lb []string
	for _, ing := range svc.Status.LoadBalancer.Ingress {
		if ing.IP != "" {
			lb = append(lb, ing.IP)
		} else if ing.Hostname != "" {
			lb = append(lb, ing.Hostname)
		}
	}
	internalPolicy := ""
	if p := svc.Spec.InternalTrafficPolicy; p != nil {
		internalPolicy = string(*p)
	}

	return &ServiceDetail{
		Name:                  svc.Name,
		Namespace:             svc.Namespace,
		Type:                  string(svc.Spec.Type),
		ClusterIP:             svc.Spec.ClusterIP,
		ClusterIPs:            clusterIPs,
		ExternalIPs:           svc.Spec.ExternalIPs,
		IPFamilies:            ipFamilies,
		IPFamilyPolicy:        ipFamilyPolicy,
		SessionAffinity:       string(svc.Spec.SessionAffinity),
		Ports:                 ports,
		Selector:              svc.Spec.Selector,
		Labels:                svc.Labels,
		Annotations:           svc.Annotations,
		Finalizers:            svc.Finalizers,
		Age:                   formatAge(svc.CreationTimestamp.Time),
		Endpoints:             endpoints,
		EndpointSlices:        endpointSlices,
		ExternalName:          svc.Spec.ExternalName,
		LoadBalancer:          lb,
		ExternalTrafficPolicy: string(svc.Spec.ExternalTrafficPolicy),
		InternalTrafficPolicy: internalPolicy,
		NetworkPolicies:       serviceNetworkPolicies(ctx, conn, svc),
	}, nil
}
