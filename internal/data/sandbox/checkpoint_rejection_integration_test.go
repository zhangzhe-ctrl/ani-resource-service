//go:build sandboxintegration

package sandbox_test

import (
	"context"
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
			if _, err := f.repo.AcceptCheckpointClone(ctx, changed); !errors.Is(err, biz.ErrCloneRejectedConflict) {
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
			if !errors.Is(rejectErr, biz.ErrCloneRejectedConflict) {
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

func TestCloneDeterministicRejectionsAreDurableAndSerialized(t *testing.T) {
	f := sandboxDatabase(t)
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *sandboxFixture, context.Context, *biz.CheckpointCloneCommand)
		want    error
	}{
		{"missing_source", func(t *testing.T, f *sandboxFixture, ctx context.Context, c *biz.CheckpointCloneCommand) {
			c.Transfer.SourceSandboxID = uuid.NewString()
		}, biz.ErrCloneRejectedNotFound},
		{"missing_checkpoint", func(t *testing.T, f *sandboxFixture, ctx context.Context, c *biz.CheckpointCloneCommand) {
			c.Transfer.CheckpointID = uuid.NewString()
		}, biz.ErrCloneRejectedNotFound},
		{"stale_version", func(t *testing.T, f *sandboxFixture, ctx context.Context, c *biz.CheckpointCloneCommand) {
			c.SourceExpectedVersion = 2
		}, biz.ErrCloneRejectedVersion},
		{"checkpoint_not_ready", func(t *testing.T, f *sandboxFixture, ctx context.Context, c *biz.CheckpointCloneCommand) {
			if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.checkpoints SET state='failed' WHERE tenant_id=$1 AND checkpoint_id=$2`, c.Transfer.TenantID, c.Transfer.CheckpointID); err != nil {
				t.Fatal(err)
			}
		}, biz.ErrCloneRejectedConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, source, _, cmd := directCloneFixture(t, f)
			tc.prepare(t, f, ctx, &cmd)
			var a, b error
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); <-start; _, a = f.repo.AcceptCheckpointClone(ctx, cmd) }()
			go func() { defer wg.Done(); <-start; _, b = f.repo.RejectCheckpointCloneWFFC(ctx, cmd) }()
			close(start)
			wg.Wait()
			if !errors.Is(a, tc.want) || !errors.Is(b, tc.want) {
				t.Fatalf("concurrent receipts: accept=%v reject=%v want=%v", a, b, tc.want)
			}
			if _, err := f.repo.LookupCheckpointClone(ctx, cmd); !errors.Is(err, tc.want) {
				t.Fatalf("lookup after lost response: %v", err)
			}
			if _, err := f.repo.AcceptCheckpointClone(ctx, cmd); !errors.Is(err, tc.want) {
				t.Fatalf("replay: %v", err)
			}
			changed := cmd
			changed.Sandbox.RequestDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			if _, err := f.repo.AcceptCheckpointClone(ctx, changed); !errors.Is(err, biz.ErrCloneRejectedConflict) {
				t.Fatalf("changed command reused rejection: %v", err)
			}
			if _, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID); !errors.Is(err, biz.ErrNotFound) {
				t.Fatalf("rejected target exists: %v", err)
			}
		})
	}
}

func TestClonePreflightRejectionSerializesWithAcceptance(t *testing.T) {
	f := sandboxDatabase(t)
	for i := 0; i < 16; i++ {
		ctx, source, _, cmd := directCloneFixture(t, f)
		var acceptErr, rejectErr error
		var winner biz.CheckpointTransfer
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, acceptErr = f.repo.AcceptCheckpointClone(ctx, cmd) }()
		go func() { defer wg.Done(); <-start; winner, rejectErr = f.repo.RejectCheckpointClonePreflight(ctx, cmd) }()
		close(start)
		wg.Wait()
		if acceptErr == nil {
			if rejectErr != nil || !winner.Replayed || winner.TargetSandboxID != cmd.Sandbox.ID {
				t.Fatalf("accepted winner lost: %v %+v", rejectErr, winner)
			}
			if _, err := f.repo.LookupCheckpointClone(ctx, cmd); err != nil {
				t.Fatal(err)
			}
		} else {
			if !errors.Is(acceptErr, biz.ErrCloneRejectedConflict) || !errors.Is(rejectErr, biz.ErrCloneRejectedConflict) {
				t.Fatalf("rejected winner mismatch: %v %v", acceptErr, rejectErr)
			}
			if _, err := f.repo.LookupCheckpointClone(ctx, cmd); !errors.Is(err, biz.ErrCloneRejectedConflict) {
				t.Fatalf("lost response lookup: %v", err)
			}
			if _, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID); !errors.Is(err, biz.ErrNotFound) {
				t.Fatalf("target created: %v", err)
			}
		}
		if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at='infinity' WHERE tenant_id=$1`, source.TenantID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloneIdentityConflictReceiptPreservesOriginalReplay(t *testing.T) {
	f := sandboxDatabase(t)
	for _, kind := range []string{"target", "operation", "accepted_clone_digest"} {
		t.Run(kind, func(t *testing.T) {
			ctx, source, _, cmd := directCloneFixture(t, f)
			defer func() {
				if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at='infinity' WHERE tenant_id=$1`, source.TenantID); err != nil {
					t.Error(err)
				}
			}()
			if kind == "accepted_clone_digest" {
				original, err := f.repo.AcceptCheckpointClone(ctx, cmd)
				if err != nil {
					t.Fatal(err)
				}
				changed := cmd
				changed.Sandbox.RequestDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				if _, err = f.repo.RejectCheckpointClonePreflight(ctx, changed); !errors.Is(err, biz.ErrCloneRejectedConflict) {
					t.Fatalf("changed digest: %v", err)
				}
				if _, err = f.repo.LookupCheckpointClone(ctx, changed); !errors.Is(err, biz.ErrCloneRejectedConflict) {
					t.Fatalf("conflict replay: %v", err)
				}
				replay, err := f.repo.AcceptCheckpointClone(ctx, cmd)
				if err != nil || !replay.Replayed || replay.ID != original.ID {
					t.Fatalf("original clone replay: %+v %v", replay, err)
				}
				otherKey := cmd
				otherKey.Transfer.IdempotencyKey = "same-digest-different-key"
				aDigest, err := biz.CheckpointCloneDigest(cmd)
				if err != nil {
					t.Fatal(err)
				}
				bDigest, err := biz.CheckpointCloneDigest(otherKey)
				if err != nil || aDigest != bDigest {
					t.Fatalf("fixture digest mismatch: %v", err)
				}
				if _, err = f.repo.AcceptCheckpointClone(ctx, otherKey); !errors.Is(err, biz.ErrCloneRejectedConflict) {
					t.Fatalf("other key should conflict: %v", err)
				}
				if _, err = f.repo.LookupCheckpointClone(ctx, otherKey); !errors.Is(err, biz.ErrCloneRejectedConflict) {
					t.Fatalf("other key conflict replay: %v", err)
				}
				lookedUp, err := f.repo.LookupCheckpointClone(ctx, cmd)
				if err != nil || !lookedUp.Replayed || lookedUp.ID != original.ID {
					t.Fatalf("other key poisoned original lookup: %+v %v", lookedUp, err)
				}
				replay, err = f.repo.AcceptCheckpointClone(ctx, cmd)
				if err != nil || !replay.Replayed || replay.ID != original.ID {
					t.Fatalf("other key poisoned original replay: %+v %v", replay, err)
				}
			} else {
				existing := cmd.Sandbox
				if kind == "operation" {
					existing.ID = uuid.NewString()
					existing.OperationID = cmd.Transfer.OperationID
				}
				if _, err := f.repo.AcceptCreate(ctx, existing); err != nil {
					t.Fatal(err)
				}
				if _, err := f.repo.RejectCheckpointClonePreflight(ctx, cmd); !errors.Is(err, biz.ErrCloneRejectedConflict) {
					t.Fatalf("occupied identity: %v", err)
				}
				if _, err := f.repo.LookupCheckpointClone(ctx, cmd); !errors.Is(err, biz.ErrCloneRejectedConflict) {
					t.Fatalf("conflict lookup: %v", err)
				}
				// Digest intentionally excludes this key, so an exact receipt
				// must separately compare it and not match another command.
				changedKey := cmd
				changedKey.Transfer.IdempotencyKey = "other-key"
				firstDigest, err := biz.CheckpointCloneDigest(cmd)
				if err != nil {
					t.Fatal(err)
				}
				otherDigest, err := biz.CheckpointCloneDigest(changedKey)
				if err != nil || firstDigest != otherDigest {
					t.Fatalf("fixture digest mismatch: %v", err)
				}
				if _, err = f.repo.LookupCheckpointClone(ctx, changedKey); !errors.Is(err, biz.ErrNotFound) {
					t.Fatalf("different key matched exact receipt: %v", err)
				}
				if _, err := f.repo.AcceptCheckpointClone(ctx, cmd); !errors.Is(err, biz.ErrCloneRejectedConflict) {
					t.Fatalf("conflict replay: %v", err)
				}
				replay, err := f.repo.AcceptCreate(ctx, existing)
				if err != nil || !replay.Replayed {
					t.Fatalf("original create replay: %+v %v", replay, err)
				}
			}
		})
	}
}
