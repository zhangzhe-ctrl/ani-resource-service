package sandbox

import (
	"context"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func (s *Service) ListSandboxPodHistory(ctx context.Context, r *pb.ListSandboxPodHistoryRequest) (*pb.ListSandboxPodHistoryResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	repo, ok := s.repo.(biz.PodHistoryRepository)
	if !ok {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	items, err := repo.ListSandboxPodHistory(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.ListSandboxPodHistoryResponse{Pods: make([]*pb.SandboxPodHistory, 0, len(items)), HistoryCompleteBeforeMigration: false}
	for _, item := range items {
		out.Pods = append(out.Pods, &pb.SandboxPodHistory{ClusterId: item.ClusterID, Namespace: item.Namespace, NamespaceUid: item.NamespaceUID, PodName: item.PodName, PodUid: item.PodUID, FirstRecordedAt: item.FirstRecordedAt.UTC().Format(time.RFC3339Nano)})
	}
	return out, nil
}
