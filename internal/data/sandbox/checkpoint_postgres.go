package sandbox

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

const checkpointSelect = `SELECT tenant_id::text,sandbox_id::text,checkpoint_id::text,operation_id::text,name,state,phase,reason,failure_reason,source,snapshot_name,snapshot_uid,content_name,content_uid,snapshot_handle,mutation_id,mutation_kind,mutation_state,protection_mutation_id,snapshot_mutation_id,release_mutation_id,created_at,completed_at,request_digest FROM sandbox.checkpoints`

func scanCheckpoint(row interface{ Scan(...any) error }) (biz.Checkpoint, string, error) {
	var c biz.Checkpoint
	var source []byte
	var digest string
	err := row.Scan(&c.TenantID, &c.SandboxID, &c.ID, &c.OperationID, &c.Name, &c.State, &c.Phase, &c.Reason, &c.FailureReason, &source, &c.SnapshotName, &c.SnapshotUID, &c.ContentName, &c.ContentUID, &c.SnapshotHandle, &c.MutationID, &c.MutationKind, &c.MutationState, &c.ProtectionMutationID, &c.SnapshotMutationID, &c.ReleaseMutationID, &c.CreatedAt, &c.CompletedAt, &digest)
	if err != nil {
		return c, "", err
	}
	err = json.Unmarshal(source, &c.Source)
	return c, digest, err
}

func (p *Postgres) AcceptCheckpoint(ctx context.Context, command biz.CheckpointCommand) (biz.Checkpoint, error) {
	if err := biz.RequireTenant(ctx, command.TenantID); err != nil {
		return biz.Checkpoint{}, err
	}
	digest, err := biz.CheckpointRequestDigest(command)
	if err != nil {
		return biz.Checkpoint{}, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return biz.Checkpoint{}, err
	}
	defer tx.Rollback(ctx)
	// All lifecycle writes lock work before sandbox. In addition to requiring
	// no active worker, incrementing its epoch fences a late read-only Finish.
	var active bool
	err = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, command.TenantID, command.SandboxID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Checkpoint{}, biz.ErrNotFound
	}
	if err != nil {
		return biz.Checkpoint{}, err
	}
	s, err := scanSandbox(tx.QueryRow(ctx, sandboxSelect+` WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s`, command.TenantID, command.SandboxID))
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Checkpoint{}, biz.ErrNotFound
	}
	if err != nil {
		return biz.Checkpoint{}, err
	}
	old, previousDigest, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND idempotency_key=$3`, command.TenantID, command.SandboxID, command.IdempotencyKey))
	if err == nil {
		if previousDigest != digest {
			return biz.Checkpoint{}, biz.ErrConflict
		}
		old.Replayed = true
		return old, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return biz.Checkpoint{}, err
	}
	if command.ExpectedVersion != s.Version {
		return biz.Checkpoint{}, biz.ErrVersion
	}
	if active || s.Pending != nil || !((s.Desired == biz.DesiredRunning && s.Observed == biz.ObservedRunning) || (s.Desired == biz.DesiredPaused && s.Observed == biz.ObservedPaused)) {
		return biz.Checkpoint{}, biz.ErrConflict
	}
	if s.Snapshot.ClusterID == "" || s.Snapshot.Namespace == "" || s.Snapshot.NamespaceUID == "" || s.ProviderRefs.PVCName == "" || s.ProviderRefs.PVCUID == "" || s.ProviderRefs.PVName == "" || s.ProviderRefs.PVUID == "" {
		return biz.Checkpoint{}, biz.ErrConflict
	}
	var activeCheckpoint bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND state IN ('accepted','running','unknown')) OR EXISTS (SELECT 1 FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND target_sandbox_id=$2 AND state IN ('accepted','creating','unknown','observing'))`, command.TenantID, command.SandboxID).Scan(&activeCheckpoint); err != nil {
		return biz.Checkpoint{}, err
	}
	if activeCheckpoint {
		return biz.Checkpoint{}, biz.ErrConflict
	}
	source := biz.CheckpointSource{ClusterID: s.Snapshot.ClusterID, Namespace: s.Snapshot.Namespace, NamespaceUID: s.Snapshot.NamespaceUID, PVCName: s.ProviderRefs.PVCName, PVCUID: s.ProviderRefs.PVCUID, PVName: s.ProviderRefs.PVName, PVUID: s.ProviderRefs.PVUID}
	raw, err := json.Marshal(source)
	if err != nil {
		return biz.Checkpoint{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sandbox.operations(tenant_id,operation_id,sandbox_id,action,idempotency_key,request_digest,intent_version,state) VALUES($1,$2,$3,'checkpoint_create',$4,$5,$6,'queued')`, command.TenantID, command.OperationID, command.SandboxID, command.IdempotencyKey, digest, s.Version); err != nil {
		return biz.Checkpoint{}, checkpointUniqueConflict(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sandbox.checkpoints(tenant_id,sandbox_id,checkpoint_id,operation_id,name,idempotency_key,request_digest,state,phase,source,snapshot_name) VALUES($1,$2,$3,$4,$5,$6,$7,'accepted','preflight',$8,$9)`, command.TenantID, command.SandboxID, command.CheckpointID, command.OperationID, command.Name, command.IdempotencyKey, digest, raw, "sandbox-checkpoint-"+command.CheckpointID); err != nil {
		return biz.Checkpoint{}, checkpointUniqueConflict(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.work SET lease_epoch=lease_epoch+1,lease_owner='',lease_until='-infinity' WHERE tenant_id=$1 AND sandbox_id=$2`, command.TenantID, command.SandboxID); err != nil {
		return biz.Checkpoint{}, err
	}
	c, _, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND checkpoint_id=$2`, command.TenantID, command.CheckpointID))
	if err != nil {
		return biz.Checkpoint{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.Checkpoint{}, err
	}
	return c, nil
}

func checkpointUniqueConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return biz.ErrConflict
	}
	return err
}

func (p *Postgres) GetCheckpoint(ctx context.Context, tenant, sandboxID, checkpointID string) (biz.Checkpoint, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return biz.Checkpoint{}, err
	}
	if !biz.UUID(sandboxID) || !biz.UUID(checkpointID) {
		return biz.Checkpoint{}, biz.ErrInvalid
	}
	c, _, err := scanCheckpoint(p.pool.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, tenant, sandboxID, checkpointID))
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Checkpoint{}, biz.ErrNotFound
	}
	return c, err
}

func (p *Postgres) ListCheckpoints(ctx context.Context, tenant, sandboxID string, page, pageSize uint32) ([]biz.Checkpoint, int64, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return nil, 0, err
	}
	if !biz.UUID(sandboxID) || pageSize > 100 {
		return nil, 0, biz.ErrInvalid
	}
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 20
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2)`, tenant, sandboxID).Scan(&exists); err != nil {
		return nil, 0, err
	}
	if !exists {
		return nil, 0, biz.ErrNotFound
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2`, tenant, sandboxID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 ORDER BY created_at DESC,checkpoint_id DESC LIMIT $3 OFFSET $4`, tenant, sandboxID, int64(pageSize), (int64(page)-1)*int64(pageSize))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]biz.Checkpoint, 0)
	for rows.Next() {
		c, _, e := scanCheckpoint(rows)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, c)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
