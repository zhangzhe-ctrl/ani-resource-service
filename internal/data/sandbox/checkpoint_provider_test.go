package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

const ckID = "77777777-7777-4777-8777-777777777777"
const ckMutation = "88888888-8888-4888-8888-888888888888"
const ckProtection = "99999999-9999-4999-8999-999999999999"

func checkpointProviderFixture(t *testing.T) (*fake.FakeDynamicClient, *KubernetesProvider, biz.Sandbox, checkpointWorkspaceIdentity, checkpointClassIdentity) {
	t.Helper()
	old, p, s := providerFixture(t)
	applyProvider(t, p, &s, biz.MutationCreateWorkspace)
	providerBoundWorkspace(t, old, s)
	s.ProviderRefs.PVName = "pv-" + s.ID
	s.ProviderRefs.PVUID = "original-pv-uid"
	pv, err := old.Resource(pvGVR).Get(context.Background(), s.ProviderRefs.PVName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedField(pv.Object, "fast", "spec", "storageClassName")
	_ = unstructured.SetNestedField(pv.Object, "test.csi", "spec", "csi", "driver")
	_ = unstructured.SetNestedField(pv.Object, "volume-handle", "spec", "csi", "volumeHandle")
	if _, err = old.Resource(pvGVR).Update(context.Background(), pv, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	read := func(g schema.GroupVersionResource, ns, name string) runtime.Object {
		o, e := old.Tracker().Get(g, ns, name)
		if e != nil {
			t.Fatal(e)
		}
		return o
	}
	c := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{checkpointSnapshotClassGVR: "VolumeSnapshotClassList"}, read(namespaceGVR, "", s.Snapshot.Namespace), read(pvcGVR, s.Snapshot.Namespace, s.ProviderRefs.PVCName), read(pvGVR, "", s.ProviderRefs.PVName))
	p.client = c
	class := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshotClass", "metadata": map[string]any{"name": "delete-class", "uid": "class-uid", "resourceVersion": "1"}, "driver": "test.csi", "deletionPolicy": "Delete"}}
	if err = c.Tracker().Add(class); err != nil {
		t.Fatal(err)
	}
	c.PrependReactor("list", "volumesnapshotclasses", func(ktesting.Action) (bool, runtime.Object, error) {
		l := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*class.DeepCopy()}}
		l.SetResourceVersion("10")
		return true, l, nil
	})
	source := checkpointWorkspaceIdentity{NamespaceUID: s.Snapshot.NamespaceUID, PVCName: s.ProviderRefs.PVCName, PVCUID: s.ProviderRefs.PVCUID, PVName: s.ProviderRefs.PVName, PVUID: s.ProviderRefs.PVUID, Driver: "test.csi", VolumeHandle: "volume-handle"}
	return c, p, s, source, checkpointClassIdentity{Name: "delete-class", UID: "class-uid", ResourceVersion: "1"}
}
func TestCheckpointSourceAndClassPreflight(t *testing.T) {
	_, p, s, want, cl := checkpointProviderFixture(t)
	got, err := p.checkpointWorkspace(context.Background(), s)
	if err != nil || got != want {
		t.Fatalf("source=%+v err=%v", got, err)
	}
	class, err := p.checkpointSelectClass(context.Background(), got.Driver)
	if err != nil || class != cl {
		t.Fatalf("class=%+v err=%v", class, err)
	}
	s.ProviderRefs.PVUID = "foreign"
	if _, err = p.checkpointWorkspace(context.Background(), s); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("foreign PV err=%v", err)
	}
}
func TestCheckpointPVCFenceAndUnknownCreate(t *testing.T) {
	c, p, s, source, class := checkpointProviderFixture(t)
	receipt, err := p.checkpointProtectPVC(context.Background(), s, source, ckProtection, true)
	if err != nil || receipt.Outcome != biz.MutationApplied {
		t.Fatalf("protect=%+v err=%v", receipt, err)
	}
	pvc, err := c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(context.Background(), source.PVCName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pvc.GetAnnotations()[checkpointPVCProtectionID] != ckProtection {
		t.Fatal("marker absent")
	}
	c.PrependReactor("create", "volumesnapshots", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, io.ErrUnexpectedEOF })
	receipt, err = p.checkpointCreateSnapshot(context.Background(), s, ckID, ckMutation, ckProtection, source, class)
	if !errors.Is(err, io.ErrUnexpectedEOF) || receipt.Outcome != biz.MutationUnknown {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}
func snapshotFixture(t *testing.T, c *fake.FakeDynamicClient, s biz.Sandbox, source checkpointWorkspaceIdentity) {
	t.Helper()
	snap := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshot", "metadata": map[string]any{"name": "sandbox-checkpoint-" + ckID, "namespace": s.Snapshot.Namespace, "uid": "snapshot-uid", "resourceVersion": "1", "labels": stringMap(labels(s)), "annotations": map[string]any{providerMutationID: ckMutation}}, "spec": map[string]any{"source": map[string]any{"persistentVolumeClaimName": source.PVCName}, "volumeSnapshotClassName": "delete-class"}, "status": map[string]any{"readyToUse": true, "boundVolumeSnapshotContentName": "content-name", "restoreSize": "5Gi"}}}
	content := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshotContent", "metadata": map[string]any{"name": "content-name", "uid": "content-uid", "resourceVersion": "1"}, "spec": map[string]any{"source": map[string]any{"volumeHandle": "volume-handle"}, "driver": "test.csi", "volumeSnapshotClassName": "delete-class", "deletionPolicy": "Delete", "volumeSnapshotRef": map[string]any{"name": "sandbox-checkpoint-" + ckID, "namespace": s.Snapshot.Namespace, "uid": "snapshot-uid"}}, "status": map[string]any{"readyToUse": true, "snapshotHandle": "underlying-snapshot-id"}}}
	if err := c.Tracker().Add(snap); err != nil {
		t.Fatal(err)
	}
	if err := c.Tracker().Add(content); err != nil {
		t.Fatal(err)
	}
}
func TestCheckpointReadyRequiresMatchingContentAndSource(t *testing.T) {
	c, p, s, source, class := checkpointProviderFixture(t)
	snapshotFixture(t, c, s, source)
	out, err := p.checkpointObserve(context.Background(), s, ckID, ckMutation, "snapshot-uid", "content-uid", source, class)
	if err != nil || out.State != "ready" || out.ContentUID != "content-uid" || !out.MinimumRestoreKnown || out.MinimumRestoreBytes != 5*1024*1024*1024 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	content, err := c.Resource(checkpointContentGVR).Get(context.Background(), "content-name", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedField(content.Object, "foreign-volume", "spec", "source", "volumeHandle")
	if _, err = c.Resource(checkpointContentGVR).Update(context.Background(), content, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.checkpointObserve(context.Background(), s, ckID, ckMutation, "snapshot-uid", "content-uid", source, class); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("foreign content err=%v", err)
	}
}
func TestCheckpointUnknownOrReplacedSnapshotNeverAdopted(t *testing.T) {
	c, p, s, source, class := checkpointProviderFixture(t)
	out, err := p.checkpointObserve(context.Background(), s, ckID, ckMutation, "", "", source, class)
	if err != nil || out.State != "absent" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	snapshotFixture(t, c, s, source)
	if _, err = p.checkpointObserve(context.Background(), s, ckID, ckMutation, "old-uid", "", source, class); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("replaced err=%v", err)
	}
	c.PrependReactor("get", "volumesnapshotcontents", func(a ktesting.Action) (bool, runtime.Object, error) {
		obj, e := c.Tracker().Get(checkpointContentGVR, "", a.(ktesting.GetAction).GetName())
		if e != nil {
			return true, nil, e
		}
		v := obj.(*unstructured.Unstructured).DeepCopy()
		v.SetUID(types.UID(fmt.Sprint("replacement")))
		return true, v, nil
	})
	if _, err = p.checkpointObserve(context.Background(), s, ckID, ckMutation, "snapshot-uid", "content-uid", source, class); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("replaced content err=%v", err)
	}
}

func TestCheckpointCreateCarriesDurableMutationIdentity(t *testing.T) {
	c, p, s, source, class := checkpointProviderFixture(t)
	if _, err := p.checkpointProtectPVC(context.Background(), s, source, ckProtection, true); err != nil {
		t.Fatal(err)
	}
	c.PrependReactor("create", "volumesnapshots", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		obj.SetUID(types.UID("snapshot-uid"))
		obj.SetResourceVersion("10")
		return false, nil, nil
	})
	got, err := p.checkpointCreateSnapshot(context.Background(), s, ckID, ckMutation, ckProtection, source, class)
	if err != nil || got.Outcome != biz.MutationApplied || got.UID != "snapshot-uid" {
		t.Fatalf("receipt=%+v err=%v", got, err)
	}
	snap, err := c.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(context.Background(), "sandbox-checkpoint-"+ckID, metav1.GetOptions{})
	if err != nil || snap.GetAnnotations()[providerMutationID] != ckMutation {
		t.Fatalf("snapshot=%v err=%v", snap, err)
	}
}

func TestCheckpointProtectionCannotBeReleasedByAnotherOperation(t *testing.T) {
	c, p, s, source, _ := checkpointProviderFixture(t)
	if _, err := p.checkpointProtectPVC(context.Background(), s, source, ckProtection, true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.checkpointProtectPVC(context.Background(), s, source, ckMutation, false); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("foreign release: %v", err)
	}
	got, err := p.checkpointProtectPVC(context.Background(), s, source, ckProtection, false)
	if err != nil || got.Outcome != biz.MutationApplied {
		t.Fatalf("release=%+v err=%v", got, err)
	}
	pvc, err := c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(context.Background(), source.PVCName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pvc.GetAnnotations()[checkpointPVCProtectionID] != "" {
		t.Fatal("stale marker")
	}
	for _, f := range pvc.GetFinalizers() {
		if f == checkpointPVCFinalizer {
			t.Fatal("stale finalizer")
		}
	}
}
func TestCheckpointCreateRejectsClassDriftBeforeWrite(t *testing.T) {
	c, p, s, source, class := checkpointProviderFixture(t)
	ctx := context.Background()
	if _, err := p.checkpointProtectPVC(ctx, s, source, ckProtection, true); err != nil {
		t.Fatal(err)
	}
	obj, err := c.Resource(checkpointSnapshotClassGVR).Get(ctx, class.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	obj.SetResourceVersion("2")
	obj.Object["deletionPolicy"] = "Retain"
	if _, err := c.Resource(checkpointSnapshotClassGVR).Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	receipt, err := p.checkpointCreateSnapshot(ctx, s, ckID, ckMutation, ckProtection, source, class)
	if !errors.Is(err, biz.ErrOwnership) || receipt.Outcome != biz.MutationNotSent {
		t.Fatalf("class drift receipt=%+v err=%v", receipt, err)
	}
	for _, action := range c.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "volumesnapshots" {
			t.Fatal("snapshot sent despite preflight class drift")
		}
	}
}

func TestCheckpointReadyRejectsPresentErrorWithoutMessage(t *testing.T) {
	for _, test := range []struct {
		name       string
		resource   schema.GroupVersionResource
		namespaced bool
		nameOf     func(biz.Sandbox) string
	}{
		{"snapshot", checkpointSnapshotGVR, true, func(_ biz.Sandbox) string { return "sandbox-checkpoint-" + ckID }},
		{"content", checkpointContentGVR, false, func(_ biz.Sandbox) string { return "content-name" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, p, s, source, class := checkpointProviderFixture(t)
			snapshotFixture(t, c, s, source)
			api := c.Resource(test.resource)
			var obj *unstructured.Unstructured
			var err error
			if test.namespaced {
				obj, err = api.Namespace(s.Snapshot.Namespace).Get(context.Background(), test.nameOf(s), metav1.GetOptions{})
			} else {
				obj, err = api.Get(context.Background(), test.nameOf(s), metav1.GetOptions{})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = unstructured.SetNestedField(obj.Object, map[string]any{}, "status", "error"); err != nil {
				t.Fatal(err)
			}
			if test.namespaced {
				_, err = api.Namespace(s.Snapshot.Namespace).Update(context.Background(), obj, metav1.UpdateOptions{})
			} else {
				_, err = api.Update(context.Background(), obj, metav1.UpdateOptions{})
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.checkpointObserve(context.Background(), s, ckID, ckMutation, "snapshot-uid", "content-uid", source, class)
			if err != nil || got.State == "ready" || got.Reason != "CSI_SNAPSHOT_LAST_ERROR" {
				t.Fatalf("empty error allowed ready: %#v %v", got, err)
			}
		})
	}
}
