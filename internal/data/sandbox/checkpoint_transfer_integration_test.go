//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func setupCheckpointTransferSchema(t *testing.T, f *sandboxFixture) {
	t.Helper()
	var exists bool
	if err := f.runtime.QueryRow(context.Background(), `SELECT to_regclass('sandbox.checkpoint_transfers') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("transfer migration: %v", err)
	}
}
func transferFixture(t *testing.T, f *sandboxFixture) (context.Context, biz.Create, biz.Checkpoint, biz.ProviderRefs) {
	t.Helper()
	ctx, c, cmd := runningCheckpointFixture(t, f)
	s, err := f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	refs := s.ProviderRefs
	refs.PodUID = ""
	raw, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET desired='paused',observed='paused',provider_refs=$3 WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID, raw); err != nil {
		t.Fatal(err)
	}
	cp, err := f.repo.AcceptCheckpoint(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.checkpoints SET state='ready',phase='done',snapshot_uid='snapshot-uid',content_name='content-name',content_uid='content-uid',snapshot_handle='snapshot-handle',completed_at=clock_timestamp() WHERE tenant_id=$1 AND checkpoint_id=$2`, c.TenantID, cp.ID); err != nil {
		t.Fatal(err)
	}
	cp, err = f.repo.GetCheckpoint(ctx, c.TenantID, c.ID, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, c, cp, refs
}

type transferTestProvider struct {
	calls          int
	visible, bound bool
	uid, pvUID     string
}

func (p *transferTestProvider) CreateCheckpointTransferPVC(_ context.Context, _ biz.Sandbox, _ biz.CheckpointTransfer, _ string) (string, string, error) {
	p.calls++
	return biz.MutationUnknown, "", nil
}
func (p *transferTestProvider) ObserveCheckpointTransferPVC(_ context.Context, _ biz.Sandbox, t biz.CheckpointTransfer) (biz.CheckpointTransferObservation, error) {
	if !p.visible {
		return biz.CheckpointTransferObservation{}, nil
	}
	out := biz.CheckpointTransferObservation{Exists: true, Bound: p.bound, PVCName: t.CandidateName, PVCUID: p.uid, PVName: "new-pv", PVUID: p.pvUID, SnapshotUID: t.SourceSnapshotUID, ContentUID: t.SourceContentUID, SnapshotHandle: t.SourceSnapshotHandle}
	if !p.bound {
		out.Reason = "SANDBOX_TRANSFER_WAIT_FOR_FIRST_CONSUMER"
	}
	return out, nil
}
func transferStep(t *testing.T, f *sandboxFixture, ctx context.Context, c biz.Create, w *biz.CheckpointTransferWorker) {
	t.Helper()
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET due_at=clock_timestamp() WHERE tenant_id=$1 AND target_sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	worked, err := w.Step(ctx)
	if err != nil || !worked {
		t.Fatalf("step: %v %v", worked, err)
	}
}
func TestCheckpointTransferUnknownRestoreAtomicGeneration(t *testing.T) {
	f := sandboxDatabase(t)
	setupCheckpointTransferSchema(t, f)
	ctx, c, cp, oldRefs := transferFixture(t, f)
	cmd := biz.CheckpointTransferCommand{TenantID: c.TenantID, TransferID: uuid.NewString(), OperationID: uuid.NewString(), CheckpointID: cp.ID, SourceSandboxID: c.ID, TargetSandboxID: c.ID, Kind: "restore", IdempotencyKey: "restore", ExpectedVersion: 1}
	accepted, err := f.repo.AcceptCheckpointTransfer(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"pause", "resume", "delete"} {
		change := biz.Command{TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), Action: action, IdempotencyKey: action, RequestDigest: biz.ActionDigest(c.InstanceID, 1, action), ExpectedVersion: 1}
		// An in-flight self restore blocks every lifecycle action. Its own
		// reference may only be released after the transfer reaches a safe state.
		if _, err = f.repo.Change(ctx, change); !errors.Is(err, biz.ErrConflict) {
			t.Fatalf("active transfer %s: got %v; want conflict", action, err)
		}
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	if work, e := f.repo.Claim(ctx, "regular-worker", time.Minute); e != nil || work != nil {
		t.Fatalf("normal worker crossed transfer fence: %#v %v", work, e)
	}
	replay := cmd
	replay.TransferID = uuid.NewString()
	replay.OperationID = uuid.NewString()
	got, err := f.repo.AcceptCheckpointTransfer(ctx, replay)
	if err != nil || !got.Replayed || got.ID != accepted.ID {
		t.Fatalf("replay: %#v %v", got, err)
	}
	p := &transferTestProvider{uid: uuid.NewString(), pvUID: uuid.NewString()}
	w, err := biz.NewCheckpointTransferWorker(f.repo, p, "worker", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	transferStep(t, f, ctx, c, w)
	for i := 0; i < 3; i++ {
		transferStep(t, f, ctx, c, w)
	}
	got, err = f.repo.GetCheckpointTransfer(ctx, c.TenantID, c.ID, cmd.TransferID)
	if err != nil || got.State != "unknown" || p.calls != 1 {
		t.Fatalf("unknown: %#v calls=%d err=%v", got, p.calls, err)
	}
	current, err := f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil || current.ProviderRefs != oldRefs {
		t.Fatalf("old workspace changed before confirmation: %#v %v", current.ProviderRefs, err)
	}
	p.visible = true
	transferStep(t, f, ctx, c, w)
	transferStep(t, f, ctx, c, w)
	pendingOp, err := f.repo.GetOperation(ctx, c.TenantID, cmd.OperationID)
	if err != nil || pendingOp.Reason != "SANDBOX_TRANSFER_WAIT_FOR_FIRST_CONSUMER" {
		t.Fatalf("pending reason: %#v %v", pendingOp, err)
	}
	current, err = f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil || current.ProviderRefs != oldRefs {
		t.Fatalf("unbound candidate switched: %#v %v", current.ProviderRefs, err)
	}
	p.bound = true
	transferStep(t, f, ctx, c, w)
	current, err = f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil || current.ProviderRefs.PVCUID != p.uid || current.ProviderRefs.PVUID != p.pvUID || current.Version != 2 {
		t.Fatalf("switch: %#v %v", current, err)
	}
	var retired, active int
	if err = f.runtime.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='retired' AND pvc_uid=$3), count(*) FILTER(WHERE state='active' AND pvc_uid=$4) FROM sandbox.workspace_generations WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID, oldRefs.PVCUID, p.uid).Scan(&retired, &active); err != nil || retired != 1 || active != 1 {
		t.Fatalf("generations: %d %d %v", retired, active, err)
	}
}
func TestCheckpointTransferVersionChangeStopsSwitch(t *testing.T) {
	f := sandboxDatabase(t)
	setupCheckpointTransferSchema(t, f)
	ctx, c, cp, oldRefs := transferFixture(t, f)
	cmd := biz.CheckpointTransferCommand{TenantID: c.TenantID, TransferID: uuid.NewString(), OperationID: uuid.NewString(), CheckpointID: cp.ID, SourceSandboxID: c.ID, TargetSandboxID: c.ID, Kind: "restore", IdempotencyKey: "restore", ExpectedVersion: 1}
	if _, err := f.repo.AcceptCheckpointTransfer(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	p := &transferTestProvider{visible: true, bound: true, uid: uuid.NewString(), pvUID: uuid.NewString()}
	w, err := biz.NewCheckpointTransferWorker(f.repo, p, "worker", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	transferStep(t, f, ctx, c, w)
	transferStep(t, f, ctx, c, w)
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET version=version+1 WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET due_at=clock_timestamp() WHERE tenant_id=$1 AND target_sandbox_id=$2`, c.TenantID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Step(ctx); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("changed version did not stop switch: %v", err)
	}
	current, err := f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil || current.ProviderRefs != oldRefs {
		t.Fatalf("old refs changed: %#v %v", current, err)
	}
}

func TestCheckpointCloneCandidateCreatesIndependentWorkspace(t *testing.T) {
	f := sandboxDatabase(t)
	setupCheckpointTransferSchema(t, f)
	ctx, source, cp, sourceRefs := transferFixture(t, f)
	target := source
	target.ID = uuid.NewString()
	target.OperationID = uuid.NewString()
	target.InstanceID = ""
	target.Spec.Name = "clone-target"
	if _, err := f.repo.AcceptCreate(ctx, target); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET desired='paused',observed='paused' WHERE tenant_id=$1 AND sandbox_id=$2`, target.TenantID, target.ID); err != nil {
		t.Fatal(err)
	}
	cmd := biz.CheckpointTransferCommand{TenantID: source.TenantID, TransferID: uuid.NewString(), OperationID: uuid.NewString(), CheckpointID: cp.ID, SourceSandboxID: source.ID, TargetSandboxID: target.ID, Kind: "clone", IdempotencyKey: "clone", ExpectedVersion: 1}
	if _, err := f.repo.AcceptCheckpointTransfer(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	p := &transferTestProvider{visible: true, bound: true, uid: uuid.NewString(), pvUID: uuid.NewString()}
	w, err := biz.NewCheckpointTransferWorker(f.repo, p, "worker", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	transferStep(t, f, ctx, target, w)
	transferStep(t, f, ctx, target, w)
	transferStep(t, f, ctx, target, w)
	clone, err := f.repo.Get(ctx, target.TenantID, target.ID)
	if err != nil || clone.ProviderRefs.PVCUID != p.uid {
		t.Fatalf("clone target: %#v %v", clone, err)
	}
	original, err := f.repo.Get(ctx, source.TenantID, source.ID)
	if err != nil || original.ProviderRefs != sourceRefs {
		t.Fatalf("clone modified original: %#v %v", original, err)
	}
}
