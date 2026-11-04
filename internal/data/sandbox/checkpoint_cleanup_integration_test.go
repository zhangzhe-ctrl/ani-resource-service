//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func TestCheckpointCleanupLedgerFencesCloneAndParent(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, checkpoint, clone := directCloneFixture(t, f)
	if _, err := f.repo.AcceptCheckpointClone(ctx, clone); err != nil {
		t.Fatal(err)
	}
	var err error
	del := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: "delete", IdempotencyKey: "delete", RequestDigest: biz.ActionDigest("", 1, "delete"), ExpectedVersion: 1}
	if _, err = f.repo.Change(ctx, del); !errors.Is(err, biz.ErrCheckpointReferenced) {
		t.Fatalf("delete ignored outstanding clone reference: %v", err)
	}
	// The independent clone target and source both exist in the tenant, but
	// the checkpoint is owned by the source alone.
	other := clone.Sandbox
	_, err = f.runtime.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET source_sandbox_id=$1 WHERE tenant_id=$2 AND checkpoint_id=$3`, other.ID, c.TenantID, checkpoint.ID)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.Code != "23503" {
		t.Fatalf("cross-sandbox source did not violate ownership FK: %v", err)
	}
	if _, err = f.repo.Change(ctx, del); !errors.Is(err, biz.ErrCheckpointReferenced) {
		t.Fatalf("delete ignored reference after rejected mismatch: %v", err)
	}
	// A definitively not-sent first mutation proves no target volume was
	// created and releases the reference through the production journal.
	transfer, err := f.repo.ClaimCheckpointTransfer(ctx, "clone", time.Minute)
	if err != nil || transfer == nil {
		t.Fatalf("clone claim: %+v %v", transfer, err)
	}
	mutation := uuid.NewString()
	if send, err := f.repo.PrepareCheckpointTransferCreate(ctx, *transfer, mutation); err != nil || !send {
		t.Fatalf("clone prepare: %v %v", send, err)
	}
	if err = f.repo.FinishCheckpointTransferCreate(ctx, *transfer, mutation, biz.MutationNotSent, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at=clock_timestamp()+interval '1 hour' WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Change(ctx, del); err != nil {
		t.Fatal(err)
	}
	if work, err := f.repo.Claim(ctx, "ordinary", time.Minute); err != nil || work != nil {
		t.Fatalf("parent ran before cleanup: %+v %v", work, err)
	}
	w, err := f.repo.ClaimCheckpointCleanup(ctx, "cleanup", time.Minute)
	if err != nil || w == nil {
		t.Fatalf("claim: %+v %v", w, err)
	}
	foreignWork := *w
	foreignWork.Sandbox.ID = uuid.NewString()
	if _, err = f.repo.PrepareCheckpointCleanup(ctx, foreignWork, uuid.NewString()); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("mismatched sandbox work accepted: %v", err)
	}
	mid := uuid.NewString()
	dispatch, err := f.repo.PrepareCheckpointCleanup(ctx, *w, mid)
	if err != nil || !dispatch {
		t.Fatalf("prepare=%v err=%v", dispatch, err)
	}
	if err = f.repo.FinishCheckpointCleanup(ctx, *w, mid, biz.MutationUnknown); err != nil {
		t.Fatal(err)
	}
	for _, late := range []string{biz.MutationNotSent, biz.MutationRejected} {
		if err = f.repo.FinishCheckpointCleanup(ctx, *w, mid, late); err != nil {
			t.Fatal(err)
		}
		var state, journal string
		if err = f.runtime.QueryRow(ctx, `SELECT cleanup_state,cleanup_mutation_id FROM sandbox.checkpoints WHERE tenant_id=$1 AND checkpoint_id=$2`, c.TenantID, checkpoint.ID).Scan(&state, &journal); err != nil || state != "unknown" || journal != mid {
			t.Fatalf("late %s cleared unknown: state=%s journal=%s err=%v", late, state, journal, err)
		}
	}
	if err = f.repo.ReleaseCheckpointCleanup(ctx, *w, time.Microsecond); err != nil {
		t.Fatal(err)
	}
	w, err = f.repo.ClaimCheckpointCleanup(ctx, "recovery", time.Minute)
	if err != nil || w == nil || w.MutationID != mid {
		t.Fatalf("recovery=%+v err=%v", w, err)
	}
	stale := *w
	stale.Epoch--
	if err = f.repo.FinishCheckpointCleanup(ctx, stale, mid, biz.MutationApplied); !errors.Is(err, biz.ErrLease) {
		t.Fatalf("stale worker completed cleanup: %v", err)
	}
	if again, err := f.repo.PrepareCheckpointCleanup(ctx, *w, uuid.NewString()); err == nil || again {
		t.Fatal("unknown cleanup dispatched again")
	}
	if err = f.repo.FinishCheckpointCleanup(ctx, *w, mid, biz.MutationApplied); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.ReleaseCheckpointCleanup(ctx, *w, time.Microsecond); err != nil {
		t.Fatal(err)
	}
	if work, err := f.repo.Claim(ctx, "ordinary", time.Minute); err != nil || work == nil {
		t.Fatalf("parent not released: %+v %v", work, err)
	}
}

type restartCheckpointCleanupProvider struct {
	deleted bool
	calls   int
	uid     string
}

func (p *restartCheckpointCleanupProvider) DeleteCheckpointSnapshot(_ context.Context, _ biz.Sandbox, checkpoint biz.Checkpoint) (string, error) {
	p.calls++
	if checkpoint.SnapshotUID != p.uid {
		return biz.MutationNotSent, biz.ErrOwnership
	}
	p.deleted = true
	return biz.MutationApplied, nil
}
func (p *restartCheckpointCleanupProvider) ObserveCheckpointCleanup(_ context.Context, _ biz.Sandbox, checkpoint biz.Checkpoint) (bool, error) {
	if checkpoint.SnapshotUID != p.uid {
		return false, biz.ErrOwnership
	}
	return p.deleted, nil
}

func TestCheckpointCleanupRestartAfterPreparedDelete(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, cp, _ := directCloneFixture(t, f)
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET desired='deleted' WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	w, err := f.repo.ClaimCheckpointCleanup(ctx, "crashed", time.Minute)
	if err != nil || w == nil {
		t.Fatalf("claim: %+v %v", w, err)
	}
	id := uuid.NewString()
	if send, err := f.repo.PrepareCheckpointCleanup(ctx, *w, id); err != nil || !send {
		t.Fatalf("prepare: %v %v", send, err)
	}
	// Process died here: no provider call and no completion receipt.
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.checkpoints SET lease_until='-infinity',due_at=clock_timestamp() WHERE tenant_id=$1 AND checkpoint_id=$2`, c.TenantID, cp.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := f.repo.ClaimCheckpointCleanup(ctx, "recovery-check", time.Minute)
	if err != nil || recovered == nil || recovered.MutationID != id || recovered.Outcome != "unknown" {
		t.Fatalf("takeover must fence prepared deletion: %+v %v", recovered, err)
	}
	for _, late := range []string{biz.MutationNotSent, biz.MutationRejected} {
		if err = f.repo.FinishCheckpointCleanup(ctx, *recovered, id, late); err != nil {
			t.Fatal(err)
		}
		var state, journal string
		if err = f.runtime.QueryRow(ctx, `SELECT cleanup_state,cleanup_mutation_id FROM sandbox.checkpoints WHERE tenant_id=$1 AND checkpoint_id=$2`, c.TenantID, cp.ID).Scan(&state, &journal); err != nil || state != "unknown" || journal != id {
			t.Fatalf("late %s cleared takeover barrier: state=%s journal=%s err=%v", late, state, journal, err)
		}
	}
	if err = f.repo.ReleaseCheckpointCleanup(ctx, *recovered, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	provider := &restartCheckpointCleanupProvider{uid: cp.SnapshotUID}
	worker, err := biz.NewCheckpointCleanupWorker(f.repo, provider, "restarted", time.Minute, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := worker.Step(ctx); err != nil || !worked || provider.calls != 1 {
		t.Fatalf("retry: %v calls=%d err=%v", worked, provider.calls, err)
	}
	if worked, err := worker.Step(ctx); err != nil || !worked {
		t.Fatalf("confirm: %v %v", worked, err)
	}
	var state string
	if err := f.runtime.QueryRow(ctx, `SELECT cleanup_state FROM sandbox.checkpoints WHERE tenant_id=$1 AND checkpoint_id=$2`, c.TenantID, cp.ID).Scan(&state); err != nil || state != "done" {
		t.Fatalf("cleanup=%s err=%v", state, err)
	}
}
