package sandbox

// Candidate-only transaction implementation. Do not wire until the transfer
// journal migration and all lifecycle fences are integrated and tested.
import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func (p *Postgres) ClaimCheckpointCleanup(ctx context.Context, owner string, lease time.Duration) (*biz.CheckpointCleanupWork, error) {
	if owner == "" || lease <= 0 {
		return nil, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Fail closed until transfer references are present. Missing schema must not
	// be mistaken for an empty list of clones.
	var transferTable *string
	if err = tx.QueryRow(ctx, `SELECT to_regclass('sandbox.checkpoint_transfers')::text`).Scan(&transferTable); err != nil || transferTable == nil {
		return nil, biz.ErrDependencyUnavailable
	}
	var tenant, id, checkpointID string
	err = tx.QueryRow(ctx, `SELECT c.tenant_id::text,c.sandbox_id::text,c.checkpoint_id::text FROM sandbox.checkpoints c JOIN sandbox.sandboxes s ON s.tenant_id=c.tenant_id AND s.sandbox_id=c.sandbox_id WHERE s.desired='deleted' AND c.cleanup_state<>'done' AND c.state IN ('ready','failed') AND c.due_at<=clock_timestamp() AND c.lease_until<clock_timestamp() AND NOT EXISTS(SELECT 1 FROM sandbox.checkpoint_transfers t WHERE t.tenant_id=c.tenant_id AND t.checkpoint_id=c.checkpoint_id AND NOT t.source_released) ORDER BY c.due_at,c.checkpoint_id LIMIT 1`).Scan(&tenant, &id, &checkpointID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ignored string
	err = tx.QueryRow(ctx, `SELECT sandbox_id::text FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, tenant, id).Scan(&ignored)
	if err != nil {
		return nil, err
	}
	s, err := scanSandbox(tx.QueryRow(ctx, sandboxSelect+` WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s`, tenant, id))
	if err != nil {
		return nil, err
	}
	c, _, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 FOR UPDATE`, tenant, id, checkpointID))
	if err != nil {
		return nil, err
	}
	if s.Desired != biz.DesiredDeleted || (c.State != "ready" && c.State != "failed") {
		return nil, nil
	}
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND checkpoint_id=$2 AND NOT source_released)`, tenant, checkpointID).Scan(&blocked)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, nil
	}
	var epoch int64
	var mutationID, outcome string
	// A prepared journal inherited from another lease may already have been
	// sent before that worker died. Preserve the uncertainty across takeover.
	err = tx.QueryRow(ctx, `UPDATE sandbox.checkpoints SET lease_owner=$4,lease_epoch=lease_epoch+1,lease_until=clock_timestamp()+$5::bigint*interval '1 microsecond',cleanup_state=CASE WHEN cleanup_state='prepared' AND cleanup_mutation_id<>'' THEN 'unknown' ELSE cleanup_state END WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 AND cleanup_state<>'done' AND lease_until<clock_timestamp() AND due_at<=clock_timestamp() RETURNING lease_epoch,cleanup_mutation_id,cleanup_state`, tenant, id, checkpointID, owner, lease.Microseconds()).Scan(&epoch, &mutationID, &outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &biz.CheckpointCleanupWork{Checkpoint: c, Sandbox: s, Owner: owner, Epoch: epoch, MutationID: mutationID, Outcome: outcome}, nil
}
func (p *Postgres) PrepareCheckpointCleanup(ctx context.Context, w biz.CheckpointCleanupWork, id string) (bool, error) {
	if !biz.UUID(id) {
		return false, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var ignored string
	if err = tx.QueryRow(ctx, `SELECT sandbox_id::text FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID).Scan(&ignored); err != nil {
		return false, err
	}
	var desired string
	if err = tx.QueryRow(ctx, `SELECT desired FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID).Scan(&desired); err != nil {
		return false, err
	}
	if w.Sandbox.TenantID != w.Checkpoint.TenantID || w.Sandbox.ID != w.Checkpoint.SandboxID {
		return false, biz.ErrOwnership
	}
	c, _, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 FOR UPDATE`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID, w.Checkpoint.ID))
	if err != nil {
		return false, err
	}
	var blocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND checkpoint_id=$2 AND NOT source_released)`, c.TenantID, c.ID).Scan(&blocked); err != nil {
		return false, err
	}
	if desired != biz.DesiredDeleted || blocked {
		return false, biz.ErrConflict
	}
	// Failed preflight / explicitly rejected writes with a completed release have
	// no snapshot to delete; they need no CSI write. All other failure paths stay
	// blocked until their external effects can be proved absent.
	safeFailed := c.State == "failed" && c.Phase == "done" && c.MutationID == "" && c.SnapshotUID == "" && (c.FailureReason == "SANDBOX_CHECKPOINT_UNSUPPORTED" || c.FailureReason == "SANDBOX_CHECKPOINT_OWNERSHIP_CONFLICT" || c.FailureReason == "SANDBOX_CHECKPOINT_PROTECTION_REJECTED" || c.FailureReason == "SANDBOX_CHECKPOINT_CREATE_REJECTED")
	if c.State != "ready" && !safeFailed {
		return false, biz.ErrConflict
	}
	if c.State == "ready" && (c.SnapshotUID == "" || c.ContentUID == "" || c.ContentName == "" || c.SnapshotHandle == "") {
		return false, biz.ErrOwnership
	}
	next := "prepared"
	if safeFailed {
		next = "done"
	}
	tag, err := tx.Exec(ctx, `UPDATE sandbox.checkpoints SET cleanup_state=$6,cleanup_mutation_id=$5 WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 AND lease_owner=$4 AND lease_epoch=$7 AND lease_until>clock_timestamp() AND cleanup_mutation_id='' AND cleanup_state=''`, c.TenantID, c.SandboxID, c.ID, w.Owner, id, next, w.Epoch)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, biz.ErrLease
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return !safeFailed, nil
}
func (p *Postgres) FinishCheckpointCleanup(ctx context.Context, w biz.CheckpointCleanupWork, id, outcome string) error {
	if id == "" || (outcome != biz.MutationApplied && outcome != biz.MutationNotSent && outcome != biz.MutationRejected && outcome != biz.MutationUnknown) {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedID, desired, operationID string
	if err = tx.QueryRow(ctx, `SELECT sandbox_id::text FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID).Scan(&lockedID); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT desired,last_operation_id::text FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID).Scan(&desired, &operationID); err != nil {
		return err
	}
	if desired != biz.DesiredDeleted {
		return biz.ErrConflict
	}
	var previous string
	err = tx.QueryRow(ctx, `SELECT cleanup_state FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 AND lease_owner=$4 AND cleanup_mutation_id=$5 AND lease_epoch=$6 AND lease_until>clock_timestamp() FOR UPDATE`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID, w.Checkpoint.ID, w.Owner, id, w.Epoch).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.ErrLease
	}
	if err != nil {
		return err
	}
	if previous != "prepared" && previous != "unknown" {
		return biz.ErrConflict
	}
	// A later negative receipt cannot prove that an earlier uncertain DELETE
	// was not dispatched. Only the first prepared attempt can be cleared.
	if previous == "unknown" && (outcome == biz.MutationNotSent || outcome == biz.MutationRejected) {
		outcome = biz.MutationUnknown
	}
	state, clear := "unknown", false
	if outcome == biz.MutationApplied {
		state = "done"
	} else if outcome == biz.MutationNotSent || outcome == biz.MutationRejected {
		state, clear = "", true
	}
	tag, err := tx.Exec(ctx, `UPDATE sandbox.checkpoints SET cleanup_state=$6,cleanup_mutation_id=CASE WHEN $7 THEN '' ELSE cleanup_mutation_id END WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 AND lease_owner=$4 AND cleanup_mutation_id=$5 AND lease_epoch=$8 AND lease_until>clock_timestamp()`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID, w.Checkpoint.ID, w.Owner, id, state, clear, w.Epoch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return biz.ErrLease
	}
	opState, reason := "unknown", "SANDBOX_CHECKPOINT_CLEANUP_UNKNOWN"
	if outcome == biz.MutationApplied {
		opState, reason = "running", ""
	} else if outcome == biz.MutationNotSent || outcome == biz.MutationRejected {
		opState, reason = "retrying", "SANDBOX_CHECKPOINT_CLEANUP_RETRYING"
	}
	_, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state=$3,reason=$4 WHERE tenant_id=$1 AND sandbox_id=$2 AND action='delete' AND operation_id=$5`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID, opState, reason, operationID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ReleaseCheckpointCleanup(ctx context.Context, w biz.CheckpointCleanupWork, retry time.Duration) error {
	tag, err := p.pool.Exec(ctx, `UPDATE sandbox.checkpoints SET lease_owner='',lease_until='-infinity',due_at=clock_timestamp()+$5::bigint*interval '1 microsecond' WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 AND lease_owner=$4 AND lease_epoch=$6`, w.Checkpoint.TenantID, w.Checkpoint.SandboxID, w.Checkpoint.ID, w.Owner, retry.Microseconds(), w.Epoch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return biz.ErrLease
	}
	return nil
}

var _ biz.CheckpointCleanupRepository = (*Postgres)(nil)
