//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func directCloneFixture(t *testing.T, f *sandboxFixture) (context.Context, biz.Create, biz.Checkpoint, biz.CheckpointCloneCommand) {
	t.Helper()
	ctx, source, cp, _ := transferFixture(t, f)
	namespace, err := biz.TenantNamespace(source.TenantID, "tenant-")
	if err != nil {
		t.Fatal(err)
	}
	source.Spec.Namespace = namespace
	raw, err := json.Marshal(source.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET snapshot=$3 WHERE tenant_id=$1 AND sandbox_id=$2`, source.TenantID, source.ID, raw); err != nil {
		t.Fatal(err)
	}
	cp.Source.Namespace = namespace
	sourceRaw, err := json.Marshal(cp.Source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.checkpoints SET source=$3 WHERE tenant_id=$1 AND checkpoint_id=$2`, source.TenantID, cp.ID, sourceRaw); err != nil {
		t.Fatal(err)
	}
	create := source
	create.ID = uuid.NewString()
	create.OperationID = uuid.NewString()
	create.InstanceID = ""
	create.Spec.Name = "direct-clone"
	transfer := biz.CheckpointTransferCommand{TenantID: source.TenantID, TransferID: uuid.NewString(), OperationID: uuid.NewString(), CheckpointID: cp.ID, SourceSandboxID: source.ID, TargetSandboxID: create.ID, Kind: "clone", IdempotencyKey: "direct-clone", ExpectedVersion: 1}
	return ctx, source, cp, biz.CheckpointCloneCommand{Transfer: transfer, Sandbox: create, SourceExpectedVersion: 1}
}
func TestCheckpointCloneDirectAtomicCreateAndReplay(t *testing.T) {
	f := sandboxDatabase(t)
	setupCheckpointTransferSchema(t, f)
	ctx, source, _, cmd := directCloneFixture(t, f)
	transfer, err := f.repo.AcceptCheckpointClone(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID)
	if err != nil || target.ID != cmd.Sandbox.ID || target.InstanceID != "" || target.Observed != biz.ObservedProvisioning || target.Desired != biz.DesiredPaused {
		t.Fatalf("new sandbox: %#v %v", target, err)
	}
	replay := cmd
	previous, err := f.repo.AcceptCheckpointClone(ctx, replay)
	if err != nil || !previous.Replayed || previous.ID != transfer.ID || previous.TargetSandboxID != target.ID {
		t.Fatalf("clone replay: %#v %v", previous, err)
	}
	replay.Transfer.TargetSandboxID = uuid.NewString()
	replay.Sandbox.ID = replay.Transfer.TargetSandboxID
	if _, err = f.repo.AcceptCheckpointClone(ctx, replay); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("changed target was replayed: %v", err)
	}
	_, err = f.runtime.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET source_sandbox_id=$3 WHERE tenant_id=$1 AND transfer_id=$2`, source.TenantID, transfer.ID, target.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("mismatched owner passed FK: %v", err)
	}
	p := &transferTestProvider{visible: true, bound: true, uid: uuid.NewString(), pvUID: uuid.NewString()}
	w, err := biz.NewCheckpointTransferWorker(f.repo, p, "worker", time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	transferStep(t, f, ctx, cmd.Sandbox, w)
	transferStep(t, f, ctx, cmd.Sandbox, w)
	transferStep(t, f, ctx, cmd.Sandbox, w)
	target, err = f.repo.Get(ctx, source.TenantID, target.ID)
	if err != nil || target.Observed != biz.ObservedPaused || target.ProviderRefs.PVCUID != p.uid {
		t.Fatalf("completed clone: %#v %v", target, err)
	}
	op, err := f.repo.GetOperation(ctx, source.TenantID, cmd.Sandbox.OperationID)
	if err != nil || op.State != "succeeded" {
		t.Fatalf("create operation: %#v %v", op, err)
	}
}
func TestCheckpointCloneAndSourceDeleteSerialize(t *testing.T) {
	f := sandboxDatabase(t)
	setupCheckpointTransferSchema(t, f)
	for i := 0; i < 12; i++ {
		ctx, source, checkpoint, cmd := directCloneFixture(t, f)
		remove := biz.Command{TenantID: source.TenantID, ID: source.ID, OperationID: uuid.NewString(), Action: "delete", IdempotencyKey: "delete", RequestDigest: biz.ActionDigest("", 1, "delete"), ExpectedVersion: 1}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var cloneErr, deleteErr error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, cloneErr = f.repo.AcceptCheckpointClone(ctx, cmd) }()
		go func() { defer wg.Done(); <-start; _, deleteErr = f.repo.Change(ctx, remove) }()
		close(start)
		wg.Wait()
		if (cloneErr == nil) == (deleteErr == nil) {
			t.Fatalf("clone/delete both succeeded or both failed: clone=%v delete=%v", cloneErr, deleteErr)
		}
		var desired string
		var references int
		if err := f.runtime.QueryRow(ctx, `SELECT desired FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2`, source.TenantID, source.ID).Scan(&desired); err != nil {
			t.Fatal(err)
		}
		if err := f.runtime.QueryRow(ctx, `SELECT count(*) FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND checkpoint_id=$2 AND source_sandbox_id=$3 AND target_sandbox_id=$4 AND NOT source_released`, source.TenantID, checkpoint.ID, source.ID, cmd.Sandbox.ID).Scan(&references); err != nil {
			t.Fatal(err)
		}
		if cloneErr == nil {
			if !errors.Is(deleteErr, biz.ErrCheckpointReferenced) || desired == biz.DesiredDeleted || references != 1 {
				t.Fatalf("accepted clone not fenced: delete=%v desired=%s references=%d", deleteErr, desired, references)
			}
			if target, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID); err != nil || target.ID != cmd.Sandbox.ID {
				t.Fatalf("clone target: %+v %v", target, err)
			}
			if _, err := f.repo.GetOperation(ctx, source.TenantID, remove.OperationID); !errors.Is(err, biz.ErrNotFound) {
				t.Fatalf("rejected delete operation persisted: %v", err)
			}
		} else {
			if !errors.Is(cloneErr, biz.ErrConflict) || desired != biz.DesiredDeleted || references != 0 {
				t.Fatalf("accepted delete not fenced: clone=%v desired=%s references=%d", cloneErr, desired, references)
			}
			if _, err := f.repo.Get(ctx, source.TenantID, cmd.Sandbox.ID); !errors.Is(err, biz.ErrNotFound) {
				t.Fatalf("rejected clone target persisted: %v", err)
			}
			if op, err := f.repo.GetOperation(ctx, source.TenantID, remove.OperationID); err != nil || op.Action != "delete" {
				t.Fatalf("delete operation: %+v %v", op, err)
			}
		}
		if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.work SET due_at='infinity' WHERE tenant_id=$1 AND sandbox_id=$2`, source.TenantID, source.ID); err != nil {
			t.Fatal(err)
		}
	}
}
