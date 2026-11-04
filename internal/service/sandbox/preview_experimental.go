package sandbox

import (
	"context"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type previewRepository interface {
	AcceptExperimentalPreviewOpen(context.Context, biz.ExperimentalPreviewOpen, bool) (biz.ExperimentalPreviewRecord, error)
	AcceptExperimentalPreviewClose(context.Context, biz.ExperimentalPreviewClose) (biz.ExperimentalPreviewRecord, error)
	ListExperimentalPreviews(context.Context, string, string, uint32, uint32) ([]biz.ExperimentalPreviewRecord, int64, error)
	GetExperimentalPreviewOperation(context.Context, string, string, string) (biz.ExperimentalPreviewOperation, error)
}

func (s *Service) previews() (previewRepository, error) {
	r, ok := s.repo.(previewRepository)
	if !ok {
		return nil, status.Error(codes.Unavailable, "SANDBOX_PREVIEW_UNAVAILABLE")
	}
	return r, nil
}
func previewResponse(r biz.ExperimentalPreviewRecord) *pb.Preview {
	return &pb.Preview{PreviewId: r.PreviewID, SandboxId: r.SandboxID, Port: uint32(r.Port), Protocol: r.Protocol, State: r.State, Reason: r.Reason, Address: r.Address, Desired: r.Desired, ExpiresAt: timestamppb.New(r.ExpiresAt), Replayed: r.Replayed}
}
func (s *Service) OpenPreview(ctx context.Context, r *pb.OpenPreviewRequest) (*pb.OpenPreviewResponse, error) {
	if r == nil || r.Port > 65535 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if !s.config.PreviewEnabled {
		return nil, status.Error(codes.Unavailable, "SANDBOX_PREVIEW_DISABLED")
	}
	repo, err := s.previews()
	if err != nil {
		return nil, err
	}
	out, err := repo.AcceptExperimentalPreviewOpen(ctx, biz.ExperimentalPreviewOpen{TenantID: r.TenantId, SandboxID: r.SandboxId, PreviewID: r.PreviewId, OperationID: r.OperationId, IdempotencyKey: r.IdempotencyKey, ExpectedVersion: r.ExpectedVersion, Port: uint16(r.Port), Protocol: r.Protocol, TTL: time.Duration(r.TtlSeconds) * time.Second}, s.config.PreviewEnabled)
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.OpenPreviewResponse{Preview: previewResponse(out), OperationId: out.OperationID}, nil
}
func (s *Service) ClosePreview(ctx context.Context, r *pb.ClosePreviewRequest) (*pb.ClosePreviewResponse, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	repo, err := s.previews()
	if err != nil {
		return nil, err
	}
	out, err := repo.AcceptExperimentalPreviewClose(ctx, biz.ExperimentalPreviewClose{TenantID: r.TenantId, SandboxID: r.SandboxId, PreviewID: r.PreviewId, OperationID: r.OperationId, IdempotencyKey: r.IdempotencyKey, ExpectedVersion: r.ExpectedVersion})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.ClosePreviewResponse{Preview: previewResponse(out), OperationId: out.OperationID}, nil
}
func (s *Service) ListPreviews(ctx context.Context, r *pb.ListPreviewsRequest) (*pb.ListPreviewsResponse, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	repo, err := s.previews()
	if err != nil {
		return nil, err
	}
	items, total, err := repo.ListExperimentalPreviews(ctx, r.TenantId, r.SandboxId, r.Page, r.PageSize)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.ListPreviewsResponse{Total: total}
	for _, item := range items {
		out.Items = append(out.Items, previewResponse(item))
	}
	return out, nil
}
func (s *Service) GetPreviewOperation(ctx context.Context, r *pb.GetPreviewOperationRequest) (*pb.GetPreviewOperationResponse, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	repo, err := s.previews()
	if err != nil {
		return nil, err
	}
	op, err := repo.GetExperimentalPreviewOperation(ctx, r.TenantId, r.SandboxId, r.OperationId)
	if err != nil {
		return nil, rpcError(err)
	}
	out := &pb.GetPreviewOperationResponse{OperationId: op.OperationID, PreviewId: op.PreviewID, Action: op.Action, State: op.State, Reason: op.Reason, CreatedAt: timestamppb.New(op.CreatedAt)}
	if op.CompletedAt != nil {
		out.CompletedAt = timestamppb.New(*op.CompletedAt)
	}
	return out, nil
}
