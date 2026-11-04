package data_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
)

type vmNADBoundary struct {
	calls       int
	unavailable bool
}

func (n *vmNADBoundary) ResolveVMNetwork(_ context.Context, s biz.VMNetworkSelection, e biz.VMNetworkExpectation) (biz.VMPrimaryPlan, error) {
	n.calls++
	if n.unavailable {
		return biz.VMPrimaryPlan{}, fmt.Errorf("NAD is unavailable")
	}
	return biz.VMPrimaryPlan{FormatVersion: 1, Namespace: s.Namespace, NADName: e.NADName, NADUID: e.NADUID, NADConfigDigest: e.NADConfigDigest, KCNSubnetRef: s.KCNSubnetRef}, nil
}

type vmNetworkBoundary struct{ value biz.AttachmentObservation }

func (p *vmNetworkBoundary) ObserveAttachment(_ context.Context, w biz.AttachmentWork) (biz.AttachmentObservation, error) {
	o := p.value
	o.Proof = biz.ObservationProof{CollectedAt: w.Now, CoveredGeneration: w.Requirement.RequestedGeneration}
	return o, nil
}

func vmConsumerFor(a biz.Attachment) biz.ConsumerSubmission {
	root := biz.WorkloadReference{APIVersion: "kubevirt.io/v1", Kind: "VirtualMachine", Namespace: a.Namespace, Name: "vm", UID: "vm-uid"}
	vmi := biz.WorkloadReference{APIVersion: "kubevirt.io/v1", Kind: "VirtualMachineInstance", Namespace: a.Namespace, Name: "vm", UID: "vmi-uid"}
	return biz.ConsumerSubmission{ProtocolVersion: 3, SnapshotRevision: 1, DesiredRunState: "running", TenantID: a.TenantID, InstanceID: a.InstanceID, SubmissionID: a.SubmissionID, Generation: a.Generation, AttachmentID: a.ID, ClusterID: a.ClusterID, Namespace: a.Namespace, State: "open", RootController: root, Controllers: []biz.ControllerReference{{Reference: vmi, Owner: root}}, CurrentPods: []biz.PodReference{{Reference: biz.WorkloadReference{APIVersion: "v1", Kind: "Pod", Namespace: a.Namespace, Name: "launcher", UID: "launcher-uid"}, Owner: vmi}}}
}

func TestVMAttachmentPGReplayStopAndRelease(t *testing.T) {
	f := newAttachmentFixture(t)
	f.r.WorkloadMode = "vm_managed"
	f.r.ConsumerType = biz.ConsumerVirtualMachine
	f.r.VMExpectation = &biz.VMNetworkExpectation{NADName: "main", NADUID: "nad-uid", NADConfigDigest: strings.Repeat("a", 64)}
	nad := &vmNADBoundary{}
	if err := f.p.SetVMNetworkResolver(nad, time.Minute); err != nil {
		t.Fatal(err)
	}
	a := f.prepare(t)
	if a.Plan != nil || a.VMPrimary == nil || a.VMPrimary.NADUID != "nad-uid" || a.VMPrimary.KCNSubnetRef == a.VMPrimary.NADName {
		t.Fatal("VM did not receive its separate frozen primary plan", a)
	}
	nad.unavailable = true
	replay, err := f.a.Prepare(context.Background(), f.r)
	if err != nil || replay.ID != a.ID || nad.calls != 1 {
		t.Fatal("replay re-resolved a changed NAD", err, nad.calls)
	}
	changed := f.r
	changed.InstanceID = "second-vm"
	changed.RequestKey = "new-request"
	if _, err = f.a.Prepare(context.Background(), changed); err == nil {
		t.Fatal("new VM accepted without its network dependency")
	}
	c := vmConsumerFor(a)
	setManagedConsumer(f, c)
	provider := &vmNetworkBoundary{value: biz.AttachmentObservation{Exists: true, VerifiedCurrent: true, PodName: "launcher", PodUID: "launcher-uid", Relations: []byte(`[{"Kind":"VNicIP","Address":"10.42.1.2","Current":true,"AddressEligible":true}]`)}}
	policy := biz.DefaultWorkerPolicy()
	policy.ObserveEvery = 5 * time.Millisecond
	policy.RetryMin = time.Millisecond
	f.worker, err = biz.NewAttachmentWorker(f.p, provider, f.consumer, uuid.NewString(), policy)
	if err != nil {
		t.Fatal(err)
	}
	a = f.step(t, a.ID)
	if a.State != biz.Attached || a.SnapshotRevision != 1 || a.RootController != c.RootController || len(a.VMCurrentAddresses) != 1 || a.VMCurrentAddresses[0].VMIUID != "vmi-uid" {
		t.Fatal("VM PG snapshot/address projection", a)
	}
	old := c.CurrentPods[0]
	c.SnapshotRevision++
	c.DesiredRunState = "stopped"
	c.RetiredPods = []biz.PodReference{old}
	c.CurrentPods = nil
	setManagedConsumer(f, c)
	// The native KC provider always serializes an array, including no live
	// relations. Keep this external boundary faithful to the persisted contract.
	provider.value = biz.AttachmentObservation{Relations: []byte("[]")}
	a = f.step(t, a.ID)
	if a.State != biz.Reserved || a.PodUID != "" || len(a.VMCurrentAddresses) != 0 || a.ID != replay.ID {
		t.Fatal("Stop released Attachment or kept an old address", a)
	}
	closed := time.Now()
	c.SnapshotRevision++
	c.State = "closed"
	c.DesiredRunState = "deleted"
	c.FinalizationID = uuid.NewString()
	c.ClosedAt = &closed
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Released || a.FinalizationID != c.FinalizationID {
		t.Fatal("closed VM could not release independently", a)
	}
}
