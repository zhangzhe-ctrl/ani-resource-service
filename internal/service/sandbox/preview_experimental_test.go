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

type testPreviewRepo struct {
	biz.Repository
	calls int
	input biz.ExperimentalPreviewOpen
}

func (r *testPreviewRepo) AcceptExperimentalPreviewOpen(_ context.Context, c biz.ExperimentalPreviewOpen, enabled bool) (biz.ExperimentalPreviewRecord, error) {
	r.calls++
	r.input = c
	return biz.ExperimentalPreviewRecord{PreviewID: c.PreviewID, SandboxID: c.SandboxID, OperationID: "original-operation", Port: c.Port, State: "queued", ExpiresAt: time.Now()}, nil
}
func (r *testPreviewRepo) AcceptExperimentalPreviewClose(context.Context, biz.ExperimentalPreviewClose) (biz.ExperimentalPreviewRecord, error) {
	return biz.ExperimentalPreviewRecord{}, nil
}
func (r *testPreviewRepo) ListExperimentalPreviews(context.Context, string, string, uint32, uint32) ([]biz.ExperimentalPreviewRecord, int64, error) {
	return nil, 0, nil
}
func (r *testPreviewRepo) GetExperimentalPreviewOperation(context.Context, string, string, string) (biz.ExperimentalPreviewOperation, error) {
	return biz.ExperimentalPreviewOperation{}, nil
}
func TestPreviewRPCDefaultDisabledAndStableOperation(t *testing.T) {
	repo := &testPreviewRepo{}
	s := &Service{repo: repo}
	req := &pb.OpenPreviewRequest{TenantId: "tenant", SandboxId: "sandbox", PreviewId: "preview", OperationId: "new", Port: 8080, TtlSeconds: 60}
	if _, err := s.OpenPreview(context.Background(), req); status.Code(err) != codes.Unavailable || repo.calls != 0 {
		t.Fatalf("disabled: %v calls=%d", err, repo.calls)
	}
	s.config.PreviewEnabled = true
	out, err := s.OpenPreview(context.Background(), req)
	if err != nil || out.OperationId != "original-operation" || out.Preview.State != "queued" || repo.input.TTL != time.Minute {
		t.Fatalf("response=%+v err=%v", out, err)
	}
	req.Port = 65536
	if _, err = s.OpenPreview(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("overflow: %v", err)
	}
}
