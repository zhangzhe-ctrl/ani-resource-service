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

type reclaimedLifecycleProvider struct{}

func (reclaimedLifecycleProvider) Observe(_ context.Context, s biz.Sandbox) (biz.Observation, error) {
	return biz.Observation{Observed: biz.ObservedDeleted, ProviderRefs: s.ProviderRefs, At: time.Now()}, nil
}
func (reclaimedLifecycleProvider) Apply(context.Context, biz.Sandbox, biz.Mutation) (biz.MutationResult, error) {
	panic("no provider mutation expected after reclaim")
}

func assertSourceReference(t *testing.T, f *sandboxFixture, ctx context.Context, tenant, id string, released bool) {
	t.Helper()
	var got bool
	if err := f.runtime.QueryRow(ctx, `SELECT source_released FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND transfer_id=$2`, tenant, id).Scan(&got); err != nil || got != released {
		t.Fatalf("source released=%v want=%v err=%v", got, released, err)
	}
}
func deleteCommand(s biz.Sandbox) biz.Command {
	return biz.Command{TenantID: s.TenantID, ID: s.ID, OperationID: uuid.NewString(), Action: "delete", IdempotencyKey: "delete", ExpectedVersion: s.Version, RequestDigest: biz.ActionDigest("", s.Version, "delete")}
}
func finishReclaimedLifecycle(t *testing.T, f *sandboxFixture, ctx context.Context, s biz.Sandbox, op string) {
	t.Helper()
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, s.TenantID, s.ID); err != nil {
		t.Fatal(err)
	}
	worker, err := biz.NewWorker(f.repo, reclaimedLifecycleProvider{}, "finish", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := worker.Step(ctx)
	if err != nil || !worked {
		t.Fatalf("final worker %v %v", worked, err)
	}
	got, err := f.repo.GetOperation(ctx, s.TenantID, op)
	if err != nil || got.State != "succeeded" {
		t.Fatalf("delete result %+v %v", got, err)
	}
	var receipt string
	if err = f.runtime.QueryRow(ctx, `SELECT cleanup_receipt FROM sandbox.workspace_bindings WHERE tenant_id=$1 AND sandbox_id=$2`, s.TenantID, s.ID).Scan(&receipt); err != nil || receipt == "" {
		t.Fatalf("workspace receipt %q %v", receipt, err)
	}
}

func TestSelfRestoreDeleteReclaimsBothDisksBeforeReleasingSnapshot(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, cp, _ := transferFixture(t, f)
	command := biz.CheckpointTransferCommand{TenantID: c.TenantID, TransferID: uuid.NewString(), OperationID: uuid.NewString(), CheckpointID: cp.ID, SourceSandboxID: c.ID, TargetSandboxID: c.ID, Kind: "restore", IdempotencyKey: "restore", ExpectedVersion: 1}
	if _, err := f.repo.AcceptCheckpointTransfer(ctx, command); err != nil {
		t.Fatal(err)
	}
	provider := &transferTestProvider{visible: true, bound: true, uid: uuid.NewString(), pvUID: uuid.NewString()}
	transfer, err := biz.NewCheckpointTransferWorker(f.repo, provider, "transfer", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		transferStep(t, f, ctx, c, transfer)
	}
	s, err := f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSourceReference(t, f, ctx, c.TenantID, command.TransferID, false)
	del := deleteCommand(s)
	if _, err = f.repo.Change(ctx, del); err != nil {
		t.Fatalf("self reference prevented delete: %v", err)
	}
	// A still-live Pod must be removable even while checkpoint cleanup waits.
	work := claimWork(t, f, ctx, "self-pod")
	pod := biz.Mutation{ID: uuid.NewString(), Kind: biz.MutationDeletePod, OperationID: work.Sandbox.LastOperationID, Version: work.Sandbox.Version, TargetUID: "pod-uid"}
	if err = f.repo.PrepareMutation(ctx, *work, pod); err != nil {
		t.Fatalf("pod delete fenced: %v", err)
	}
	if err = f.repo.Finish(ctx, *work, biz.Progress{Sandbox: work.Sandbox, Next: time.Nanosecond, Receipt: &biz.MutationResult{Mutation: pod, Outcome: biz.MutationApplied}}); err != nil {
		t.Fatal(err)
	}
	cleanup := &generationCleanupTestProvider{pvc: true, pv: true}
	worker, err := biz.NewWorkspaceGenerationCleanupWorker(f.repo, cleanup, "generation", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		generationCleanupStep(t, f, ctx, c, worker)
	} // old disk provenance + unknown delete
	cleanup.pvc, cleanup.pv = false, false
	generationCleanupStep(t, f, ctx, c, worker)
	assertSourceReference(t, f, ctx, c.TenantID, command.TransferID, false)
	cleanup.pvc, cleanup.pv = true, true
	for range 2 {
		generationCleanupStep(t, f, ctx, c, worker)
	} // active disk provenance + unknown delete
	cleanup.pvc = false
	generationCleanupStep(t, f, ctx, c, worker) // still-live PV never releases
	assertSourceReference(t, f, ctx, c.TenantID, command.TransferID, false)
	cleanup.pv = false
	generationCleanupStep(t, f, ctx, c, worker)
	assertSourceReference(t, f, ctx, c.TenantID, command.TransferID, true)
	if work, err := f.repo.Claim(ctx, "before-checkpoint-cleanup", time.Minute); err != nil || work != nil {
		t.Fatalf("checkpoint skipped: %+v %v", work, err)
	}
	ck, err := f.repo.ClaimCheckpointCleanup(ctx, "checkpoint", time.Minute)
	if err != nil || ck == nil {
		t.Fatalf("checkpoint claim %+v %v", ck, err)
	}
	id := uuid.NewString()
	if send, err := f.repo.PrepareCheckpointCleanup(ctx, *ck, id); err != nil || !send {
		t.Fatalf("checkpoint prep %v %v", send, err)
	}
	if err = f.repo.FinishCheckpointCleanup(ctx, *ck, id, biz.MutationApplied); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.ReleaseCheckpointCleanup(ctx, *ck, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	finishReclaimedLifecycle(t, f, ctx, s, del.OperationID)
}

func TestCloneTargetReclaimReleasesSourceWhileDeleteRaces(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, source, _, cmd := directCloneFixture(t, f)
	if _, err := f.repo.AcceptCheckpointClone(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	provider := &transferTestProvider{visible: true, bound: true, uid: uuid.NewString(), pvUID: uuid.NewString()}
	transfer, err := biz.NewCheckpointTransferWorker(f.repo, provider, "transfer", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		transferStep(t, f, ctx, cmd.Sandbox, transfer)
	}
	target, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	src, err := f.repo.Get(ctx, source.TenantID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	srcDel := deleteCommand(src)
	if _, err = f.repo.Change(ctx, srcDel); !errors.Is(err, biz.ErrCheckpointReferenced) {
		t.Fatalf("source not protected: %v", err)
	}
	del := deleteCommand(target)
	if _, err = f.repo.Change(ctx, del); err != nil {
		t.Fatal(err)
	}
	cleanup := &generationCleanupTestProvider{pvc: true, pv: true}
	worker, err := biz.NewWorkspaceGenerationCleanupWorker(f.repo, cleanup, "generation", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		generationCleanupStep(t, f, ctx, cmd.Sandbox, worker)
	}
	cleanup.pvc = false
	generationCleanupStep(t, f, ctx, cmd.Sandbox, worker)
	if _, err = f.repo.Change(ctx, srcDel); !errors.Is(err, biz.ErrCheckpointReferenced) {
		t.Fatalf("source released before PV: %v", err)
	}
	cleanup.pv = false
	done := make(chan error, 1)
	go func() { _, e := f.repo.Change(ctx, srcDel); done <- e }()
	generationCleanupStep(t, f, ctx, cmd.Sandbox, worker)
	racingErr := <-done
	if racingErr != nil && !errors.Is(racingErr, biz.ErrCheckpointReferenced) {
		t.Fatalf("race delete: %v", racingErr)
	}
	assertSourceReference(t, f, ctx, source.TenantID, cmd.Transfer.TransferID, true)
	if racingErr != nil {
		if _, err = f.repo.Change(ctx, srcDel); err != nil {
			t.Fatalf("source still blocked after reclaim: %v", err)
		}
	}
	finishReclaimedLifecycle(t, f, ctx, target, del.OperationID)
}

func TestCloneReferenceReleaseOnlyForCertainFirstWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		name, outcome string
		release       bool
	}{
		{"not_sent", biz.MutationNotSent, true},
		{"rejected", biz.MutationRejected, true},
		{"unknown", biz.MutationUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := sandboxDatabase(t)
			ctx, source, _, cmd := directCloneFixture(t, f)
			if _, err := f.repo.AcceptCheckpointClone(ctx, cmd); err != nil {
				t.Fatal(err)
			}
			w, err := f.repo.ClaimCheckpointTransfer(ctx, "worker", time.Minute)
			if err != nil || w == nil {
				t.Fatalf("claim %+v %v", w, err)
			}
			mutation := uuid.NewString()
			if send, err := f.repo.PrepareCheckpointTransferCreate(ctx, *w, mutation); err != nil || !send {
				t.Fatalf("prepare %v %v", send, err)
			}
			if err = f.repo.FinishCheckpointTransferCreate(ctx, *w, mutation, tc.outcome, ""); err != nil {
				t.Fatal(err)
			}
			assertSourceReference(t, f, ctx, source.TenantID, cmd.Transfer.TransferID, tc.release)
			if !tc.release {
				// A later rejection cannot erase a possibly delivered earlier write.
				if err = f.repo.FinishCheckpointTransferCreate(ctx, *w, mutation, biz.MutationRejected, ""); err != nil {
					t.Fatal(err)
				}
				assertSourceReference(t, f, ctx, source.TenantID, cmd.Transfer.TransferID, false)
				s, err := f.repo.Get(ctx, source.TenantID, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.repo.Change(ctx, deleteCommand(s)); !errors.Is(err, biz.ErrCheckpointReferenced) {
					t.Fatalf("unknown clone allowed source delete: %v", err)
				}
			}
		})
	}
}
