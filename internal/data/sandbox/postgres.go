package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	migrations "github.com/zhangzhe-ctrl/ani-resource-service/migrations/sandbox"
)

type Postgres struct{ pool *pgxpool.Pool }

var schemaFiles = []string{"001_sandbox.sql", "002_workspace_bindings.sql", "003_recovery_and_create_intent.sql", "004_tenant_listing.sql", "005_code_runs.sql", "006_file_operations.sql", "007_access_tokens.sql", "008_tenant_space.sql", "009_code_run_completion.sql", "010_operation_completion.sql", "011_file_operation_completion.sql", "012_pod_history.sql", "013_preview_experimental.sql", "014_checkpoint.sql", "015_checkpoint_transfer.sql", "016_checkpoint_transfer_rejections.sql", "017_checkpoint_clone_rejection_reasons.sql", "018_checkpoint_clone_identity_conflicts.sql"}

func validDigest(v string) bool {
	if len(v) != 64 || strings.ToLower(v) != v {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}

func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid sandbox database configuration")
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("sandbox database unavailable")
	}
	r := &Postgres{pool: p}
	if err = r.CheckReady(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return r, nil
}
func (p *Postgres) Close() { p.pool.Close() }
func (p *Postgres) CheckReady(ctx context.Context) error {
	for i, name := range schemaFiles {
		b, err := migrations.Files.ReadFile(name)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		var got string
		if err = p.pool.QueryRow(ctx, "SELECT checksum FROM sandbox.schema_version WHERE version=$1", i+1).Scan(&got); err != nil || got != hex.EncodeToString(h[:]) {
			return fmt.Errorf("sandbox schema not migrated or checksum differs")
		}
	}
	return nil
}
func Migrate(ctx context.Context, dsn, role string) error {
	if role == "" {
		return biz.ErrInvalid
	}
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer p.Close()
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(84015009)"); err != nil {
		return err
	}
	var current string
	if err = tx.QueryRow(ctx, "SELECT current_user").Scan(&current); err != nil {
		return err
	}
	if current == role {
		return fmt.Errorf("migration owner and runtime role must differ")
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT to_regclass('sandbox.schema_version') IS NOT NULL").Scan(&exists); err != nil {
		return err
	}
	b, _ := migrations.Files.ReadFile(schemaFiles[0])
	h := sha256.Sum256(b)
	sum := hex.EncodeToString(h[:])
	if !exists {
		if _, err = tx.Exec(ctx, string(b)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO sandbox.schema_version VALUES(1,$1)", sum); err != nil {
			return err
		}
	} else {
		var old string
		if err = tx.QueryRow(ctx, "SELECT checksum FROM sandbox.schema_version WHERE version=1").Scan(&old); err != nil {
			return err
		}
		if old != sum {
			return fmt.Errorf("sandbox migration checksum differs")
		}
	}
	for i, name := range schemaFiles[1:] {
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			return err
		}
		h := sha256.Sum256(body)
		want := hex.EncodeToString(h[:])
		version := i + 2
		var old string
		err = tx.QueryRow(ctx, "SELECT checksum FROM sandbox.schema_version WHERE version=$1", version).Scan(&old)
		if errors.Is(err, pgx.ErrNoRows) {
			if _, err = tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO sandbox.schema_version VALUES($1,$2)", version, want); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if old != want {
			return fmt.Errorf("sandbox migration %s checksum differs", name)
		}
	}
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err = tx.Exec(ctx, "GRANT USAGE ON SCHEMA sandbox TO "+quoted+"; GRANT SELECT ON sandbox.schema_version TO "+quoted+"; GRANT SELECT,INSERT,UPDATE ON sandbox.sandboxes,sandbox.operations,sandbox.work,sandbox.workspace_bindings,sandbox.code_runs,sandbox.file_operations,sandbox.access_tokens,sandbox.tenant_spaces,sandbox.tenant_space_mutations,sandbox.pod_history,sandbox.preview_ports,sandbox.preview_port_operations,sandbox.checkpoints,sandbox.checkpoint_transfers,sandbox.workspace_generations,sandbox.checkpoint_transfer_rejections,sandbox.checkpoint_clone_identity_conflicts TO "+quoted); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) AcceptCreate(ctx context.Context, c biz.Create) (biz.ACK, error) {
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return biz.ACK{}, err
	}
	if !biz.UUID(c.ID) || !biz.UUID(c.OperationID) || len([]rune(c.InstanceID)) > 128 || !validDigest(c.RequestDigest) || (c.IntentDigest != "" && !validDigest(c.IntentDigest)) || c.Spec.ImageDigest == "" {
		return biz.ACK{}, biz.ErrInvalid
	}
	raw, _ := json.Marshal(c.Spec)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return biz.ACK{}, err
	}
	defer tx.Rollback(ctx)
	// Serialize acceptance with close, including the legacy absent-row case.
	// Once a space is tracked, new commands require its exact ready namespace.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1::text,84015011))", c.TenantID); err != nil {
		return biz.ACK{}, err
	}
	// Both the resource and operation IDs are unique acceptance identities. Lock
	// them before inspecting rows so concurrent first deliveries have a replay
	// path rather than racing an INSERT into a uniqueness violation.
	keys := []string{"sandbox:create:resource:" + c.TenantID + ":" + c.ID, "sandbox:create:operation:" + c.TenantID + ":" + c.OperationID}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1::text,84015010))", key); err != nil {
			return biz.ACK{}, err
		}
	}
	// A durably rejected clone owns its target/operation identity too.
	var rejectedClone bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.checkpoint_transfer_rejections WHERE tenant_id=$1 AND kind='clone' AND (target_sandbox_id=$2 OR operation_id=$3 OR create_operation_id=$3))`, c.TenantID, c.ID, c.OperationID).Scan(&rejectedClone); err != nil {
		return biz.ACK{}, err
	}
	if rejectedClone {
		return biz.ACK{}, biz.ErrConflict
	}
	var existingSandbox, existingAction string
	if err = tx.QueryRow(ctx, "SELECT sandbox_id::text,action FROM sandbox.operations WHERE tenant_id=$1 AND operation_id=$2", c.TenantID, c.OperationID).Scan(&existingSandbox, &existingAction); err == nil {
		if existingSandbox != c.ID || existingAction != "create" {
			return biz.ACK{}, biz.ErrConflict
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return biz.ACK{}, err
	}
	var oldOp string
	var oldInstance, oldIntent string
	var sameSnapshot bool
	var oldDigest []byte
	err = tx.QueryRow(ctx, `SELECT s.create_operation_id::text,s.instance_id,s.snapshot=$3::jsonb,o.request_digest,s.create_intent_digest
		FROM sandbox.sandboxes s JOIN sandbox.operations o ON o.tenant_id=s.tenant_id AND o.operation_id=s.create_operation_id
		WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s`, c.TenantID, c.ID, raw).Scan(&oldOp, &oldInstance, &sameSnapshot, &oldDigest, &oldIntent)
	if err == nil {
		if oldOp == c.OperationID && oldInstance == c.InstanceID && sameSnapshot && string(oldDigest) == c.RequestDigest && oldIntent == c.IntentDigest {
			return biz.ACK{OperationID: c.OperationID, ResourceID: c.ID, Accepted: true, Replayed: true}, nil
		}
		return biz.ACK{}, biz.ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return biz.ACK{}, err
	}
	space, spaceErr := readTenantSpace(ctx, tx, c.TenantID, true)
	if spaceErr != nil && !errors.Is(spaceErr, biz.ErrNotFound) {
		return biz.ACK{}, spaceErr
	}
	if spaceErr == nil && (space.Phase != "ready" || space.NamespaceUID != c.Spec.NamespaceUID) {
		return biz.ACK{}, biz.ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.sandboxes(tenant_id,sandbox_id,instance_id,version,desired,observed,create_operation_id,last_operation_id,snapshot,create_intent_digest) VALUES($1,$2,$3,1,'running','provisioning',$4,$4,$5,$6)`, c.TenantID, c.ID, c.InstanceID, c.OperationID, raw, c.IntentDigest)
	if err != nil {
		return biz.ACK{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.operations(tenant_id,operation_id,sandbox_id,action,idempotency_key,request_digest,intent_version,state) VALUES($1,$2,$3,'create',$4,$5,1,'queued')`, c.TenantID, c.OperationID, c.ID, c.OperationID, c.RequestDigest)
	if err != nil {
		return biz.ACK{}, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO sandbox.work(tenant_id,sandbox_id) VALUES($1,$2)", c.TenantID, c.ID)
	if err != nil {
		return biz.ACK{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.ACK{}, err
	}
	return biz.ACK{OperationID: c.OperationID, ResourceID: c.ID, Accepted: true}, nil
}

// All readers obtain the original create digest with the immutable snapshot;
// the latest action's digest is a different command identity.
const sandboxSelect = `SELECT s.tenant_id::text,s.sandbox_id::text,s.instance_id,s.version,s.desired,s.observed,s.reason,
 s.create_operation_id::text,s.last_operation_id::text,s.snapshot,s.provider_refs,s.observed_at,
 s.create_intent_digest,o.request_digest,s.pending_mutation
 FROM sandbox.sandboxes s JOIN sandbox.operations o ON o.tenant_id=s.tenant_id AND o.operation_id=s.create_operation_id`

func (p *Postgres) Get(ctx context.Context, tenant, id string) (biz.Sandbox, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return biz.Sandbox{}, err
	}
	s, err := scanSandbox(p.pool.QueryRow(ctx, sandboxSelect+" WHERE s.tenant_id=$1 AND s.sandbox_id=$2", tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Sandbox{}, biz.ErrNotFound
	}
	return s, err
}
func (p *Postgres) List(ctx context.Context, tenant string, f biz.ListFilter) ([]biz.Sandbox, int64, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return nil, 0, err
	}
	if f.PageSize > 100 {
		return nil, 0, biz.ErrInvalid
	}
	page, size := int64(f.Page), int64(f.PageSize)
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, sandboxSelect+` WHERE s.tenant_id=$1
		ORDER BY s.created_at DESC,s.sandbox_id DESC LIMIT $2 OFFSET $3`, tenant, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []biz.Sandbox{}
	for rows.Next() {
		x, err := scanSandbox(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, x)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	var total int64
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM sandbox.sandboxes WHERE tenant_id=$1", tenant).Scan(&total); err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
func (p *Postgres) GetOperation(ctx context.Context, tenant, id string) (biz.Operation, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return biz.Operation{}, err
	}
	var o biz.Operation
	err := p.pool.QueryRow(ctx, `SELECT o.operation_id::text,o.sandbox_id::text,s.instance_id,o.action,o.idempotency_key,o.state,o.reason,o.intent_version,o.created_at,o.completed_at
		FROM sandbox.operations o JOIN sandbox.sandboxes s ON s.tenant_id=o.tenant_id AND s.sandbox_id=o.sandbox_id
		WHERE o.tenant_id=$1 AND o.operation_id=$2`, tenant, id).Scan(&o.ID, &o.SandboxID, &o.InstanceID, &o.Action, &o.IdempotencyKey, &o.State, &o.Reason, &o.IntentVersion, &o.CreatedAt, &o.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, biz.ErrNotFound
	}
	o.TenantID = tenant
	return o, err
}

// ListOperations reads retained lifecycle, code and file mutation history even
// after deletion. Existence, count and page use one database snapshot.
func (p *Postgres) ListOperations(ctx context.Context, tenant, id string, page, pageSize uint32) ([]biz.Operation, int64, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return nil, 0, err
	}
	if !biz.UUID(id) || pageSize > 100 {
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
	var instance string
	err = tx.QueryRow(ctx, `SELECT instance_id FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2`, tenant, id).Scan(&instance)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, biz.ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT operation_id::text,action,state,reason,intent_version,created_at,completed_at FROM (
		SELECT operation_id,action,state,reason,intent_version,created_at,completed_at,'lifecycle' AS source
		  FROM sandbox.operations WHERE tenant_id=$1 AND sandbox_id=$2
		UNION ALL
		SELECT run_id,'code_run',
		  CASE WHEN state='dispatching' AND updated_at < statement_timestamp()-interval '320 seconds' THEN 'unknown' ELSE state END,
		  CASE WHEN state='dispatching' AND updated_at < statement_timestamp()-interval '320 seconds' THEN 'SANDBOX_DISPATCH_OUTCOME_UNKNOWN' ELSE reason END,
		  0::bigint,created_at,
		  CASE WHEN state='dispatching' AND updated_at < statement_timestamp()-interval '320 seconds' THEN NULL ELSE completed_at END,'code_run'
		  FROM sandbox.code_runs WHERE tenant_id=$1 AND sandbox_id=$2
		UNION ALL
		SELECT operation_id,CASE action WHEN 'write' THEN 'file_write' WHEN 'delete' THEN 'file_delete' ELSE 'file_operation' END,
		  CASE WHEN state='dispatching' AND updated_at < statement_timestamp()-interval '75 seconds' THEN 'unknown' ELSE state END,
		  CASE WHEN state='dispatching' AND updated_at < statement_timestamp()-interval '75 seconds' THEN 'SANDBOX_FILE_OUTCOME_UNKNOWN' ELSE reason END,
		  0::bigint,created_at,
		  CASE WHEN state='dispatching' AND updated_at < statement_timestamp()-interval '75 seconds' THEN NULL ELSE completed_at END,'file'
		  FROM sandbox.file_operations WHERE tenant_id=$1 AND sandbox_id=$2
		UNION ALL
		SELECT operation_id,CASE action WHEN 'open' THEN 'preview_open' ELSE 'preview_close' END,
		  state,reason,0::bigint,created_at,completed_at,'preview'
		  FROM sandbox.preview_port_operations WHERE tenant_id=$1 AND sandbox_id=$2
		) history ORDER BY created_at DESC,operation_id DESC,source DESC LIMIT $3 OFFSET $4`, tenant, id, int64(pageSize), (int64(page)-1)*int64(pageSize))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]biz.Operation, 0)
	for rows.Next() {
		o := biz.Operation{TenantID: tenant, SandboxID: id, InstanceID: instance}
		if err = rows.Scan(&o.ID, &o.Action, &o.State, &o.Reason, &o.IntentVersion, &o.CreatedAt, &o.CompletedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, o)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	var total int64
	if err = tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM sandbox.operations WHERE tenant_id=$1 AND sandbox_id=$2) +
		(SELECT count(*) FROM sandbox.code_runs WHERE tenant_id=$1 AND sandbox_id=$2) +
		(SELECT count(*) FROM sandbox.file_operations WHERE tenant_id=$1 AND sandbox_id=$2) +
		(SELECT count(*) FROM sandbox.preview_port_operations WHERE tenant_id=$1 AND sandbox_id=$2)`, tenant, id).Scan(&total); err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// cloneWorkspaceState reads the durable origin and active generation while the
// caller holds work then Sandbox locks. A clone can start only after its own
// creation succeeded and the currently committed volume matches a completed
// source-backed transfer (which may be a later restore).
func cloneWorkspaceState(ctx context.Context, tx pgx.Tx, tenant, sandbox string) (bool, bool, error) {
	var origin, valid bool
	err := tx.QueryRow(ctx, `SELECT
	  EXISTS(SELECT 1 FROM sandbox.checkpoint_transfers c WHERE c.tenant_id=$1 AND c.target_sandbox_id=$2 AND c.kind='clone'),
	  EXISTS(SELECT 1 FROM sandbox.checkpoint_transfers origin
	    JOIN sandbox.sandboxes s ON s.tenant_id=origin.tenant_id AND s.sandbox_id=origin.target_sandbox_id
	    JOIN sandbox.workspace_generations g ON g.tenant_id=s.tenant_id AND g.sandbox_id=s.sandbox_id AND g.state='active'
	    JOIN sandbox.checkpoint_transfers t ON t.tenant_id=g.tenant_id AND t.transfer_id=g.generation_id AND t.target_sandbox_id=g.sandbox_id AND t.state='ready'
	    WHERE origin.tenant_id=$1 AND origin.target_sandbox_id=$2 AND origin.kind='clone' AND origin.state='ready'
	      AND g.pvc_name=t.candidate_name AND g.pvc_uid=t.candidate_uid AND g.pv_name=t.candidate_pv_name AND g.pv_uid=t.candidate_pv_uid
	      AND g.pvc_name=COALESCE(s.provider_refs->>'PVCName','') AND g.pvc_uid=COALESCE(s.provider_refs->>'PVCUID','')
	      AND g.pv_name=COALESCE(s.provider_refs->>'PVName','') AND g.pv_uid=COALESCE(s.provider_refs->>'PVUID','')
	      AND t.mutation_id=COALESCE(s.provider_refs->>'PVCSourceMutationID','') AND t.source_snapshot_uid=COALESCE(s.provider_refs->>'PVCSourceSnapshotUID','')
	      AND t.source_content_uid=COALESCE(s.provider_refs->>'PVCSourceContentUID','') AND t.source_snapshot_handle=COALESCE(s.provider_refs->>'PVCSourceSnapshotHandle','')
	      AND t.source_snapshot_name=COALESCE(s.provider_refs->>'PVCSourceSnapshotName','')
	      AND t.candidate_uid<>'' AND t.candidate_pv_uid<>'' AND t.mutation_id<>'')`, tenant, sandbox).Scan(&origin, &valid)
	return origin, valid, err
}

func (p *Postgres) Change(ctx context.Context, c biz.Command) (biz.ACK, error) {
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return biz.ACK{}, err
	}
	if !biz.UUID(c.ID) || !biz.UUID(c.OperationID) || len([]rune(c.InstanceID)) > 128 || c.IdempotencyKey == "" || len([]rune(c.IdempotencyKey)) > 256 || !validDigest(c.RequestDigest) || c.ExpectedVersion < 0 {
		return biz.ACK{}, biz.ErrInvalid
	}
	newDesired := map[string]string{"pause": biz.DesiredPaused, "resume": biz.DesiredRunning, "delete": biz.DesiredDeleted}[c.Action]
	if newDesired == "" {
		return biz.ACK{}, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return biz.ACK{}, err
	}
	defer tx.Rollback(ctx)
	// Worker paths lock work before touching the Sandbox row. Keep the action
	// path in the same order so a concurrent Finish cannot deadlock with an
	// action that updates due_at after changing the desired state.
	var workTenant, workSandbox string
	err = tx.QueryRow(ctx, "SELECT tenant_id::text,sandbox_id::text FROM sandbox.work WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE", c.TenantID, c.ID).Scan(&workTenant, &workSandbox)
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.ACK{}, biz.ErrNotFound
	}
	if err != nil {
		return biz.ACK{}, err
	}
	var version int64
	var desired, historicalInstance string
	err = tx.QueryRow(ctx, "SELECT version,desired,instance_id FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE", workTenant, workSandbox).Scan(&version, &desired, &historicalInstance)
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.ACK{}, biz.ErrNotFound
	}
	if err != nil {
		return biz.ACK{}, err
	}
	var old, oldDigest string
	if e := tx.QueryRow(ctx, "SELECT operation_id::text,request_digest FROM sandbox.operations WHERE tenant_id=$1 AND sandbox_id=$2 AND action=$3 AND idempotency_key=$4", c.TenantID, c.ID, c.Action, c.IdempotencyKey).Scan(&old, &oldDigest); e == nil {
		if oldDigest != c.RequestDigest {
			// Historical operations included the stored parent in the digest.
			// Only the stored historical value may establish equivalent replay.
			if historicalInstance == "" || c.RequestDigest != biz.ActionDigest("", c.ExpectedVersion, c.Action) || oldDigest != biz.ActionDigest(historicalInstance, c.ExpectedVersion, c.Action) {
				return biz.ACK{}, biz.ErrConflict
			}
		}
		return biz.ACK{OperationID: old, ResourceID: c.ID, Accepted: true, Replayed: true}, nil
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return biz.ACK{}, e
	}
	if c.ExpectedVersion > 0 && c.ExpectedVersion != version {
		return biz.ACK{}, biz.ErrVersion
	}
	// A checkpoint in flight must settle before a lifecycle change starts.
	var checkpointBlocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND state IN ('accepted','running','unknown'))`, c.TenantID, c.ID).Scan(&checkpointBlocked); err != nil {
		return biz.ACK{}, err
	}
	if checkpointBlocked {
		return biz.ACK{}, biz.ErrConflict
	}
	if c.Action == "delete" {
		var hasCheckpoints bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2)`, c.TenantID, c.ID).Scan(&hasCheckpoints); err != nil {
			return biz.ACK{}, err
		}
		if hasCheckpoints {
			var transferTable *string
			if err = tx.QueryRow(ctx, `SELECT to_regclass('sandbox.checkpoint_transfers')::text`).Scan(&transferTable); err != nil || transferTable == nil {
				return biz.ACK{}, biz.ErrDependencyUnavailable
			}
			var hasReferences bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.checkpoint_transfers t JOIN sandbox.checkpoints cp ON cp.tenant_id=t.tenant_id AND cp.checkpoint_id=t.checkpoint_id WHERE cp.tenant_id=$1 AND cp.sandbox_id=$2 AND t.target_sandbox_id<>cp.sandbox_id AND NOT t.source_released)`, c.TenantID, c.ID).Scan(&hasReferences); err != nil {
				return biz.ACK{}, err
			}
			if hasReferences {
				return biz.ACK{}, biz.ErrCheckpointReferenced
			}
		}
	}
	var transferBlocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND target_sandbox_id=$2 AND state IN ('accepted','creating','unknown','observing'))`, c.TenantID, c.ID).Scan(&transferBlocked); err != nil {
		return biz.ACK{}, err
	}
	if transferBlocked {
		return biz.ACK{}, biz.ErrConflict
	}
	if c.Action == "resume" || c.Action == "pause" {
		origin, valid, err := cloneWorkspaceState(ctx, tx, c.TenantID, c.ID)
		if err != nil {
			return biz.ACK{}, err
		}
		if origin && !valid {
			return biz.ACK{}, biz.ErrConflict
		}
	}
	if desired == biz.DesiredDeleted {
		return biz.ACK{}, biz.ErrConflict
	}
	if c.Action == "pause" || c.Action == "delete" {
		// Under the same work→sandbox lock order as preview acceptance, fence
		// any outstanding listener before publishing the lifecycle change.
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET desired='closed',state=CASE WHEN state IN ('opening','ready') THEN 'closing' ELSE state END,address='',updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND state<>'closed'`, c.TenantID, c.ID); err != nil {
			return biz.ACK{}, err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE sandbox.sandboxes SET version=version+1,desired=$3,last_operation_id=$4,updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2", c.TenantID, c.ID, newDesired, c.OperationID); err != nil {
		return biz.ACK{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sandbox.operations(tenant_id,operation_id,sandbox_id,action,idempotency_key,request_digest,intent_version,state) VALUES($1,$2,$3,$4,$5,$6,$7,'queued')`, c.TenantID, c.OperationID, c.ID, c.Action, c.IdempotencyKey, c.RequestDigest, version+1); err != nil {
		return biz.ACK{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state='superseded',completed_at=clock_timestamp()
		WHERE tenant_id=$1 AND sandbox_id=$2 AND operation_id<>$3 AND state IN ('queued','running','retrying','unknown')`, c.TenantID, c.ID, c.OperationID); err != nil {
		return biz.ACK{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE sandbox.work SET due_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2", c.TenantID, c.ID); err != nil {
		return biz.ACK{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.ACK{}, err
	}
	return biz.ACK{OperationID: c.OperationID, ResourceID: c.ID, Accepted: true}, nil
}

func scanSandbox(row interface{ Scan(...any) error }) (biz.Sandbox, error) {
	var s biz.Sandbox
	var raw, refs, pending []byte
	err := row.Scan(&s.TenantID, &s.ID, &s.InstanceID, &s.Version, &s.Desired, &s.Observed, &s.Reason,
		&s.CreateOperationID, &s.LastOperationID, &raw, &refs, &s.ObservedAt,
		&s.CreateIntentDigest, &s.CreateRequestDigest, &pending)
	if err != nil {
		return biz.Sandbox{}, err
	}
	if err = json.Unmarshal(raw, &s.Snapshot); err != nil {
		return biz.Sandbox{}, err
	}
	if len(refs) != 0 {
		if err = json.Unmarshal(refs, &s.ProviderRefs); err != nil {
			return biz.Sandbox{}, err
		}
	}
	if len(pending) != 0 {
		if err = json.Unmarshal(pending, &s.Pending); err != nil {
			return biz.Sandbox{}, err
		}
	}
	return s, nil
}

func (p *Postgres) Claim(ctx context.Context, owner string, lease time.Duration) (*biz.Work, error) {
	if owner == "" || lease <= 0 {
		return nil, biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var tenant, id string
	var epoch int64
	err = tx.QueryRow(ctx, `SELECT w.tenant_id::text,w.sandbox_id::text,w.lease_epoch FROM sandbox.work w JOIN sandbox.sandboxes s ON s.tenant_id=w.tenant_id AND s.sandbox_id=w.sandbox_id WHERE w.due_at<=clock_timestamp() AND w.lease_until<clock_timestamp() AND NOT EXISTS (SELECT 1 FROM sandbox.checkpoints c WHERE c.tenant_id=w.tenant_id AND c.sandbox_id=w.sandbox_id AND (c.state IN ('accepted','running','unknown') OR (s.desired='deleted' AND c.cleanup_state<>'done' AND NOT EXISTS (SELECT 1 FROM sandbox.checkpoint_transfers self_ref WHERE self_ref.tenant_id=c.tenant_id AND self_ref.checkpoint_id=c.checkpoint_id AND self_ref.target_sandbox_id=c.sandbox_id AND NOT self_ref.source_released)))) AND NOT EXISTS (SELECT 1 FROM sandbox.checkpoint_transfers t WHERE t.tenant_id=w.tenant_id AND t.target_sandbox_id=w.sandbox_id AND t.state IN ('accepted','creating','unknown','observing')) ORDER BY w.due_at FOR UPDATE OF w SKIP LOCKED LIMIT 1`).Scan(&tenant, &id, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	epoch++
	if _, err = tx.Exec(ctx, `UPDATE sandbox.work SET lease_owner=$3,lease_epoch=$4,lease_until=clock_timestamp()+$5::bigint*interval '1 microsecond' WHERE tenant_id=$1 AND sandbox_id=$2`, tenant, id, owner, epoch, lease.Microseconds()); err != nil {
		return nil, err
	}
	row := tx.QueryRow(ctx, sandboxSelect+" WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s", tenant, id)
	s, err := scanSandbox(row)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.operations SET state='running'
		WHERE tenant_id=$1 AND operation_id=$2 AND state IN ('queued','retrying','unknown')`, tenant, s.LastOperationID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &biz.Work{Sandbox: s, LeaseOwner: owner, LeaseEpoch: epoch}, nil
}

// PrepareMutation is the durable fence before any Provider write. Actions and
// workers take the same work -> sandbox lock order; accepting a newer intent
// never erases an outstanding mutation whose Provider outcome is still unknown.
func (p *Postgres) PrepareMutation(ctx context.Context, work biz.Work, mutation biz.Mutation) error {
	if !biz.UUID(mutation.ID) || !biz.UUID(mutation.OperationID) || mutation.Version <= 0 || mutation.Attempt < 0 {
		return biz.ErrInvalid
	}
	switch mutation.Kind {
	case biz.MutationCreateWorkspace, biz.MutationCreatePod:
	case biz.MutationDeleteWorkspace, biz.MutationDeletePod:
		if mutation.TargetUID == "" {
			return biz.ErrInvalid
		}
	default:
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockWork(ctx, tx, work, true); err != nil {
		return err
	}
	current, err := scanSandbox(tx.QueryRow(ctx, sandboxSelect+" WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s", work.Sandbox.TenantID, work.Sandbox.ID))
	if err != nil {
		return err
	}
	if current.Version != work.Sandbox.Version || current.LastOperationID != work.Sandbox.LastOperationID {
		return biz.ErrLease
	}
	var checkpointBlocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sandbox.checkpoints WHERE tenant_id=$1 AND sandbox_id=$2 AND state IN ('accepted','running','unknown')) OR EXISTS (SELECT 1 FROM sandbox.checkpoint_transfers WHERE tenant_id=$1 AND target_sandbox_id=$2 AND state IN ('accepted','creating','unknown','observing'))`, current.TenantID, current.ID).Scan(&checkpointBlocked); err != nil {
		return err
	}
	if checkpointBlocked {
		return biz.ErrConflict
	}
	if mutation.Kind == biz.MutationCreateWorkspace || mutation.Kind == biz.MutationCreatePod {
		origin, valid, err := cloneWorkspaceState(ctx, tx, current.TenantID, current.ID)
		if err != nil {
			return err
		}
		if origin && (mutation.Kind == biz.MutationCreateWorkspace || !valid) {
			return biz.ErrConflict
		}
	}
	if mutation.Kind == biz.MutationDeleteWorkspace {
		var retired bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.workspace_generations WHERE tenant_id=$1 AND sandbox_id=$2 AND state IN ('retired','active'))`, current.TenantID, current.ID).Scan(&retired); err != nil {
			return err
		}
		if retired {
			return biz.ErrConflict
		}
	}
	if current.Pending != nil {
		// An identical first preparation can be retried after its commit reply
		// was lost. A claimed pending mutation always needs a new dispatch attempt.
		if work.Sandbox.Pending == nil && *current.Pending == mutation && mutation.Attempt == 0 && !mutation.PreviousUncertain && mutation.Version == current.Version && mutation.OperationID == current.LastOperationID {
			return tx.Commit(ctx)
		}
		if work.Sandbox.Pending == nil || *current.Pending != *work.Sandbox.Pending {
			return biz.ErrConflict
		}
		previous := *current.Pending
		origin := mutation
		origin.Attempt, origin.PreviousUncertain = previous.Attempt, previous.PreviousUncertain
		if (previous.Kind != biz.MutationDeletePod && previous.Kind != biz.MutationDeleteWorkspace) || origin != previous || previous.Attempt == math.MaxInt64 || mutation.Attempt != previous.Attempt+1 || !mutation.PreviousUncertain {
			return biz.ErrConflict
		}
	} else {
		// A late receipt may have cleared the journal since Claim. Never turn
		// that stale pending snapshot into a fresh authorization to send again.
		if work.Sandbox.Pending != nil {
			return biz.ErrConflict
		}
		if mutation.Attempt != 0 || mutation.PreviousUncertain || mutation.Version != current.Version || mutation.OperationID != current.LastOperationID {
			return biz.ErrInvalid
		}
	}
	raw, err := json.Marshal(mutation)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE sandbox.sandboxes SET pending_mutation=$3,updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2", current.TenantID, current.ID, raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func readWorkLease(ctx context.Context, tx pgx.Tx, work biz.Work) (owned, live bool, err error) {
	err = tx.QueryRow(ctx, `SELECT lease_owner=$3 AND lease_epoch=$4,lease_until>clock_timestamp() FROM sandbox.work
		WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, work.Sandbox.TenantID, work.Sandbox.ID, work.LeaseOwner, work.LeaseEpoch).Scan(&owned, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, biz.ErrLease
	}
	return owned, live, err
}

func lockWork(ctx context.Context, tx pgx.Tx, work biz.Work, active bool) error {
	owned, live, err := readWorkLease(ctx, tx, work)
	if err != nil {
		return err
	}
	if !owned || (active && !live) {
		return biz.ErrLease
	}
	return nil
}

func mergeRefs(current, receipt biz.ProviderRefs) biz.ProviderRefs {
	for _, pair := range []struct {
		target *string
		value  string
	}{
		{&current.PVCName, receipt.PVCName}, {&current.PVCUID, receipt.PVCUID},
		{&current.PVName, receipt.PVName}, {&current.PVUID, receipt.PVUID},
		{&current.PodName, receipt.PodName}, {&current.PodUID, receipt.PodUID},
		{&current.PVCResourceVersion, receipt.PVCResourceVersion}, {&current.PodResourceVersion, receipt.PodResourceVersion},
		{&current.ResourceVersion, receipt.ResourceVersion}, {&current.CleanupReceipt, receipt.CleanupReceipt},
	} {
		if pair.value != "" {
			*pair.target = pair.value
		}
	}
	return current
}

// Preserve a receipt merged after this worker claimed the row. A subsequent
// fresh observation may clear an absent object, but an older in-flight read
// cannot erase the UID just learned from an attributable Provider response.
func observedRefs(current, claimed, observed biz.ProviderRefs) biz.ProviderRefs {
	for _, field := range []struct {
		target           *string
		before, observed string
	}{
		{&current.PVCName, claimed.PVCName, observed.PVCName}, {&current.PVCUID, claimed.PVCUID, observed.PVCUID},
		{&current.PVName, claimed.PVName, observed.PVName}, {&current.PVUID, claimed.PVUID, observed.PVUID},
		{&current.PodName, claimed.PodName, observed.PodName}, {&current.PodUID, claimed.PodUID, observed.PodUID},
		{&current.PVCResourceVersion, claimed.PVCResourceVersion, observed.PVCResourceVersion},
		{&current.PodResourceVersion, claimed.PodResourceVersion, observed.PodResourceVersion},
		{&current.ResourceVersion, claimed.ResourceVersion, observed.ResourceVersion},
		{&current.CleanupReceipt, claimed.CleanupReceipt, observed.CleanupReceipt},
	} {
		if *field.target == field.before {
			*field.target = field.observed
		}
	}
	return current
}

func (p *Postgres) Finish(ctx context.Context, work biz.Work, progress biz.Progress) error {
	if progress.Sandbox.ID != work.Sandbox.ID || progress.Sandbox.TenantID != work.Sandbox.TenantID {
		return biz.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// A late response is still useful after lease takeover when its exact
	// mutation remains journaled. It may merge that receipt, but it must not
	// overwrite the successor's observations or release the successor's lease.
	ownsLease, _, err := readWorkLease(ctx, tx, work)
	if err != nil {
		return err
	}
	if !ownsLease && progress.Receipt == nil {
		return biz.ErrLease
	}
	current, err := scanSandbox(tx.QueryRow(ctx, sandboxSelect+" WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s", work.Sandbox.TenantID, work.Sandbox.ID))
	if err != nil {
		return err
	}
	sameIntent := ownsLease && current.Version == work.Sandbox.Version && current.LastOperationID == work.Sandbox.LastOperationID
	previousRefs := current.ProviderRefs
	if sameIntent {
		current.Observed, current.Reason, current.ObservedAt = progress.Sandbox.Observed, progress.Sandbox.Reason, progress.Sandbox.ObservedAt
		current.ProviderRefs = observedRefs(current.ProviderRefs, work.Sandbox.ProviderRefs, progress.Sandbox.ProviderRefs)
		var failedCloneReason *string
		if err = tx.QueryRow(ctx, `SELECT (SELECT reason FROM sandbox.operations WHERE tenant_id=$1 AND operation_id=$2 AND action='checkpoint_clone' AND state='failed')`, current.TenantID, current.LastOperationID).Scan(&failedCloneReason); err != nil {
			return err
		}
		if failedCloneReason != nil {
			current.Observed, current.Reason = biz.ObservedUnknown, *failedCloneReason
		}
	}
	if receipt := progress.Receipt; receipt != nil {
		if current.Pending == nil || *current.Pending != receipt.Mutation {
			return biz.ErrConflict
		}
		switch receipt.Outcome {
		case biz.MutationApplied:
			current.Pending = nil
		case biz.MutationRejected, biz.MutationNotSent:
			// This receipt describes only the current dispatch. A prior attempt
			// may still be executing, so its durable uncertainty must survive.
			if !current.Pending.PreviousUncertain {
				current.Pending = nil
			}
		case biz.MutationUnknown:
		default:
			return biz.ErrInvalid
		}
		current.ProviderRefs = mergeRefs(current.ProviderRefs, receipt.Refs)
	}
	// Only the locked previous facts and the merged, journal-validated current
	// facts are authoritative. In particular, never persist stale progress from
	// a worker that lost its lease or whose intent has changed.
	if err = recordSandboxPod(ctx, tx, current, previousRefs); err != nil {
		return err
	}
	if err = recordSandboxPod(ctx, tx, current, current.ProviderRefs); err != nil {
		return err
	}
	refs, err := json.Marshal(current.ProviderRefs)
	if err != nil {
		return err
	}
	var pending any
	if current.Pending != nil {
		pending, err = json.Marshal(current.Pending)
		if err != nil {
			return err
		}
	}
	// The snapshot is immutable acceptance input. A worker may update observed
	// facts and a matching journal receipt, never rewrite the frozen spec.
	if _, err = tx.Exec(ctx, `UPDATE sandbox.sandboxes SET observed=$3,reason=$4,provider_refs=$5,observed_at=$6,
		pending_mutation=$7,updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2`, current.TenantID, current.ID, current.Observed, current.Reason, refs, current.ObservedAt, pending); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sandbox.workspace_bindings(tenant_id,sandbox_id,namespace,pvc_name,pvc_uid,cleanup_receipt) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(tenant_id,sandbox_id) DO UPDATE SET namespace=EXCLUDED.namespace,pvc_name=EXCLUDED.pvc_name,
		pvc_uid=CASE WHEN EXCLUDED.pvc_uid<>'' THEN EXCLUDED.pvc_uid ELSE sandbox.workspace_bindings.pvc_uid END,
		cleanup_receipt=CASE WHEN EXCLUDED.cleanup_receipt<>'' THEN EXCLUDED.cleanup_receipt ELSE sandbox.workspace_bindings.cleanup_receipt END`, current.TenantID, current.ID, current.Snapshot.Namespace, current.ProviderRefs.PVCName, current.ProviderRefs.PVCUID, current.ProviderRefs.CleanupReceipt); err != nil {
		return err
	}
	if sameIntent && current.LastOperationID != "" {
		state := "retrying"
		if current.Observed == biz.ObservedUnknown || current.Pending != nil {
			state = "unknown"
		} else if (current.Desired == biz.DesiredRunning && current.Observed == biz.ObservedRunning) || (current.Desired == biz.DesiredPaused && current.Observed == biz.ObservedPaused) || (current.Desired == biz.DesiredDeleted && current.Observed == biz.ObservedDeleted) {
			state = "succeeded"
		}
		result, err := tx.Exec(ctx, `UPDATE sandbox.operations SET state=$3,reason=$4,
			completed_at=CASE WHEN $3='succeeded' THEN clock_timestamp() ELSE NULL END WHERE tenant_id=$1 AND operation_id=$2
			AND state IN ('queued','running','retrying','unknown')`, current.TenantID, current.LastOperationID, state, current.Reason)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			var operationState, operationAction string
			if err = tx.QueryRow(ctx, "SELECT state,action FROM sandbox.operations WHERE tenant_id=$1 AND operation_id=$2", current.TenantID, current.LastOperationID).Scan(&operationState, &operationAction); errors.Is(err, pgx.ErrNoRows) {
				return biz.ErrNotFound
			} else if err != nil {
				return err
			} else if operationState != "succeeded" && operationState != "superseded" && !(operationState == "failed" && operationAction == "checkpoint_clone") {
				return biz.ErrConflict
			}
		}
	}
	if !ownsLease {
		if _, err = tx.Exec(ctx, "UPDATE sandbox.work SET due_at=LEAST(due_at,clock_timestamp()) WHERE tenant_id=$1 AND sandbox_id=$2", current.TenantID, current.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	deleted := current.Pending == nil && current.Desired == biz.DesiredDeleted && current.Observed == biz.ObservedDeleted
	next := progress.Next
	if !sameIntent {
		next = 0
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.work SET due_at=CASE WHEN $5 THEN 'infinity'::timestamptz ELSE clock_timestamp()+$6::bigint*interval '1 microsecond' END,
		lease_owner='',lease_until='-infinity' WHERE tenant_id=$1 AND sandbox_id=$2 AND lease_owner=$3 AND lease_epoch=$4`, current.TenantID, current.ID, work.LeaseOwner, work.LeaseEpoch, deleted, next.Microseconds()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
