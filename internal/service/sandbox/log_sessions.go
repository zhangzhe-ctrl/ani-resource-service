package sandbox

import (
	"context"
	"sync"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	logProcessConnections = 100
	logTenantConnections  = 10
	logSandboxConnections = 3
	logSendTimeout        = 10 * time.Second
	logSessionLifetime    = 10 * time.Minute
	logFrameBytes         = 16 * 1024
)

type sandboxLogLimiter struct {
	mu                 sync.Mutex
	active             int
	tenants, sandboxes map[string]int
}

func newSandboxLogLimiter() *sandboxLogLimiter {
	return &sandboxLogLimiter{tenants: map[string]int{}, sandboxes: map[string]int{}}
}

// Process-local limits protect the service even when callers bypass Gov.
var sandboxLogSessions = newSandboxLogLimiter()

func (l *sandboxLogLimiter) acquire(tenant, id string) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := tenant + "/" + id
	if l.active >= logProcessConnections || l.tenants[tenant] >= logTenantConnections || l.sandboxes[key] >= logSandboxConnections {
		return nil, status.Error(codes.ResourceExhausted, "SANDBOX_LOG_CONNECTION_LIMIT")
	}
	l.active++
	l.tenants[tenant]++
	l.sandboxes[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.active--
			l.tenants[tenant]--
			l.sandboxes[key]--
			if l.tenants[tenant] == 0 {
				delete(l.tenants, tenant)
			}
			if l.sandboxes[key] == 0 {
				delete(l.sandboxes, key)
			}
		})
	}, nil
}

// streamSandboxLogSession is called only after the Resource transport has
// authenticated the delegated caller and loaded the tenant-scoped Sandbox.
// One send can be pending at a time; on timeout the RPC must return so gRPC
// cancels the pending Send. A bounded slow consumer cannot hold the session.
func streamSandboxLogSession(parent context.Context, reader biz.LogReader, s biz.Sandbox, opts biz.LogOptions, send func(biz.LogFrame) error) error {
	if reader == nil || send == nil {
		return biz.ErrInvalid
	}
	if err := biz.RequireTenant(parent, s.TenantID); err != nil {
		return err
	}
	release, err := sandboxLogSessions.acquire(s.TenantID, s.ID)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(parent, logSessionLifetime)
	defer cancel()
	return reader.StreamLogs(ctx, s, opts, func(frame biz.LogFrame) error {
		if len(frame.Data) > logFrameBytes {
			return biz.ErrInvalid
		}
		return sendSandboxLogFrame(ctx, frame, logSendTimeout, send)
	})
}

func sendSandboxLogFrame(ctx context.Context, frame biz.LogFrame, timeout time.Duration, send func(biz.LogFrame) error) error {
	done := make(chan error, 1)
	go func() { done <- send(frame) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return status.Error(codes.DeadlineExceeded, "SANDBOX_LOG_SLOW_CONSUMER")
	}
}
