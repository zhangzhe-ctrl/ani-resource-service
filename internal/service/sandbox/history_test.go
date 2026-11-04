package sandbox

import (
	"context"
	"testing"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type historyServiceRepo struct {
	serviceRepo
	tenant, sandbox string
	page, size      uint32
}

func (r *historyServiceRepo) ListOperations(_ context.Context, tenant, sandbox string, page, size uint32) ([]biz.Operation, int64, error) {
	r.tenant, r.sandbox, r.page, r.size = tenant, sandbox, page, size
	completed := time.Unix(1010, 0).UTC()
	return []biz.Operation{{ID: testOp, SandboxID: sandbox, Action: "code_run", State: "succeeded", CreatedAt: time.Unix(1000, 0), CompletedAt: &completed}}, 7, nil
}

func TestListOperationsChecksTenantAndForwardsPagination(t *testing.T) {
	s := newTestService(t, validSelection())
	r := new(historyServiceRepo)
	s.repo = r
	response, err := s.ListOperations(serviceContext(), &pb.ListOperationsRequest{TenantId: testTenant, SandboxId: testSandbox, Page: 2, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	if r.tenant != testTenant || r.sandbox != testSandbox || r.page != 2 || r.size != 3 || response.Total != 7 || len(response.Items) != 1 || response.Items[0].CompletedAt.AsTime().Unix() != 1010 {
		t.Fatalf("incorrect history response: repo=%+v response=%+v", r, response)
	}
	_, err = s.ListOperations(serviceContext(), &pb.ListOperationsRequest{TenantId: "99999999-9999-4999-8999-999999999999", SandboxId: testSandbox})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign tenant: %v", err)
	}
	_, err = s.ListOperations(serviceContext(), &pb.ListOperationsRequest{TenantId: testTenant, SandboxId: "invalid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid sandbox: %v", err)
	}
}

func TestCodeRunResponsePreservesKnownTimes(t *testing.T) {
	created := time.Unix(1000, 0).UTC()
	completed := created.Add(time.Second)
	got := codeRunResponse(biz.CodeRunIntent{CreatedAt: created, CompletedAt: &completed})
	if got.CreatedAt.AsTime() != created || got.CompletedAt.AsTime() != completed {
		t.Fatalf("times: %+v", got)
	}
	unknown := codeRunResponse(biz.CodeRunIntent{CreatedAt: created, State: "unknown"})
	if unknown.CompletedAt != nil {
		t.Fatal("unknown outcome was given a completion time")
	}
}
