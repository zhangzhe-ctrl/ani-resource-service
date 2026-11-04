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

func runningCheckpointFixture(t *testing.T, f *sandboxFixture) (context.Context, biz.Create, biz.CheckpointCommand) {
	t.Helper()
	ctx, c, work := acceptAndClaim(t, f)
	running := work.Sandbox
	running.Observed = biz.ObservedRunning
	running.ProviderRefs = biz.ProviderRefs{PVCName: "workspace", PVCUID: uuid.NewString(), PVName: "pv", PVUID: uuid.NewString(), PodName: "pod", PodUID: uuid.NewString()}
	if err := f.repo.Finish(ctx, *work, biz.Progress{Sandbox: running, Next: time.Hour}); err != nil {
		t.Fatal(err)
	}
	return ctx, c, biz.CheckpointCommand{TenantID: c.TenantID, SandboxID: c.ID, CheckpointID: uuid.NewString(), OperationID: uuid.NewString(), Name: "test", IdempotencyKey: "checkpoint-request", ExpectedVersion: 1}
}

func TestCheckpointAcceptanceReplayFenceAndIsolation(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, command := runningCheckpointFixture(t, f)
	checkpoint, err := f.repo.AcceptCheckpoint(ctx, command)
	if err != nil || checkpoint.ID != command.CheckpointID || checkpoint.Source.NamespaceUID != c.Spec.NamespaceUID || checkpoint.Source.PVCUID == "" || checkpoint.Source.PVUID == "" || checkpoint.Phase != "preflight" {
		t.Fatalf("accept: %#v %v", checkpoint, err)
	}
	replay := command
	replay.CheckpointID, replay.OperationID = uuid.NewString(), uuid.NewString()
	got, err := f.repo.AcceptCheckpoint(ctx, replay)
	if err != nil || !got.Replayed || got.ID != checkpoint.ID {
		t.Fatalf("replay: %#v %v", got, err)
	}
	replay.Name = "different"
	if _, err = f.repo.AcceptCheckpoint(ctx, replay); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("digest mismatch: %v", err)
	}
	items, total, err := f.repo.ListCheckpoints(ctx, c.TenantID, c.ID, 1, 1)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != checkpoint.ID {
		t.Fatalf("list: %#v %d %v", items, total, err)
	}
	op, err := f.repo.GetOperation(ctx, c.TenantID, command.OperationID)
	if err != nil || op.Action != "checkpoint_create" || op.State != "queued" {
		t.Fatalf("operation: %#v %v", op, err)
	}
	ops, count, err := f.repo.ListOperations(ctx, c.TenantID, c.ID, 1, 100)
	if err != nil || count < 1 {
		t.Fatalf("operation list count=%d err=%v", count, err)
	}
	found := false
	for _, listed := range ops {
		if listed.ID == command.OperationID && listed.Action == "checkpoint_create" {
			found = true
		}
	}
	if !found {
		t.Fatal("checkpoint operation absent from sandbox operation history")
	}
	foreignTenant := uuid.NewString()
	foreign := biz.WithCaller(context.Background(), biz.Caller{TenantID: foreignTenant, Actor: "foreign"})
	if _, err = f.repo.GetCheckpoint(foreign, foreignTenant, c.ID, checkpoint.ID); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("tenant isolation: %v", err)
	}
	for _, action := range []string{"pause", "resume", "delete"} {
		cmd := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: action, IdempotencyKey: action, RequestDigest: biz.ActionDigest(c.InstanceID, 1, action), ExpectedVersion: 1}
		if _, err = f.repo.Change(ctx, cmd); !errors.Is(err, biz.ErrConflict) {
			t.Fatalf("%s was not blocked: %v", action, err)
		}
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	if work, err := f.repo.Claim(ctx, "worker", time.Minute); err != nil || work != nil {
		t.Fatalf("lifecycle claim crossed checkpoint fence: %#v %v", work, err)
	}
}

func TestCheckpointLifecycleConcurrentAcceptance(t *testing.T) {
	f := sandboxDatabase(t)
	for i := 0; i < 12; i++ {
		ctx, c, command := runningCheckpointFixture(t, f)
		pause := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "pause", RequestDigest: biz.ActionDigest(c.InstanceID, 1, "pause"), ExpectedVersion: 1}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var acceptErr, pauseErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, acceptErr = f.repo.AcceptCheckpoint(ctx, command) }()
		go func() { defer wg.Done(); <-start; _, pauseErr = f.repo.Change(ctx, pause) }()
		close(start)
		wg.Wait()
		if (acceptErr == nil) == (pauseErr == nil) {
			t.Fatalf("both succeeded or both failed: checkpoint=%v pause=%v", acceptErr, pauseErr)
		}
		if acceptErr != nil && !errors.Is(acceptErr, biz.ErrVersion) && !errors.Is(acceptErr, biz.ErrConflict) {
			t.Fatalf("unexpected acceptance error: %v", acceptErr)
		}
		if pauseErr != nil && !errors.Is(pauseErr, biz.ErrConflict) {
			t.Fatalf("unexpected lifecycle error: %v", pauseErr)
		}
		if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at='infinity' WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckpointRejectsActiveLeaseAndPendingMutation(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, command := runningCheckpointFixture(t, f)
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	work := claimWork(t, f, ctx, "active")
	if _, err := f.repo.AcceptCheckpoint(ctx, command); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("active lease: %v", err)
	}
	mutation := biz.Mutation{ID: uuid.NewString(), Kind: biz.MutationDeletePod, OperationID: work.Sandbox.LastOperationID, Version: work.Sandbox.Version, TargetUID: work.Sandbox.ProviderRefs.PodUID}
	if err := f.repo.PrepareMutation(ctx, *work, mutation); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Finish(ctx, *work, biz.Progress{Sandbox: work.Sandbox, Next: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.AcceptCheckpoint(ctx, command); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("pending mutation: %v", err)
	}
}

func TestCheckpointJournalUnknownAndConfirmedCompletion(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, command := runningCheckpointFixture(t, f)
	if _, err := f.repo.AcceptCheckpoint(ctx, command); err != nil {
		t.Fatal(err)
	}
	w, err := f.repo.ClaimCheckpoint(ctx, "checkpoint-worker", time.Minute)
	if err != nil || w == nil {
		t.Fatalf("claim: %#v %v", w, err)
	}
	source := w.Checkpoint.Source
	source.CSIDriver, source.VolumeHandle, source.SnapshotClass, source.SnapshotClassUID, source.SnapshotClassResourceVersion = "csi.invalid", "volume-handle", "snapclass", uuid.NewString(), "42"
	bad := source
	bad.PVCUID = uuid.NewString()
	if err = f.repo.RecordCheckpointSource(ctx, *w, bad); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("source changed: %v", err)
	}
	if err = f.repo.RecordCheckpointSource(ctx, *w, source); err != nil {
		t.Fatal(err)
	}
	protect := biz.CheckpointMutation{ID: uuid.NewString(), Kind: "protect"}
	if ok, err := f.repo.PrepareCheckpointMutation(ctx, *w, protect); err != nil || !ok {
		t.Fatalf("prepare: %v %v", ok, err)
	}
	if ok, err := f.repo.PrepareCheckpointMutation(ctx, *w, protect); err != nil || ok {
		t.Fatalf("repeated prepare sent again: %v %v", ok, err)
	}
	if err = f.repo.FinishCheckpointMutation(ctx, *w, protect, biz.MutationUnknown, ""); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.FinishCheckpointMutation(ctx, *w, protect, biz.MutationNotSent, ""); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, command.CheckpointID)
	if err != nil || got.State != "unknown" || got.MutationID != protect.ID {
		t.Fatalf("unknown lost: %#v %v", got, err)
	}
	if err = f.repo.FinishCheckpointMutation(ctx, *w, protect, biz.MutationApplied, source.PVCUID); err != nil {
		t.Fatal(err)
	}
	create := biz.CheckpointMutation{ID: uuid.NewString(), Kind: "create"}
	if ok, err := f.repo.PrepareCheckpointMutation(ctx, *w, create); err != nil || !ok {
		t.Fatalf("create prepare: %v %v", ok, err)
	}
	snapshotUID := uuid.NewString()
	if err = f.repo.FinishCheckpointMutation(ctx, *w, create, biz.MutationApplied, snapshotUID); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.RecordCheckpointReady(ctx, *w, uuid.NewString(), "content", uuid.NewString(), "snapshot-handle"); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("incorrect snapshot accepted: %v", err)
	}
	if err = f.repo.RecordCheckpointReady(ctx, *w, snapshotUID, "content", uuid.NewString(), "snapshot-handle"); err != nil {
		t.Fatal(err)
	}
	release := biz.CheckpointMutation{ID: uuid.NewString(), Kind: "release"}
	if ok, err := f.repo.PrepareCheckpointMutation(ctx, *w, release); err != nil || !ok {
		t.Fatalf("release prepare: %v %v", ok, err)
	}
	if err = f.repo.FinishCheckpointMutation(ctx, *w, release, biz.MutationApplied, source.PVCUID); err != nil {
		t.Fatal(err)
	}
	got, err = f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, command.CheckpointID)
	if err != nil || got.State != "ready" || got.Phase != "done" || got.ContentUID == "" || got.CompletedAt == nil || got.ProtectionMutationID != protect.ID || got.SnapshotMutationID != create.ID || got.ReleaseMutationID != release.ID {
		t.Fatalf("ready: %#v %v", got, err)
	}
	op, err := f.repo.GetOperation(ctx, c.TenantID, command.OperationID)
	if err != nil || op.State != "succeeded" || op.CompletedAt == nil {
		t.Fatalf("operation: %#v %v", op, err)
	}
	pause := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "pause", RequestDigest: biz.ActionDigest(c.InstanceID, 1, "pause"), ExpectedVersion: 1}
	if _, err = f.repo.Change(ctx, pause); err != nil {
		t.Fatalf("completed checkpoint blocks pause: %v", err)
	}
	remove := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: "delete", IdempotencyKey: "delete", RequestDigest: biz.ActionDigest(c.InstanceID, 2, "delete"), ExpectedVersion: 2}
	if _, err = f.repo.Change(ctx, remove); err != nil {
		t.Fatalf("delete should start checkpoint cleanup: %v", err)
	}
	if work, err := f.repo.Claim(ctx, "before-cleanup", time.Minute); err != nil || work != nil {
		t.Fatalf("lifecycle crossed checkpoint cleanup barrier: %+v %v", work, err)
	}
}

func TestCheckpointWorkerLeaseAndSchedule(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, command := runningCheckpointFixture(t, f)
	if _, err := f.repo.AcceptCheckpoint(ctx, command); err != nil {
		t.Fatal(err)
	}
	w, err := f.repo.ClaimCheckpoint(ctx, "first", time.Minute)
	if err != nil || w == nil {
		t.Fatalf("claim: %v", err)
	}
	if err = f.repo.ReleaseCheckpointLease(ctx, *w, time.Hour); err != nil {
		t.Fatal(err)
	}
	if next, err := f.repo.ClaimCheckpoint(ctx, "next", time.Minute); err != nil || next != nil {
		t.Fatalf("scheduled work claimed early: %#v %v", next, err)
	}
	if err = f.repo.FailCheckpointPreflight(ctx, *w, "SANDBOX_CHECKPOINT_UNSUPPORTED"); !errors.Is(err, biz.ErrLease) {
		t.Fatalf("released lease still writable: %v", err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.checkpoints SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	next, err := f.repo.ClaimCheckpoint(ctx, "next", time.Minute)
	if err != nil || next == nil || next.LeaseEpoch <= w.LeaseEpoch {
		t.Fatalf("takeover: %#v %v", next, err)
	}
	if err = f.repo.FailCheckpointPreflight(ctx, *next, "SANDBOX_CHECKPOINT_UNSUPPORTED"); err != nil {
		t.Fatal(err)
	}
	op, err := f.repo.GetOperation(ctx, c.TenantID, command.OperationID)
	if err != nil || op.State != "failed" || op.CompletedAt == nil {
		t.Fatalf("failure: %#v %v", op, err)
	}
	remove := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: "delete", IdempotencyKey: "delete", RequestDigest: biz.ActionDigest(c.InstanceID, 1, "delete"), ExpectedVersion: 1}
	if _, err = f.repo.Change(ctx, remove); err != nil {
		t.Fatalf("safe failed checkpoint should enter cleanup: %v", err)
	}
	if work, err := f.repo.Claim(ctx, "before-cleanup", time.Minute); err != nil || work != nil {
		t.Fatalf("lifecycle crossed failed checkpoint cleanup barrier: %+v %v", work, err)
	}
}

func TestCheckpointTakeoverTreatsPreparedWriteAsUnknown(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, command := runningCheckpointFixture(t, f)
	if _, err := f.repo.AcceptCheckpoint(ctx, command); err != nil {
		t.Fatal(err)
	}
	w, err := f.repo.ClaimCheckpoint(ctx, "first", time.Minute)
	if err != nil || w == nil {
		t.Fatalf("claim: %v", err)
	}
	source := w.Checkpoint.Source
	source.CSIDriver, source.VolumeHandle, source.SnapshotClass, source.SnapshotClassUID, source.SnapshotClassResourceVersion = "driver", "handle", "class", uuid.NewString(), "1"
	if err = f.repo.RecordCheckpointSource(ctx, *w, source); err != nil {
		t.Fatal(err)
	}
	m := biz.CheckpointMutation{ID: uuid.NewString(), Kind: "protect"}
	if dispatch, err := f.repo.PrepareCheckpointMutation(ctx, *w, m); err != nil || !dispatch {
		t.Fatalf("prepare: %v %v", dispatch, err)
	}
	if err = f.repo.ReleaseCheckpointLease(ctx, *w, 0); err != nil {
		t.Fatal(err)
	}
	next, err := f.repo.ClaimCheckpoint(ctx, "next", time.Minute)
	if err != nil || next == nil {
		t.Fatalf("reclaim: %v", err)
	}
	if next.Checkpoint.State != "unknown" || next.Checkpoint.MutationState != "unknown" {
		t.Fatalf("prepared write not marked unknown: %#v", next.Checkpoint)
	}
	if err = f.repo.FinishCheckpointMutation(ctx, *next, m, biz.MutationRejected, ""); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, command.CheckpointID)
	if err != nil || got.State != "unknown" || got.MutationID != m.ID {
		t.Fatalf("rejection erased uncertain write: %#v %v", got, err)
	}
}
