package sandbox

import (
	"context"
	"testing"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type sandboxLogStream struct {
	grpc.ServerStream
	ctx    context.Context
	frames []*pb.StreamSandboxLogsResponse
}

func (s *sandboxLogStream) Context() context.Context { return s.ctx }
func (s *sandboxLogStream) Send(frame *pb.StreamSandboxLogsResponse) error {
	s.frames = append(s.frames, frame)
	return nil
}

func TestStreamSandboxLogsRequestAndTenantScope(t *testing.T) {
	svc, _, _ := environmentService()
	calls := 0
	svc.config.LogReader = sandboxLogReaderFunc(func(_ context.Context, _ biz.Sandbox, opts biz.LogOptions, send func(biz.LogFrame) error) error {
		calls++
		if opts.TailLines != 100 || opts.Since == nil || opts.PodUID != "original-pod" {
			t.Fatalf("options %+v", opts)
		}
		return send(biz.LogFrame{Kind: "READY", PodUID: opts.PodUID})
	})
	stream := &sandboxLogStream{ctx: serviceContext()}
	r := &pb.StreamSandboxLogsRequest{TenantId: testTenant, SandboxId: testSandbox, SinceTime: timestamppb.New(time.Now()), PodUid: "original-pod"}
	if err := svc.StreamSandboxLogs(r, stream); err != nil || calls != 1 || len(stream.frames) != 1 || stream.frames[0].PodUid != "original-pod" {
		t.Fatalf("frames=%+v calls=%d err=%v", stream.frames, calls, err)
	}
	r.TenantId = "66666666-6666-4666-8666-666666666666"
	if err := svc.StreamSandboxLogs(r, stream); status.Code(err) != codes.PermissionDenied || calls != 1 {
		t.Fatalf("cross tenant err=%v calls=%d", err, calls)
	}
	r.TenantId = testTenant
	r.PodUid = ""
	if err := svc.StreamSandboxLogs(r, stream); status.Code(err) != codes.InvalidArgument || calls != 1 {
		t.Fatalf("missing UID err=%v calls=%d", err, calls)
	}
	r.PodUid = "original-pod"
	r.TailLines = 1001
	if err := svc.StreamSandboxLogs(r, stream); status.Code(err) != codes.InvalidArgument || calls != 1 {
		t.Fatalf("tail limit err=%v calls=%d", err, calls)
	}
}
