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

type podHistoryServiceRepo struct{ serviceRepo }

func (r *podHistoryServiceRepo) ListSandboxPodHistory(_ context.Context, tenant, sandbox string) ([]biz.PodHistory, error) {
	if tenant != testTenant || sandbox != testSandbox {
		return nil, biz.ErrDenied
	}
	return []biz.PodHistory{{ClusterID: "cluster", Namespace: "ns", NamespaceUID: "ns-uid", PodName: "pod", PodUID: "pod-uid", FirstRecordedAt: time.Unix(1000, 0)}}, nil
}

func TestPodHistoryAuthorizesTenantAndProjectsRecordedFacts(t *testing.T) {
	s := newTestService(t, validSelection())
	s.repo = &podHistoryServiceRepo{}
	out, err := s.ListSandboxPodHistory(serviceContext(), &pb.ListSandboxPodHistoryRequest{TenantId: testTenant, SandboxId: testSandbox})
	if err != nil || len(out.Pods) != 1 || out.Pods[0].PodUid != "pod-uid" || out.Pods[0].FirstRecordedAt != "1970-01-01T00:16:40Z" || out.HistoryCompleteBeforeMigration {
		t.Fatalf("bad result: %+v %v", out, err)
	}
	_, err = s.ListSandboxPodHistory(serviceContext(), &pb.ListSandboxPodHistoryRequest{TenantId: "99999999-9999-4999-8999-999999999999", SandboxId: testSandbox})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign tenant: %v", err)
	}
	_, err = s.ListSandboxPodHistory(serviceContext(), &pb.ListSandboxPodHistoryRequest{TenantId: testTenant, SandboxId: "invalid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid sandbox: %v", err)
	}
}
