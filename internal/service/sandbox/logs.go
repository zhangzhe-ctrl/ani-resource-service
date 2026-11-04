package sandbox

import (
	"errors"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Service) StreamSandboxLogs(r *pb.StreamSandboxLogsRequest, stream grpc.ServerStreamingServer[pb.StreamSandboxLogsResponse]) error {
	if r == nil || stream == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) || r.TailLines > 1000 || len(r.PodUid) > 128 ||
		(r.SinceTime == nil) != (r.PodUid == "") {
		return rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(stream.Context(), r.TenantId); err != nil {
		return rpcError(err)
	}
	if s.config.LogReader == nil {
		return status.Error(codes.Unavailable, "SANDBOX_LOGS_UNAVAILABLE")
	}
	opts := biz.LogOptions{TailLines: r.TailLines, Follow: r.Follow, PodUID: r.PodUid}
	if opts.TailLines == 0 {
		opts.TailLines = 100
	}
	if r.SinceTime != nil {
		if r.SinceTime.CheckValid() != nil || hasUnknown(r.SinceTime.ProtoReflect()) {
			return rpcError(biz.ErrInvalid)
		}
		since := r.SinceTime.AsTime()
		if since.IsZero() {
			return rpcError(biz.ErrInvalid)
		}
		opts.Since = &since
	}
	sandbox, err := s.repo.Get(stream.Context(), r.TenantId, r.SandboxId)
	if err != nil {
		return rpcError(err)
	}
	err = streamSandboxLogSession(stream.Context(), s.config.LogReader, sandbox, opts, func(frame biz.LogFrame) error {
		return stream.Send(&pb.StreamSandboxLogsResponse{Kind: frame.Kind, Data: frame.Data, Reason: frame.Reason, PodUid: frame.PodUID})
	})
	if err == nil {
		return nil
	}
	if code := status.Code(err); code == codes.ResourceExhausted {
		return status.Error(code, "SANDBOX_LOG_CONNECTION_LIMIT")
	} else if code == codes.DeadlineExceeded && !errors.Is(err, biz.ErrDataPlaneUnavailable) {
		return status.Error(code, "SANDBOX_LOG_TIMEOUT")
	}
	return rpcError(err)
}
