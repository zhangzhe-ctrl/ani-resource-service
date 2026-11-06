package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var sandboxNetworkPolicyGVR = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}

// ReadEgressDeclaration reads one bounded snapshot of the NetworkPolicy
// declarations selecting this exact Pod. Other cluster/network policies and
// whether the CNI enforces any declaration are outside this observation.
func (p *KubernetesProvider) ReadEgressDeclaration(ctx context.Context, s biz.Sandbox) (biz.EgressDeclaration, error) {
	if err := biz.RequireTenant(ctx, s.TenantID); err != nil {
		return biz.EgressDeclaration{}, err
	}
	if !biz.UUID(s.ID) || s.Version < 1 || s.Desired != biz.DesiredRunning || s.Observed != biz.ObservedRunning || s.Pending != nil || s.ProviderRefs.PodName != "sandbox-"+s.ID || s.ProviderRefs.PodUID == "" {
		return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
	}
	if err := p.requireCluster(s); err != nil {
		return biz.EgressDeclaration{}, err
	}
	namespaceUID, err := p.verifyTenantNamespace(ctx, s, "")
	if err != nil {
		return biz.EgressDeclaration{}, err
	}
	pods := p.client.Resource(podGVR).Namespace(s.Snapshot.Namespace)
	pod, err := pods.Get(ctx, s.ProviderRefs.PodName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
	}
	if err != nil {
		return biz.EgressDeclaration{}, err
	}
	if string(pod.GetUID()) != s.ProviderRefs.PodUID || pod.GetResourceVersion() == "" || !owned(pod, s) || !p.matchesPod(pod, s) || !podReady(pod) {
		return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
	}
	selected := klabels.Set(pod.GetLabels())
	isolated, unrestricted := false, false
	rules := []biz.EgressRule{}
	opts := metav1.ListOptions{Limit: 500}
	snapshot, count, size := "", 0, 0
	for page := 0; ; page++ {
		if page >= 21 {
			return biz.EgressDeclaration{}, fmt.Errorf("%w: network policy snapshot exceeds page limit", biz.ErrDataPlaneUnavailable)
		}
		list, err := p.client.Resource(sandboxNetworkPolicyGVR).Namespace(s.Snapshot.Namespace).List(ctx, opts)
		if err != nil {
			return biz.EgressDeclaration{}, err
		}
		if list.GetResourceVersion() == "" || (snapshot != "" && list.GetResourceVersion() != snapshot) {
			return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
		}
		snapshot = list.GetResourceVersion()
		for i := range list.Items {
			obj := &list.Items[i]
			count++
			raw, err := obj.MarshalJSON()
			if err != nil {
				return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
			}
			size += len(raw)
			if count > 10000 || size > 4*1024*1024 || obj.GetNamespace() != s.Snapshot.Namespace {
				return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
			}
			// An absent selector is invalid rather than implicitly selecting
			// every Pod. Valid objects always have spec.podSelector.
			spec, ok := obj.Object["spec"].(map[string]any)
			if !ok || spec["podSelector"] == nil {
				return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
			}
			var policy networkingv1.NetworkPolicy
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &policy); err != nil {
				return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
			}
			selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
			if err != nil {
				return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
			}
			if !selector.Matches(selected) {
				continue
			}
			// API server defaults include Egress when the egress field is
			// present and non-empty. For explicit policyTypes, only Egress
			// makes the Pod egress-isolated.
			egress := len(policy.Spec.PolicyTypes) == 0 && len(policy.Spec.Egress) > 0
			for _, typ := range policy.Spec.PolicyTypes {
				if typ == networkingv1.PolicyTypeEgress {
					egress = true
				} else if typ != networkingv1.PolicyTypeIngress {
					return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
				}
			}
			if !egress {
				continue
			}
			isolated = true
			for _, rule := range policy.Spec.Egress {
				if len(rule.To) == 0 && len(rule.Ports) == 0 {
					unrestricted = true
				}
				rules = append(rules, egressRule(rule))
			}
		}
		next := list.GetContinue()
		if next == "" {
			break
		}
		if next == opts.Continue {
			return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
		}
		opts.Continue = next
	}
	current, err := pods.Get(ctx, s.ProviderRefs.PodName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
	}
	if err != nil {
		return biz.EgressDeclaration{}, err
	}
	if current.GetUID() != pod.GetUID() || current.GetResourceVersion() != pod.GetResourceVersion() {
		return biz.EgressDeclaration{}, biz.ErrDataPlaneUnavailable
	}
	if _, err := p.verifyTenantNamespace(ctx, s, namespaceUID); err != nil {
		return biz.EgressDeclaration{}, err
	}
	declaration := "restricted"
	if !isolated || unrestricted {
		declaration = "unrestricted"
	} else if len(rules) == 0 {
		declaration = "deny_all"
	}
	// A stable order keeps pagination-free snapshots comparable even if the API
	// server returns NetworkPolicies in a different order.
	sort.Slice(rules, func(i, j int) bool {
		a, _ := json.Marshal(rules[i])
		b, _ := json.Marshal(rules[j])
		return string(a) < string(b)
	})
	return biz.EgressDeclaration{Declaration: declaration, Isolated: isolated, Rules: rules}, nil
}

func egressRule(in networkingv1.NetworkPolicyEgressRule) biz.EgressRule {
	out := biz.EgressRule{Peers: []biz.EgressPeer{}, Ports: []biz.EgressPort{}}
	for _, peer := range in.To {
		dest := biz.EgressPeer{PodSelector: egressSelector(peer.PodSelector), NamespaceSelector: egressSelector(peer.NamespaceSelector)}
		if peer.IPBlock != nil {
			dest.IPBlock = &biz.EgressIPBlock{CIDR: peer.IPBlock.CIDR, Except: append([]string{}, peer.IPBlock.Except...)}
		}
		out.Peers = append(out.Peers, dest)
	}
	for _, port := range in.Ports {
		item := biz.EgressPort{Protocol: "TCP", PortType: "all"}
		if port.Protocol != nil {
			item.Protocol = string(*port.Protocol)
		}
		if port.Port != nil {
			item.PortType = "number"
			if port.Port.Type == 1 {
				item.PortType = "name"
			}
			item.Port = port.Port.String()
		}
		if port.EndPort != nil {
			item.EndPort = *port.EndPort
		}
		out.Ports = append(out.Ports, item)
	}
	return out
}

func egressSelector(in *metav1.LabelSelector) *biz.EgressLabelSelector {
	if in == nil {
		return nil
	}
	out := &biz.EgressLabelSelector{MatchLabels: map[string]string{}, MatchExpressions: []biz.EgressLabelExpression{}}
	for k, v := range in.MatchLabels {
		out.MatchLabels[k] = v
	}
	for _, expression := range in.MatchExpressions {
		out.MatchExpressions = append(out.MatchExpressions, biz.EgressLabelExpression{Key: expression.Key, Operator: string(expression.Operator), Values: append([]string{}, expression.Values...)})
	}
	return out
}
