//go:build sandboxintegration

package sandbox_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func TestCheckpointWFFCCloneRejectionSerializesWithAcceptance(t *testing.T) {
	f := sandboxDatabase(t)
	for i := 0; i < 12; i++ {
		ctx, source, _, cmd := directCloneFixture(t, f)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var acceptErr, rejectErr error
		var rejectedResult biz.CheckpointTransfer
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, acceptErr = f.repo.AcceptCheckpointClone(ctx, cmd) }()
		go func() {
			defer wg.Done()
			<-start
			rejectedResult, rejectErr = f.repo.RejectCheckpointCloneWFFC(ctx, cmd)
		}()
		close(start)
		wg.Wait()
		if acceptErr == nil {
			if rejectErr != nil || !rejectedResult.Replayed || rejectedResult.TargetSandboxID != cmd.Sandbox.ID {
				t.Fatalf("accepted winner lost: accept=%v reject=%v result=%+v", acceptErr, rejectErr, rejectedResult)
			}
			if _, err := f.repo.LookupCheckpointClone(ctx, cmd); err != nil {
				t.Fatalf("accepted replay %v", err)
			}
		} else {
			if !errors.Is(acceptErr, biz.ErrCheckpointWFFCUnsupported) || !errors.Is(rejectErr, biz.ErrCheckpointWFFCUnsupported) {
				t.Fatalf("rejection inconsistent: accept=%v reject=%v", acceptErr, rejectErr)
			}
			if _, err := f.repo.LookupCheckpointClone(ctx, cmd); !errors.Is(err, biz.ErrCheckpointWFFCUnsupported) {
				t.Fatalf("rejection replay %v", err)
			}
			if _, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID); !errors.Is(err, biz.ErrNotFound) {
				t.Fatalf("rejected clone created target: %v", err)
			}
			changed := cmd
			changed.Sandbox.RequestDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			if _, err := f.repo.AcceptCheckpointClone(ctx, changed); !errors.Is(err, biz.ErrConflict) {
				t.Fatalf("changed command reused tombstone: %v", err)
			}
			if _, err := f.repo.AcceptCreate(ctx, cmd.Sandbox); !errors.Is(err, biz.ErrConflict) {
				t.Fatalf("ordinary create reused rejected target: %v", err)
			}
		}
	}
}

func TestCheckpointWFFCRestoreRejectionSerializesWithAcceptance(t *testing.T) {
	f := sandboxDatabase(t)
	for i := 0; i < 12; i++ {
		ctx, source, checkpoint, _ := transferFixture(t, f)
		cmd := biz.CheckpointTransferCommand{TenantID: source.TenantID, TransferID: uuid.NewString(), OperationID: uuid.NewString(), CheckpointID: checkpoint.ID, SourceSandboxID: source.ID, TargetSandboxID: source.ID, Kind: "restore", IdempotencyKey: "restore", ExpectedVersion: 1}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var acceptErr, rejectErr error
		var rejectedResult biz.CheckpointTransfer
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, acceptErr = f.repo.AcceptCheckpointTransfer(ctx, cmd) }()
		go func() {
			defer wg.Done()
			<-start
			rejectedResult, rejectErr = f.repo.RejectCheckpointTransferWFFC(ctx, cmd)
		}()
		close(start)
		wg.Wait()
		if acceptErr == nil {
			if rejectErr != nil || !rejectedResult.Replayed {
				t.Fatalf("accepted winner lost: %v %+v", rejectErr, rejectedResult)
			}
		} else {
			if !errors.Is(acceptErr, biz.ErrCheckpointWFFCUnsupported) || !errors.Is(rejectErr, biz.ErrCheckpointWFFCUnsupported) {
				t.Fatalf("restore rejection inconsistent: %v %v", acceptErr, rejectErr)
			}
			replay := cmd
			replay.TransferID, replay.OperationID = uuid.NewString(), uuid.NewString()
			if _, err := f.repo.LookupCheckpointTransfer(ctx, replay); !errors.Is(err, biz.ErrCheckpointWFFCUnsupported) {
				t.Fatalf("tombstone replay %v", err)
			}
			if _, err := f.repo.AcceptCheckpointTransfer(ctx, replay); !errors.Is(err, biz.ErrCheckpointWFFCUnsupported) {
				t.Fatalf("tombstone acceptance %v", err)
			}
			replay.CheckpointID = uuid.NewString()
			if _, err := f.repo.AcceptCheckpointTransfer(ctx, replay); !errors.Is(err, biz.ErrConflict) {
				t.Fatalf("changed checkpoint reused tombstone %v", err)
			}
		}
	}
}

func TestCloneRejectionSerializesWithOrdinaryCreate(t *testing.T) {
	f := sandboxDatabase(t)
	for i := 0; i < 12; i++ {
		ctx, source, _, cmd := directCloneFixture(t, f)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var createErr, rejectErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, createErr = f.repo.AcceptCreate(ctx, cmd.Sandbox) }()
		go func() { defer wg.Done(); <-start; _, rejectErr = f.repo.RejectCheckpointCloneWFFC(ctx, cmd) }()
		close(start)
		wg.Wait()
		if createErr == nil {
			if !errors.Is(rejectErr, biz.ErrConflict) {
				t.Fatalf("accepted ordinary target also rejected clone: %v", rejectErr)
			}
			if _, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID); err != nil {
				t.Fatal(err)
			}
		} else {
			if !errors.Is(createErr, biz.ErrConflict) || !errors.Is(rejectErr, biz.ErrCheckpointWFFCUnsupported) {
				t.Fatalf("create/rejection: %v %v", createErr, rejectErr)
			}
			if _, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID); !errors.Is(err, biz.ErrNotFound) {
				t.Fatalf("rejected target created: %v", err)
			}
		}
		// The next fixture claims globally, so do not let this iteration's
		// successful ordinary-create work be claimed as its new source.
		if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at='infinity' WHERE tenant_id=$1`, source.TenantID); err != nil {
			t.Fatal(err)
		}
	}
}
