package main

import (
	"context"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v3/transport"
	terminalpb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/terminal/v1"
	sandboxpb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sandboxRetry keeps Sandbox registered on the shared listener while its
// database schema or other Sandbox-specific dependencies are unavailable.
// A successfully opened target is published once; its normal durable worker
// owns all subsequent retries and existing unknown mutation barriers.
type sandboxRetry struct {
	sandboxpb.UnimplementedSandboxServiceServer
	terminalpb.UnimplementedSandboxTerminalServiceServer
	open     func(context.Context) (*sandboxRetryTarget, error)
	interval time.Duration

	stateMu sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}

	targetMu      sync.RWMutex
	target        *sandboxRetryTarget
	close         sync.Once
	streamsMu     sync.Mutex
	streamsClosed bool
	streams       map[*sandboxRetryStream]context.CancelFunc
}

type sandboxRetryStream struct {
	grpc.ServerStreamingServer[sandboxpb.StreamSandboxLogsResponse]
	ctx context.Context
}

func (s *sandboxRetryStream) Context() context.Context { return s.ctx }

// Stop cancels active streams before waiting for their handlers to release the
// target read lock. Reject later streams so the target can be closed promptly.
func (r *sandboxRetry) stopStreams() {
	r.streamsMu.Lock()
	defer r.streamsMu.Unlock()
	r.streamsClosed = true
	for _, cancel := range r.streams {
		cancel()
	}
}

type sandboxRetryTarget struct {
	service   sandboxpb.SandboxServiceServer
	terminal  terminalpb.SandboxTerminalServiceServer
	execution transport.Server
	close     func()
}

func newSandboxRetry(open func(context.Context) (*sandboxRetryTarget, error), interval time.Duration) *sandboxRetry {
	return &sandboxRetry{open: open, interval: interval, done: make(chan struct{})}
}

// Start is called by Kratos alongside its other transport servers. It must not
// fail the application when Sandbox alone cannot yet be assembled.
func (r *sandboxRetry) Start(parent context.Context) error {
	r.stateMu.Lock()
	if r.started || r.stopped {
		r.stateMu.Unlock()
		return nil
	}
	r.started = true
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.stateMu.Unlock()
	defer close(r.done)
	defer cancel()

	timer := time.NewTimer(r.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		target, err := r.open(ctx)
		if ctx.Err() != nil {
			if target != nil && target.close != nil {
				target.close()
			}
			return nil
		}
		if err != nil || target == nil || target.service == nil || target.execution == nil {
			if target != nil && target.close != nil {
				target.close()
			}
			timer.Reset(r.interval)
			continue
		}
		r.targetMu.Lock()
		r.target = target
		r.targetMu.Unlock()
		// The worker server stops on this same context. No new migration or
		// external mutation is performed by this composition loop.
		return target.execution.Start(ctx)
	}
}

func (r *sandboxRetry) Stop(ctx context.Context) error {
	r.stopStreams()
	r.stateMu.Lock()
	r.stopped = true
	started, cancel := r.started, r.cancel
	r.stateMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !started {
		return nil
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close is also used by failed application construction before Start. Calls
// that already reached the recovered handler finish before its pool is closed.
func (r *sandboxRetry) Close() {
	r.close.Do(func() {
		_ = r.Stop(context.Background())
		r.targetMu.Lock()
		defer r.targetMu.Unlock()
		if r.target != nil && r.target.close != nil {
			r.target.close()
		}
		r.target = nil
	})
}

func (r *sandboxRetry) OpenTenantSpace(ctx context.Context, req *sandboxpb.OpenTenantSpaceRequest) (*sandboxpb.OpenTenantSpaceResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.OpenTenantSpace(ctx, req)
}
func (r *sandboxRetry) CloseTenantSpace(ctx context.Context, req *sandboxpb.CloseTenantSpaceRequest) (*sandboxpb.CloseTenantSpaceResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.CloseTenantSpace(ctx, req)
}
func (r *sandboxRetry) GetTenantSpace(ctx context.Context, req *sandboxpb.GetTenantSpaceRequest) (*sandboxpb.GetTenantSpaceResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.GetTenantSpace(ctx, req)
}
func (r *sandboxRetry) ResolveCreateSpec(ctx context.Context, req *sandboxpb.ResolveCreateSpecRequest) (*sandboxpb.ResolveCreateSpecResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ResolveCreateSpec(ctx, req)
}
func (r *sandboxRetry) AcceptCreate(ctx context.Context, req *sandboxpb.AcceptCreateRequest) (*sandboxpb.AcceptCreateResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.AcceptCreate(ctx, req)
}
func (r *sandboxRetry) AcceptSandboxClone(ctx context.Context, req *sandboxpb.AcceptSandboxCloneRequest) (*sandboxpb.AcceptSandboxCloneResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.AcceptSandboxClone(ctx, req)
}
func (r *sandboxRetry) RestoreSandboxCheckpoint(ctx context.Context, req *sandboxpb.RestoreSandboxCheckpointRequest) (*sandboxpb.RestoreSandboxCheckpointResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.RestoreSandboxCheckpoint(ctx, req)
}
func (r *sandboxRetry) GetSandbox(ctx context.Context, req *sandboxpb.GetSandboxRequest) (*sandboxpb.GetSandboxResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.GetSandbox(ctx, req)
}
func (r *sandboxRetry) ListSandboxes(ctx context.Context, req *sandboxpb.ListSandboxesRequest) (*sandboxpb.ListSandboxesResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ListSandboxes(ctx, req)
}
func (r *sandboxRetry) ListOperations(ctx context.Context, req *sandboxpb.ListOperationsRequest) (*sandboxpb.ListOperationsResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ListOperations(ctx, req)
}
func (r *sandboxRetry) ListSandboxPodHistory(ctx context.Context, req *sandboxpb.ListSandboxPodHistoryRequest) (*sandboxpb.ListSandboxPodHistoryResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ListSandboxPodHistory(ctx, req)
}
func (r *sandboxRetry) GetSandboxEgress(ctx context.Context, req *sandboxpb.GetSandboxEgressRequest) (*sandboxpb.GetSandboxEgressResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.GetSandboxEgress(ctx, req)
}
func (r *sandboxRetry) ListSandboxEvents(ctx context.Context, req *sandboxpb.ListSandboxEventsRequest) (*sandboxpb.ListSandboxEventsResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ListSandboxEvents(ctx, req)
}
func (r *sandboxRetry) ListSandboxCheckpoints(ctx context.Context, req *sandboxpb.ListSandboxCheckpointsRequest) (*sandboxpb.ListSandboxCheckpointsResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ListSandboxCheckpoints(ctx, req)
}
func (r *sandboxRetry) CreateSandboxCheckpoint(ctx context.Context, req *sandboxpb.CreateSandboxCheckpointRequest) (*sandboxpb.CreateSandboxCheckpointResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.CreateSandboxCheckpoint(ctx, req)
}
func (r *sandboxRetry) ListEnvironmentVariables(ctx context.Context, req *sandboxpb.ListEnvironmentVariablesRequest) (*sandboxpb.ListEnvironmentVariablesResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ListEnvironmentVariables(ctx, req)
}
func (r *sandboxRetry) StreamSandboxLogs(req *sandboxpb.StreamSandboxLogsRequest, stream grpc.ServerStreamingServer[sandboxpb.StreamSandboxLogsResponse]) error {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return sandboxUnavailable()
	}
	// A nil stream is never delivered by gRPC; forwarding it preserves test
	// fakes and does not create a lifetime that must be cancelled.
	if stream == nil {
		return r.target.service.StreamSandboxLogs(req, stream)
	}
	r.streamsMu.Lock()
	if r.streamsClosed {
		r.streamsMu.Unlock()
		return sandboxUnavailable()
	}
	ctx, cancel := context.WithCancel(stream.Context())
	wrapped := &sandboxRetryStream{ServerStreamingServer: stream, ctx: ctx}
	if r.streams == nil {
		r.streams = make(map[*sandboxRetryStream]context.CancelFunc)
	}
	r.streams[wrapped] = cancel
	r.streamsMu.Unlock()
	defer func() {
		cancel()
		r.streamsMu.Lock()
		delete(r.streams, wrapped)
		r.streamsMu.Unlock()
	}()
	return r.target.service.StreamSandboxLogs(req, wrapped)
}
func (r *sandboxRetry) GetOperation(ctx context.Context, req *sandboxpb.GetOperationRequest) (*sandboxpb.GetOperationResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.GetOperation(ctx, req)
}
func (r *sandboxRetry) Pause(ctx context.Context, req *sandboxpb.PauseRequest) (*sandboxpb.PauseResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.Pause(ctx, req)
}
func (r *sandboxRetry) Resume(ctx context.Context, req *sandboxpb.ResumeRequest) (*sandboxpb.ResumeResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.Resume(ctx, req)
}
func (r *sandboxRetry) Delete(ctx context.Context, req *sandboxpb.DeleteRequest) (*sandboxpb.DeleteResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.Delete(ctx, req)
}

func (r *sandboxRetry) RunCode(ctx context.Context, req *sandboxpb.RunCodeRequest) (*sandboxpb.RunCodeResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.RunCode(ctx, req)
}

func (r *sandboxRetry) GetCodeRun(ctx context.Context, req *sandboxpb.GetCodeRunRequest) (*sandboxpb.GetCodeRunResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.GetCodeRun(ctx, req)
}

func (r *sandboxRetry) QueryFiles(ctx context.Context, req *sandboxpb.QueryFilesRequest) (*sandboxpb.QueryFilesResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.QueryFiles(ctx, req)
}
func (r *sandboxRetry) MutateFiles(ctx context.Context, req *sandboxpb.MutateFilesRequest) (*sandboxpb.MutateFilesResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.MutateFiles(ctx, req)
}
func (r *sandboxRetry) GetFileOperation(ctx context.Context, req *sandboxpb.GetFileOperationRequest) (*sandboxpb.GetFileOperationResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.GetFileOperation(ctx, req)
}

func (r *sandboxRetry) CreateToken(ctx context.Context, req *sandboxpb.CreateTokenRequest) (*sandboxpb.CreateTokenResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.CreateToken(ctx, req)
}
func (r *sandboxRetry) ValidateToken(ctx context.Context, req *sandboxpb.ValidateTokenRequest) (*sandboxpb.ValidateTokenResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ValidateToken(ctx, req)
}
func (r *sandboxRetry) RevokeToken(ctx context.Context, req *sandboxpb.RevokeTokenRequest) (*sandboxpb.RevokeTokenResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.RevokeToken(ctx, req)
}

var _ transport.Server = (*sandboxRetry)(nil)
var _ sandboxpb.SandboxServiceServer = (*sandboxRetry)(nil)

func (r *sandboxRetry) OpenPreview(ctx context.Context, req *sandboxpb.OpenPreviewRequest) (*sandboxpb.OpenPreviewResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.OpenPreview(ctx, req)
}
func (r *sandboxRetry) ClosePreview(ctx context.Context, req *sandboxpb.ClosePreviewRequest) (*sandboxpb.ClosePreviewResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ClosePreview(ctx, req)
}
func (r *sandboxRetry) ListPreviews(ctx context.Context, req *sandboxpb.ListPreviewsRequest) (*sandboxpb.ListPreviewsResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.ListPreviews(ctx, req)
}
func (r *sandboxRetry) GetPreviewOperation(ctx context.Context, req *sandboxpb.GetPreviewOperationRequest) (*sandboxpb.GetPreviewOperationResponse, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil {
		return nil, sandboxUnavailable()
	}
	return r.target.service.GetPreviewOperation(ctx, req)
}

func (r *sandboxRetry) ResolveTerminalSession(ctx context.Context, req *terminalpb.ResolveTerminalSessionRequest) (*terminalpb.SandboxTerminalBinding, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil || r.target.terminal == nil {
		return nil, status.Error(codes.Unavailable, "SANDBOX_TERMINAL_UNAVAILABLE")
	}
	return r.target.terminal.ResolveTerminalSession(ctx, req)
}
func (r *sandboxRetry) ValidateTerminalSession(ctx context.Context, req *terminalpb.SandboxTerminalBinding) (*terminalpb.TerminalSessionValidation, error) {
	r.targetMu.RLock()
	defer r.targetMu.RUnlock()
	if r.target == nil || r.target.terminal == nil {
		return nil, status.Error(codes.Unavailable, "SANDBOX_TERMINAL_UNAVAILABLE")
	}
	return r.target.terminal.ValidateTerminalSession(ctx, req)
}
