//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

type checkpointTestProvider struct {
	mu                                                            sync.Mutex
	calls                                                         map[string]int
	createUnknown, allowObservation, rejectCreate, releaseUnknown bool
	snapshotUID, contentUID, handle                               string
	createEntered, unblockCreate                                  chan struct{}
}

func (p *checkpointTestProvider) PreflightCheckpoint(_ context.Context, _ biz.Sandbox, c biz.Checkpoint) (biz.CheckpointSource, error) {
	s := c.Source
	s.CSIDriver = "driver"
	s.VolumeHandle = "volume-handle"
	s.SnapshotClass = "class"
	s.SnapshotClassUID = "class-uid"
	s.SnapshotClassResourceVersion = "1"
	return s, nil
}
func (p *checkpointTestProvider) ApplyCheckpointMutation(_ context.Context, _ biz.Sandbox, c biz.Checkpoint, m biz.CheckpointMutation) (biz.CheckpointMutationReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[m.Kind]++
	out := biz.CheckpointMutationReceipt{Mutation: m, Outcome: biz.MutationApplied, UID: c.Source.PVCUID}
	if m.Kind == "create" {
		if p.createEntered != nil {
			close(p.createEntered)
			<-p.unblockCreate
		}
		out.UID = p.snapshotUID
		if p.createUnknown {
			out.Outcome = biz.MutationUnknown
			out.UID = ""
		} else if p.rejectCreate {
			out.Outcome = biz.MutationRejected
			out.UID = ""
		}
	}
	if m.Kind == "release" && p.releaseUnknown {
		out.Outcome = biz.MutationUnknown
		out.UID = ""
	}
	return out, nil
}

func TestCheckpointWorkersCannotDispatchSameMutationConcurrently(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, cmd := runningCheckpointFixture(t, f)
	if _, err := f.repo.AcceptCheckpoint(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	p := &checkpointTestProvider{calls: map[string]int{}, snapshotUID: uuid.NewString(), contentUID: uuid.NewString(), handle: "handle", createEntered: make(chan struct{}), unblockCreate: make(chan struct{})}
	w1, err := biz.NewCheckpointWorker(f.repo, p, "first", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := biz.NewCheckpointWorker(f.repo, p, "second", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	checkpointStep(t, f, ctx, c, w1)
	checkpointStep(t, f, ctx, c, w1)
	checkpointDue(t, f, ctx, c.TenantID, c.ID)
	finished := make(chan error, 1)
	go func() {
		worked, e := w1.Step(ctx)
		if e == nil && !worked {
			e = errors.New("first worker did not claim")
		}
		finished <- e
	}()
	<-p.createEntered
	worked, err := w2.Step(ctx)
	if err != nil || worked {
		t.Fatalf("second worker claimed active lease: %v %v", worked, err)
	}
	close(p.unblockCreate)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if p.calls["create"] != 1 {
		t.Fatalf("dispatched %d times", p.calls["create"])
	}
}
func (p *checkpointTestProvider) ObserveCheckpointMutation(_ context.Context, _ biz.Sandbox, c biz.Checkpoint, m biz.CheckpointMutation) (biz.CheckpointMutationReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := biz.CheckpointMutationReceipt{Mutation: m, Outcome: biz.MutationUnknown}
	if p.allowObservation && m.Kind == "create" {
		out.Outcome = biz.MutationApplied
		out.UID = p.snapshotUID
	}
	if p.allowObservation && m.Kind == "release" {
		out.Outcome = biz.MutationApplied
		out.UID = c.Source.PVCUID
	}
	return out, nil
}
func (p *checkpointTestProvider) ObserveCheckpointReady(_ context.Context, _ biz.Sandbox, c biz.Checkpoint) (biz.CheckpointReadyObservation, error) {
	return biz.CheckpointReadyObservation{Ready: true, SnapshotUID: p.snapshotUID, ContentName: "content", ContentUID: p.contentUID, SnapshotHandle: p.handle}, nil
}
func checkpointDue(t *testing.T, f *sandboxFixture, ctx context.Context, tenant, id string) {
	t.Helper()
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.checkpoints SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, tenant, id); err != nil {
		t.Fatal(err)
	}
}
func checkpointStep(t *testing.T, f *sandboxFixture, ctx context.Context, c biz.Create, w *biz.CheckpointWorker) {
	t.Helper()
	checkpointDue(t, f, ctx, c.TenantID, c.ID)
	worked, err := w.Step(ctx)
	if err != nil || !worked {
		t.Fatalf("checkpoint step: %v %v", worked, err)
	}
}
func TestCheckpointWorkerUnknownCreateRecoveredWithoutRedispatch(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, cmd := runningCheckpointFixture(t, f)
	if _, err := f.repo.AcceptCheckpoint(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	p := &checkpointTestProvider{calls: map[string]int{}, createUnknown: true, snapshotUID: uuid.NewString(), contentUID: uuid.NewString(), handle: "snapshot-handle"}
	w, err := biz.NewCheckpointWorker(f.repo, p, "worker", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	checkpointStep(t, f, ctx, c, w) // preflight
	checkpointStep(t, f, ctx, c, w) // protect
	checkpointStep(t, f, ctx, c, w) // uncertain create
	for i := 0; i < 3; i++ {
		checkpointStep(t, f, ctx, c, w)
	} // absence is not a receipt
	got, err := f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, cmd.CheckpointID)
	if err != nil || got.State != "unknown" || got.MutationKind != "create" || p.calls["create"] != 1 {
		t.Fatalf("uncertain create: %#v calls=%v err=%v", got, p.calls, err)
	}
	replay := cmd
	replay.CheckpointID = uuid.NewString()
	replay.OperationID = uuid.NewString()
	if previous, err := f.repo.AcceptCheckpoint(ctx, replay); err != nil || !previous.Replayed || previous.ID != cmd.CheckpointID {
		t.Fatalf("replay: %#v %v", previous, err)
	}
	pause := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "pause", RequestDigest: biz.ActionDigest(c.InstanceID, 1, "pause"), ExpectedVersion: 1}
	if _, err := f.repo.Change(ctx, pause); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("unknown did not block lifecycle: %v", err)
	}
	p.allowObservation = true
	checkpointStep(t, f, ctx, c, w) // recover known snapshot UID
	checkpointStep(t, f, ctx, c, w) // confirm content
	checkpointStep(t, f, ctx, c, w) // release
	got, err = f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, cmd.CheckpointID)
	if err != nil || got.State != "ready" || got.SnapshotUID != p.snapshotUID || got.ContentUID != p.contentUID || got.SnapshotHandle != p.handle || p.calls["create"] != 1 || p.calls["release"] != 1 {
		t.Fatalf("ready: %#v calls=%v err=%v", got, p.calls, err)
	}
}
func TestCheckpointWorkerCreateRejectionReleasesBeforeFailure(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, cmd := runningCheckpointFixture(t, f)
	if _, err := f.repo.AcceptCheckpoint(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	p := &checkpointTestProvider{calls: map[string]int{}, rejectCreate: true, releaseUnknown: true, snapshotUID: uuid.NewString(), contentUID: uuid.NewString(), handle: "handle"}
	w, err := biz.NewCheckpointWorker(f.repo, p, "worker", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		checkpointStep(t, f, ctx, c, w)
	}
	got, err := f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, cmd.CheckpointID)
	if err != nil || got.State != "running" || got.Phase != "release" {
		t.Fatalf("premature failure: %#v %v", got, err)
	}
	checkpointStep(t, f, ctx, c, w)
	got, err = f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, cmd.CheckpointID)
	if err != nil || got.State != "unknown" || got.FailureReason != "SANDBOX_CHECKPOINT_CREATE_REJECTED" {
		t.Fatalf("release uncertainty lost original failure: %#v %v", got, err)
	}
	p.allowObservation = true
	checkpointStep(t, f, ctx, c, w)
	got, err = f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, cmd.CheckpointID)
	if err != nil || got.State != "failed" || got.Reason != "SANDBOX_CHECKPOINT_CREATE_REJECTED" || p.calls["release"] != 1 {
		t.Fatalf("failed: %#v %v", got, err)
	}
}

type checkpointErrorProvider struct {
	*checkpointTestProvider
	preflightErr   error
	observationErr error
	pendingReason  string
	pending        bool
}

func (p *checkpointErrorProvider) PreflightCheckpoint(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (biz.CheckpointSource, error) {
	if p.preflightErr != nil {
		return biz.CheckpointSource{}, p.preflightErr
	}
	return p.checkpointTestProvider.PreflightCheckpoint(ctx, s, c)
}
func (p *checkpointErrorProvider) ObserveCheckpointReady(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (biz.CheckpointReadyObservation, error) {
	if p.observationErr != nil {
		return biz.CheckpointReadyObservation{}, p.observationErr
	}
	if p.pending {
		return biz.CheckpointReadyObservation{Reason: p.pendingReason}, nil
	}
	return p.checkpointTestProvider.ObserveCheckpointReady(ctx, s, c)
}

func TestCheckpointWorkerPersistsSanitizedPendingDiagnosticsAndRecovers(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, cmd := runningCheckpointFixture(t, f)
	if _, err := f.repo.AcceptCheckpoint(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	p := &checkpointErrorProvider{checkpointTestProvider: &checkpointTestProvider{calls: map[string]int{}, snapshotUID: uuid.NewString(), contentUID: uuid.NewString(), handle: "handle"}, preflightErr: biz.ErrDenied}
	w, err := biz.NewCheckpointWorker(f.repo, p, "worker", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	assert := func(state, reason string) {
		t.Helper()
		cp, err := f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, cmd.CheckpointID)
		if err != nil || cp.State != state || cp.Reason != reason || cp.CompletedAt != nil {
			t.Fatalf("checkpoint=%+v err=%v", cp, err)
		}
		op, err := f.repo.GetOperation(ctx, c.TenantID, cmd.OperationID)
		if err != nil || op.State != "running" || op.Reason != reason || op.CompletedAt != nil {
			t.Fatalf("operation=%+v err=%v", op, err)
		}
		rows, total, err := f.repo.ListCheckpoints(ctx, c.TenantID, c.ID, 1, 20)
		if err != nil || total != 1 || len(rows) != 1 || rows[0].Reason != reason {
			t.Fatalf("list=%+v total=%d err=%v", rows, total, err)
		}
	}
	checkpointStep(t, f, ctx, c, w)
	assert("running", "SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED")
	p.preflightErr = nil
	checkpointStep(t, f, ctx, c, w)
	assert("running", "")
	checkpointStep(t, f, ctx, c, w)
	checkpointStep(t, f, ctx, c, w)
	p.pendingReason = "CSI_SNAPSHOT_LAST_ERROR"
	p.pending = true
	checkpointStep(t, f, ctx, c, w)
	assert("running", "CSI_SNAPSHOT_LAST_ERROR")
	checkpointStep(t, f, ctx, c, w)
	assert("running", "CSI_SNAPSHOT_LAST_ERROR")
	p.pendingReason = ""
	checkpointStep(t, f, ctx, c, w)
	assert("running", "")
	p.pending = false
	checkpointStep(t, f, ctx, c, w)
	assert("running", "")
	checkpointStep(t, f, ctx, c, w)
	cp, err := f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, cmd.CheckpointID)
	if err != nil || cp.State != "ready" || cp.Reason != "" {
		t.Fatalf("recovery=%+v err=%v", cp, err)
	}
}
