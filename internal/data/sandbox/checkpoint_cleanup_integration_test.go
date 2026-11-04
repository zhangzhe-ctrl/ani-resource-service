//go:build sandboxintegration

package sandbox_test

import (
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
