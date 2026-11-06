package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sandboxLogReaderFunc func(context.Context, biz.Sandbox, biz.LogOptions, func(biz.LogFrame) error) error

func (f sandboxLogReaderFunc) StreamLogs(ctx context.Context, s biz.Sandbox, o biz.LogOptions, send func(biz.LogFrame) error) error {
	return f(ctx, s, o, send)
}

func TestSandboxLogSlowConsumerTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	err := sendSandboxLogFrame(context.Background(), biz.LogFrame{Kind: "DATA"}, 5*time.Millisecond, func(biz.LogFrame) error {
		<-release
		return nil
	})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("slow send: %v", err)
	}
}

func TestSandboxLogLimiterPerSandboxAndRelease(t *testing.T) {
	l := newSandboxLogLimiter()
	var releases []func()
	for i := 0; i < logSandboxConnections; i++ {
		release, err := l.acquire("tenant", "sandbox")
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := l.acquire("tenant", "sandbox"); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("limit: %v", err)
	}
	other, err := l.acquire("tenant", "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	for _, release := range releases {
		release()
		release()
	}
	if l.active != 0 || len(l.tenants) != 0 || len(l.sandboxes) != 0 {
		t.Fatalf("sessions not released: %+v", l)
	}
}

func TestSandboxLogSessionCancelsSlowSendAndRejectsCrossTenant(t *testing.T) {
	s := biz.Sandbox{TenantID: "11111111-1111-4111-8111-111111111111", ID: "22222222-2222-4222-8222-222222222222"}
	parent := biz.WithCaller(context.Background(), biz.Caller{TenantID: s.TenantID, Actor: "test"})
	ctx, cancel := context.WithCancel(parent)
	reader := sandboxLogReaderFunc(func(ctx context.Context, _ biz.Sandbox, _ biz.LogOptions, send func(biz.LogFrame) error) error {
		return send(biz.LogFrame{Kind: "DATA", Data: []byte("data")})
	})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- streamSandboxLogSession(ctx, reader, s, biz.LogOptions{TailLines: 1}, func(biz.LogFrame) error { <-release; return nil })
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	close(release)
	foreign := biz.WithCaller(context.Background(), biz.Caller{TenantID: "33333333-3333-4333-8333-333333333333", Actor: "test"})
	if err := streamSandboxLogSession(foreign, reader, s, biz.LogOptions{TailLines: 1}, func(biz.LogFrame) error { t.Fatal("must not send"); return nil }); !errors.Is(err, biz.ErrDenied) {
		t.Fatalf("foreign: %v", err)
	}
}
