package sandbox

import (
	"context"
	"reflect"
	"sort"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Service) ListSandboxEvents(ctx context.Context, r *pb.ListSandboxEventsRequest) (*pb.ListSandboxEventsResponse, error) {
	return s.listSandboxEvents(ctx, r)
}

// ListSandboxSecurityEvents fails closed until a trustworthy, sandbox-scoped
// security event source is configured. A Kubernetes Warning is not evidence
// of a security event, and an empty warning list is not a security audit.
func (s *Service) ListSandboxSecurityEvents(ctx context.Context, r *pb.ListSandboxSecurityEventsRequest) (*pb.ListSandboxSecurityEventsResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) || r.PageSize > 100 || (r.Severity != "" && r.Severity != "info" && r.Severity != "warning" && r.Severity != "critical") {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	if _, err := s.repo.Get(ctx, r.TenantId, r.SandboxId); err != nil {
		return nil, rpcError(err)
	}
	return nil, status.Error(codes.Unavailable, "SANDBOX_SECURITY_EVENT_SOURCE_UNAVAILABLE")
}

func (s *Service) listSandboxEvents(ctx context.Context, r *pb.ListSandboxEventsRequest) (*pb.ListSandboxEventsResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) || r.PageSize > 100 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	if s.config.EventReader == nil {
		return nil, status.Error(codes.Unavailable, "SANDBOX_EVENTS_UNAVAILABLE")
	}
	sandbox, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	historyRepo, ok := s.repo.(biz.PodHistoryRepository)
	if !ok {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	history, err := historyRepo.ListSandboxPodHistory(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	if len(history) == 0 {
		return nil, rpcError(biz.ErrDataPlaneUnavailable)
	}
	items, err := s.config.EventReader.ReadEvents(ctx, sandbox, history)
	if err != nil {
		return nil, rpcError(err)
	}
	current, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	if current.ID != sandbox.ID || current.TenantID != sandbox.TenantID || current.Version != sandbox.Version || current.ProviderRefs.PodUID != sandbox.ProviderRefs.PodUID || current.ProviderRefs.PodName != sandbox.ProviderRefs.PodName || current.Snapshot.ClusterID != sandbox.Snapshot.ClusterID || current.Snapshot.Namespace != sandbox.Snapshot.Namespace || current.Snapshot.NamespaceUID != sandbox.Snapshot.NamespaceUID {
		return nil, rpcError(biz.ErrDataPlaneUnavailable)
	}
	currentHistory, err := historyRepo.ListSandboxPodHistory(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	if !reflect.DeepEqual(history, currentHistory) {
		return nil, rpcError(biz.ErrDataPlaneUnavailable)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
	page, size := r.Page, r.PageSize
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	start := uint64(page-1) * uint64(size)
	out := &pb.ListSandboxEventsResponse{Total: int64(len(items)), Scope: "retained_events_for_recorded_pods", HistoryCompleteBeforeMigration: false}
	if start >= uint64(len(items)) {
		return out, nil
	}
	end := start + uint64(size)
	if end > uint64(len(items)) {
		end = uint64(len(items))
	}
	for _, item := range items[start:end] {
		out.Items = append(out.Items, &pb.SandboxEvent{Id: item.ID, OccurredAt: item.OccurredAt.UTC().Format(time.RFC3339Nano), Type: item.Type, Reason: item.Reason, Message: item.Message})
	}
	return out, nil
}
