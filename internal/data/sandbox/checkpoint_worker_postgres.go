package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

var _ biz.CheckpointWorkerRepository = (*Postgres)(nil)

// ClaimCheckpoint uses the lifecycle work row first, just as Change and
// AcceptCheckpoint do. The separate epoch fences a worker after takeover.
func (p *Postgres) ClaimCheckpoint(ctx context.Context, owner string, lease time.Duration) (*biz.CheckpointWork, error) {
	if owner == "" || lease <= 0 {
		return nil, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var tenant, id, checkpointID string
	err = tx.QueryRow(ctx, `SELECT w.tenant_id::text,w.sandbox_id::text,c.checkpoint_id::text FROM sandbox.work w JOIN sandbox.checkpoints c ON c.tenant_id=w.tenant_id AND c.sandbox_id=w.sandbox_id WHERE c.state IN ('accepted','running','unknown') AND c.lease_until<clock_timestamp() AND c.due_at<=clock_timestamp() ORDER BY c.due_at,c.checkpoint_id FOR UPDATE OF w SKIP LOCKED LIMIT 1`).Scan(&tenant, &id, &checkpointID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s, err := scanSandbox(tx.QueryRow(ctx, sandboxSelect+` WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s`, tenant, id))
	if err != nil {
		return nil, err
	}
	var epoch int64
	err = tx.QueryRow(ctx, `UPDATE sandbox.checkpoints SET lease_owner=$4,lease_epoch=lease_epoch+1,lease_until=clock_timestamp()+$5::bigint*interval '1 microsecond',state=CASE WHEN mutation_id<>'' THEN 'unknown' WHEN state='accepted' THEN 'running' ELSE state END,mutation_state=CASE WHEN mutation_id<>'' THEN 'unknown' ELSE mutation_state END,reason=CASE WHEN mutation_id<>'' AND reason NOT IN ('SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED','SANDBOX_CHECKPOINT_PROVIDER_IDENTITY_MISMATCH') THEN 'SANDBOX_CHECKPOINT_OUTCOME_UNKNOWN' ELSE reason END,updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 AND state IN ('accepted','running','unknown') AND lease_until<clock_timestamp() RETURNING lease_epoch`, tenant, id, checkpointID, owner, lease.Microseconds()).Scan(&epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c, _, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, tenant, id, checkpointID))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state=CASE WHEN $3='unknown' THEN 'unknown' ELSE 'running' END WHERE tenant_id=$1 AND operation_id=$2 AND state IN ('queued','running','unknown')`, tenant, c.OperationID, c.State); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &biz.CheckpointWork{Checkpoint: c, Sandbox: s, LeaseOwner: owner, LeaseEpoch: epoch}, nil
}

func checkpointLock(ctx context.Context, tx pgx.Tx, w biz.CheckpointWork) (biz.Checkpoint, error) {
	c := w.Checkpoint
	if !biz.UUID(c.TenantID) || !biz.UUID(c.SandboxID) || !biz.UUID(c.ID) || w.LeaseOwner == "" || w.LeaseEpoch <= 0 {
		return c, biz.ErrInvalid
	}
	var marker int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, c.TenantID, c.SandboxID).Scan(&marker); err != nil {
		return c, err
	}
	if err := tx.QueryRow(ctx, `SELECT 1 FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, c.TenantID, c.SandboxID).Scan(&marker); err != nil {
		return c, err
	}
	current, _, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 FOR UPDATE`, c.TenantID, c.SandboxID, c.ID))
	if err != nil {
		return c, err
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT lease_owner=$4 AND lease_epoch=$5 AND lease_until>clock_timestamp() FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SandboxID, c.ID, w.LeaseOwner, w.LeaseEpoch).Scan(&owned); err != nil {
		return c, err
	}
	if !owned {
		return c, biz.ErrLease
	}
	return current, nil
}

// RecordCheckpointSource records the CSI identity before the first external
// write. The Sandbox/PVC/PV identity cannot be changed by the worker.
func (p *Postgres) RecordCheckpointSource(ctx context.Context, w biz.CheckpointWork, source biz.CheckpointSource) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := checkpointLock(ctx, tx, w)
	if err != nil {
		return err
	}
	if c.Phase != "preflight" || c.MutationID != "" || source.ClusterID != c.Source.ClusterID || source.Namespace != c.Source.Namespace || source.NamespaceUID != c.Source.NamespaceUID || source.PVCName != c.Source.PVCName || source.PVCUID != c.Source.PVCUID || source.PVName != c.Source.PVName || source.PVUID != c.Source.PVUID || source.CSIDriver == "" || source.VolumeHandle == "" || source.SnapshotClass == "" || source.SnapshotClassUID == "" || source.SnapshotClassResourceVersion == "" {
		return biz.ErrConflict
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.checkpoints SET source=$4,phase='protect',reason='',updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SandboxID, c.ID, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET reason='' WHERE tenant_id=$1 AND operation_id=$2`, c.TenantID, c.OperationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordCheckpointPendingReason stores only bounded, sanitized diagnostic
// codes, retaining the current state and all mutation/cleanup barriers.
func (p *Postgres) RecordCheckpointPendingReason(ctx context.Context, w biz.CheckpointWork, reason string) error {
	switch reason {
	case "", "CSI_SNAPSHOT_LAST_ERROR", "SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED", "SANDBOX_CHECKPOINT_PROVIDER_IDENTITY_MISMATCH":
	default:
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := checkpointLock(ctx, tx, w)
	if err != nil {
		return err
	}
	if c.State != "accepted" && c.State != "running" && c.State != "unknown" {
		return biz.ErrConflict
	}
	if reason == "" {
		switch c.Reason {
		case "CSI_SNAPSHOT_LAST_ERROR", "SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED", "SANDBOX_CHECKPOINT_PROVIDER_IDENTITY_MISMATCH":
			if c.MutationID != "" {
				reason = "SANDBOX_CHECKPOINT_OUTCOME_UNKNOWN"
			}
		default:
			return tx.Commit(ctx)
		}
	}
	if reason == "CSI_SNAPSHOT_LAST_ERROR" && (c.Phase != "observe" || c.MutationID != "") {
		return biz.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.checkpoints SET reason=$4,updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SandboxID, c.ID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET reason=$3 WHERE tenant_id=$1 AND operation_id=$2 AND state IN ('queued','running','unknown')`, c.TenantID, c.OperationID, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Preflight has made no provider writes, so a definitive unsupported source
// may fail the operation without leaving a provider mutation to reconcile.
func (p *Postgres) FailCheckpointPreflight(ctx context.Context, w biz.CheckpointWork, reason string) error {
	if reason != "SANDBOX_CHECKPOINT_UNSUPPORTED" && reason != "SANDBOX_CHECKPOINT_OWNERSHIP_CONFLICT" {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := checkpointLock(ctx, tx, w)
	if err != nil {
		return err
	}
	if c.Phase != "preflight" || c.MutationID != "" {
		return biz.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.checkpoints SET phase='done',state='failed',reason=$4,failure_reason=$4,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SandboxID, c.ID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state='failed',reason=$3,completed_at=clock_timestamp() WHERE tenant_id=$1 AND operation_id=$2`, c.TenantID, c.OperationID, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// A committed pending mutation is never dispatched again, including after a
// worker restart or a lost commit reply. Only attributable observation may
// establish that an uncertain call applied.
func (p *Postgres) PrepareCheckpointMutation(ctx context.Context, w biz.CheckpointWork, m biz.CheckpointMutation) (bool, error) {
	if !biz.UUID(m.ID) {
		return false, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	c, err := checkpointLock(ctx, tx, w)
	if err != nil {
		return false, err
	}
	if c.MutationID != "" {
		if c.MutationID == m.ID && c.MutationKind == m.Kind {
			return false, tx.Commit(ctx)
		}
		return false, biz.ErrConflict
	}
	if (c.Phase != "protect" || m.Kind != "protect") && (c.Phase != "create" || m.Kind != "create") && (c.Phase != "release" || m.Kind != "release") {
		return false, biz.ErrConflict
	}
	var protectID, createID, releaseID string
	switch m.Kind {
	case "protect":
		protectID = m.ID
	case "create":
		createID = m.ID
	case "release":
		releaseID = m.ID
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.checkpoints SET state='running',reason=CASE WHEN reason IN ('CSI_SNAPSHOT_LAST_ERROR','SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED','SANDBOX_CHECKPOINT_PROVIDER_IDENTITY_MISMATCH') THEN '' ELSE reason END,mutation_id=$4,mutation_kind=$5,mutation_state='prepared',protection_mutation_id=CASE WHEN $6<>'' THEN $6 ELSE protection_mutation_id END,snapshot_mutation_id=CASE WHEN $7<>'' THEN $7 ELSE snapshot_mutation_id END,release_mutation_id=CASE WHEN $8<>'' THEN $8 ELSE release_mutation_id END,updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SandboxID, c.ID, m.ID, m.Kind, protectID, createID, releaseID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state='running',reason=(SELECT reason FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$3 AND checkpoint_id=$4) WHERE tenant_id=$1 AND operation_id=$2`, c.TenantID, c.OperationID, c.SandboxID, c.ID); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// Finish accepts only the exact journal entry while the caller owns the lease.
// A rejected first create after protection still requires a confirmed release.
func (p *Postgres) FinishCheckpointMutation(ctx context.Context, w biz.CheckpointWork, m biz.CheckpointMutation, outcome, uid string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := checkpointLock(ctx, tx, w)
	if err != nil {
		return err
	}
	if c.MutationID != m.ID || c.MutationKind != m.Kind || !biz.UUID(m.ID) {
		return biz.ErrConflict
	}
	phase, state, reason := c.Phase, "running", c.Reason
	failureReason := c.FailureReason
	snapshotUID := c.SnapshotUID
	clear := false
	switch outcome {
	case biz.MutationUnknown:
		state = "unknown"
		reason = "SANDBOX_CHECKPOINT_OUTCOME_UNKNOWN"
	case biz.MutationApplied:
		if reason == "SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED" || reason == "SANDBOX_CHECKPOINT_PROVIDER_IDENTITY_MISMATCH" || reason == "SANDBOX_CHECKPOINT_OUTCOME_UNKNOWN" {
			reason = ""
		}
		switch m.Kind {
		case "protect":
			if uid != c.Source.PVCUID {
				return biz.ErrOwnership
			}
			phase = "create"
		case "create":
			if uid == "" {
				return biz.ErrInvalid
			}
			snapshotUID = uid
			phase = "observe"
		case "release":
			if uid != c.Source.PVCUID {
				return biz.ErrOwnership
			}
			phase = "done"
			if failureReason != "" {
				state = "failed"
				reason = failureReason
			} else {
				if c.SnapshotUID == "" || c.ContentName == "" || c.ContentUID == "" || c.SnapshotHandle == "" {
					return biz.ErrConflict
				}
				state = "ready"
				reason = ""
			}
		default:
			return biz.ErrInvalid
		}
		clear = true
	case biz.MutationRejected, biz.MutationNotSent:
		// A later negative result cannot revoke an earlier uncertain dispatch.
		if c.MutationState == "unknown" {
			state = "unknown"
			reason = "SANDBOX_CHECKPOINT_OUTCOME_UNKNOWN"
			break
		}
		// Release may itself be uncertain on a stale/reordered provider reply;
		// retain its barrier for observation instead of guessing completion.
		switch m.Kind {
		case "protect":
			phase = "done"
			state = "failed"
			reason = "SANDBOX_CHECKPOINT_PROTECTION_REJECTED"
			failureReason = reason
			clear = true
		case "create":
			phase = "release"
			reason = "SANDBOX_CHECKPOINT_CREATE_REJECTED"
			failureReason = reason
			clear = true
		case "release":
			state = "unknown"
			reason = "SANDBOX_CHECKPOINT_RELEASE_UNCONFIRMED"
		default:
			return biz.ErrInvalid
		}
	default:
		return biz.ErrInvalid
	}
	_, err = tx.Exec(ctx, `UPDATE sandbox.checkpoints SET state=$4,phase=$5,reason=$6,snapshot_uid=$7,mutation_id=CASE WHEN $8 THEN '' ELSE mutation_id END,mutation_kind=CASE WHEN $8 THEN '' ELSE mutation_kind END,mutation_state=CASE WHEN $8 THEN '' ELSE 'unknown' END,completed_at=CASE WHEN $4 IN ('ready','failed') THEN clock_timestamp() ELSE NULL END,updated_at=clock_timestamp(),failure_reason=$9 WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SandboxID, c.ID, state, phase, reason, snapshotUID, clear, failureReason)
	if err != nil {
		return err
	}
	opState := state
	if state == "ready" {
		opState = "succeeded"
	} else if state == "failed" {
		opState = "failed"
	}
	_, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state=$3,reason=$4,completed_at=CASE WHEN $3 IN ('succeeded','failed') THEN clock_timestamp() ELSE NULL END WHERE tenant_id=$1 AND operation_id=$2`, c.TenantID, c.OperationID, opState, reason)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordCheckpointReady requires the caller to have verified both directions of
// the VolumeSnapshot/Content binding and the original CSI volume handle.
func (p *Postgres) RecordCheckpointReady(ctx context.Context, w biz.CheckpointWork, snapshotUID, contentName, contentUID, snapshotHandle string) error {
	if snapshotUID == "" || contentName == "" || contentUID == "" || snapshotHandle == "" {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := checkpointLock(ctx, tx, w)
	if err != nil {
		return err
	}
	if c.Phase != "observe" || c.MutationID != "" || c.SnapshotUID != snapshotUID {
		return biz.ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE sandbox.checkpoints SET content_name=$4,content_uid=$5,snapshot_handle=$6,phase='release',reason='',updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SandboxID, c.ID, contentName, contentUID, snapshotHandle)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET reason='' WHERE tenant_id=$1 AND operation_id=$2`, c.TenantID, c.OperationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ReleaseCheckpointLease(ctx context.Context, w biz.CheckpointWork, next time.Duration) error {
	if next < 0 {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := checkpointLock(ctx, tx, w)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE sandbox.checkpoints SET lease_owner='',lease_until='-infinity',due_at=clock_timestamp()+$4::bigint*interval '1 microsecond',updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SandboxID, c.ID, next.Microseconds())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
