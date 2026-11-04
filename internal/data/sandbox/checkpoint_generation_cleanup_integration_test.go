//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

type generationCleanupTestProvider struct {
	pvc, pv bool
	calls   int
}

func (p *generationCleanupTestProvider) ObserveWorkspaceGeneration(_ context.Context, _ biz.Sandbox, _ biz.WorkspaceGenerationCleanup) (biz.WorkspaceGenerationObservation, error) {
	return biz.WorkspaceGenerationObservation{PVCExists: p.pvc, PVExists: p.pv, Driver: "test.csi", VolumeHandle: "volume-handle"}, nil
}
func (p *generationCleanupTestProvider) DeleteWorkspaceGenerationPVC(_ context.Context, _ biz.Sandbox, _ biz.WorkspaceGenerationCleanup, _ string) (string, error) {
	p.calls++
	return biz.MutationUnknown, nil
}
func generationCleanupStep(t *testing.T, f *sandboxFixture, ctx context.Context, c biz.Create, w *biz.WorkspaceGenerationCleanupWorker) {
	t.Helper()
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.workspace_generations SET cleanup_due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND state IN ('retired','active')`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	worked, err := w.Step(ctx)
	if err != nil || !worked {
		t.Fatalf("generation step: %v %v", worked, err)
	}
}
func TestWorkspaceGenerationUnknownDeleteWaitsForPVCAndPV(t *testing.T) {
	f := sandboxDatabase(t)
	setupCheckpointTransferSchema(t, f)
	ctx, c, cp, old := transferFixture(t, f)
	cmd := biz.CheckpointTransferCommand{TenantID: c.TenantID, TransferID: uuid.NewString(), OperationID: uuid.NewString(), CheckpointID: cp.ID, SourceSandboxID: c.ID, TargetSandboxID: c.ID, Kind: "restore", IdempotencyKey: "restore", ExpectedVersion: 1}
	if _, err := f.repo.AcceptCheckpointTransfer(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	source := &transferTestProvider{visible: true, bound: true, uid: uuid.NewString(), pvUID: uuid.NewString()}
	transfer, err := biz.NewCheckpointTransferWorker(f.repo, source, "transfer", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	transferStep(t, f, ctx, c, transfer)
	transferStep(t, f, ctx, c, transfer)
	transferStep(t, f, ctx, c, transfer)
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET desired='deleted' WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	provider := &generationCleanupTestProvider{pvc: true, pv: true}
	worker, err := biz.NewWorkspaceGenerationCleanupWorker(f.repo, provider, "cleanup", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	generationCleanupStep(t, f, ctx, c, worker) // freeze CSI provenance
	generationCleanupStep(t, f, ctx, c, worker) // uncertain UID delete
	generationCleanupStep(t, f, ctx, c, worker) // still present: never resend
	if provider.calls != 1 {
		t.Fatalf("uncertain delete dispatched %d times", provider.calls)
	}
	provider.pvc = false
	generationCleanupStep(t, f, ctx, c, worker)
	var state string
	if err = f.runtime.QueryRow(ctx, `SELECT state FROM sandbox.workspace_generations WHERE tenant_id=$1 AND sandbox_id=$2 AND pvc_uid=$3`, c.TenantID, c.ID, old.PVCUID).Scan(&state); err != nil || state != "retired" {
		t.Fatalf("PV not gone but state=%s err=%v", state, err)
	}
	provider.pv = false
	generationCleanupStep(t, f, ctx, c, worker)
	if err = f.runtime.QueryRow(ctx, `SELECT state FROM sandbox.workspace_generations WHERE tenant_id=$1 AND sandbox_id=$2 AND pvc_uid=$3`, c.TenantID, c.ID, old.PVCUID).Scan(&state); err != nil || state != "reclaimed" {
		t.Fatalf("reclaim state=%s err=%v", state, err)
	}
	if provider.calls != 1 {
		t.Fatalf("delete dispatched %d times", provider.calls)
	}
}
