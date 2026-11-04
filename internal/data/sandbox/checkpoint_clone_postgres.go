package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

var _ biz.CheckpointCloneRepository = (*Postgres)(nil)

// Lookup is a read-only replay check, used before reauthorizing today's image,
// namespace or catalogue policy. Only the exact frozen command may replay.
func (p *Postgres) LookupCheckpointClone(ctx context.Context, command biz.CheckpointCloneCommand) (biz.CheckpointTransfer, error) {
	var zero biz.CheckpointTransfer
	c := command.Transfer
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return zero, err
	}
	digest, err := biz.CheckpointCloneDigest(command)
	if err != nil {
		return zero, err
	}
	old, stored, err := scanTransfer(p.pool.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND source_sandbox_id=$2 AND checkpoint_id=$3 AND kind='clone' AND idempotency_key=$4`, c.TenantID, c.SourceSandboxID, c.CheckpointID, c.IdempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		if e := cloneRejection(ctx, p.pool, command, digest); e != nil {
			return zero, e
		}
		return zero, biz.ErrNotFound
	}
	if err != nil {
		return zero, err
	}
	if stored != digest {
		return zero, biz.ErrConflict
	}
	old.Replayed = true
	return old, nil
}

// AcceptCheckpointClone atomically reserves an independently owned Sandbox and
// its checkpoint transfer. It is an internal candidate: callers must already
// have performed normal create authorization and frozen the requested spec.
func (p *Postgres) AcceptCheckpointClone(ctx context.Context, command biz.CheckpointCloneCommand) (biz.CheckpointTransfer, error) {
	var zero biz.CheckpointTransfer
	c, create := command.Transfer, command.Sandbox
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return zero, err
	}
	digest, err := biz.CheckpointCloneDigest(command)
	if err != nil {
		return zero, err
	}
	imageDigest, ok := biz.ImageDigest(create.Spec.ImageReference)
	namespace, namespaceErr := biz.TenantNamespace(c.TenantID, "tenant-")
	if !validDigest(create.IntentDigest) || !validDigest(create.RequestDigest) || !validDigest(create.Spec.SpecDigest) || !validDigest(create.Spec.SelectionDigest) || !ok || create.Spec.ImageDigest != imageDigest || !biz.UUID(create.Spec.ClusterID) || namespaceErr != nil || create.Spec.Namespace != namespace || create.Spec.NamespaceUID == "" || create.Spec.RuntimeClass == "" || create.Spec.Workspace.StorageClass == "" || create.Spec.Workspace.Size == "" || create.Spec.Workspace.ReclaimPolicy != "Delete" {
		return zero, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,84015011))`, c.TenantID); err != nil {
		return zero, err
	}
	cloneKey := "sandbox:checkpoint:clone:" + c.TenantID + ":" + c.SourceSandboxID + ":" + c.CheckpointID + ":" + c.IdempotencyKey
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,84015010))`, cloneKey); err != nil {
		return zero, err
	}
	old, stored, err := scanTransfer(tx.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND source_sandbox_id=$2 AND checkpoint_id=$3 AND kind='clone' AND idempotency_key=$4`, c.TenantID, c.SourceSandboxID, c.CheckpointID, c.IdempotencyKey))
	if err == nil {
		if stored != digest {
			return zero, biz.ErrConflict
		}
		old.Replayed = true
		return old, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	keys := []string{"sandbox:create:resource:" + c.TenantID + ":" + create.ID, "sandbox:create:operation:" + c.TenantID + ":" + create.OperationID, "sandbox:create:operation:" + c.TenantID + ":" + c.OperationID}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,84015010))`, key); err != nil {
			return zero, err
		}
	}
	if err = cloneRejection(ctx, tx, command, digest); err != nil {
		return zero, err
	}
	source, err := lockTransferSandboxes(ctx, tx, c.TenantID, c.SourceSandboxID)
	if err != nil {
		return zero, err
	}
	if source[c.SourceSandboxID].Desired == biz.DesiredDeleted {
		return zero, biz.ErrConflict
	}
	if source[c.SourceSandboxID].Version != command.SourceExpectedVersion {
		return zero, biz.ErrVersion
	}
	checkpoint, _, err := scanCheckpoint(tx.QueryRow(ctx, checkpointSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3 FOR UPDATE`, c.TenantID, c.SourceSandboxID, c.CheckpointID))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, biz.ErrNotFound
	}
	if err != nil {
		return zero, err
	}
	var cleanupState string
	if err = tx.QueryRow(ctx, `SELECT cleanup_state FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND checkpoint_id=$3`, c.TenantID, c.SourceSandboxID, c.CheckpointID).Scan(&cleanupState); err != nil {
		return zero, err
	}
	if cleanupState != "" {
		return zero, biz.ErrConflict
	}
	if checkpoint.State != "ready" || checkpoint.SnapshotUID == "" || checkpoint.ContentUID == "" || checkpoint.SnapshotHandle == "" || checkpoint.SnapshotName == "" || checkpoint.Source.ClusterID != create.Spec.ClusterID || checkpoint.Source.Namespace != create.Spec.Namespace || checkpoint.Source.NamespaceUID != create.Spec.NamespaceUID {
		return zero, biz.ErrConflict
	}
	space, spaceErr := readTenantSpace(ctx, tx, c.TenantID, true)
	if spaceErr != nil && !errors.Is(spaceErr, biz.ErrNotFound) {
		return zero, spaceErr
	}
	if spaceErr == nil && (space.Phase != "ready" || space.NamespaceUID != create.Spec.NamespaceUID) {
		return zero, biz.ErrConflict
	}
	snapshot, err := json.Marshal(create.Spec)
	if err != nil {
		return zero, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.sandboxes(tenant_id,sandbox_id,instance_id,version,desired,observed,create_operation_id,last_operation_id,snapshot,create_intent_digest) VALUES($1,$2,'',1,'paused','provisioning',$3,$4,$5,$6)`, c.TenantID, create.ID, create.OperationID, c.OperationID, snapshot, create.IntentDigest)
	if err != nil {
		return zero, checkpointUniqueConflict(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.operations(tenant_id,operation_id,sandbox_id,action,idempotency_key,request_digest,intent_version,state) VALUES($1,$2,$3,'create',$8,$4,1,'queued'),($1,$5,$3,'checkpoint_clone',$6,$7,1,'queued')`, c.TenantID, create.OperationID, create.ID, create.RequestDigest, c.OperationID, c.IdempotencyKey, digest, create.OperationID)
	if err != nil {
		return zero, checkpointUniqueConflict(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sandbox.work(tenant_id,sandbox_id) VALUES($1,$2)`, c.TenantID, create.ID); err != nil {
		return zero, err
	}
	emptyRefs, err := json.Marshal(biz.ProviderRefs{})
	if err != nil {
		return zero, err
	}
	candidateName, err := biz.CheckpointWorkspaceName(create.ID, c.TransferID)
	if err != nil {
		return zero, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.checkpoint_transfers(tenant_id,transfer_id,operation_id,checkpoint_id,source_sandbox_id,target_sandbox_id,kind,idempotency_key,request_digest,expected_version,source_expected_version,source_snapshot_name,source_snapshot_uid,source_content_uid,source_snapshot_handle,original_refs,candidate_name,state) VALUES($1,$2,$3,$4,$5,$6,'clone',$7,$8,1,$15,$9,$10,$11,$12,$13,$14,'accepted')`, c.TenantID, c.TransferID, c.OperationID, c.CheckpointID, c.SourceSandboxID, create.ID, c.IdempotencyKey, digest, checkpoint.SnapshotName, checkpoint.SnapshotUID, checkpoint.ContentUID, checkpoint.SnapshotHandle, emptyRefs, candidateName, command.SourceExpectedVersion)
	if err != nil {
		return zero, checkpointUniqueConflict(err)
	}
	result, _, err := scanTransfer(tx.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND transfer_id=$2`, c.TenantID, c.TransferID))
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return result, nil
}
