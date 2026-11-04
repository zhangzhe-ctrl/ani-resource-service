package sandbox

import (
	"context"
	"errors"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func egressFixture(t *testing.T) (*fake.FakeDynamicClient, *KubernetesProvider, biz.Sandbox, context.Context) {
	t.Helper()
	original, provider, s, ctx := environmentFixture(t)
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{sandboxNetworkPolicyGVR: "NetworkPolicyList"})
	for _, object := range []struct {
		resource        schema.GroupVersionResource
		namespace, name string
	}{
		{namespaceGVR, "", s.Snapshot.Namespace}, {podGVR, s.Snapshot.Namespace, s.ProviderRefs.PodName},
	} {
		obj, err := original.Tracker().Get(object.resource, object.namespace, object.name)
		if err != nil {
			t.Fatal(err)
		}
		if err = client.Tracker().Create(object.resource, obj.DeepCopyObject(), object.namespace); err != nil {
			t.Fatal(err)
		}
	}
	provider.client = client
	return client, provider, s, ctx
}

func policyFixture(namespace, name string, spec map[string]any) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": spec}}
}

func policyList(items ...unstructured.Unstructured) *unstructured.UnstructuredList {
	list := &unstructured.UnstructuredList{Items: items}
	list.SetResourceVersion("20")
	return list
}

func TestEgressDeclarationKubernetesPolicyUnion(t *testing.T) {
	cases := []struct {
		name      string
		specs     []map[string]any
		want      string
		isolated  bool
		wantRules int
	}{
		{"no_policy", nil, "unrestricted", false, 0},
		{"ingress_only", []map[string]any{{"podSelector": map[string]any{}, "policyTypes": []any{"Ingress"}}}, "unrestricted", false, 0},
		{"nonmatching", []map[string]any{{"podSelector": map[string]any{"matchLabels": map[string]any{"unrelated": "yes"}}, "policyTypes": []any{"Egress"}}}, "unrestricted", false, 0},
		{"deny_all", []map[string]any{{"podSelector": map[string]any{}, "policyTypes": []any{"Egress"}, "egress": []any{}}}, "deny_all", true, 0},
		{"empty_rule_allows_all", []map[string]any{{"podSelector": map[string]any{}, "policyTypes": []any{"Egress"}, "egress": []any{map[string]any{}}}}, "unrestricted", true, 1},
		{"defaulted_egress", []map[string]any{{"podSelector": map[string]any{}, "egress": []any{map[string]any{"ports": []any{map[string]any{"protocol": "TCP", "port": int64(443)}}}}}}, "restricted", true, 1},
		{"additive", []map[string]any{{"podSelector": map[string]any{}, "policyTypes": []any{"Egress"}}, {"podSelector": map[string]any{}, "policyTypes": []any{"Egress"}, "egress": []any{map[string]any{"to": []any{map[string]any{"ipBlock": map[string]any{"cidr": "192.0.2.0/24", "except": []any{"192.0.2.128/25"}}}}, "ports": []any{map[string]any{"port": "https"}}}}}}, "restricted", true, 1},
		{"wildcard_wins", []map[string]any{{"podSelector": map[string]any{}, "policyTypes": []any{"Egress"}}, {"podSelector": map[string]any{}, "policyTypes": []any{"Egress"}, "egress": []any{map[string]any{}}}}, "unrestricted", true, 1},
		{"empty_types_and_empty_egress", []map[string]any{{"podSelector": map[string]any{}, "egress": []any{}}}, "unrestricted", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, provider, s, ctx := egressFixture(t)
			obj, err := client.Tracker().Get(podGVR, s.Snapshot.Namespace, s.ProviderRefs.PodName)
			if err != nil {
				t.Fatal(err)
			}
			pod := obj.(*unstructured.Unstructured).DeepCopy()
			pod.SetResourceVersion("10")
			if err := client.Tracker().Update(podGVR, pod, s.Snapshot.Namespace); err != nil {
				t.Fatal(err)
			}
			items := []unstructured.Unstructured{}
			for i, spec := range tc.specs {
				items = append(items, policyFixture(s.Snapshot.Namespace, string(rune('a'+i)), spec))
			}
			client.PrependReactor("list", "networkpolicies", func(ktesting.Action) (bool, runtime.Object, error) { return true, policyList(items...), nil })
			got, err := provider.ReadEgressDeclaration(ctx, s)
			if err != nil || got.Declaration != tc.want || got.Isolated != tc.isolated || len(got.Rules) != tc.wantRules {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if tc.name == "additive" {
				p := got.Rules[0].Peers[0].IPBlock
				port := got.Rules[0].Ports[0]
				if p == nil || p.CIDR != "192.0.2.0/24" || len(p.Except) != 1 || p.Except[0] != "192.0.2.128/25" || port.Protocol != "TCP" || port.PortType != "name" || port.Port != "https" {
					t.Fatalf("lost rule detail: %+v", got.Rules[0])
				}
			}
		})
	}
}

func TestEgressDeclarationRejectsChangedSnapshotAndForeignTenant(t *testing.T) {
	for _, variant := range []string{"tenant", "missing_version", "pod_changed", "namespace_changed", "list_version_changed", "malformed_selector"} {
		t.Run(variant, func(t *testing.T) {
			client, provider, s, ctx := egressFixture(t)
			obj, err := client.Tracker().Get(podGVR, s.Snapshot.Namespace, s.ProviderRefs.PodName)
			if err != nil {
				t.Fatal(err)
			}
			pod := obj.(*unstructured.Unstructured).DeepCopy()
			if variant != "missing_version" {
				pod.SetResourceVersion("10")
			}
			if err := client.Tracker().Update(podGVR, pod, s.Snapshot.Namespace); err != nil {
				t.Fatal(err)
			}
			if variant == "tenant" {
				ctx = biz.WithCaller(ctx, biz.Caller{TenantID: "33333333-3333-4333-8333-333333333333", Actor: "test"})
			}
			podReads, namespaceReads, lists := 0, 0, 0
			client.PrependReactor("get", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
				podReads++
				if variant == "pod_changed" && podReads == 2 {
					changed := pod.DeepCopy()
					changed.SetResourceVersion("11")
					return true, changed, nil
				}
				return false, nil, nil
			})
			client.PrependReactor("get", "namespaces", func(ktesting.Action) (bool, runtime.Object, error) {
				namespaceReads++
				if variant == "namespace_changed" && namespaceReads == 2 {
					ns, err := client.Tracker().Get(namespaceGVR, "", s.Snapshot.Namespace)
					if err != nil {
						t.Fatal(err)
					}
					changed := ns.(*unstructured.Unstructured).DeepCopy()
					changed.SetUID("other")
					return true, changed, nil
				}
				return false, nil, nil
			})
			client.PrependReactor("list", "networkpolicies", func(action ktesting.Action) (bool, runtime.Object, error) {
				lists++
				out := policyList()
				if variant == "list_version_changed" && lists == 1 {
					out.SetContinue("page-2")
				}
				if variant == "list_version_changed" && lists == 2 {
					if action.(interface{ GetListOptions() metav1.ListOptions }).GetListOptions().Continue != "page-2" {
						t.Fatal("continuation missing")
					}
					out.SetResourceVersion("21")
				}
				if variant == "malformed_selector" {
					out = policyList(policyFixture(s.Snapshot.Namespace, "bad", map[string]any{"policyTypes": []any{"Egress"}}))
				}
				return true, out, nil
			})
			got, err := provider.ReadEgressDeclaration(ctx, s)
			if err == nil || got.Declaration != "" {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if variant == "tenant" && !errors.Is(err, biz.ErrDenied) {
				t.Fatalf("tenant error: %v", err)
			}
		})
	}
}

func TestEgressDeclarationReadsEveryPageAndPreservesSelectors(t *testing.T) {
	client, provider, s, ctx := egressFixture(t)
	obj, err := client.Tracker().Get(podGVR, s.Snapshot.Namespace, s.ProviderRefs.PodName)
	if err != nil {
		t.Fatal(err)
	}
	pod := obj.(*unstructured.Unstructured).DeepCopy()
	pod.SetResourceVersion("10")
	if err = client.Tracker().Update(podGVR, pod, s.Snapshot.Namespace); err != nil {
		t.Fatal(err)
	}
	policy := policyFixture(s.Snapshot.Namespace, "selected", map[string]any{
		"podSelector": map[string]any{"matchExpressions": []any{map[string]any{"key": providerOwnerLabel, "operator": "In", "values": []any{s.ID}}}},
		"policyTypes": []any{"Egress"},
		"egress": []any{map[string]any{
			"to":    []any{map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"app": "dns"}}, "podSelector": map[string]any{"matchExpressions": []any{map[string]any{"key": "role", "operator": "In", "values": []any{"resolver"}}}}}},
			"ports": []any{map[string]any{"protocol": "UDP", "port": int64(53)}},
		}},
	})
	calls := 0
	client.PrependReactor("list", "networkpolicies", func(action ktesting.Action) (bool, runtime.Object, error) {
		calls++
		opts := action.(interface{ GetListOptions() metav1.ListOptions }).GetListOptions()
		if calls == 1 && opts.Continue == "" {
			list := policyList()
			list.SetContinue("next")
			return true, list, nil
		}
		if calls == 2 && opts.Continue == "next" {
			return true, policyList(policy), nil
		}
		t.Fatalf("unexpected page %d: %+v", calls, opts)
		return true, nil, nil
	})
	got, err := provider.ReadEgressDeclaration(ctx, s)
	if err != nil || calls != 2 || got.Declaration != "restricted" || len(got.Rules) != 1 || len(got.Rules[0].Peers) != 1 || len(got.Rules[0].Ports) != 1 {
		t.Fatalf("got=%+v calls=%d err=%v", got, calls, err)
	}
	peer, port := got.Rules[0].Peers[0], got.Rules[0].Ports[0]
	if peer.NamespaceSelector == nil || peer.NamespaceSelector.MatchLabels["app"] != "dns" || peer.PodSelector == nil || len(peer.PodSelector.MatchExpressions) != 1 || peer.PodSelector.MatchExpressions[0].Values[0] != "resolver" || port.Protocol != "UDP" || port.PortType != "number" || port.Port != "53" {
		t.Fatalf("lost peer or port: %+v %+v", peer, port)
	}
}
