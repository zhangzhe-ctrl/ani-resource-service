package vm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ktesting "k8s.io/client-go/testing"
)

// This investigation-only regression preserves the real shared-cache audit and
// ObserveState logic. It substitutes only Kubernetes LIST/Watch delivery, using
// the same callback injection as TestVMSharedStateOldListCannotOverwriteNewWatchEvent.
func TestInvestigationUnrelatedPodWatchDuringListMustNotInvalidateHealthyVM(t *testing.T) {
	ctx := context.Background()
	o, v, client, _ := sharedStateFixture(t)
	unrelated := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "unrelated-pod", "namespace": "unrelated-namespace", "uid": "unrelated-uid", "resourceVersion": "20"},
		"status":   map[string]any{"phase": "Running"},
	}}
	if _, err := client.Resource(podResource).Namespace(unrelated.GetNamespace()).Create(ctx, unrelated, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	primeSharedState(t, o, v)
	before, err := o.ObserveState(ctx, v)
	if err != nil || before.State != "Running" || !before.Running {
		t.Fatalf("invalid initial observation: %+v err=%v", before, err)
	}
	if roots := o.eventRoots(unrelated); len(roots) != 0 {
		t.Fatalf("test pod unexpectedly related to a VM: %+v", roots)
	}
	// Control: unchanged shared LIST keeps the target VM healthy.
	o.audit(ctx)
	control, err := o.ObserveState(ctx, v)
	if err != nil || control.State != "Running" {
		t.Fatalf("matching LIST control failed: %+v err=%v", control, err)
	}
	oldList, err := client.Resource(podResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	newer := unrelated.DeepCopy()
	newer.SetResourceVersion("21")
	newer.SetAnnotations(map[string]string{"investigation.ani.io/benign-update": "true"})
	first := true
	client.PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		if !first {
			return false, nil, nil
		}
		first = false
		// The LIST snapshot was taken before this newer Watch callback. No
		// target VM/VMI/launcher object is modified or removed.
		o.changed(o.resources[2], unrelated, newer)
		return true, oldList.DeepCopy(), nil
	})
	o.audit(ctx)
	after, readErr := o.ObserveState(ctx, v)
	o.mu.Lock()
	valid := []bool{o.resources[0].valid, o.resources[1].valid, o.resources[2].valid}
	o.mu.Unlock()
	t.Logf("source age=%s control=%s target_changed=false unrelated_event_roots=0 cache_valid=%v after=%s err=%v is_unknown=%v", time.Since(before.ObservedAt), control.State, valid, after.State, readErr, readErr == biz.ErrUnknown)
	if readErr != nil || after.State != "Running" || !after.Running {
		t.Errorf("unrelated Pod Watch event invalidated target VM's unexpired Running evidence: state=%q running=%v err=%v; want Running and nil", after.State, after.Running, readErr)
	}
	// Recovery requires a later shared LIST matching the newer unrelated Pod.
	if err := client.Tracker().Update(podResource, newer, newer.GetNamespace()); err != nil {
		t.Fatal(err)
	}
	o.audit(ctx)
	recovered, err := o.ObserveState(ctx, v)
	t.Logf("after consistent shared audit: state=%s err=%v", recovered.State, err)
	if err != nil || recovered.State != "Running" {
		t.Fatalf("recovery control failed: %+v err=%v", recovered, err)
	}
}

func TestVMSharedStateUnrelatedChurnStillRenewsTargetProof(t *testing.T) {
	ctx := context.Background()
	o, v, client, _ := sharedStateFixture(t)
	primeSharedState(t, o, v)
	unrelated := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod"}}
	unrelated.SetName("unrelated")
	unrelated.SetNamespace("other-tenant")
	unrelated.SetUID("unrelated-uid")
	unrelated.SetResourceVersion("opaque-0")
	o.changed(o.resources[2], nil, unrelated)
	old := unrelated
	client.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		list, err := client.Tracker().List(podResource, podResource.GroupVersion().WithKind("Pod"), "")
		if err != nil {
			t.Fatal(err)
		}
		items := list.(*unstructured.UnstructuredList)
		items.Items = append(items.Items, *old.DeepCopy())
		newer := old.DeepCopy()
		newer.SetResourceVersion(old.GetResourceVersion() + "-next")
		_ = unstructured.SetNestedField(newer.Object, "Pending", "status", "phase")
		o.changed(o.resources[2], old, newer)
		old = newer
		return true, items, nil
	})
	key := v.Spec.Namespace + "/" + v.Native.Name
	for range 20 {
		o.mu.Lock()
		proof := o.proofs[key]
		proof.verifiedAt = time.Now().Add(-61 * time.Second)
		o.proofs[key] = proof
		o.mu.Unlock()
		o.audit(ctx)
		if x, err := o.ObserveState(ctx, v); err != nil || !x.Running || time.Since(x.ObservedAt) > time.Second {
			t.Fatal("unrelated continuous churn prevented fresh target proof", x, err)
		}
	}
}

func TestVMSharedStateMetadataDoesNotRenewWatchEvidence(t *testing.T) {
	o, v, client, _ := sharedStateFixture(t)
	primeSharedState(t, o, v)
	first, err := o.ObserveState(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := client.Resource(vmiResource).Namespace(v.Spec.Namespace).Get(context.Background(), v.Native.Name, metav1.GetOptions{})
	after := before.DeepCopy()
	after.SetAnnotations(map[string]string{"unrelated": "change"})
	after.SetResourceVersion("opaque-metadata")
	o.changed(o.resources[1], before, after)
	x, err := o.ObserveState(context.Background(), v)
	if err != nil || !x.ObservedAt.Equal(first.ObservedAt) {
		t.Fatal("metadata-only event fabricated a fresh runtime fact", x, err)
	}
	// LIST still has the earlier metadata/RV, but proves identical runtime.
	o.audit(context.Background())
	if x, err := o.ObserveState(context.Background(), v); err != nil || !x.Running {
		t.Fatal("metadata-only List/Watch difference invalidated runtime", x, err)
	}
}

func TestVMSharedStateFailedAuditPreservesOnlyUnexpiredProof(t *testing.T) {
	o, v, client, _ := sharedStateFixture(t)
	primeSharedState(t, o, v)
	before, _ := o.ObserveState(context.Background(), v)
	client.PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("temporary list failure")
	})
	o.audit(context.Background())
	if x, err := o.ObserveState(context.Background(), v); err != nil || !x.Running || !x.ObservedAt.Equal(before.ObservedAt) {
		t.Fatal("failed audit discarded or renewed valid prior evidence", x, err)
	}
	key := v.Spec.Namespace + "/" + v.Native.Name
	o.mu.Lock()
	proof := o.proofs[key]
	proof.verifiedAt = time.Now().Add(-61 * time.Second)
	o.proofs[key] = proof
	o.mu.Unlock()
	// Even meaningful Watch updates cannot indefinitely extend a failed audit.
	old, _ := client.Resource(vmiResource).Namespace(v.Spec.Namespace).Get(context.Background(), v.Native.Name, metav1.GetOptions{})
	newer := old.DeepCopy()
	_ = unstructured.SetNestedField(newer.Object, "Succeeded", "status", "phase")
	newer.SetResourceVersion("later-watch")
	o.changed(o.resources[1], old, newer)
	o.audit(context.Background())
	if x, err := o.ObserveState(context.Background(), v); !errors.Is(err, biz.ErrUnknown) || x.State != "Unknown" {
		t.Fatal("Watch event or failed audit extended expired proof", x, err)
	}
}

func TestVMSharedStateTwoVMsHaveIndependentProofs(t *testing.T) {
	ctx := context.Background()
	o, v, client, _ := sharedStateFixture(t)
	other := v
	other.ID = "other-vm"
	other.Native.Name = "other-root"
	other.Native.UID = "other-root-uid"
	root, _ := client.Resource(vmResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	root.SetName(other.Native.Name)
	root.SetUID(types.UID(other.Native.UID))
	root.SetLabels(labelVM(other))
	if _, err := client.Resource(vmResource).Namespace(v.Spec.Namespace).Create(ctx, root, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	vmi := manualTerminalVMI(other, "Running")
	vmi.SetResourceVersion("other-vmi-initial")
	if _, err := client.Resource(vmiResource).Namespace(v.Spec.Namespace).Create(ctx, vmi, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	primeSharedState(t, o, v)
	oldList, err := client.Resource(vmiResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	newer := vmi.DeepCopy()
	newer.SetResourceVersion("other-newer-watch")
	_ = unstructured.SetNestedField(newer.Object, "Succeeded", "status", "phase")
	o.changed(o.resources[1], vmi, newer)
	client.PrependReactor("list", "virtualmachineinstances", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, oldList.DeepCopy(), nil
	})
	otherKey := other.Spec.Namespace + "/" + other.Native.Name
	o.mu.Lock()
	proof := o.proofs[otherKey]
	proof.verifiedAt = time.Now().Add(-61 * time.Second)
	o.proofs[otherKey] = proof
	o.mu.Unlock()
	o.audit(ctx)
	if x, err := o.ObserveState(ctx, v); err != nil || !x.Running {
		t.Fatal("another VM's stale LIST invalidated healthy target", x, err)
	}
	if x, err := o.ObserveState(ctx, other); !errors.Is(err, biz.ErrUnknown) || x.State != "Unknown" {
		t.Fatal("inconsistent target renewed its expired proof", x, err)
	}
	// Contradictory root absence is hard failure immediately, even while the
	// previously matching target proof is younger than 60 seconds.
	client.PrependReactor("list", "virtualmachines", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*root}}, nil
	})
	o.audit(ctx)
	if _, err := o.ObserveState(ctx, v); !errors.Is(err, biz.ErrUnknown) {
		t.Fatal("explicit root absence retained healthy evidence", err)
	}
}

type recoveryNotifications struct {
	stateNotifications
	batches [][]biz.ObservationRecovery
	fail    bool
}

func (n *recoveryNotifications) RecoverObservations(_ context.Context, batch []biz.ObservationRecovery) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.batches = append(n.batches, append([]biz.ObservationRecovery(nil), batch...))
	if n.fail {
		return errors.New("recovery database unavailable")
	}
	return nil
}

func TestVMSharedStateColdRecoveryIsBoundedAndRetries(t *testing.T) {
	o, v, client, _ := sharedStateFixture(t)
	notifier := &recoveryNotifications{}
	o.notifier = notifier
	root, _ := client.Resource(vmResource).Namespace(v.Spec.Namespace).Get(context.Background(), v.Native.Name, metav1.GetOptions{})
	for i := range 260 {
		copy := root.DeepCopy()
		copy.SetName(fmt.Sprintf("other-%d", i))
		copy.SetUID(types.UID(fmt.Sprintf("other-uid-%d", i)))
		if _, err := client.Resource(vmResource).Namespace(v.Spec.Namespace).Create(context.Background(), copy, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	primeSharedState(t, o, v)
	// None of these other roots was read through ObserveState. Fresh audit
	// proofs must still release pre-existing durable backoff after restart.
	notifier.mu.Lock()
	notifier.batches = nil
	notifier.fail = true
	notifier.mu.Unlock()
	o.audit(context.Background())
	o.flushRecoveries(context.Background())
	for range 100 {
		o.flushRecoveries(context.Background())
	}
	notifier.mu.Lock()
	if len(notifier.batches) != 1 || len(notifier.batches[0]) != 128 {
		t.Fatal("recovery failure became an unbounded retry or batch", notifier.batches)
	}
	notifier.batches = nil
	notifier.fail = false
	notifier.mu.Unlock()
	o.mu.Lock()
	o.recoveryRetry = time.Time{}
	o.mu.Unlock()
	for range 3 {
		o.flushRecoveries(context.Background())
	}
	notifier.mu.Lock()
	seen := map[string]bool{}
	for _, batch := range notifier.batches {
		if len(batch) > 128 {
			t.Fatal("recovery batch exceeded bound", len(batch))
		}
		for _, proof := range batch {
			if proof.Native.UID == "" || proof.ObservedAt.IsZero() {
				t.Fatal("recovery lost exact native UID or real proof time", proof)
			}
			seen[proof.Native.UID] = true
		}
	}
	notifier.mu.Unlock()
	if len(seen) != 261 {
		t.Fatal("cold shared recovery missed roots", len(seen))
	}
	o.mu.Lock()
	o.capacity = 4
	o.mu.Unlock()
	o.audit(context.Background())
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.recoveries) > 4 || !o.overflow {
		t.Fatal("recovery queue overflow was not bounded")
	}
}
