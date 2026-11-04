//go:build sandboxintegration

package sandbox_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

// Exercises all three persistent fences with a simulated provider. Kubernetes
// and CSI receipts are separately covered by the provider tests.
func TestCheckpointDeletionOrdersSnapshotPodRetiredAndCurrentWorkspace(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, cmd := runningCheckpointFixture(t, f)
	cp, err := f.repo.AcceptCheckpoint(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.checkpoints SET state='ready',phase='done',snapshot_uid='snapshot',content_name='content-name',content_uid='content',snapshot_handle='handle' WHERE tenant_id=$1 AND checkpoint_id=$2`, c.TenantID, cp.ID); err != nil {
		t.Fatal(err)
	}
	s, err := f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	retired := s.ProviderRefs
	retired.PVCUID, retired.PVUID = uuid.NewString(), uuid.NewString()
	raw, err := json.Marshal(retired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `INSERT INTO sandbox.workspace_generations(tenant_id,sandbox_id,generation_id,pvc_name,pvc_uid,pv_name,pv_uid,state,provider_refs) VALUES($1,$2,$3,$4,$5,$6,$7,'retired',$8)`, c.TenantID, c.ID, uuid.NewString(), retired.PVCName, retired.PVCUID, retired.PVName, retired.PVUID, raw); err != nil {
		t.Fatal(err)
	}
	del := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: "delete", IdempotencyKey: "delete", RequestDigest: biz.ActionDigest("", 1, "delete"), ExpectedVersion: 1}
	if _, err = f.repo.Change(ctx, del); err != nil {
		t.Fatal(err)
	}
	if work, err := f.repo.Claim(ctx, "before-checkpoint-cleanup", time.Minute); err != nil || work != nil {
		t.Fatalf("lifecycle crossed snapshot fence: %+v %v", work, err)
	}
	cleanup, err := f.repo.ClaimCheckpointCleanup(ctx, "checkpoint-cleanup", time.Minute)
	if err != nil || cleanup == nil {
		t.Fatalf("checkpoint claim: %+v %v", cleanup, err)
	}
	mutationID := uuid.NewString()
	if send, err := f.repo.PrepareCheckpointCleanup(ctx, *cleanup, mutationID); err != nil || !send {
		t.Fatalf("checkpoint prepare: %v %v", send, err)
	}
	// Simulate the checkpoint adapter's confirmed deletion receipt.
	if err = f.repo.FinishCheckpointCleanup(ctx, *cleanup, mutationID, biz.MutationApplied); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.ReleaseCheckpointCleanup(ctx, *cleanup, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	work := claimWork(t, f, ctx, "pod-delete")
	pod := biz.Mutation{ID: uuid.NewString(), Kind: biz.MutationDeletePod, OperationID: work.Sandbox.LastOperationID, Version: work.Sandbox.Version, TargetUID: work.Sandbox.ProviderRefs.PodUID}
	if err = f.repo.PrepareMutation(ctx, *work, pod); err != nil {
		t.Fatalf("retired generation incorrectly blocked Pod delete: %v", err)
	}
	refs := work.Sandbox.ProviderRefs
	refs.PodUID = ""
	progress := work.Sandbox
	progress.Observed = biz.ObservedUnknown
	if err = f.repo.Finish(ctx, *work, biz.Progress{Sandbox: progress, Next: time.Nanosecond, Receipt: &biz.MutationResult{Mutation: pod, Outcome: biz.MutationApplied, Refs: refs}}); err != nil {
		t.Fatal(err)
	}
	work = claimWork(t, f, ctx, "current-workspace-blocked")
	workspace := biz.Mutation{ID: uuid.NewString(), Kind: biz.MutationDeleteWorkspace, OperationID: work.Sandbox.LastOperationID, Version: work.Sandbox.Version, TargetUID: work.Sandbox.ProviderRefs.PVCUID}
	if err = f.repo.PrepareMutation(ctx, *work, workspace); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("current workspace crossed retired-generation fence: %v", err)
	}
	if err = f.repo.Finish(ctx, *work, biz.Progress{Sandbox: work.Sandbox, Next: time.Nanosecond}); err != nil {
		t.Fatal(err)
	}
	provider := &generationCleanupTestProvider{pvc: true, pv: true}
	generationWorker, err := biz.NewWorkspaceGenerationCleanupWorker(f.repo, provider, "generation-cleanup", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	generationCleanupStep(t, f, ctx, c, generationWorker)
	generationCleanupStep(t, f, ctx, c, generationWorker)
	provider.pvc, provider.pv = false, false
	generationCleanupStep(t, f, ctx, c, generationWorker)
	work = claimWork(t, f, ctx, "current-workspace-delete")
	workspace = biz.Mutation{ID: uuid.NewString(), Kind: biz.MutationDeleteWorkspace, OperationID: work.Sandbox.LastOperationID, Version: work.Sandbox.Version, TargetUID: work.Sandbox.ProviderRefs.PVCUID}
	if err = f.repo.PrepareMutation(ctx, *work, workspace); err != nil {
		t.Fatalf("current workspace still fenced after reclaim: %v", err)
	}
}
