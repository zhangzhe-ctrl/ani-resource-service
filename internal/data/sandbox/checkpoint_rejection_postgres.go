package sandbox

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

type rejectionReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func restoreRejection(ctx context.Context, q rejectionReader, c biz.CheckpointTransferCommand, digest string) error {
	var found, exact bool
	err := q.QueryRow(ctx, `SELECT count(*)>0,COALESCE(bool_and(request_digest=$5 AND source_sandbox_id=$6 AND checkpoint_id=$7),false) FROM sandbox.checkpoint_transfer_rejections WHERE tenant_id=$1 AND target_sandbox_id=$2 AND kind=$3 AND idempotency_key=$4`, c.TenantID, c.TargetSandboxID, c.Kind, c.IdempotencyKey, digest, c.SourceSandboxID, c.CheckpointID).Scan(&found, &exact)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if !exact {
		return biz.ErrConflict
	}
	return biz.ErrCheckpointWFFCUnsupported
}
func cloneRejection(ctx context.Context, q rejectionReader, c biz.CheckpointCloneCommand, digest string) error {
	t := c.Transfer
	var found, exact bool
	err := q.QueryRow(ctx, `SELECT count(*)>0,COALESCE(bool_and(request_digest=$8 AND target_sandbox_id=$2 AND operation_id=$3 AND create_operation_id=$7 AND source_sandbox_id=$4 AND checkpoint_id=$5 AND idempotency_key=$6),false) FROM sandbox.checkpoint_transfer_rejections WHERE tenant_id=$1 AND kind='clone' AND (target_sandbox_id=$2 OR operation_id IN ($3,$7) OR create_operation_id IN ($3,$7) OR (source_sandbox_id=$4 AND checkpoint_id=$5 AND idempotency_key=$6))`, t.TenantID, t.TargetSandboxID, t.OperationID, t.SourceSandboxID, t.CheckpointID, t.IdempotencyKey, c.Sandbox.OperationID, digest).Scan(&found, &exact)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if !exact {
		return biz.ErrConflict
	}
	return biz.ErrCheckpointWFFCUnsupported
}

func (p *Postgres) RejectCheckpointTransferWFFC(ctx context.Context, c biz.CheckpointTransferCommand) (biz.CheckpointTransfer, error) {
	var zero biz.CheckpointTransfer
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return zero, err
	}
	digest, err := biz.CheckpointTransferDigest(c)
	if err != nil || c.Kind != "restore" {
		return zero, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	sandboxes, err := lockTransferSandboxes(ctx, tx, c.TenantID, c.SourceSandboxID, c.TargetSandboxID)
	if err != nil {
		return zero, err
	}
	old, stored, err := scanTransfer(tx.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND target_sandbox_id=$2 AND kind=$3 AND idempotency_key=$4`, c.TenantID, c.TargetSandboxID, c.Kind, c.IdempotencyKey))
	if err == nil {
		if stored != digest {
			return zero, biz.ErrConflict
		}
		old.Replayed = true
		if err = tx.Commit(ctx); err != nil {
			return zero, err
		}
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if err = restoreRejection(ctx, tx, c, digest); err != nil {
		return zero, err
	}
	target := sandboxes[c.TargetSandboxID]
	if target.Version != c.ExpectedVersion {
		return zero, biz.ErrVersion
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.TargetSandboxID).Scan(&active); err != nil {
		return zero, err
	}
	if active || target.Pending != nil || target.Desired != biz.DesiredPaused || target.Observed != biz.ObservedPaused || target.ProviderRefs.PodUID != "" || target.ProviderRefs.PVCUID == "" || target.ProviderRefs.PVUID == "" || target.ProviderRefs.PVCName == "" || target.ProviderRefs.PVName == "" {
		return zero, biz.ErrConflict
	}
	checkpoint, _, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 FOR UPDATE`, c.TenantID, c.SourceSandboxID, c.CheckpointID))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, biz.ErrNotFound
	}
	if err != nil {
		return zero, err
	}
	if checkpoint.State != "ready" || checkpoint.SnapshotUID == "" || checkpoint.ContentUID == "" || checkpoint.SnapshotHandle == "" || checkpoint.SnapshotName == "" || checkpoint.Source.ClusterID != target.Snapshot.ClusterID || checkpoint.Source.Namespace != target.Snapshot.Namespace || checkpoint.Source.NamespaceUID != target.Snapshot.NamespaceUID {
		return zero, biz.ErrConflict
	}
	var blocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND state IN ('accepted','running','unknown')) OR EXISTS(SELECT 1 FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND target_sandbox_id=$2 AND state IN ('accepted','creating','unknown','observing')) OR EXISTS(SELECT 1 FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 AND cleanup_state<>'')`, c.TenantID, c.TargetSandboxID, c.CheckpointID).Scan(&blocked); err != nil {
		return zero, err
	}
	if blocked {
		return zero, biz.ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.checkpoint_transfer_rejections(tenant_id,kind,target_sandbox_id,source_sandbox_id,checkpoint_id,operation_id,idempotency_key,request_digest,reason) VALUES($1,'restore',$2,$3,$4,$5,$6,$7,'SANDBOX_TRANSFER_WFFC_UNSUPPORTED')`, c.TenantID, c.TargetSandboxID, c.SourceSandboxID, c.CheckpointID, c.OperationID, c.IdempotencyKey, digest)
	if err != nil {
		return zero, checkpointUniqueConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return zero, biz.ErrCheckpointWFFCUnsupported
}
func (p *Postgres) RejectCheckpointCloneWFFC(ctx context.Context, c biz.CheckpointCloneCommand) (biz.CheckpointTransfer, error) {
	var zero biz.CheckpointTransfer
	t := c.Transfer
	if err := biz.RequireTenant(ctx, t.TenantID); err != nil {
		return zero, err
	}
	digest, err := biz.CheckpointCloneDigest(c)
	if err != nil {
		return zero, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,84015011))`, t.TenantID); err != nil {
		return zero, err
	}
	key := "sandbox:checkpoint:clone:" + t.TenantID + ":" + t.SourceSandboxID + ":" + t.CheckpointID + ":" + t.IdempotencyKey
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,84015010))`, key); err != nil {
		return zero, err
	}
	old, stored, err := scanTransfer(tx.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND source_sandbox_id=$2 AND checkpoint_id=$3 AND kind='clone' AND idempotency_key=$4`, t.TenantID, t.SourceSandboxID, t.CheckpointID, t.IdempotencyKey))
	if err == nil {
		if stored != digest {
			return zero, biz.ErrConflict
		}
		old.Replayed = true
		if err = tx.Commit(ctx); err != nil {
			return zero, err
		}
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	keys := []string{"sandbox:create:resource:" + t.TenantID + ":" + c.Sandbox.ID, "sandbox:create:operation:" + t.TenantID + ":" + c.Sandbox.OperationID, "sandbox:create:operation:" + t.TenantID + ":" + t.OperationID}
	sort.Strings(keys)
	for _, key = range keys {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,84015010))`, key); err != nil {
			return zero, err
		}
	}
	if err = cloneRejection(ctx, tx, c, digest); err != nil {
		return zero, err
	}
	var occupied bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2) OR EXISTS(SELECT 1 FROM sandbox.operations WHERE tenant_id=$1 AND operation_id IN ($3,$4))`, t.TenantID, t.TargetSandboxID, t.OperationID, c.Sandbox.OperationID).Scan(&occupied); err != nil {
		return zero, err
	}
	if occupied {
		return zero, biz.ErrConflict
	}
	sources, err := lockTransferSandboxes(ctx, tx, t.TenantID, t.SourceSandboxID)
	if err != nil {
		return zero, err
	}
	source := sources[t.SourceSandboxID]
	if source.Desired == biz.DesiredDeleted {
		return zero, biz.ErrConflict
	}
	if source.Version != c.SourceExpectedVersion {
		return zero, biz.ErrVersion
	}
	checkpoint, _, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 FOR UPDATE`, t.TenantID, t.SourceSandboxID, t.CheckpointID))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, biz.ErrNotFound
	}
	if err != nil {
		return zero, err
	}
	if checkpoint.State != "ready" || checkpoint.SnapshotUID == "" || checkpoint.ContentUID == "" || checkpoint.SnapshotHandle == "" || checkpoint.SnapshotName == "" || checkpoint.Source.ClusterID != c.Sandbox.Spec.ClusterID || checkpoint.Source.Namespace != c.Sandbox.Spec.Namespace || checkpoint.Source.NamespaceUID != c.Sandbox.Spec.NamespaceUID {
		return zero, biz.ErrConflict
	}
	var cleaning bool
	if err = tx.QueryRow(ctx, `SELECT cleanup_state<>'' FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, t.TenantID, t.SourceSandboxID, t.CheckpointID).Scan(&cleaning); err != nil {
		return zero, err
	}
	if cleaning {
		return zero, biz.ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.checkpoint_transfer_rejections(tenant_id,kind,target_sandbox_id,source_sandbox_id,checkpoint_id,operation_id,create_operation_id,idempotency_key,request_digest,reason) VALUES($1,'clone',$2,$3,$4,$5,$8,$6,$7,'SANDBOX_TRANSFER_WFFC_UNSUPPORTED')`, t.TenantID, t.TargetSandboxID, t.SourceSandboxID, t.CheckpointID, t.OperationID, t.IdempotencyKey, digest, c.Sandbox.OperationID)
	if err != nil {
		return zero, checkpointUniqueConflict(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return zero, biz.ErrCheckpointWFFCUnsupported
}
