package data_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
)

func managedConsumerFor(a biz.Attachment, uid string, revision int64) biz.ConsumerSubmission {
	root := biz.WorkloadReference{APIVersion: "kubeflow.org/v1", Kind: "Notebook", Namespace: a.Namespace, Name: "notebook", UID: "notebook-uid"}
	set := biz.WorkloadReference{APIVersion: "apps/v1", Kind: "StatefulSet", Namespace: a.Namespace, Name: "notebook", UID: "set-uid"}
	c := biz.ConsumerSubmission{ProtocolVersion: 2, TenantID: a.TenantID, InstanceID: a.InstanceID, SubmissionID: a.SubmissionID, Generation: a.Generation, AttachmentID: a.ID, ClusterID: a.ClusterID, Namespace: a.Namespace, State: "open", SnapshotRevision: revision, DesiredRunState: "running", RootController: root, Controllers: []biz.ControllerReference{{Reference: set, Owner: root}}}
	if uid != "" {
		c.CurrentPods = []biz.PodReference{{Reference: biz.WorkloadReference{APIVersion: "v1", Kind: "Pod", Namespace: a.Namespace, Name: "notebook-0", UID: uid}, Owner: set}}
	}
	return c
}
func managedControllerObjects(f attachmentFixture, a biz.Attachment, c biz.ConsumerSubmission) {
	root := attachmentPod(a, c.RootController.Name, c.RootController.UID)
	root["apiVersion"] = "kubeflow.org/v1"
	root["kind"] = "Notebook"
	set := map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet", "metadata": map[string]any{"name": "notebook", "namespace": a.Namespace, "uid": "set-uid", "resourceVersion": "1", "ownerReferences": []any{map[string]any{"apiVersion": "kubeflow.org/v1", "kind": "Notebook", "name": "notebook", "uid": "notebook-uid", "controller": true}}}}
	f.api.Objects["notebooks/"+a.Namespace+"/notebook"] = root
	f.api.Objects["statefulsets/"+a.Namespace+"/notebook"] = set
}
func managedPodObject(a biz.Attachment, uid string) map[string]any {
	pod := attachmentPod(a, "notebook-0", uid)
	pod["metadata"].(map[string]any)["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "notebook", "uid": "set-uid", "controller": true}}
	pod["status"] = map[string]any{"podIP": "10.42.1.2"}
	return pod
}
func managedNetworkObjects(f attachmentFixture, a biz.Attachment, podUID string) {
	nicUID := "nic-" + podUID
	f.api.Objects["vnics/"+a.Namespace+"/nic"] = map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "VNic", "metadata": map[string]any{"namespace": a.Namespace, "name": "nic", "uid": nicUID, "resourceVersion": "1", "ownerReferences": []any{map[string]any{"apiVersion": "v1", "kind": "Pod", "name": "notebook-0", "uid": podUID}}}, "spec": map[string]any{"type": "VETH", "subnet": a.Plan.PrimaryNetworkRef}}
	f.api.Objects["vnicips/"+a.Namespace+"/current-ip"] = map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "VNicIP", "metadata": map[string]any{"namespace": a.Namespace, "name": "current-ip", "uid": "ip-" + podUID, "resourceVersion": "1", "ownerReferences": []any{map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "VNic", "name": "nic", "uid": nicUID}}}, "spec": map[string]any{"subnet": a.Plan.PrimaryNetworkRef, "vNic": "nic", "ipAddress": "10.42.1.2"}, "status": map[string]any{"vNic": "nic"}}
}
func setManagedConsumer(f attachmentFixture, c biz.ConsumerSubmission) {
	f.consumer.mu.Lock()
	defer f.consumer.mu.Unlock()
	f.consumer.err = nil
	f.consumer.value = c
}

func managedNotebookOwner(f attachmentFixture) map[string]any {
	set := f.api.Objects["statefulsets/"+f.r.Namespace+"/notebook"]
	return set["metadata"].(map[string]any)["ownerReferences"].([]any)[0].(map[string]any)
}

func attachmentProtocolBlocked(t *testing.T, f attachmentFixture, id string) bool {
	t.Helper()
	var blocked bool
	if err := f.owner.QueryRow(context.Background(), `SELECT protocol_blocked FROM network_attachments WHERE tenant_id=$1 AND attachment_id=$2`, f.r.TenantID, id).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	return blocked
}

func TestManagedAttachmentPGNotebookServedOwnerAliases(t *testing.T) {
	for _, tc := range []struct {
		name, api, kind, uid string
		controller, valid    bool
	}{
		{"v1", "kubeflow.org/v1", "Notebook", "notebook-uid", true, true},
		// Notebook 1.10 emits this served-version owner while GET returns v1.
		{"v1beta1", "kubeflow.org/v1beta1", "Notebook", "notebook-uid", true, true},
		{"unknown-version", "kubeflow.org/v9", "Notebook", "notebook-uid", true, false},
		{"wrong-group", "other.org/v1beta1", "Notebook", "notebook-uid", true, false},
		{"wrong-kind", "kubeflow.org/v1beta1", "Other", "notebook-uid", true, false},
		{"wrong-uid", "kubeflow.org/v1beta1", "Notebook", "replacement-uid", true, false},
		{"not-controller", "kubeflow.org/v1beta1", "Notebook", "notebook-uid", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAttachmentFixture(t)
			f.r.WorkloadMode = "controller_managed"
			f.r.ConsumerType = biz.ConsumerContainerInstance
			a := f.prepare(t)
			c := managedConsumerFor(a, "pod-1", 1)
			f.api.Mu.Lock()
			managedControllerObjects(f, a, c)
			owner := managedNotebookOwner(f)
			owner["apiVersion"], owner["kind"], owner["uid"], owner["controller"] = tc.api, tc.kind, tc.uid, tc.controller
			f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = managedPodObject(a, "pod-1")
			managedNetworkObjects(f, a, "pod-1")
			f.api.Mu.Unlock()
			setManagedConsumer(f, c)
			a = f.step(t, a.ID)
			if tc.valid {
				if a.State != biz.Attached || a.PodUID != "pod-1" || a.RootController != c.RootController || attachmentProtocolBlocked(t, f, a.ID) {
					t.Fatalf("served Notebook owner did not retain canonical independent UID identity: state=%s reason=%s", a.State, a.Reason)
				}
			} else if a.Reason != biz.AttachmentProtocol || !attachmentProtocolBlocked(t, f, a.ID) {
				t.Fatal("invalid controller identity was accepted", a)
			}
		})
	}
}

func TestManagedAttachmentPGProtocolRecoveryRequiresFrozenSnapshotAndConfirm(t *testing.T) {
	f := newAttachmentFixture(t)
	f.r.WorkloadMode = "controller_managed"
	f.r.ConsumerType = biz.ConsumerContainerInstance
	a := f.prepare(t)
	c := managedConsumerFor(a, "pod-1", 1)
	f.api.Mu.Lock()
	managedControllerObjects(f, a, c)
	managedNotebookOwner(f)["apiVersion"] = "kubeflow.org/v9"
	f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = managedPodObject(a, "pod-1")
	managedNetworkObjects(f, a, "pod-1")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	// Create the historic block through the real worker and PostgreSQL, never
	// UPDATE the flag or synthesize a Confirm receipt.
	a = f.step(t, a.ID)
	if a.Reason != biz.AttachmentProtocol || !attachmentProtocolBlocked(t, f, a.ID) {
		t.Fatal("prior independent provider refusal was not durable", a)
	}
	frozenHash := a.SnapshotHash
	f.api.Mu.Lock()
	managedNotebookOwner(f)["apiVersion"] = "kubeflow.org/v1beta1"
	f.api.Mu.Unlock()
	a = f.step(t, a.ID)
	if !attachmentProtocolBlocked(t, f, a.ID) || a.Reason != biz.AttachmentProtocol {
		t.Fatal("recovery without durable Confirm was accepted", a)
	}
	confirmed, err := f.a.Confirm(context.Background(), biz.ConfirmAttachment{TenantID: a.TenantID, AttachmentID: a.ID, ClusterID: a.ClusterID, Namespace: a.Namespace, PodName: "notebook-0", PodUID: "pod-1", ExpectedVersion: a.Version, ExpectedSnapshotRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	a = confirmed
	for _, tc := range []string{"older-snapshot", "same-version-mutation", "root-replacement", "missing-current-address"} {
		t.Run(tc, func(t *testing.T) {
			candidate := managedConsumerFor(a, "pod-1", 1)
			switch tc {
			case "older-snapshot":
				candidate.SnapshotRevision = 0
			case "same-version-mutation":
				candidate.DesiredRunState = "stopped"
			case "root-replacement":
				candidate.RootController.UID = "replaced-root"
				candidate.Controllers[0].Owner = candidate.RootController
			case "missing-current-address":
				f.api.Mu.Lock()
				delete(f.api.Objects, "vnicips/"+a.Namespace+"/current-ip")
				f.api.Mu.Unlock()
			}
			setManagedConsumer(f, candidate)
			a = f.step(t, a.ID)
			if !attachmentProtocolBlocked(t, f, a.ID) || a.Reason != biz.AttachmentProtocol || a.SnapshotHash != frozenHash || a.SnapshotRevision != 1 {
				t.Fatal("unproven recovery cleared the durable fence or changed the frozen snapshot", a)
			}
			f.api.Mu.Lock()
			managedNetworkObjects(f, a, "pod-1")
			f.api.Mu.Unlock()
		})
	}
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Attached || a.Reason != "" || a.PodUID != "pod-1" || a.SnapshotRevision != 1 || a.SnapshotHash != frozenHash || attachmentProtocolBlocked(t, f, a.ID) {
		t.Fatalf("durable Confirm plus unchanged snapshot and complete owner/address proof failed to recover: state=%s reason=%s", a.State, a.Reason)
	}
	// The clean result and the high water survive a fresh production read.
	a, err = f.a.Get(context.Background(), a.TenantID, a.ID)
	if err != nil || a.State != biz.Attached || a.SnapshotHash != frozenHash || a.RootController != c.RootController {
		t.Fatal("recovery was not durable", a, err)
	}
}

func TestManagedAttachmentPGProtocolRecoveryFencesConcurrentConfirmAndClosing(t *testing.T) {
	f := newAttachmentFixture(t)
	f.r.WorkloadMode = "controller_managed"
	f.r.ConsumerType = biz.ConsumerContainerInstance
	a := f.prepare(t)
	c := managedConsumerFor(a, "pod-1", 1)
	f.api.Mu.Lock()
	managedControllerObjects(f, a, c)
	managedNotebookOwner(f)["apiVersion"] = "kubeflow.org/v9"
	f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = managedPodObject(a, "pod-1")
	managedNetworkObjects(f, a, "pod-1")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if !attachmentProtocolBlocked(t, f, a.ID) {
		t.Fatal("missing prior durable fence")
	}
	f.api.Mu.Lock()
	managedNotebookOwner(f)["apiVersion"] = "kubeflow.org/v1beta1"
	f.api.Mu.Unlock()
	// A real Confirm commits after Claim and independent observation but before
	// Finish. Its version/lease change must fence the in-flight worker's result.
	provider := net05aAttachmentProviderFunc(func(ctx context.Context, work biz.AttachmentWork) (biz.AttachmentObservation, error) {
		o, err := f.provider.ObserveAttachment(ctx, work)
		if err != nil {
			return o, err
		}
		_, err = f.a.Confirm(ctx, biz.ConfirmAttachment{TenantID: a.TenantID, AttachmentID: a.ID, ClusterID: a.ClusterID, Namespace: a.Namespace, PodName: "notebook-0", PodUID: "pod-1", ExpectedVersion: work.Attachment.Version, ExpectedSnapshotRevision: 1})
		return o, err
	})
	policy := biz.DefaultWorkerPolicy()
	policy.ObserveEvery = time.Millisecond
	race, err := biz.NewAttachmentWorker(f.p, provider, f.consumer, uuid.NewString(), policy)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Millisecond)
	if _, err = race.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !attachmentProtocolBlocked(t, f, a.ID) {
		t.Fatal("stale lease cleared the fence after concurrent Confirm")
	}
	a = f.step(t, a.ID)
	if a.Reason != "" || a.State != biz.Attached || attachmentProtocolBlocked(t, f, a.ID) {
		t.Fatal("fresh worker did not consume durable Confirm and recover", a)
	}
	// Reproduce another independent refusal, then accept a closing high water.
	f.api.Mu.Lock()
	managedNotebookOwner(f)["uid"] = "wrong-root"
	f.api.Mu.Unlock()
	a = f.step(t, a.ID)
	if !attachmentProtocolBlocked(t, f, a.ID) {
		t.Fatal("wrong UID did not fence")
	}
	f.api.Mu.Lock()
	managedNotebookOwner(f)["uid"] = c.RootController.UID
	f.api.Mu.Unlock()
	closing := managedConsumerFor(a, "pod-1", 2)
	closing.State, closing.DesiredRunState, closing.FinalizationID = "closing", "deleted", uuid.NewString()
	setManagedConsumer(f, closing)
	a = f.step(t, a.ID)
	if a.State != biz.Releasing || a.SnapshotRevision != 2 || !attachmentProtocolBlocked(t, f, a.ID) {
		t.Fatal("closing was reopened during recovery", a)
	}
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Releasing || a.SnapshotRevision != 2 || !attachmentProtocolBlocked(t, f, a.ID) {
		t.Fatal("late open snapshot cleared closing fence", a)
	}
}

func TestManagedAttachmentPGStopStartRevisionAndFinalClosure(t *testing.T) {
	f := newAttachmentFixture(t)
	f.r.WorkloadMode = "controller_managed"
	f.r.ConsumerType = biz.ConsumerContainerInstance
	a := f.prepare(t)
	c := managedConsumerFor(a, "pod-1", 1)
	f.api.Mu.Lock()
	managedControllerObjects(f, a, c)
	f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = managedPodObject(a, "pod-1")
	managedNetworkObjects(f, a, "pod-1")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Attached || a.PodUID != "pod-1" || a.SnapshotRevision != 1 || a.RootController.UID != "notebook-uid" {
		t.Fatal("first verified generation did not attach", a)
	}
	old := c.CurrentPods[0]
	f.api.Mu.Lock()
	delete(f.api.Objects, "pods/"+a.Namespace+"/notebook-0")
	f.api.Mu.Unlock()
	c.SnapshotRevision = 2
	c.DesiredRunState = "stopped"
	c.CurrentPods = nil
	c.RetiredPods = []biz.PodReference{old}
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Reserved || a.PodUID != "" || a.Reason != "" || a.SnapshotRevision != 2 {
		t.Fatal("Stop retained current address or released logical attachment", a)
	}
	c.SnapshotRevision = 3
	c.DesiredRunState = "running"
	next := old
	next.Reference.UID = "pod-2"
	c.CurrentPods = []biz.PodReference{next}
	f.api.Mu.Lock()
	f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = managedPodObject(a, "pod-2")
	managedNetworkObjects(f, a, "pod-2")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Attached || a.PodUID != "pod-2" || a.SnapshotRevision != 3 {
		t.Fatal("Start permanently blocked replacement", a)
	}
	ctx := context.Background()
	confirm := biz.ConfirmAttachment{TenantID: a.TenantID, AttachmentID: a.ID, ExpectedVersion: a.Version, ExpectedSnapshotRevision: 1, ClusterID: a.ClusterID, Namespace: a.Namespace, PodName: "notebook-0", PodUID: "pod-1"}
	if _, err := f.a.Confirm(ctx, confirm); biz.ReasonOf(err) != biz.VersionConflict {
		t.Fatal("late old Confirm accepted", err)
	}
	// A delayed old snapshot cannot re-select its retired Pod.
	stale := managedConsumerFor(a, "pod-1", 1)
	setManagedConsumer(f, stale)
	a = f.step(t, a.ID)
	if a.PodUID != "pod-2" || a.SnapshotRevision != 3 || a.Reason == biz.AttachmentProtocol {
		t.Fatal("old snapshot rolled back or blocked current", a)
	}
	finalization := uuid.NewString()
	c.SnapshotRevision = 4
	c.State = "closing"
	c.DesiredRunState = "deleted"
	c.FinalizationID = finalization
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Releasing || a.FinalizationID != finalization {
		t.Fatal("closing not persisted", a)
	}
	f.api.Mu.Lock()
	delete(f.api.Objects, "pods/"+a.Namespace+"/notebook-0")
	f.api.Mu.Unlock()
	now := time.Now()
	c.SnapshotRevision = 5
	c.State = "closed"
	c.CurrentPods = nil
	c.RetiredPods = append(c.RetiredPods, next)
	c.ClosedAt = &now
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Releasing {
		t.Fatal("existing Notebook/StatefulSet permitted premature release", a)
	}
	f.api.Mu.Lock()
	delete(f.api.Objects, "notebooks/"+a.Namespace+"/notebook")
	delete(f.api.Objects, "statefulsets/"+a.Namespace+"/notebook")
	delete(f.api.Objects, "vnics/"+a.Namespace+"/nic")
	delete(f.api.Objects, "vnicips/"+a.Namespace+"/current-ip")
	f.api.Mu.Unlock()
	a = f.step(t, a.ID)
	if a.State != biz.Released || a.PodUID != "" || a.SnapshotRevision != 5 {
		t.Fatal("closed clean managed attachment did not release", a)
	}
	// Persistent revision/finalization survives a fresh repository read.
	a, err := f.a.Get(ctx, a.TenantID, a.ID)
	if err != nil || a.ConsumerSnapshot == nil || a.ConsumerSnapshot.State != "closed" || a.SnapshotRevision != 5 {
		t.Fatal("accepted snapshot not durable", a, err)
	}
}

func TestManagedAttachmentPGWaitsForUnknownCandidateAndRejectsForgedOwner(t *testing.T) {
	f := newAttachmentFixture(t)
	f.r.WorkloadMode = "controller_managed"
	f.r.ConsumerType = biz.ConsumerContainerInstance
	a := f.prepare(t)
	c := managedConsumerFor(a, "", 1)
	f.api.Mu.Lock()
	managedControllerObjects(f, a, c)
	f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = managedPodObject(a, "pod-ahead")
	managedNetworkObjects(f, a, "pod-ahead")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Reserved || a.PodUID != "" || a.Reason == biz.AttachmentProtocol {
		t.Fatal("snapshot-ahead candidate permanently blocked", a)
	}
	c = managedConsumerFor(a, "pod-ahead", 2)
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Attached || a.PodUID != "pod-ahead" {
		t.Fatal("new snapshot failed to promote candidate", a)
	}
	f.api.Mu.Lock()
	pod := managedPodObject(a, "pod-ahead")
	pod["metadata"].(map[string]any)["ownerReferences"].([]any)[0].(map[string]any)["uid"] = "forged-set"
	f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = pod
	f.api.Mu.Unlock()
	a = f.step(t, a.ID)
	if a.Reason != biz.AttachmentProtocol {
		t.Fatal("forged owner UID authorized", a)
	}
}

func TestManagedAttachmentPGHistoricalAddressAndNicCleanupFence(t *testing.T) {
	f := newAttachmentFixture(t)
	f.r.WorkloadMode = "controller_managed"
	f.r.ConsumerType = biz.ConsumerContainerInstance
	a := f.prepare(t)
	c := managedConsumerFor(a, "pod-1", 1)
	putNetwork := func(podUID, nicUID, ipName, address string) {
		pod := managedPodObject(a, podUID)
		pod["status"] = map[string]any{"podIP": address}
		f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = pod
		f.api.Objects["vnics/"+a.Namespace+"/nic"] = map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "VNic", "metadata": map[string]any{"namespace": a.Namespace, "name": "nic", "uid": nicUID, "resourceVersion": "1", "ownerReferences": []any{map[string]any{"apiVersion": "v1", "kind": "Pod", "name": "notebook-0", "uid": podUID}}}, "spec": map[string]any{"type": "VETH", "subnet": a.Plan.PrimaryNetworkRef}}
		f.api.Objects["vnicips/"+a.Namespace+"/"+ipName] = map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "VNicIP", "metadata": map[string]any{"namespace": a.Namespace, "name": ipName, "uid": ipName + "-uid", "resourceVersion": "1", "ownerReferences": []any{map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "VNic", "name": "nic", "uid": nicUID}}}, "spec": map[string]any{"subnet": a.Plan.PrimaryNetworkRef, "vNic": "nic", "ipAddress": address}, "status": map[string]any{"vNic": "nic"}}
	}
	f.api.Mu.Lock()
	managedControllerObjects(f, a, c)
	putNetwork("pod-1", "nic-1", "old-ip", "10.42.1.2")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Attached {
		t.Fatal("initial generation not attached", a)
	}
	old := c.CurrentPods[0]
	c.SnapshotRevision = 2
	c.CurrentPods = nil
	c.RetiredPods = []biz.PodReference{old}
	c.DesiredRunState = "stopped"
	f.api.Mu.Lock()
	delete(f.api.Objects, "pods/"+a.Namespace+"/notebook-0")
	delete(f.api.Objects, "vnics/"+a.Namespace+"/nic")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	c.SnapshotRevision = 3
	c.DesiredRunState = "running"
	next := old
	next.Reference.UID = "pod-2"
	c.CurrentPods = []biz.PodReference{next}
	f.api.Mu.Lock()
	putNetwork("pod-2", "nic-2", "new-ip", "10.42.1.3")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Attached || a.Reason != "" || a.PodUID != "pod-2" {
		t.Fatal("same-name new VNic conflicted with history", a)
	}
	var eligible int
	var oldEligible bool
	if err := f.owner.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE r->>'AddressEligible'='true'),coalesce(bool_or(r->>'Address'='10.42.1.2' AND r->>'AddressEligible'='true'),false) FROM network_attachments a CROSS JOIN LATERAL jsonb_array_elements(a.provider_relations) r WHERE a.tenant_id=$1 AND a.attachment_id=$2`, a.TenantID, a.ID).Scan(&eligible, &oldEligible); err != nil {
		t.Fatal(err)
	}
	if eligible != 1 || oldEligible {
		t.Fatal("historical IP retained address/LB eligibility", eligible, oldEligible)
	}
	finalization := uuid.NewString()
	now := time.Now()
	c.SnapshotRevision = 4
	c.DesiredRunState = "deleted"
	c.State = "closed"
	c.FinalizationID = finalization
	c.ClosedAt = &now
	c.CurrentPods = nil
	c.RetiredPods = append(c.RetiredPods, next)
	f.api.Mu.Lock()
	delete(f.api.Objects, "pods/"+a.Namespace+"/notebook-0")
	delete(f.api.Objects, "vnics/"+a.Namespace+"/nic")
	delete(f.api.Objects, "vnicips/"+a.Namespace+"/old-ip")
	delete(f.api.Objects, "vnicips/"+a.Namespace+"/new-ip")
	delete(f.api.Objects, "notebooks/"+a.Namespace+"/notebook")
	delete(f.api.Objects, "statefulsets/"+a.Namespace+"/notebook")
	f.api.Objects["nics/kcn-system/residual-nic"] = map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "Nic", "metadata": map[string]any{"namespace": "kcn-system", "name": "residual-nic", "uid": "nic-runtime-uid", "resourceVersion": "1", "annotations": map[string]any{"networking.kubercloud.com/pod": a.Namespace + "/notebook-0"}}, "spec": map[string]any{"vNic": a.Namespace + "/nic", "type": "VETH"}}
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Releasing || a.Reason != biz.CleanupPending {
		t.Fatal("Nic residual allowed early release", a)
	}
	f.api.Mu.Lock()
	delete(f.api.Objects, "nics/kcn-system/residual-nic")
	f.api.Mu.Unlock()
	a = f.step(t, a.ID)
	if a.State != biz.Released {
		t.Fatal("Nic absence did not finish cleanup", a)
	}
}
