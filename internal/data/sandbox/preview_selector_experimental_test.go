package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestExperimentalSelectorRequiresOptInAndExactSelector(t *testing.T) {
	id, _, _ := previewFixture()
	id.serviceUID = ""
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}, ProviderRefs: biz.ProviderRefs{PodName: id.podName, PodUID: id.podUID}}
	cli := fake.NewSimpleDynamicClient(runtime.NewScheme())
	p := experimentalNodePortProvider{client: cli, verify: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }, verifyScope: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }}
	m := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	if _, err := p.createSelectorService(context.Background(), s, id, m); !errors.Is(err, biz.ErrUnsupported) || len(cli.Actions()) != 0 {
		t.Fatalf("unverified listener: %v", err)
	}
	p.enabled = true
	var created *unstructured.Unstructured
	cli.PrependReactor("create", "services", func(action clienttesting.Action) (bool, runtime.Object, error) {
		created = action.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		created.SetUID("service-uid")
		ports, _, _ := unstructured.NestedSlice(created.Object, "spec", "ports")
		ports[0].(map[string]any)["nodePort"] = int64(30123)
		_ = unstructured.SetNestedSlice(created.Object, ports, "spec", "ports")
		return true, created, nil
	})
	uid, err := p.createSelectorService(context.Background(), s, id, m)
	if err != nil || uid != "service-uid" {
		t.Fatalf("create uid=%q %v", uid, err)
	}
	if !previewSelectorServiceMatches(created, id, m) {
		t.Fatal("exact selector rejected")
	}
	cli.PrependReactor("get", "services", func(clienttesting.Action) (bool, runtime.Object, error) { return true, created.DeepCopy(), nil })
	if uid, err := p.observeSelectorService(context.Background(), s, id, m); err != nil || uid != "service-uid" {
		t.Fatalf("observe uid=%q err=%v", uid, err)
	}
	_ = unstructured.SetNestedField(created.Object, "another-sandbox", "spec", "selector", providerOwnerLabel)
	if previewSelectorServiceMatches(created, id, m) {
		t.Fatal("different sandbox selector accepted")
	}
	if _, err := p.observeSelectorService(context.Background(), s, id, m); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("foreign selector observed: %v", err)
	}
}

func TestExperimentalSelectorDispatchedTimeoutRemainsUnknown(t *testing.T) {
	id, _, _ := previewFixture()
	id.serviceUID = ""
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}, ProviderRefs: biz.ProviderRefs{PodName: id.podName, PodUID: id.podUID}}
	cli := fake.NewSimpleDynamicClient(runtime.NewScheme())
	cli.PrependReactor("create", "services", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, context.DeadlineExceeded })
	p := experimentalNodePortProvider{client: cli, enabled: true, verify: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }, verifyScope: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }}
	_, err := p.createSelectorService(context.Background(), s, id, biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"})
	if !errors.Is(err, errPreviewOutcomeUnknown) || errors.Is(err, errPreviewNotSent) {
		t.Fatalf("dispatched timeout misclassified: %v", err)
	}
}

func TestExperimentalSelectorAddressRequiresControllerTarget(t *testing.T) {
	id, svc, slice := previewFixture()
	m := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	svc.APIVersion = "v1"
	svc.Kind = "Service"
	svc.Spec.Type = corev1.ServiceTypeNodePort
	svc.Spec.Ports[0].NodePort = 30123
	svc.Labels[providerMutationID] = m.ID
	svc.Spec.Selector = map[string]string{providerOwnerLabel: id.sandbox, providerTenantLabel: id.tenant, providerServiceLabel: "ani-resource-service"}
	pod := &corev1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: id.podName, Namespace: id.namespace, UID: types.UID(id.podUID), Labels: map[string]string{providerOwnerLabel: id.sandbox, providerTenantLabel: id.tenant, providerServiceLabel: "ani-resource-service"}}, Spec: corev1.PodSpec{NodeName: "node1"}, Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: id.podIP, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
	node := &corev1.Node{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Node"}, ObjectMeta: metav1.ObjectMeta{Name: "node1", UID: "node-uid"}, Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.10.1.66"}}}}
	slice.Labels[discoveryv1.LabelManagedBy] = "endpointslice-controller.k8s.io"
	asObj := func(in any) *unstructured.Unstructured {
		raw, e := runtime.DefaultUnstructuredConverter.ToUnstructured(in)
		if e != nil {
			t.Fatal(e)
		}
		return &unstructured.Unstructured{Object: raw}
	}
	cli := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{previewEndpointSliceGVR: "EndpointSliceList"}, asObj(svc), asObj(pod), asObj(node))
	bad := false
	cli.PrependReactor("list", "endpointslices", func(clienttesting.Action) (bool, runtime.Object, error) {
		copy := slice.DeepCopy()
		if bad {
			copy.Endpoints[0].TargetRef.UID = "replacement"
		}
		list := &unstructured.UnstructuredList{}
		list.SetResourceVersion("1")
		list.Items = []unstructured.Unstructured{*asObj(copy)}
		return true, list, nil
	})
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}, ProviderRefs: biz.ProviderRefs{PodName: id.podName, PodUID: id.podUID}}
	p := experimentalNodePortProvider{client: cli, verify: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }, verifyScope: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }}
	addr, err := p.selectorAddress(context.Background(), s, id, m, "http")
	if err != nil || addr != "http://10.10.1.66:30123" {
		t.Fatalf("address=%q err=%v", addr, err)
	}
	slice.Endpoints[0].TargetRef.APIVersion = "" // Kubernetes controller may omit this optional field.
	addr, err = p.selectorAddress(context.Background(), s, id, m, "http")
	if err != nil || addr != "http://10.10.1.66:30123" {
		t.Fatalf("omitted target API version: address=%q err=%v", addr, err)
	}
	slice.Endpoints[0].TargetRef.APIVersion = "apps/v1"
	if _, err = p.selectorAddress(context.Background(), s, id, m, "http"); !errors.Is(err, biz.ErrDataPlaneUnavailable) {
		t.Fatalf("incorrect target API version accepted: %v", err)
	}
	slice.Endpoints[0].TargetRef.APIVersion = ""
	bad = true
	if _, err = p.selectorAddress(context.Background(), s, id, m, "http"); !errors.Is(err, biz.ErrDataPlaneUnavailable) {
		t.Fatalf("replacement pod accepted: %v", err)
	}
}
