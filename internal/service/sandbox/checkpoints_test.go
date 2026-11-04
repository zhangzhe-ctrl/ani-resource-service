package sandbox

import (
	"context"
	"fmt"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync/atomic"
	"testing"
	"time"
)

type checkpointsListRepo struct {
	*environmentRepo
	items      []biz.Checkpoint
	page, size uint32
	command    biz.CheckpointCommand
}

func (r *checkpointsListRepo) AcceptCheckpoint(_ context.Context, command biz.CheckpointCommand) (biz.Checkpoint, error) {
	r.command = command
	return biz.Checkpoint{TenantID: command.TenantID, SandboxID: command.SandboxID, ID: command.CheckpointID, OperationID: command.OperationID, Name: command.Name, State: "accepted"}, nil
}
func (r *checkpointsListRepo) GetCheckpoint(context.Context, string, string, string) (biz.Checkpoint, error) {
	return biz.Checkpoint{}, biz.ErrNotFound
}

func TestCreateCheckpointValidatesAndAcceptsOperation(t *testing.T) {
	svc, base, _ := environmentService()
	repo := &checkpointsListRepo{environmentRepo: base}
	svc.repo = repo
	req := &pb.CreateSandboxCheckpointRequest{TenantId: testTenant, SandboxId: testSandbox, Name: "before-change", IdempotencyKey: "key-1", ExpectedVersion: 3}
	out, err := svc.CreateSandboxCheckpoint(serviceContext(), req)
	if err != nil || !biz.UUID(out.CheckpointId) || !biz.UUID(out.OperationId) || out.State != "accepted" || repo.command.ExpectedVersion != 3 {
		t.Fatalf("out=%+v command=%+v err=%v", out, repo.command, err)
	}
	req.IdempotencyKey = ""
	if _, err = svc.CreateSandboxCheckpoint(serviceContext(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty key accepted: %v", err)
	}
	req.IdempotencyKey = "key-1"
	req.TenantId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if _, err = svc.CreateSandboxCheckpoint(serviceContext(), req); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign tenant accepted: %v", err)
	}
}

func (r *checkpointsListRepo) ListCheckpoints(_ context.Context, tenant, id string, page, size uint32) ([]biz.Checkpoint, int64, error) {
	if tenant != testTenant || id != testSandbox {
		return nil, 0, biz.ErrNotFound
	}
	r.page, r.size = page, size
	return r.items, int64(len(r.items)), nil
}

type checkpointReadObserver struct {
	result biz.CheckpointObservation
	calls  int
	err    error
}

func (o *checkpointReadObserver) ObserveRecordedCheckpoint(context.Context, biz.Sandbox, biz.Checkpoint) (biz.CheckpointObservation, error) {
	o.calls++
	return o.result, o.err
}
func TestListCheckpointLedgerVerifiesReadyAndScopesTenant(t *testing.T) {
	svc, base, _ := environmentService()
	repo := &checkpointsListRepo{environmentRepo: base, items: []biz.Checkpoint{{ID: "77777777-7777-4777-8777-777777777777", TenantID: testTenant, SandboxID: testSandbox, Name: "test", State: "ready", CreatedAt: time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC)}}}
	svc.repo = repo
	observer := &checkpointReadObserver{result: biz.CheckpointObservation{State: "ready"}}
	svc.config.CheckpointObserver = observer
	r := &pb.ListSandboxCheckpointsRequest{TenantId: testTenant, SandboxId: testSandbox, Page: 1, PageSize: 1}
	out, err := svc.ListSandboxCheckpoints(serviceContext(), r)
	if err != nil || out.Total != 1 || out.Source != "sandbox_checkpoint_ledger" || repo.page != 1 || repo.size != 1 || len(out.Items) != 1 || out.Items[0].Status != "ready" || out.Items[0].SizeKnown || out.Items[0].ProviderStatus != "ready" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	observer.result = biz.CheckpointObservation{State: "missing", Reason: "CHECKPOINT_PROVIDER_MISSING"}
	out, err = svc.ListSandboxCheckpoints(serviceContext(), r)
	if err != nil || out.Items[0].Status != "unknown" || out.Items[0].Reason != "CHECKPOINT_PROVIDER_MISSING" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	r.Page = 2
	if _, err = svc.ListSandboxCheckpoints(serviceContext(), r); status.Code(err) != codes.Unavailable {
		t.Fatalf("impossible page accepted: %v", err)
	}
	r.Page = 1
	r.TenantId = "66666666-6666-4666-8666-666666666666"
	before := observer.calls
	if _, err = svc.ListSandboxCheckpoints(serviceContext(), r); status.Code(err) != codes.PermissionDenied || observer.calls != before {
		t.Fatalf("err=%v calls=%d", err, observer.calls)
	}
}
func TestListCheckpointLedgerFailsWithoutObserverForReady(t *testing.T) {
	svc, base, _ := environmentService()
	svc.repo = &checkpointsListRepo{environmentRepo: base, items: []biz.Checkpoint{{ID: "77777777-7777-4777-8777-777777777777", TenantID: testTenant, SandboxID: testSandbox, Name: "test", State: "ready", CreatedAt: time.Now()}}}
	_, err := svc.ListSandboxCheckpoints(serviceContext(), &pb.ListSandboxCheckpointsRequest{TenantId: testTenant, SandboxId: testSandbox})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err=%v", err)
	}
}

type boundedCheckpointObserver struct{ current, peak atomic.Int32 }

func (o *boundedCheckpointObserver) ObserveRecordedCheckpoint(ctx context.Context, _ biz.Sandbox, _ biz.Checkpoint) (biz.CheckpointObservation, error) {
	n := o.current.Add(1)
	defer o.current.Add(-1)
	for old := o.peak.Load(); n > old && !o.peak.CompareAndSwap(old, n); old = o.peak.Load() {
	}
	select {
	case <-ctx.Done():
		return biz.CheckpointObservation{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return biz.CheckpointObservation{State: "ready"}, nil
	}
}
func TestCheckpointLedgerObserverConcurrencyIsBoundedAndOrderStable(t *testing.T) {
	svc, base, _ := environmentService()
	repo := &checkpointsListRepo{environmentRepo: base}
	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		repo.items = append(repo.items, biz.Checkpoint{ID: fmt.Sprintf("77777777-7777-4777-8777-%012x", i+1), TenantID: testTenant, SandboxID: testSandbox, Name: fmt.Sprint(i), State: "ready", CreatedAt: now})
	}
	svc.repo = repo
	observer := new(boundedCheckpointObserver)
	svc.config.CheckpointObserver = observer
	out, err := svc.ListSandboxCheckpoints(serviceContext(), &pb.ListSandboxCheckpointsRequest{TenantId: testTenant, SandboxId: testSandbox, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if observer.peak.Load() > 8 || observer.peak.Load() < 2 {
		t.Fatalf("unexpected peak %d", observer.peak.Load())
	}
	for i, item := range out.Items {
		if item.Name != fmt.Sprint(i) {
			t.Fatalf("order %d => %s", i, item.Name)
		}
	}
}

func TestCheckpointListExposesOnlySanitizedPendingReason(t *testing.T) {
	svc, base, _ := environmentService()
	repo := &checkpointsListRepo{environmentRepo: base, items: []biz.Checkpoint{{ID: "77777777-7777-4777-8777-777777777777", TenantID: testTenant, SandboxID: testSandbox, Name: "pending", State: "running", Reason: "CSI_SNAPSHOT_LAST_ERROR", CreatedAt: time.Now()}}}
	svc.repo = repo
	request := &pb.ListSandboxCheckpointsRequest{TenantId: testTenant, SandboxId: testSandbox}
	out, err := svc.ListSandboxCheckpoints(serviceContext(), request)
	if err != nil || out.Items[0].Reason != "CSI_SNAPSHOT_LAST_ERROR" || out.Items[0].Status != "running" {
		t.Fatalf("response=%+v err=%v", out, err)
	}
	repo.items[0].Reason = "secret from backend"
	out, err = svc.ListSandboxCheckpoints(serviceContext(), request)
	if err != nil || out.Items[0].Reason != "CHECKPOINT_REASON_UNAVAILABLE" {
		t.Fatalf("unsanitized response=%+v err=%v", out, err)
	}
}
