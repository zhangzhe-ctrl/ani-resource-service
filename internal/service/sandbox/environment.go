package sandbox

import (
	"context"
	"sort"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Service) ListEnvironmentVariables(ctx context.Context, r *pb.ListEnvironmentVariablesRequest) (*pb.ListEnvironmentVariablesResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) || r.PageSize > 100 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	if s.config.EnvironmentReader == nil {
		return nil, status.Error(codes.Unavailable, "SANDBOX_ENVIRONMENT_UNAVAILABLE")
	}
	sandbox, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	items, err := s.config.EnvironmentReader.ReadEnvironment(ctx, sandbox)
	if err != nil {
		return nil, rpcError(err)
	}
	// Detect lifecycle changes recorded while the Pod was observed. This is a
	// point-in-time observation, not an atomic lock over Kubernetes and DB.
	current, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	if current.ID != sandbox.ID || current.TenantID != sandbox.TenantID || current.Version != sandbox.Version || current.Desired != biz.DesiredRunning || current.Observed != biz.ObservedRunning || current.Pending != nil || current.ProviderRefs.PodUID != sandbox.ProviderRefs.PodUID || current.ProviderRefs.PodName != sandbox.ProviderRefs.PodName || current.Snapshot.ClusterID != sandbox.Snapshot.ClusterID || current.Snapshot.Namespace != sandbox.Snapshot.Namespace || current.Snapshot.NamespaceUID != sandbox.Snapshot.NamespaceUID {
		return nil, rpcError(biz.ErrDataPlaneUnavailable)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	page, size := r.Page, r.PageSize
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	start := uint64(page-1) * uint64(size)
	out := &pb.ListEnvironmentVariablesResponse{Total: int64(len(items))}
	if start >= uint64(len(items)) {
		return out, nil
	}
	end := start + uint64(size)
	if end > uint64(len(items)) {
		end = uint64(len(items))
	}
	for _, item := range items[start:end] {
		out.Items = append(out.Items, &pb.SandboxEnvironmentVariable{Name: item.Name, Source: item.Source, SecretRef: item.SecretRef, FieldRef: item.FieldRef})
	}
	return out, nil
}
