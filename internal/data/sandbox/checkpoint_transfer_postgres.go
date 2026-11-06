package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

var _ biz.CheckpointTransferRepository = (*Postgres)(nil)

func checkpointTransferTargetStopped(t biz.CheckpointTransfer, s biz.Sandbox) bool {
	return s.Desired == biz.DesiredPaused && (s.Observed == biz.ObservedPaused || (t.Kind == "clone" && s.Observed == biz.ObservedProvisioning)) && s.Pending == nil && s.ProviderRefs.PodUID == ""
}

const transferSelect = `SELECT tenant_id::text,transfer_id::text,operation_id::text,checkpoint_id::text,source_sandbox_id::text,target_sandbox_id::text,kind,state,reason,expected_version,source_expected_version,source_snapshot_name,source_snapshot_uid,source_content_uid,source_snapshot_handle,original_refs,candidate_name,candidate_uid,candidate_pv_name,candidate_pv_uid,mutation_id,created_at,completed_at,request_digest FROM sandbox.checkpoint_transfers`

func scanTransfer(row interface{ Scan(...any) error }) (biz.CheckpointTransfer, string, error) {
	var t biz.CheckpointTransfer
	var refs []byte
	var digest string
	err := row.Scan(&t.TenantID, &t.ID, &t.OperationID, &t.CheckpointID, &t.SourceSandboxID, &t.TargetSandboxID, &t.Kind, &t.State, &t.Reason, &t.ExpectedVersion, &t.SourceExpectedVersion, &t.SourceSnapshotName, &t.SourceSnapshotUID, &t.SourceContentUID, &t.SourceSnapshotHandle, &refs, &t.CandidateName, &t.CandidateUID, &t.CandidatePVName, &t.CandidatePVUID, &t.MutationID, &t.CreatedAt, &t.CompletedAt, &digest)
	if err != nil {
		return t, "", err
	}
	err = json.Unmarshal(refs, &t.OriginalRefs)
	return t, digest, err
}

// lockTransferSandboxes enforces a stable cross-Sandbox order. A checkpoint
// delete implementation must acquire these same rows before its source row.
func lockTransferSandboxes(ctx context.Context, tx pgx.Tx, tenant string, ids ...string) (map[string]biz.Sandbox, error) {
	sort.Strings(ids)
	ret := map[string]biz.Sandbox{}
	for _, id := range ids {
		if _, ok := ret[id]; ok {
			continue
		}
		var marker int
		err := tx.QueryRow(ctx, `SELECT 1 FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, tenant, id).Scan(&marker)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, biz.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		ret[id] = biz.Sandbox{}
	}
	for _, id := range ids {
		if ret[id].ID != "" {
			continue
		}
		s, err := scanSandbox(tx.QueryRow(ctx, sandboxSelect+` WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s`, tenant, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, biz.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		ret[id] = s
	}
	return ret, nil
}

// LookupCheckpointTransfer finds an exact accepted command before consulting
// mutable cluster policy. Acceptance rechecks under the regular row locks.
func (p *Postgres) LookupCheckpointTransfer(ctx context.Context, c biz.CheckpointTransferCommand) (biz.CheckpointTransfer, error) {
	var zero biz.CheckpointTransfer
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return zero, err
	}
	digest, err := biz.CheckpointTransferDigest(c)
	if err != nil {
		return zero, err
	}
	old, stored, err := scanTransfer(p.pool.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND target_sandbox_id=$2 AND kind=$3 AND idempotency_key=$4`, c.TenantID, c.TargetSandboxID, c.Kind, c.IdempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		if e := restoreRejection(ctx, p.pool, c, digest); e != nil {
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

func (p *Postgres) AcceptCheckpointTransfer(ctx context.Context, c biz.CheckpointTransferCommand) (biz.CheckpointTransfer, error) {
	var zero biz.CheckpointTransfer
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return zero, err
	}
	digest, err := biz.CheckpointTransferDigest(c)
	if err != nil {
		return zero, err
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
		return old, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if err = restoreRejection(ctx, tx, c, digest); err != nil {
		return zero, err
	}
	target := sandboxes[c.TargetSandboxID]
	sourceSandbox := sandboxes[c.SourceSandboxID]
	if sourceSandbox.Desired == biz.DesiredDeleted {
		return zero, biz.ErrConflict
	}
	if target.Version != c.ExpectedVersion {
		return zero, biz.ErrVersion
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.TargetSandboxID).Scan(&active); err != nil {
		return zero, err
	}
	if active || target.Pending != nil || target.Desired != biz.DesiredPaused || target.Observed != biz.ObservedPaused || target.ProviderRefs.PodUID != "" {
		return zero, biz.ErrConflict
	}
	if c.Kind == "restore" && (target.ProviderRefs.PVCName == "" || target.ProviderRefs.PVCUID == "" || target.ProviderRefs.PVName == "" || target.ProviderRefs.PVUID == "") {
		return zero, biz.ErrConflict
	}
	// Clone targets are internal, independently allocated shells, with a
	// separately authorized frozen spec. They are never another instance.
	if c.Kind == "clone" && (target.ProviderRefs.PVCUID != "" || target.ProviderRefs.PVUID != "" || target.ProviderRefs.PVCName != "" || target.ProviderRefs.PVName != "") {
		return zero, biz.ErrConflict
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
	if checkpoint.State != "ready" || checkpoint.SnapshotUID == "" || checkpoint.ContentUID == "" || checkpoint.SnapshotHandle == "" || checkpoint.SnapshotName == "" || checkpoint.Source.ClusterID != target.Snapshot.ClusterID || checkpoint.Source.Namespace != target.Snapshot.Namespace || checkpoint.Source.NamespaceUID != target.Snapshot.NamespaceUID {
		return zero, biz.ErrConflict
	}
	var blocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND state IN ('accepted','running','unknown')) OR EXISTS(SELECT 1 FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND target_sandbox_id=$2 AND state IN ('accepted','creating','unknown','observing'))`, c.TenantID, c.TargetSandboxID).Scan(&blocked); err != nil {
		return zero, err
	}
	if blocked {
		return zero, biz.ErrConflict
	}
	raw, err := json.Marshal(target.ProviderRefs)
	if err != nil {
		return zero, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.operations(tenant_id,operation_id,sandbox_id,action,idempotency_key,request_digest,intent_version,state) VALUES($1,$2,$3,$4,$5,$6,$7,'queued')`, c.TenantID, c.OperationID, c.TargetSandboxID, "checkpoint_"+c.Kind, c.IdempotencyKey, digest, c.ExpectedVersion)
	if err != nil {
		return zero, checkpointUniqueConflict(err)
	}
	candidateName, err := biz.CheckpointWorkspaceName(c.TargetSandboxID, c.TransferID)
	if err != nil {
		return zero, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.checkpoint_transfers(tenant_id,transfer_id,operation_id,checkpoint_id,source_sandbox_id,target_sandbox_id,kind,idempotency_key,request_digest,expected_version,source_snapshot_name,source_snapshot_uid,source_content_uid,source_snapshot_handle,original_refs,candidate_name,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'accepted')`, c.TenantID, c.TransferID, c.OperationID, c.CheckpointID, c.SourceSandboxID, c.TargetSandboxID, c.Kind, c.IdempotencyKey, digest, c.ExpectedVersion, checkpoint.SnapshotName, checkpoint.SnapshotUID, checkpoint.ContentUID, checkpoint.SnapshotHandle, raw, candidateName)
	if err != nil {
		return zero, checkpointUniqueConflict(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.work SET lease_epoch=lease_epoch+1,lease_owner='',lease_until='-infinity' WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.TargetSandboxID); err != nil {
		return zero, err
	}
	out, _, err := scanTransfer(tx.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND transfer_id=$2`, c.TenantID, c.TransferID))
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return out, nil
}
func (p *Postgres) GetCheckpointTransfer(ctx context.Context, tenant, target, id string) (biz.CheckpointTransfer, error) {
	var zero biz.CheckpointTransfer
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return zero, err
	}
	if !biz.UUID(target) || !biz.UUID(id) {
		return zero, biz.ErrInvalid
	}
	t, _, err := scanTransfer(p.pool.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND target_sandbox_id=$2 AND transfer_id=$3`, tenant, target, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, biz.ErrNotFound
	}
	return t, err
}
func (p *Postgres) ClaimCheckpointTransfer(ctx context.Context, owner string, lease time.Duration) (*biz.CheckpointTransferWork, error) {
	if owner == "" || lease <= 0 {
		return nil, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var tenant, source, target, id string
	// Advisory source/target serialization is done after choosing a candidate;
	// all contenders acquire the work rows in canonical order.
	err = tx.QueryRow(ctx, `SELECT tenant_id::text,source_sandbox_id::text,target_sandbox_id::text,transfer_id::text FROM sandbox.checkpoint_transfers WHERE state IN ('accepted','creating','unknown','observing') AND due_at<=clock_timestamp() AND lease_until<clock_timestamp() ORDER BY due_at,transfer_id LIMIT 1`).Scan(&tenant, &source, &target, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sandboxes, err := lockTransferSandboxes(ctx, tx, tenant, source, target)
	if err != nil {
		return nil, err
	}
	var epoch int64
	err = tx.QueryRow(ctx, `UPDATE sandbox.checkpoint_transfers SET lease_owner=$3,lease_epoch=lease_epoch+1,lease_until=clock_timestamp()+$4::bigint*interval '1 microsecond',state=CASE WHEN state='creating' THEN 'unknown' ELSE state END,reason=CASE WHEN state='creating' THEN 'SANDBOX_TRANSFER_OUTCOME_UNKNOWN' ELSE reason END WHERE tenant_id=$1 AND transfer_id=$2 AND state IN ('accepted','creating','unknown','observing') AND lease_until<clock_timestamp() AND due_at<=clock_timestamp() RETURNING lease_epoch`, tenant, id, owner, lease.Microseconds()).Scan(&epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, _, err := scanTransfer(tx.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND transfer_id=$2`, tenant, id))
	if err != nil {
		return nil, err
	}
	state := "running"
	if t.State == "unknown" {
		state = "unknown"
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state=$3,reason=$4 WHERE tenant_id=$1 AND operation_id=$2`, tenant, t.OperationID, state, t.Reason); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &biz.CheckpointTransferWork{Transfer: t, Sandbox: sandboxes[target], LeaseOwner: owner, LeaseEpoch: epoch}, nil
}
func lockTransfer(ctx context.Context, tx pgx.Tx, w biz.CheckpointTransferWork) (biz.CheckpointTransfer, biz.Sandbox, error) {
	t := w.Transfer
	if !biz.UUID(t.TenantID) || !biz.UUID(t.ID) || w.LeaseOwner == "" || w.LeaseEpoch <= 0 {
		return t, biz.Sandbox{}, biz.ErrInvalid
	}
	sandboxes, err := lockTransferSandboxes(ctx, tx, t.TenantID, t.SourceSandboxID, t.TargetSandboxID)
	if err != nil {
		return t, biz.Sandbox{}, err
	}
	current, _, err := scanTransfer(tx.QueryRow(ctx, transferSelect+` WHERE tenant_id=$1 AND transfer_id=$2 FOR UPDATE`, t.TenantID, t.ID))
	if err != nil {
		return t, biz.Sandbox{}, err
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT lease_owner=$3 AND lease_epoch=$4 AND lease_until>clock_timestamp() FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND transfer_id=$2`, t.TenantID, t.ID, w.LeaseOwner, w.LeaseEpoch).Scan(&owned); err != nil {
		return t, biz.Sandbox{}, err
	}
	if !owned {
		return t, biz.Sandbox{}, biz.ErrLease
	}
	return current, sandboxes[t.TargetSandboxID], nil
}
func (p *Postgres) PrepareCheckpointTransferCreate(ctx context.Context, w biz.CheckpointTransferWork, mutationID string) (bool, error) {
	if !biz.UUID(mutationID) {
		return false, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	t, s, err := lockTransfer(ctx, tx, w)
	if err != nil {
		return false, err
	}
	var sourceDeleting bool
	if err = tx.QueryRow(ctx, `SELECT desired='deleted' FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2`, t.TenantID, t.SourceSandboxID).Scan(&sourceDeleting); err != nil {
		return false, err
	}
	if sourceDeleting {
		return false, biz.ErrConflict
	}
	if t.MutationID != "" {
		if t.MutationID == mutationID {
			return false, tx.Commit(ctx)
		}
		return false, biz.ErrConflict
	}
	if t.State != "accepted" || s.Version != t.ExpectedVersion || !checkpointTransferTargetStopped(t, s) || s.ProviderRefs != t.OriginalRefs {
		return false, biz.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET state='creating',mutation_id=$3 WHERE tenant_id=$1 AND transfer_id=$2`, t.TenantID, t.ID, mutationID); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
func (p *Postgres) FinishCheckpointTransferCreate(ctx context.Context, w biz.CheckpointTransferWork, mutationID, outcome, uid string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	t, target, err := lockTransfer(ctx, tx, w)
	if err != nil {
		return err
	}
	if t.MutationID != mutationID || !biz.UUID(mutationID) || (t.State != "creating" && t.State != "unknown") {
		return biz.ErrConflict
	}
	state, reason := t.State, t.Reason
	switch outcome {
	case biz.MutationApplied:
		if uid == "" {
			return biz.ErrInvalid
		}
		state = "observing"
		reason = ""
	case biz.MutationUnknown:
		state = "unknown"
		reason = "SANDBOX_TRANSFER_OUTCOME_UNKNOWN"
	case biz.MutationRejected, biz.MutationNotSent, biz.MutationWFFCUnsupported:
		if t.State == "unknown" {
			state = "unknown"
			reason = "SANDBOX_TRANSFER_OUTCOME_UNKNOWN"
		} else {
			state = "failed"
			reason = "SANDBOX_TRANSFER_CREATE_REJECTED"
			if outcome == biz.MutationWFFCUnsupported {
				reason = "SANDBOX_TRANSFER_WFFC_UNSUPPORTED"
			}
		}
	default:
		return biz.ErrInvalid
	}
	_, err = tx.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET state=$3,reason=$4,candidate_uid=CASE WHEN $3='observing' THEN $5 ELSE candidate_uid END,source_released=CASE WHEN $3='failed' AND state='creating' AND candidate_uid='' THEN true ELSE source_released END,completed_at=CASE WHEN $3='failed' THEN clock_timestamp() ELSE NULL END WHERE tenant_id=$1 AND transfer_id=$2`, t.TenantID, t.ID, state, reason, uid)
	if err != nil {
		return err
	}
	opState := state
	if state == "observing" {
		opState = "running"
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state=$3,reason=$4,completed_at=CASE WHEN $3='failed' THEN clock_timestamp() ELSE NULL END WHERE tenant_id=$1 AND operation_id=$2`, t.TenantID, t.OperationID, opState, reason); err != nil {
		return err
	}
	if t.Kind == "clone" && state == "failed" {
		if _, err = tx.Exec(ctx, `UPDATE sandbox.sandboxes SET observed='unknown',reason=$3,updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, t.TenantID, t.TargetSandboxID, reason); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state='failed',reason=$3,completed_at=clock_timestamp() WHERE tenant_id=$1 AND operation_id=$2 AND state IN ('queued','running','retrying','unknown')`, t.TenantID, target.CreateOperationID, reason); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SwitchCheckpointTransfer is called only after a trusted provider has
// verified the exact target PVC UID, its CSI snapshot source and bound PV UID.
// Old workspace identities remain retired and are never deleted here.
func (p *Postgres) SwitchCheckpointTransfer(ctx context.Context, w biz.CheckpointTransferWork, pvcName, pvcUID, pvName, pvUID string) error {
	if pvcName == "" || pvcUID == "" || pvName == "" || pvUID == "" {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	t, s, err := lockTransfer(ctx, tx, w)
	if err != nil {
		return err
	}
	if t.State != "observing" || t.CandidateName != pvcName || t.CandidateUID != pvcUID || pvcUID == t.OriginalRefs.PVCUID || s.Version != t.ExpectedVersion || !checkpointTransferTargetStopped(t, s) || s.ProviderRefs != t.OriginalRefs {
		return biz.ErrConflict
	}
	var sourceLive bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.checkpoints c JOIN sandbox.sandboxes s ON s.tenant_id=c.tenant_id AND s.sandbox_id=c.sandbox_id WHERE c.tenant_id=$1 AND c.sandbox_id=$2 AND c.checkpoint_id=$3 AND c.state='ready' AND c.cleanup_state='' AND c.snapshot_uid=$4 AND c.content_uid=$5 AND c.snapshot_handle=$6 AND s.desired<>'deleted')`, t.TenantID, t.SourceSandboxID, t.CheckpointID, t.SourceSnapshotUID, t.SourceContentUID, t.SourceSnapshotHandle).Scan(&sourceLive); err != nil {
		return err
	}
	if !sourceLive {
		return biz.ErrConflict
	}
	// The active generation is backfilled on first restore and retired in the
	// same transaction; duplicate or mismatched historical ownership conflicts.
	if t.OriginalRefs.PVCUID != "" {
		oldRefs, marshalErr := json.Marshal(t.OriginalRefs)
		if marshalErr != nil {
			return marshalErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO sandbox.workspace_generations(tenant_id,sandbox_id,generation_id,pvc_name,pvc_uid,pv_name,pv_uid,state,provider_refs) SELECT $1,$2,$3,$4,$5,$6,$7,'active',$8 WHERE NOT EXISTS(SELECT 1 FROM sandbox.workspace_generations WHERE tenant_id=$1 AND sandbox_id=$2 AND state='active')`, t.TenantID, t.TargetSandboxID, uuid.NewString(), t.OriginalRefs.PVCName, t.OriginalRefs.PVCUID, t.OriginalRefs.PVName, t.OriginalRefs.PVUID, oldRefs)
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE sandbox.workspace_generations SET state='retired' WHERE tenant_id=$1 AND sandbox_id=$2 AND state='active' AND pvc_name=$3 AND pvc_uid=$4 AND pv_name=$5 AND pv_uid=$6`, t.TenantID, t.TargetSandboxID, t.OriginalRefs.PVCName, t.OriginalRefs.PVCUID, t.OriginalRefs.PVName, t.OriginalRefs.PVUID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return biz.ErrConflict
		}
	}
	refs := s.ProviderRefs
	refs.PVCName = pvcName
	refs.PVCUID = pvcUID
	refs.PVName = pvName
	refs.PVUID = pvUID
	refs.PVCResourceVersion = ""
	refs.CleanupReceipt = ""
	refs.PVCSourceSnapshotName = t.SourceSnapshotName
	refs.PVCSourceSnapshotUID = t.SourceSnapshotUID
	refs.PVCSourceContentUID = t.SourceContentUID
	refs.PVCSourceSnapshotHandle = t.SourceSnapshotHandle
	refs.PVCSourceMutationID = t.MutationID
	raw, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.workspace_generations(tenant_id,sandbox_id,generation_id,pvc_name,pvc_uid,pv_name,pv_uid,state,source_checkpoint_id,source_snapshot_uid,provider_refs) VALUES($1,$2,$3,$4,$5,$6,$7,'active',$8,$9,$10)`, t.TenantID, t.TargetSandboxID, t.ID, pvcName, pvcUID, pvName, pvUID, t.CheckpointID, t.SourceSnapshotUID, raw)
	if err != nil {
		return checkpointUniqueConflict(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.sandboxes SET provider_refs=$3,version=version+1,last_operation_id=$4,observed='paused',reason='',updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, t.TenantID, t.TargetSandboxID, raw, t.OperationID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sandbox.workspace_bindings(tenant_id,sandbox_id,namespace,pvc_name,pvc_uid,cleanup_receipt) VALUES($1,$2,$3,$4,$5,'') ON CONFLICT(tenant_id,sandbox_id) DO UPDATE SET namespace=EXCLUDED.namespace,pvc_name=EXCLUDED.pvc_name,pvc_uid=EXCLUDED.pvc_uid,cleanup_receipt=''`, t.TenantID, t.TargetSandboxID, s.Snapshot.Namespace, pvcName, pvcUID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET state='ready',reason='',candidate_pv_name=$3,candidate_pv_uid=$4,completed_at=clock_timestamp() WHERE tenant_id=$1 AND transfer_id=$2`, t.TenantID, t.ID, pvName, pvUID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state='succeeded',reason='',completed_at=clock_timestamp() WHERE tenant_id=$1 AND operation_id=$2`, t.TenantID, t.OperationID); err != nil {
		return err
	}
	if t.Kind == "clone" {
		if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state='succeeded',reason='',completed_at=clock_timestamp() WHERE tenant_id=$1 AND operation_id=$2 AND state IN ('queued','running','retrying','unknown')`, t.TenantID, s.CreateOperationID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ReleaseCheckpointTransferLease(ctx context.Context, w biz.CheckpointTransferWork, next time.Duration) error {
	if next < 0 {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	t, _, err := lockTransfer(ctx, tx, w)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET lease_owner='',lease_until='-infinity',due_at=clock_timestamp()+$3::bigint*interval '1 microsecond' WHERE tenant_id=$1 AND transfer_id=$2`, t.TenantID, t.ID, next.Microseconds()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) RecordCheckpointTransferPending(ctx context.Context, w biz.CheckpointTransferWork, reason string) error {
	if reason != "SANDBOX_TRANSFER_WAIT_FOR_FIRST_CONSUMER" && reason != "SANDBOX_TRANSFER_PVC_PENDING" {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	t, _, err := lockTransfer(ctx, tx, w)
	if err != nil {
		return err
	}
	if t.State != "observing" {
		return biz.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET reason=$3 WHERE tenant_id=$1 AND transfer_id=$2`, t.TenantID, t.ID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET reason=$3 WHERE tenant_id=$1 AND operation_id=$2`, t.TenantID, t.OperationID, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
