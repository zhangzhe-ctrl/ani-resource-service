package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
)

// The pre-egress v1 byte format is fixed: legacy persisted commands must
// replay after the optional intent field is introduced.
func TestLegacySandboxSpecAndCommandDigestsStayV1(t *testing.T) {
	svc := newTestService(t, validSelection())
	spec, err := svc.ResolveCreateSpec(serviceContext(), validResolveRequest())
	if err != nil {
		t.Fatal(err)
	}
	old := struct {
		Name, ImageReference, ImageDigest, PresetID, PresetCode, PresetName, ComputeType                                string
		CPUMillis, MemoryMiB                                                                                            int64
		SelectionDigest, ClusterID, Namespace, NamespaceUID, RuntimeClass, MountPath, Size, StorageClass, ReclaimPolicy string
		ContractVersion                                                                                                 uint32
		ImageID                                                                                                         string `json:"ImageID,omitempty"`
		ImageScope                                                                                                      string `json:"ImageScope,omitempty"`
	}{spec.Name, spec.ImageReference, spec.ImageDigest, spec.PresetId, spec.PresetCode, spec.PresetName, spec.ComputeType, spec.CpuMillis, spec.MemoryMib, spec.SelectionDigest, spec.ClusterId, spec.Namespace, spec.NamespaceUid, spec.RuntimeClass, spec.Workspace.MountPath, spec.Workspace.Size, spec.Workspace.StorageClass, spec.Workspace.ReclaimPolicy, spec.ContractVersion, spec.ImageId, spec.ImageScope}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append([]byte("sandbox-spec-v1\n"), raw...))
	if spec.SpecDigest != hex.EncodeToString(sum[:]) {
		t.Fatal("legacy spec digest changed")
	}
	command := &pb.CommandContext{TenantId: testTenant, ResourceId: testSandbox, OperationId: testOp}
	got, err := pb.CommandDigest(command, "", spec, "")
	if err != nil {
		t.Fatal(err)
	}
	oldCommand := struct {
		TenantID     string                        `json:"tenant_id"`
		ResourceID   string                        `json:"resource_id"`
		OperationID  string                        `json:"operation_id"`
		InstanceID   string                        `json:"instance_id"`
		Spec         *pb.ResolveCreateSpecResponse `json:"spec"`
		IntentDigest string                        `json:"create_intent_digest,omitempty"`
	}{testTenant, testSandbox, testOp, "", spec, ""}
	raw, err = json.Marshal(oldCommand)
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(append([]byte("sandbox-create-v1\n"), raw...))
	if got != hex.EncodeToString(sum[:]) {
		t.Fatal("legacy command digest changed")
	}
	data := validResolveRequest().Data
	data.EgressIntent = &pb.SandboxEgressIntent{NetworkEgressPolicy: "allowlist"}
	snapshot := &pb.SandboxSnapshot{Name: data.Name, ImageReference: data.ImageReference, PresetId: data.PresetId, Workspace: &pb.WorkspaceSpec{Size: "10Gi"}}
	if pb.MatchLegacyCreateIntent(data, snapshot) {
		t.Fatal("new egress request incorrectly replayed legacy snapshot")
	}
}
