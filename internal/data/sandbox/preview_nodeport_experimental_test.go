package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestExperimentalNodePortRequiresEnableAndJournal(t *testing.T) {
	id, _, _ := previewFixture()
	id.serviceUID = ""
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}, ProviderRefs: biz.ProviderRefs{PodName: id.podName, PodUID: id.podUID}}
	cli := fake.NewSimpleDynamicClient(runtime.NewScheme())
	verified := 0
	p := experimentalNodePortProvider{client: cli, verify: func(context.Context, biz.Sandbox, previewObjectIdentity) error { verified++; return nil }}
	mutation := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	if _, err := p.createService(context.Background(), s, id, mutation); !errors.Is(err, biz.ErrUnsupported) || verified != 0 || len(cli.Actions()) != 0 {
		t.Fatalf("disabled issued call: %v", err)
	}
	p.enabled = true
	if _, err := p.createService(context.Background(), s, id, biz.PreviewExperimentMutation{Kind: "create"}); !errors.Is(err, biz.ErrInvalid) || len(cli.Actions()) != 0 {
		t.Fatalf("unjournaled create: %v", err)
	}
	cli.PrependReactor("create", "services", func(action clienttesting.Action) (bool, runtime.Object, error) {
		obj := action.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		obj.SetUID("service-uid")
		ports, _, _ := unstructured.NestedSlice(obj.Object, "spec", "ports")
		ports[0].(map[string]any)["nodePort"] = int64(30123)
		_ = unstructured.SetNestedSlice(obj.Object, ports, "spec", "ports")
		return true, obj, nil
	})
	uid, err := p.createService(context.Background(), s, id, mutation)
	if err != nil || uid != "service-uid" || verified != 1 {
		t.Fatalf("create: %q %v", uid, err)
	}
	action := cli.Actions()[0].(clienttesting.CreateAction)
	created := action.GetObject().(*unstructured.Unstructured)
	selector, found, _ := unstructured.NestedMap(created.Object, "spec", "selector")
	if found && len(selector) != 0 {
		t.Fatal("service permits label-selected endpoints")
	}
	if created.GetLabels()[providerMutationID] != mutation.ID {
		t.Fatal("missing journal identity")
	}
}

func TestExperimentalNodePortUnknownAndUIDDelete(t *testing.T) {
	id, _, _ := previewFixture()
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}, ProviderRefs: biz.ProviderRefs{PodName: id.podName, PodUID: id.podUID}}
	cli := fake.NewSimpleDynamicClient(runtime.NewScheme())
	p := experimentalNodePortProvider{enabled: true, client: cli, verify: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }}
	p.verifyScope = func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }
	cli.PrependReactor("create", "services", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, context.DeadlineExceeded })
	_, err := p.createService(context.Background(), s, id, biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"})
	if !errors.Is(err, errPreviewOutcomeUnknown) {
		t.Fatalf("timeout inferred absence: %v", err)
	}
	cli.PrependReactor("get", "services", func(clienttesting.Action) (bool, runtime.Object, error) {
		yes := true
		obj := &unstructured.Unstructured{}
		obj.SetName(id.serviceName)
		obj.SetNamespace(id.namespace)
		obj.SetUID(types.UID(id.serviceUID))
		obj.SetLabels(map[string]string{providerTenantLabel: id.tenant, providerOwnerLabel: id.sandbox, providerServiceLabel: "ani-resource-service", providerMutationID: uuid.NewString()})
		obj.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: id.podName, UID: types.UID(id.podUID), Controller: &yes}})
		return true, obj, nil
	})
	cli.PrependReactor("delete", "services", func(a clienttesting.Action) (bool, runtime.Object, error) {
		v := a.(clienttesting.DeleteAction).GetDeleteOptions().Preconditions
		if v == nil || v.UID == nil || *v.UID != types.UID(id.serviceUID) {
			t.Fatal("missing UID precondition")
		}
		return true, nil, nil
	})
	if err = p.deleteService(context.Background(), s, id, biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "delete", TargetUID: id.serviceUID}); err != nil {
		t.Fatal(err)
	}
	if err = p.deleteService(context.Background(), s, id, biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "delete", TargetUID: "foreign"}); !errors.Is(err, biz.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestExperimentalObserveRequiresRecordedUIDAndScope(t *testing.T) {
	id, svc, _ := previewFixture()
	create := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	svc.APIVersion = "v1"
	svc.Kind = "Service"
	svc.Spec.Type = corev1.ServiceTypeNodePort
	svc.Spec.Ports[0].NodePort = 30123
	svc.Labels[providerMutationID] = create.ID
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(svc)
	if err != nil {
		t.Fatal(err)
	}
	cli := fake.NewSimpleDynamicClient(runtime.NewScheme(), &unstructured.Unstructured{Object: raw})
	checks := 0
	p := experimentalNodePortProvider{client: cli, verifyScope: func(context.Context, biz.Sandbox, previewObjectIdentity) error { checks++; return nil }}
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}}
	found, err := p.observeService(context.Background(), s, id, create)
	if err != nil || !found || checks != 2 {
		t.Fatalf("observe=%t checks=%d err=%v", found, checks, err)
	}
	id.serviceUID = "foreign"
	if _, err = p.observeService(context.Background(), s, id, create); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("replacement accepted: %v", err)
	}
}

func TestExperimentalSliceRequiresIngressProofAndUIDDelete(t *testing.T) {
	id, svc, _ := previewFixture()
	serviceCreate := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	svc.APIVersion = "v1"
	svc.Kind = "Service"
	svc.Spec.Type = corev1.ServiceTypeNodePort
	svc.Spec.Ports[0].NodePort = 30123
	svc.Labels[providerMutationID] = serviceCreate.ID
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(svc)
	if err != nil {
		t.Fatal(err)
	}
	cli := fake.NewSimpleDynamicClient(runtime.NewScheme(), &unstructured.Unstructured{Object: raw})
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}, ProviderRefs: biz.ProviderRefs{PodName: id.podName, PodUID: id.podUID}}
	p := experimentalNodePortProvider{enabled: true, client: cli, verify: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }, verifyScope: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }}
	create := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create_slice", TargetUID: id.serviceUID}
	if _, err = p.createSlice(context.Background(), s, id, serviceCreate, create); !errors.Is(err, biz.ErrUnsupported) || len(cli.Actions()) != 0 {
		t.Fatalf("missing ingress proof exposed Slice: %v", err)
	}
	p.verifyIngress = func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }
	var created *unstructured.Unstructured
	cli.PrependReactor("create", "endpointslices", func(action clienttesting.Action) (bool, runtime.Object, error) {
		created = action.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		created.SetUID(types.UID(id.sliceUID))
		return true, created, nil
	})
	uid, err := p.createSlice(context.Background(), s, id, serviceCreate, create)
	if err != nil || uid != id.sliceUID {
		t.Fatalf("create slice=%q %v", uid, err)
	}
	cli.PrependReactor("get", "endpointslices", func(clienttesting.Action) (bool, runtime.Object, error) { return true, created.DeepCopy(), nil })
	observed, err := p.observeSlice(context.Background(), s, id, create)
	if err != nil || observed != id.sliceUID {
		t.Fatalf("slice observation=%q %v", observed, err)
	}
	wrong := id
	wrong.sliceUID = "replacement-uid"
	if _, err := p.observeSlice(context.Background(), s, wrong, create); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("replacement slice accepted: %v", err)
	}
	cli.PrependReactor("delete", "endpointslices", func(action clienttesting.Action) (bool, runtime.Object, error) {
		uid := action.(clienttesting.DeleteAction).GetDeleteOptions().Preconditions.UID
		if uid == nil || *uid != types.UID(id.sliceUID) {
			t.Fatal("slice deletion missing UID precondition")
		}
		return true, nil, nil
	})
	del := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "delete_slice", TargetUID: id.sliceUID}
	if err = p.deleteSlice(context.Background(), s, id, create, del); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentalNoSlicesDoesNotIgnoreForeignSlice(t *testing.T) {
	id, _, _ := previewFixture()
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}}
	cli := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{previewEndpointSliceGVR: "EndpointSliceList"})
	foreign := false
	cli.PrependReactor("list", "endpointslices", func(clienttesting.Action) (bool, runtime.Object, error) {
		list := &unstructured.UnstructuredList{}
		list.SetResourceVersion("1")
		if foreign {
			list.Items = []unstructured.Unstructured{{Object: map[string]any{"metadata": map[string]any{"name": "foreign", "labels": map[string]any{"kubernetes.io/service-name": id.serviceName}}}}}
		}
		return true, list, nil
	})
	p := experimentalNodePortProvider{client: cli, verifyScope: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }}
	empty, err := p.noPreviewSlices(context.Background(), s, id)
	if err != nil || !empty {
		t.Fatalf("empty=%t %v", empty, err)
	}
	foreign = true
	empty, err = p.noPreviewSlices(context.Background(), s, id)
	if err != nil || empty {
		t.Fatalf("foreign ignored empty=%t %v", empty, err)
	}
}
