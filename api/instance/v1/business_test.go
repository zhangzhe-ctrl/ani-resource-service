package instancev1

import (
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func currentBusiness() *CreateSpec {
	return &CreateSpec{WorkspaceMountPath: "/workspace", Name: "workspace-a", ImageId: "img_" + strings.Repeat("b", 32), ImageScope: "platform", PresetId: "20000000-0000-4000-8000-000000000001", InstanceKind: "container", ComputeType: "cpu", ImageReference: "registry.invalid/work@sha256:" + strings.Repeat("a", 64), Cpu: "1000m", Memory: "2048Mi", ClusterId: "11111111-1111-4111-8111-111111111111", Namespace: "tenant-a", ServiceAccount: "workload", StorageClass: "ani-block", WorkspaceSize: "1Gi", Shell: "/bin/sh", Uid: 1000, Gid: 1000, Scheduling: &SchedulingOptions{PodPlacement: SchedulingOptions_NONE.Enum()}, PresetName: "CPU", PresetCode: "cpu-small"}
}
func TestCommandIdentityAndBusinessDigest(t *testing.T) {
	b := currentBusiness()
	c := &CommandContext{TenantId: "tenant", ResourceId: "resource", OperationId: "operation"}
	want, e := CommandDigest(c, b)
	if e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*CreateSpec){"cpu": func(s *CreateSpec) { s.Cpu = "2" }, "image": func(s *CreateSpec) { s.ImageReference = "changed" }, "placement": func(s *CreateSpec) { s.Scheduling.PodPlacement = SchedulingOptions_SPREAD_PREFERRED.Enum() }, "display": func(s *CreateSpec) { s.PresetName = "changed" }} {
		t.Run(name, func(t *testing.T) {
			v := proto.Clone(b).(*CreateSpec)
			change(v)
			got, e := CommandDigest(c, v)
			if e != nil || got == want {
				t.Fatal("changed command lost conflict binding", e)
			}
		})
	}
	other := proto.Clone(c).(*CommandContext)
	other.OperationId = "other"
	got, _ := CommandDigest(other, b)
	if got == want {
		t.Fatal("operation not bound")
	}
	b.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01})
	if _, e = CommandDigest(c, b); e == nil {
		t.Fatal("unknown command fields accepted")
	}
}
func TestOldTransportRejected(t *testing.T) {
	for _, raw := range []string{`{"business":{}}`, `{"gpu_owner_attachment":{}}`, `{"original_charges":[]}`} {
		if _, e := DecodeBusinessPayload([]byte(raw)); e == nil {
			t.Fatal("old transport accepted")
		}
	}
}
func TestSchedulingPresenceAndConflict(t *testing.T) {
	raw := &SchedulingOptions{PodPlacement: SchedulingOptions_SPREAD_PREFERRED.Enum()}
	effective, e := ResolveSchedulingOptions(raw, nil)
	if e != nil || effective.GetPodPlacement() != SchedulingOptions_SPREAD_PREFERRED {
		t.Fatal(e)
	}
	if e = ValidateSchedulingConsistency(raw, &SchedulingOptions{PodPlacement: SchedulingOptions_NONE.Enum()}); e == nil {
		t.Fatal("conflicting frozen options accepted")
	}
	if e = ValidateSchedulingOptions(&SchedulingOptions{}, true); e == nil {
		t.Fatal("missing placement accepted")
	}
}
