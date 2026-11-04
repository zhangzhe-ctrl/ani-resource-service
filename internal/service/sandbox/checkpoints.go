package sandbox

import (
	"context"
	"time"

	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"golang.org/x/sync/errgroup"
)

// CreateSandboxCheckpoint accepts a durable asynchronous operation. The
// request key identifies retries; no provider write is made by the handler.
func (s *Service) CreateSandboxCheckpoint(ctx context.Context, r *pb.CreateSandboxCheckpointRequest) (*pb.CreateSandboxCheckpointResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) || r.ExpectedVersion < 1 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	repo, ok := s.repo.(biz.CheckpointRepository)
	if !ok {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	command := biz.CheckpointCommand{TenantID: r.TenantId, SandboxID: r.SandboxId, CheckpointID: uuid.NewString(), OperationID: uuid.NewString(), Name: r.Name, IdempotencyKey: r.IdempotencyKey, ExpectedVersion: r.ExpectedVersion}
	if _, err := biz.CheckpointRequestDigest(command); err != nil {
		return nil, rpcError(err)
	}
	c, err := repo.AcceptCheckpoint(ctx, command)
	if err != nil {
		return nil, rpcError(err)
	}
	if c.TenantID != r.TenantId || c.SandboxID != r.SandboxId || !biz.UUID(c.ID) || !biz.UUID(c.OperationID) || c.Name != r.Name {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	if c.State != "accepted" && c.State != "running" && c.State != "unknown" && c.State != "failed" && c.State != "ready" {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	return &pb.CreateSandboxCheckpointResponse{CheckpointId: c.ID, OperationId: c.OperationID, State: c.State, Replayed: c.Replayed}, nil
}

// ListSandboxCheckpoints returns an authoritative ledger page. Ready records
// are rechecked against the exact recorded provider UIDs before projection.
func (s *Service) ListSandboxCheckpoints(ctx context.Context, r *pb.ListSandboxCheckpointsRequest) (*pb.ListSandboxCheckpointsResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) || r.PageSize > 100 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	repo, ok := s.repo.(biz.CheckpointRepository)
	if !ok {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	original, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	items, total, err := repo.ListCheckpoints(ctx, r.TenantId, r.SandboxId, r.Page, r.PageSize)
	if err != nil {
		return nil, rpcError(err)
	}
	page, size := r.Page, r.PageSize
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	if total < 0 || len(items) > 100 {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	start := uint64(page-1) * uint64(size)
	expected := uint64(0)
	if start < uint64(total) {
		expected = uint64(total) - start
		if expected > uint64(size) {
			expected = uint64(size)
		}
	}
	if uint64(len(items)) != expected {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	for _, item := range items {
		if item.TenantID != r.TenantId || item.SandboxID != r.SandboxId || !biz.UUID(item.ID) || item.Name == "" || item.CreatedAt.IsZero() {
			return nil, rpcError(biz.ErrDependencyUnavailable)
		}
		if item.State != "accepted" && item.State != "running" && item.State != "unknown" && item.State != "failed" && item.State != "ready" {
			return nil, rpcError(biz.ErrDependencyUnavailable)
		}
	}
	out := &pb.ListSandboxCheckpointsResponse{Items: make([]*pb.SandboxCheckpoint, len(items)), Total: total, Source: "sandbox_checkpoint_ledger"}
	group, readCtx := errgroup.WithContext(ctx)
	group.SetLimit(8)
	for i, item := range items {
		group.Go(func() error {
			status, reason, provider := item.State, checkpointSafeReason(item.Reason), "not_observed"
			if status == "ready" {
				if s.config.CheckpointObserver == nil {
					return biz.ErrDependencyUnavailable
				}
				observation, err := s.config.CheckpointObserver.ObserveRecordedCheckpoint(readCtx, original, item)
				if err != nil {
					return err
				}
				provider = observation.State
				if provider != "ready" {
					status = "unknown"
					reason = checkpointSafeReason(observation.Reason)
				}
			}
			out.Items[i] = &pb.SandboxCheckpoint{Id: item.ID, Name: item.Name, Status: status, Reason: reason, Scope: "filesystem_crash_consistent", SizeKnown: false, CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano), ProviderStatus: provider}
			return nil
		})
	}
	if err = group.Wait(); err != nil {
		return nil, rpcError(err)
	}
	current, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	if current.ID != original.ID || current.TenantID != original.TenantID || current.Snapshot.ClusterID != original.Snapshot.ClusterID || current.Snapshot.Namespace != original.Snapshot.Namespace || current.Snapshot.NamespaceUID != original.Snapshot.NamespaceUID {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	return out, nil
}
func checkpointSafeReason(reason string) string {
	switch reason {
	case "", "SANDBOX_CHECKPOINT_UNSUPPORTED", "SANDBOX_CHECKPOINT_OWNERSHIP_CONFLICT", "SANDBOX_CHECKPOINT_OUTCOME_UNKNOWN", "SANDBOX_CHECKPOINT_PROTECTION_REJECTED", "SANDBOX_CHECKPOINT_CREATE_REJECTED", "SANDBOX_CHECKPOINT_RELEASE_UNCONFIRMED", "CSI_SNAPSHOT_LAST_ERROR", "SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED", "SANDBOX_CHECKPOINT_PROVIDER_IDENTITY_MISMATCH", "CHECKPOINT_IDENTITY_INCOMPLETE", "CHECKPOINT_PROVIDER_MISSING", "CHECKPOINT_IDENTITY_MISMATCH", "CHECKPOINT_PROVIDER_DELETING", "CHECKPOINT_PROVIDER_NOT_READY":
		return reason
	default:
		return "CHECKPOINT_REASON_UNAVAILABLE"
	}
}
