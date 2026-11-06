//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

type failedCloneLifecycleProvider struct{ calls int }

func (*failedCloneLifecycleProvider) Observe(_ context.Context, s biz.Sandbox) (biz.Observation, error) {
	return biz.Observation{Observed: biz.ObservedPaused, ProviderRefs: s.ProviderRefs, At: time.Now()}, nil
}
func (p *failedCloneLifecycleProvider) Apply(_ context.Context, _ biz.Sandbox, m biz.Mutation) (biz.MutationResult, error) {
	p.calls++
	return biz.MutationResult{Mutation: m, Outcome: biz.MutationApplied}, nil
}
func resumeClone(s biz.Sandbox) biz.Command {
	return biz.Command{TenantID: s.TenantID, ID: s.ID, OperationID: uuid.NewString(), Action: "resume", IdempotencyKey: uuid.NewString(), ExpectedVersion: s.Version, RequestDigest: biz.ActionDigest("", s.Version, "resume")}
}

func TestFailedCloneNeverStartsEmptyAndStillDeletes(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, source, _, cmd := directCloneFixture(t, f)
	if _, err := f.repo.AcceptCheckpointClone(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	transfer, err := f.repo.ClaimCheckpointTransfer(ctx, "clone", time.Minute)
	if err != nil || transfer == nil {
		t.Fatalf("claim %+v %v", transfer, err)
	}
	id := uuid.NewString()
	if send, err := f.repo.PrepareCheckpointTransferCreate(ctx, *transfer, id); err != nil || !send {
		t.Fatalf("prepare %v %v", send, err)
	}
	if err = f.repo.FinishCheckpointTransferCreate(ctx, *transfer, id, biz.MutationRejected, ""); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.ReleaseCheckpointTransferLease(ctx, *transfer, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	target, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Change(ctx, resumeClone(target)); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("failed clone resumed: %v", err)
	}
	pause := biz.Command{TenantID: target.TenantID, ID: target.ID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: uuid.NewString(), ExpectedVersion: target.Version, RequestDigest: biz.ActionDigest("", target.Version, "pause")}
	if _, err = f.repo.Change(ctx, pause); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("failed clone accepted pause: %v", err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, target.TenantID, target.ID); err != nil {
		t.Fatal(err)
	}
	provider := &failedCloneLifecycleProvider{}
	worker, err := biz.NewWorker(f.repo, provider, "lifecycle", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := worker.Step(ctx); err != nil || !worked {
		t.Fatalf("failed clone observation did not settle: %v %v", worked, err)
	}
	op, err := f.repo.GetOperation(ctx, target.TenantID, cmd.Transfer.OperationID)
	if err != nil || op.State != "failed" {
		t.Fatalf("clone failure overwritten: %+v %v", op, err)
	}
	target, err = f.repo.Get(ctx, target.TenantID, target.ID)
	if err != nil || target.Observed != biz.ObservedUnknown || target.Reason != "SANDBOX_TRANSFER_CREATE_REJECTED" {
		t.Fatalf("failed state lost: %+v %v", target, err)
	}
	// Simulate stale/legacy running intent and ensure the provider cannot create
	// a fresh empty PVC even when its observation reports no workspace.
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET desired='running' WHERE tenant_id=$1 AND sandbox_id=$2`, target.TenantID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, target.TenantID, target.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := worker.Step(ctx); !worked || !errors.Is(err, biz.ErrConflict) || provider.calls != 0 {
		t.Fatalf("empty create crossed fence: worked=%v err=%v calls=%d", worked, err, provider.calls)
	}
	// A rejected worker preparation created no pending write; delete stays usable.
	target, err = f.repo.Get(ctx, target.TenantID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Change(ctx, deleteCommand(target)); err != nil {
		t.Fatalf("delete failed clone: %v", err)
	}
}

func TestUnknownCloneLateReceiptAndValidatedResume(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, source, _, cmd := directCloneFixture(t, f)
	if _, err := f.repo.AcceptCheckpointClone(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	w, err := f.repo.ClaimCheckpointTransfer(ctx, "clone", time.Minute)
	if err != nil || w == nil {
		t.Fatalf("claim %+v %v", w, err)
	}
	id := uuid.NewString()
	if send, err := f.repo.PrepareCheckpointTransferCreate(ctx, *w, id); err != nil || !send {
		t.Fatalf("prepare %v %v", send, err)
	}
	if err = f.repo.FinishCheckpointTransferCreate(ctx, *w, id, biz.MutationUnknown, ""); err != nil {
		t.Fatal(err)
	}
	target, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Change(ctx, resumeClone(target)); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("unknown clone resumed: %v", err)
	}
	if _, err = f.repo.Change(ctx, deleteCommand(target)); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("unknown clone delete crossed fence: %v", err)
	}
	if err = f.repo.FinishCheckpointTransferCreate(ctx, *w, id, biz.MutationRejected, ""); err != nil {
		t.Fatal(err)
	}
	assertSourceReference(t, f, ctx, source.TenantID, cmd.Transfer.TransferID, false)
	pvcUID, pvUID := uuid.NewString(), uuid.NewString()
	if err = f.repo.FinishCheckpointTransferCreate(ctx, *w, id, biz.MutationApplied, pvcUID); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.ReleaseCheckpointTransferLease(ctx, *w, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	provider := &transferTestProvider{visible: true, bound: true, uid: pvcUID, pvUID: pvUID}
	worker, err := biz.NewCheckpointTransferWorker(f.repo, provider, "recovery", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	transferStep(t, f, ctx, cmd.Sandbox, worker)
	target, err = f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID)
	if err != nil || target.ProviderRefs.PVCUID != pvcUID {
		t.Fatalf("late receipt did not commit: %+v %v", target, err)
	}
	// Tampering a persisted identity must not authorize starting the clone.
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET provider_refs=jsonb_set(provider_refs,'{PVCSourceSnapshotUID}',to_jsonb('wrong'::text)) WHERE tenant_id=$1 AND sandbox_id=$2`, target.TenantID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Change(ctx, resumeClone(target)); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("mismatched source resumed: %v", err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET provider_refs=jsonb_set(provider_refs,'{PVCSourceSnapshotUID}',to_jsonb($3::text)) WHERE tenant_id=$1 AND sandbox_id=$2`, target.TenantID, target.ID, target.ProviderRefs.PVCSourceSnapshotUID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Change(ctx, resumeClone(target)); err != nil {
		t.Fatalf("valid committed clone did not resume: %v", err)
	}
}

type wffcTransferProvider struct{}

func (wffcTransferProvider) CreateCheckpointTransferPVC(context.Context, biz.Sandbox, biz.CheckpointTransfer, string) (string, string, error) {
	return biz.MutationNotSent, "", biz.ErrCheckpointWFFCUnsupported
}
func (wffcTransferProvider) ObserveCheckpointTransferPVC(context.Context, biz.Sandbox, biz.CheckpointTransfer) (biz.CheckpointTransferObservation, error) {
	panic("no WFFC PVC was sent")
}
func TestCloneWFFCDriftFailsClearlyAndReleasesUnsentReference(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, source, _, cmd := directCloneFixture(t, f)
	if _, err := f.repo.AcceptCheckpointClone(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	worker, err := biz.NewCheckpointTransferWorker(f.repo, wffcTransferProvider{}, "wffc", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	transferStep(t, f, ctx, cmd.Sandbox, worker)
	op, err := f.repo.GetOperation(ctx, source.TenantID, cmd.Transfer.OperationID)
	if err != nil || op.State != "failed" || op.Reason != "SANDBOX_TRANSFER_WFFC_UNSUPPORTED" {
		t.Fatalf("operation %+v %v", op, err)
	}
	assertSourceReference(t, f, ctx, source.TenantID, cmd.Transfer.TransferID, true)
	target, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Change(ctx, resumeClone(target)); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("unsupported clone resumed: %v", err)
	}
}
