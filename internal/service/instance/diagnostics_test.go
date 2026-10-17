package instance

import (
	"context"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type diagnosticRepo struct {
	cpuIntake
	view biz.Instance
}

func (r *diagnosticRepo) Get(_ context.Context, tenant, id string) (biz.Instance, error) {
	if tenant != r.view.TenantID || id != r.view.ID {
		return biz.Instance{}, biz.ErrNotFound
	}
	return r.view, nil
}

type diagnosticBoundary struct {
	cpuEnvironmentBoundary
	stream func(context.Context, func(biz.LogFrame) error) error
}

func (b diagnosticBoundary) ReadRuntime(context.Context, biz.Instance) (biz.Runtime, error) {
	return biz.Runtime{}, nil
}
func (b diagnosticBoundary) ReadEvents(context.Context, biz.Instance) (biz.Events, error) {
	return biz.Events{}, nil
}
func (b diagnosticBoundary) StreamLogs(ctx context.Context, _ biz.Instance, _ uint32, _ bool, emit func(biz.LogFrame) error) error {
	return b.stream(ctx, emit)
}

type sendingStream struct {
	grpc.ServerStream
	ctx  context.Context
	send func(*pb.InstanceLogFrame) error
}

func (s sendingStream) Context() context.Context          { return s.ctx }
func (s sendingStream) Send(f *pb.InstanceLogFrame) error { return s.send(f) }
func newDiagnosticService(t *testing.T, b diagnosticBoundary, limits biz.LogLimits) (*Service, context.Context, *pb.StreamInstanceLogsRequest) {
	t.Helper()
	const tenant = "11111111-1111-4111-8111-111111111111"
	const id = "22222222-2222-4222-8222-222222222222"
	repo := &diagnosticRepo{view: biz.Instance{TenantID: tenant, ID: id, Spec: biz.Spec{ClusterID: tenant}}}
	s, e := NewService(repo, &cpuImageBoundary{}, &b, Config{NamespacePrefix: "tenant-", ClusterID: tenant, ServiceAccount: "workload", StorageClass: "ani-block", WorkspaceSize: "1Gi", Shell: "/bin/sh", LogLimits: limits})
	if e != nil {
		t.Fatal(e)
	}
	return s, biz.WithCaller(context.Background(), biz.Caller{TenantID: tenant, Actor: "governance:user:42"}), &pb.StreamInstanceLogsRequest{TenantId: tenant, InstanceId: id, TailLines: 100, Follow: true}
}
func TestLogSlowClientReleasesConnection(t *testing.T) {
	s, ctx, r := newDiagnosticService(t, diagnosticBoundary{stream: func(_ context.Context, emit func(biz.LogFrame) error) error { return emit(biz.LogFrame{Kind: "READY"}) }}, biz.LogLimits{WriteTimeoutSeconds: 1})
	release := make(chan struct{})
	finished := make(chan struct{})
	e := s.StreamInstanceLogs(r, sendingStream{ctx: ctx, send: func(*pb.InstanceLogFrame) error { defer close(finished); <-release; return nil }})
	close(release)
	<-finished
	if status.Code(e) != codes.ResourceExhausted || s.logTotal != 0 || len(s.logTenants) != 0 || len(s.logInstances) != 0 {
		t.Fatal("slow client leaked allowance", e, s.logTotal)
	}
}
func TestLogConnectionLimitAndCancellation(t *testing.T) {
	ready := make(chan struct{})
	s, base, r := newDiagnosticService(t, diagnosticBoundary{stream: func(ctx context.Context, emit func(biz.LogFrame) error) error {
		if e := emit(biz.LogFrame{Kind: "READY"}); e != nil {
			return e
		}
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	}}, biz.LogLimits{InstanceConnections: 1})
	ctx, cancel := context.WithCancel(base)
	done := make(chan error, 1)
	go func() {
		done <- s.StreamInstanceLogs(r, sendingStream{ctx: ctx, send: func(*pb.InstanceLogFrame) error { return nil }})
	}()
	<-ready
	e := s.StreamInstanceLogs(r, sendingStream{ctx: base, send: func(*pb.InstanceLogFrame) error { return nil }})
	if status.Code(e) != codes.ResourceExhausted {
		t.Fatal("connection cap bypassed", e)
	}
	cancel()
	if e = <-done; status.Code(e) != codes.Canceled {
		t.Fatal("cancellation did not reach upstream", e)
	}
	if s.logTotal != 0 {
		t.Fatal("connection counter leaked")
	}
	foreign := biz.WithCaller(context.Background(), biz.Caller{TenantID: r.InstanceId, Actor: "governance:user:42"})
	e = s.StreamInstanceLogs(r, sendingStream{ctx: foreign})
	if status.Code(e) != codes.PermissionDenied {
		t.Fatal("foreign log request accepted", e)
	}
}
