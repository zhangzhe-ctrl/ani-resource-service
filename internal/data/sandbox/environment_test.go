package sandbox

import (
	"context"
	"errors"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func environmentFixture(t *testing.T) (*fake.FakeDynamicClient, *KubernetesProvider, biz.Sandbox, context.Context) {
	t.Helper()
	client, provider, s := providerFixture(t)
	s.Observed = biz.ObservedRunning
	s.ProviderRefs.PodName = "sandbox-" + s.ID
	s.ProviderRefs.PodUID = "expected-pod-uid"
	pod := renderPod(s, s.ProviderRefs.PodName, labels(s))
	pod.SetUID(types.UID(s.ProviderRefs.PodUID))
	pod.Object["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "sandbox", "ready": true}}}
	if err := client.Tracker().Add(pod); err != nil {
		t.Fatal(err)
	}
	ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: s.TenantID, Actor: "governance:test"})
	return client, provider, s, ctx
}

func TestReadEnvironmentRejectsNamespaceReplacementDuringObservation(t *testing.T) {
	client, provider, s, ctx := environmentFixture(t)
	reads := 0
	client.PrependReactor("get", "namespaces", func(action ktesting.Action) (bool, runtime.Object, error) {
		reads++
		if reads != 2 {
			return false, nil, nil
		}
		original, err := client.Tracker().Get(namespaceGVR, "", s.Snapshot.Namespace)
		if err != nil {
			return true, nil, err
		}
		replacement := original.DeepCopyObject().(*unstructured.Unstructured)
		replacement.SetUID("replacement-uid")
		return true, replacement, nil
	})
	items, err := provider.ReadEnvironment(ctx, s)
	if len(items) != 0 || !errors.Is(err, biz.ErrOwnership) || reads != 2 {
		t.Fatalf("namespace replacement: items=%+v reads=%d err=%v", items, reads, err)
	}
}

func TestReadEnvironmentReportsOnlyExplicitMetadata(t *testing.T) {
	client, provider, s, ctx := environmentFixture(t)
	items, err := provider.ReadEnvironment(ctx, s)
	if err != nil || len(items) != 1 || items[0] != (biz.EnvironmentVariable{Name: "ANI_SANDBOX_POD_UID", Source: "downward_api", FieldRef: "metadata.uid"}) {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	pod, err := client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Get(ctx, s.ProviderRefs.PodName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	containers, _, _ := unstructured.NestedSlice(pod.Object, "spec", "containers")
	delete(containers[0].(map[string]any), "env")
	if err = unstructured.SetNestedSlice(pod.Object, containers, "spec", "containers"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	items, err = provider.ReadEnvironment(ctx, s)
	if err != nil || len(items) != 0 {
		t.Fatalf("legacy empty env: %+v %v", items, err)
	}
}

func TestReadEnvironmentRejectsUntrustedOrMissingTargets(t *testing.T) {
	for _, name := range []string{"cross_tenant", "paused", "pending", "missing", "wrong_uid", "wrong_owner", "secret_env", "wrong_namespace_uid", "unready", "terminating"} {
		t.Run(name, func(t *testing.T) {
			client, provider, s, ctx := environmentFixture(t)
			switch name {
			case "cross_tenant":
				ctx = biz.WithCaller(ctx, biz.Caller{TenantID: "33333333-3333-4333-8333-333333333333", Actor: "x"})
			case "paused":
				s.Desired = biz.DesiredPaused
			case "pending":
				s.Pending = &biz.Mutation{ID: "pending"}
			case "wrong_uid":
				s.ProviderRefs.PodUID = "different"
			case "wrong_namespace_uid":
				s.Snapshot.NamespaceUID = "different"
			case "missing":
				if err := client.Tracker().Delete(podGVR, s.Snapshot.Namespace, s.ProviderRefs.PodName); err != nil {
					t.Fatal(err)
				}
			default:
				pod, err := client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Get(ctx, s.ProviderRefs.PodName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				switch name {
				case "wrong_owner":
					pod.SetLabels(map[string]string{providerOwnerLabel: "other", providerTenantLabel: s.TenantID, providerServiceLabel: "ani-resource-service"})
				case "unready":
					pod.Object["status"] = map[string]any{"phase": "Pending"}
				case "terminating":
					now := metav1.Now()
					pod.SetDeletionTimestamp(&now)
				case "secret_env":
					containers, _, _ := unstructured.NestedSlice(pod.Object, "spec", "containers")
					containers[0].(map[string]any)["env"] = []any{map[string]any{"name": "SECRET", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "other", "key": "secret"}}}}
					if err = unstructured.SetNestedSlice(pod.Object, containers, "spec", "containers"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			items, err := provider.ReadEnvironment(ctx, s)
			if err == nil || len(items) != 0 {
				t.Fatalf("must reject target: %+v %v", items, err)
			}
			if name == "cross_tenant" && !errors.Is(err, biz.ErrDenied) {
				t.Fatalf("wrong tenant error: %v", err)
			}
		})
	}
}
