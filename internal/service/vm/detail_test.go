package vm

import (
	"context"
	"testing"

	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/vm/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type detailTestNative struct {
	biz.NativePort
	calls int
	err   error
}

func (n *detailTestNative) ReadRuntime(context.Context, biz.VM) (biz.Runtime, error) {
	n.calls++
	return biz.Runtime{State: "AVAILABLE", AgentState: "CONNECTED", Hostname: "guest-a"}, n.err
}

type detailTestRepo struct{ snapshotServiceRepo }

func (r *detailTestRepo) List(context.Context, string, biz.ListFilter) ([]biz.VM, int64, error) {
	return []biz.VM{r.current}, 1, nil
}

func TestDetailRuntimeIsTenantBoundAndNeverReadByList(t *testing.T) {
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
	r := &detailTestRepo{snapshotServiceRepo: snapshotServiceRepo{current: biz.VM{ID: id, TenantID: request.TenantId, Name: "VM", Spec: spec, Native: biz.ObjectRef{UID: "vm-uid"}}}}
	n := &detailTestNative{}
	s.repo, s.native = r, n
	out, err := s.GetVM(ctx, &pb.GetVMRequest{TenantId: request.TenantId, VmId: id})
	if err != nil || out.Runtime.Hostname != "guest-a" || n.calls != 1 {
		t.Fatalf("detail not wired: %v %+v", err, out)
	}
	_, err = s.ListVMs(ctx, &pb.ListVMsRequest{TenantId: request.TenantId})
	if err != nil || n.calls != 1 {
		t.Fatalf("list runtime fanout: %v calls=%d", err, n.calls)
	}
	_, err = s.GetVM(ctx, &pb.GetVMRequest{TenantId: uuid.NewString(), VmId: id})
	if status.Code(err) != codes.PermissionDenied || n.calls != 1 {
		t.Fatalf("tenant scope bypass: %v", err)
	}
	n.err = biz.ErrDependency
	out, err = s.GetVM(ctx, &pb.GetVMRequest{TenantId: request.TenantId, VmId: id})
	if err != nil || out.Runtime.State != "UNAVAILABLE" || out.Runtime.Hostname != "" || out.Id != id {
		t.Fatalf("dependency hid detail or leaked unverified guest: %v %+v", err, out)
	}
}
