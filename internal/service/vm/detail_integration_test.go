//go:build instanceintegration

package vm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/vm/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/vm"
	"github.com/zhangzhe-ctrl/ani-resource-service/tests/testenv"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

// PostgreSQL, owner persistence, service tenant checks and the native guest
// adapter are real. Only the external Kubernetes API is controlled.
func TestDetailReadsPersistedVMThroughNativeGuestAdapter(t *testing.T) {
	s, ctx, request := dnsServiceFixture(t)
	resolved, err := s.ResolveCreateSpec(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	spec, err := domainSpec(id, resolved.CreateSpec)
	if err != nil {
		t.Fatal(err)
	}
	db := testenv.NewDatabase(t)
	if err = data.Migrate(ctx, db.Owner.Config().ConnString(), db.RuntimeRole); err != nil {
		t.Fatal(err)
	}
	repo, err := data.OpenPostgres(ctx, db.RuntimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err = repo.AcceptCreate(ctx, biz.Acceptance{TenantID: request.TenantId, VMID: id, OperationID: uuid.NewString(), RequestDigest: spec.RequestDigest, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	op, v, err := repo.Claim(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v.Native = biz.ObjectRef{Kind: "VirtualMachine", Namespace: spec.Namespace, Name: "vm-" + id, UID: uuid.NewString()}
	snapshotAt := time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC)
	v.Snapshots = []biz.Snapshot{{ID: uuid.NewString(), Name: "before-upgrade", SourceVMID: id, SourceVMUID: v.Native.UID, CreatedAt: snapshotAt}}
	op.State = "succeeded"
	if err = repo.Save(ctx, op, v); err != nil {
		t.Fatal(err)
	}
	ns := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": spec.Namespace, "uid": spec.NamespaceUID, "labels": map[string]any{"network.ani.io/tenant-id": v.TenantID, "network.ani.io/managed-by": "ani-network-service"}}}}
	// The real renderer supplies the exact owner labels, avoiding a test-only
	// alternative to the production identity scheme.

	vmi := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance", "metadata": map[string]any{"name": v.Native.Name, "namespace": spec.Namespace, "uid": "vmi-detail"}, "status": map[string]any{"phase": "Running", "nodeName": "node-detail", "conditions": []any{map[string]any{"type": "AgentConnected", "status": "True"}}}}}
	controller := true
	vmi.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "kubevirt.io/v1", Kind: "VirtualMachine", Name: v.Native.Name, UID: types.UID(v.Native.UID), Controller: &controller}})
	kube := fake.NewSimpleDynamicClient(runtime.NewScheme(), ns, vmi)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hostname":"persisted-guest","supportedCommands":[{"name":"guest-get-fsinfo","enabled":false}]}`))
	}))
	defer server.Close()
	native, err := data.NewNative(kube, &rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	uncreated := v
	uncreated.Native.UID = ""
	if _, err = native.EnsureVM(ctx, uncreated); err != nil {
		t.Fatal(err)
	}
	vmResource := schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachines"}
	root, err := kube.Resource(vmResource).Namespace(spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	root.SetUID(types.UID(v.Native.UID))
	if _, err = kube.Resource(vmResource).Namespace(spec.Namespace).Update(ctx, root, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	s.repo, s.native = repo, native
	out, err := s.GetVM(ctx, &pb.GetVMRequest{TenantId: v.TenantID, VmId: id})
	if err != nil || out.Runtime.Hostname != "persisted-guest" || out.Runtime.NodeName != "node-detail" || out.CreatedAt == "" || out.FrozenSpec.Compute.PresetId != spec.PresetID {
		t.Fatalf("persisted detail chain failed: %v %+v", err, out)
	}
	snapshots, err := s.ListSnapshots(ctx, &pb.GetVMRequest{TenantId: v.TenantID, VmId: id})
	if err != nil || len(snapshots.Items) != 1 || snapshots.Items[0].Name != "before-upgrade" || snapshots.Items[0].CreatedAt != snapshotAt.Format(time.RFC3339Nano) {
		t.Fatalf("snapshot metadata lost: %v %+v", err, snapshots)
	}
	other := biz.WithCaller(context.Background(), biz.Caller{TenantID: uuid.NewString(), Actor: "user:2"})
	_, err = s.GetVM(other, &pb.GetVMRequest{TenantId: v.TenantID, VmId: id})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-tenant caller accepted: %v", err)
	}
	otherTenant := uuid.NewString()
	other = biz.WithCaller(context.Background(), biz.Caller{TenantID: otherTenant, Actor: "user:2"})
	_, err = s.GetVM(other, &pb.GetVMRequest{TenantId: otherTenant, VmId: id})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("cross-tenant repository read accepted: %v", err)
	}
}
