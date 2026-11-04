package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

var _ biz.WorkspaceGenerationCleanupRepository = (*Postgres)(nil)

const generationCleanupSelect = `SELECT tenant_id::text,sandbox_id::text,generation_id::text,cleanup_state,cleanup_mutation_id,cleanup_driver,cleanup_volume_handle,provider_refs FROM sandbox.workspace_generations`

func scanGenerationCleanup(row interface{ Scan(...any) error }) (biz.WorkspaceGenerationCleanup, error) {
	var g biz.WorkspaceGenerationCleanup
	var refs []byte
	err := row.Scan(&g.TenantID, &g.SandboxID, &g.GenerationID, &g.State, &g.MutationID, &g.Driver, &g.VolumeHandle, &refs)
	if err != nil {
		return g, err
	}
	err = json.Unmarshal(refs, &g.Refs)
	return g, err
}
func (p *Postgres) ClaimWorkspaceGenerationCleanup(ctx context.Context, owner string, lease time.Duration) (*biz.WorkspaceGenerationCleanupWork, error) {
	if owner == "" || lease <= 0 {
		return nil, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var tenant, id, generation string
	err = tx.QueryRow(ctx, `SELECT w.tenant_id::text,w.sandbox_id::text,g.generation_id::text FROM sandbox.work w JOIN sandbox.sandboxes s ON s.tenant_id=w.tenant_id AND s.sandbox_id=w.sandbox_id JOIN sandbox.workspace_generations g ON g.tenant_id=w.tenant_id AND g.sandbox_id=w.sandbox_id WHERE s.desired='deleted' AND g.state IN ('retired','active') AND (g.state='retired' OR NOT EXISTS(SELECT 1 FROM sandbox.workspace_generations prior WHERE prior.tenant_id=g.tenant_id AND prior.sandbox_id=g.sandbox_id AND prior.state='retired')) AND g.cleanup_lease_until<clock_timestamp() AND g.cleanup_due_at<=clock_timestamp() ORDER BY CASE g.state WHEN 'retired' THEN 0 ELSE 1 END,g.cleanup_due_at,g.generation_id FOR UPDATE OF w SKIP LOCKED LIMIT 1`).Scan(&tenant, &id, &generation)
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
	if s.Desired != biz.DesiredDeleted || s.Pending != nil {
		return nil, tx.Commit(ctx)
	}
	var epoch int64
	err = tx.QueryRow(ctx, `UPDATE sandbox.workspace_generations SET cleanup_lease_owner=$4,cleanup_lease_epoch=cleanup_lease_epoch+1,cleanup_lease_until=clock_timestamp()+$5::bigint*interval '1 microsecond',cleanup_state=CASE WHEN cleanup_state='deleting' THEN 'unknown' ELSE cleanup_state END WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3 AND state IN ('retired','active') AND cleanup_lease_until<clock_timestamp() RETURNING cleanup_lease_epoch`, tenant, id, generation, owner, lease.Microseconds()).Scan(&epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g, err := scanGenerationCleanup(tx.QueryRow(ctx, generationCleanupSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3`, tenant, id, generation))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &biz.WorkspaceGenerationCleanupWork{Generation: g, Sandbox: s, LeaseOwner: owner, LeaseEpoch: epoch}, nil
}
func lockGenerationCleanup(ctx context.Context, tx pgx.Tx, w biz.WorkspaceGenerationCleanupWork) (biz.WorkspaceGenerationCleanup, error) {
	g := w.Generation
	if !biz.UUID(g.TenantID) || !biz.UUID(g.SandboxID) || !biz.UUID(g.GenerationID) || w.LeaseOwner == "" || w.LeaseEpoch < 1 {
		return g, biz.ErrInvalid
	}
	var desired string
	var pending []byte
	var marker int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, g.TenantID, g.SandboxID).Scan(&marker); err != nil {
		return g, err
	}
	if err := tx.QueryRow(ctx, `SELECT desired,pending_mutation FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, g.TenantID, g.SandboxID).Scan(&desired, &pending); err != nil {
		return g, err
	}
	if desired != biz.DesiredDeleted || len(pending) != 0 {
		return g, biz.ErrConflict
	}
	current, err := scanGenerationCleanup(tx.QueryRow(ctx, generationCleanupSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3 AND state IN ('retired','active') FOR UPDATE`, g.TenantID, g.SandboxID, g.GenerationID))
	if err != nil {
		return g, err
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT cleanup_lease_owner=$4 AND cleanup_lease_epoch=$5 AND cleanup_lease_until>clock_timestamp() FROM sandbox.workspace_generations WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3`, g.TenantID, g.SandboxID, g.GenerationID, w.LeaseOwner, w.LeaseEpoch).Scan(&owned); err != nil {
		return g, err
	}
	if !owned {
		return g, biz.ErrLease
	}
	return current, nil
}
func (p *Postgres) RecordWorkspaceGenerationSource(ctx context.Context, w biz.WorkspaceGenerationCleanupWork, driver, handle string) error {
	if driver == "" || handle == "" {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	g, err := lockGenerationCleanup(ctx, tx, w)
	if err != nil {
		return err
	}
	if g.State != "" || g.MutationID != "" || g.Refs.PVCUID == "" || g.Refs.PVUID == "" {
		return biz.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.workspace_generations SET cleanup_state='ready',cleanup_driver=$4,cleanup_volume_handle=$5 WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3`, g.TenantID, g.SandboxID, g.GenerationID, driver, handle); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) PrepareWorkspaceGenerationDelete(ctx context.Context, w biz.WorkspaceGenerationCleanupWork, id string) (bool, error) {
	if !biz.UUID(id) {
		return false, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	g, err := lockGenerationCleanup(ctx, tx, w)
	if err != nil {
		return false, err
	}
	if g.MutationID != "" {
		if g.MutationID == id {
			return false, tx.Commit(ctx)
		}
		return false, biz.ErrConflict
	}
	if g.State != "ready" || g.Driver == "" || g.VolumeHandle == "" {
		return false, biz.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.workspace_generations SET cleanup_state='deleting',cleanup_mutation_id=$4 WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3`, g.TenantID, g.SandboxID, g.GenerationID, id); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
func (p *Postgres) FinishWorkspaceGenerationDelete(ctx context.Context, w biz.WorkspaceGenerationCleanupWork, id, outcome string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	g, err := lockGenerationCleanup(ctx, tx, w)
	if err != nil {
		return err
	}
	if g.MutationID != id || !biz.UUID(id) || (g.State != "deleting" && g.State != "unknown") {
		return biz.ErrConflict
	}
	state, clear := g.State, false
	switch outcome {
	case biz.MutationApplied:
		state = "observing"
	case biz.MutationUnknown:
		state = "unknown"
	case biz.MutationNotSent, biz.MutationRejected:
		if g.State == "unknown" {
			state = "unknown"
		} else {
			state = "ready"
			clear = true
		}
	default:
		return biz.ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.workspace_generations SET cleanup_state=$4,cleanup_mutation_id=CASE WHEN $5 THEN '' ELSE cleanup_mutation_id END WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3`, g.TenantID, g.SandboxID, g.GenerationID, state, clear); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) FinishWorkspaceGenerationReclaim(ctx context.Context, w biz.WorkspaceGenerationCleanupWork) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	g, err := lockGenerationCleanup(ctx, tx, w)
	if err != nil {
		return err
	}
	if (g.State != "unknown" && g.State != "observing") || g.MutationID == "" || g.Driver == "" || g.VolumeHandle == "" {
		return biz.ErrConflict
	}
	// If this is the active generation, publish the attributed reclaim receipt
	// alongside the generation/reference state. The ordinary lifecycle worker
	// can then complete deletion with the same PVC/PV identity.
	var generationState string
	if err = tx.QueryRow(ctx, `SELECT state FROM sandbox.workspace_generations WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3`, g.TenantID, g.SandboxID, g.GenerationID).Scan(&generationState); err != nil {
		return err
	}
	if generationState == "active" {
		var version int64
		var raw []byte
		if err = tx.QueryRow(ctx, `SELECT version,provider_refs FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2`, g.TenantID, g.SandboxID).Scan(&version, &raw); err != nil {
			return err
		}
		var refs biz.ProviderRefs
		if err = json.Unmarshal(raw, &refs); err != nil {
			return err
		}
		if refs.PVCName != g.Refs.PVCName || refs.PVCUID != g.Refs.PVCUID || refs.PVName != g.Refs.PVName || refs.PVUID != g.Refs.PVUID || refs.PVCSourceMutationID != g.Refs.PVCSourceMutationID {
			return biz.ErrOwnership
		}
		refs.CleanupReceipt = fmt.Sprintf("k8s-delete:%s:%d:pvc=%s:pv=%s", g.SandboxID, version, refs.PVCUID, refs.PVUID)
		raw, err = json.Marshal(refs)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE sandbox.sandboxes SET provider_refs=$3 WHERE tenant_id=$1 AND sandbox_id=$2`, g.TenantID, g.SandboxID, raw); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE sandbox.workspace_bindings SET cleanup_receipt=$5 WHERE tenant_id=$1 AND sandbox_id=$2 AND pvc_name=$3 AND pvc_uid=$4`, g.TenantID, g.SandboxID, refs.PVCName, refs.PVCUID, refs.CleanupReceipt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return biz.ErrOwnership
		}
	}
	// A restored generation retains its source reference until this exact PVC/PV
	// has a journaled delete and the CSI provisioner has reclaimed its PV.  The
	// identity match must agree with the immutable commit-time transfer facts.
	if g.Refs.PVCSourceSnapshotName != "" {
		tag, err := tx.Exec(ctx, `UPDATE sandbox.checkpoint_transfers SET source_released=true WHERE tenant_id=$1 AND target_sandbox_id=$2 AND candidate_name=$3 AND candidate_uid=$4 AND candidate_pv_name=$5 AND candidate_pv_uid=$6 AND source_snapshot_name=$7 AND source_snapshot_uid=$8 AND source_content_uid=$9 AND source_snapshot_handle=$10 AND mutation_id=$11 AND state='ready' AND NOT source_released`, g.TenantID, g.SandboxID, g.Refs.PVCName, g.Refs.PVCUID, g.Refs.PVName, g.Refs.PVUID, g.Refs.PVCSourceSnapshotName, g.Refs.PVCSourceSnapshotUID, g.Refs.PVCSourceContentUID, g.Refs.PVCSourceSnapshotHandle, g.Refs.PVCSourceMutationID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return biz.ErrOwnership
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.workspace_generations SET state='reclaimed',cleanup_state='done',cleanup_completed_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3`, g.TenantID, g.SandboxID, g.GenerationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) ReleaseWorkspaceGenerationCleanup(ctx context.Context, w biz.WorkspaceGenerationCleanupWork, next time.Duration) error {
	if next < 0 {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Reclaimed rows still need their lease cleared after a successful step.
	g := w.Generation
	var marker int
	if err = tx.QueryRow(ctx, `SELECT 1 FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, g.TenantID, g.SandboxID).Scan(&marker); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE sandbox.workspace_generations SET cleanup_lease_owner='',cleanup_lease_until='-infinity',cleanup_due_at=clock_timestamp()+$6::bigint*interval '1 microsecond' WHERE tenant_id=$1 AND sandbox_id=$2 AND generation_id=$3 AND cleanup_lease_owner=$4 AND cleanup_lease_epoch=$5 AND cleanup_lease_until>clock_timestamp()`, g.TenantID, g.SandboxID, g.GenerationID, w.LeaseOwner, w.LeaseEpoch, next.Microseconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return biz.ErrLease
	}
	return tx.Commit(ctx)
}
