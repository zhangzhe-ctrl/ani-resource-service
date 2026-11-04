package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

const previewSelect = `SELECT tenant_id::text,sandbox_id::text,preview_id::text,open_operation_id::text,sandbox_version,pod_uid,port,protocol,desired,CASE WHEN state='ready' AND (desired<>'open' OR expires_at<=statement_timestamp()) THEN 'closing' ELSE state END,reason,service_uid,create_mutation_id,slice_uid,slice_create_mutation_id,pod_ip,CASE WHEN desired='open' AND state='ready' AND expires_at>statement_timestamp() THEN address ELSE '' END,pending_mutation,expires_at FROM sandbox.preview_ports`

func scanPreview(row interface{ Scan(...any) error }) (biz.ExperimentalPreviewRecord, error) {
	var r biz.ExperimentalPreviewRecord
	var port int32
	var pending []byte
	err := row.Scan(&r.TenantID, &r.SandboxID, &r.PreviewID, &r.OpenOperationID, &r.Version, &r.PodUID, &port, &r.Protocol, &r.Desired, &r.State, &r.Reason, &r.ServiceUID, &r.CreateMutationID, &r.SliceUID, &r.SliceCreateMutationID, &r.PodIP, &r.Address, &pending, &r.ExpiresAt)
	if err != nil {
		return r, err
	}
	if port < 1 || port > 65535 {
		return r, biz.ErrInvalid
	}
	r.Port = uint16(port)
	if len(pending) > 0 {
		var value biz.PreviewExperimentMutation
		if err = json.Unmarshal(pending, &value); err != nil {
			return r, err
		}
		r.Pending = &value
	}
	return r, nil
}
func previewOpenDigest(c biz.ExperimentalPreviewOpen) (string, time.Duration, error) {
	ttl := c.TTL
	if ttl == 0 {
		ttl = time.Hour
	}
	if !biz.UUID(c.TenantID) || !biz.UUID(c.SandboxID) || !biz.UUID(c.PreviewID) || !biz.UUID(c.OperationID) || c.ExpectedVersion < 1 || c.Port == 0 || (c.Protocol != "http" && c.Protocol != "tcp") || ttl < time.Minute || ttl > time.Hour || ttl%time.Second != 0 || c.IdempotencyKey == "" || !utf8.ValidString(c.IdempotencyKey) || strings.ContainsRune(c.IdempotencyKey, 0) || len([]rune(c.IdempotencyKey)) > 256 {
		return "", 0, biz.ErrInvalid
	}
	b, err := json.Marshal(struct {
		Version    int64
		Port       uint16
		Protocol   string
		TTLSeconds int64
	}{c.ExpectedVersion, c.Port, c.Protocol, int64(ttl / time.Second)})
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), ttl, nil
}

// AcceptExperimentalPreviewOpen records an asynchronous intent. The feature
// is disabled by default; no public entrypoint is wired to this candidate.
// Callers must pass an operator-controlled capability, not a request field.
func (p *Postgres) AcceptExperimentalPreviewOpen(ctx context.Context, c biz.ExperimentalPreviewOpen, enabled bool) (biz.ExperimentalPreviewRecord, error) {
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	digest, ttl, err := previewOpenDigest(c)
	if err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	if !enabled {
		return biz.ExperimentalPreviewRecord{}, biz.ErrUnsupported
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	defer tx.Rollback(ctx)
	// Same lock order as Change/worker: work then Sandbox then preview row.
	s, err := lockCodeRunSandbox(ctx, tx, c.TenantID, c.SandboxID)
	if err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	var stored string
	err = tx.QueryRow(ctx, `SELECT request_digest FROM sandbox.preview_ports WHERE tenant_id=$1 AND sandbox_id=$2 AND idempotency_key=$3`, c.TenantID, c.SandboxID, c.IdempotencyKey).Scan(&stored)
	if err == nil {
		if stored != digest {
			return biz.ExperimentalPreviewRecord{}, biz.ErrConflict
		}
		r, e := scanPreview(tx.QueryRow(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND idempotency_key=$3`, c.TenantID, c.SandboxID, c.IdempotencyKey))
		if e != nil {
			return r, e
		}
		r.Replayed = true
		r.OperationID = r.OpenOperationID
		return r, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return biz.ExperimentalPreviewRecord{}, err
	}
	if s.Version != c.ExpectedVersion {
		return biz.ExperimentalPreviewRecord{}, biz.ErrVersion
	}
	if s.Desired != biz.DesiredRunning || s.Observed != biz.ObservedRunning || s.Pending != nil || s.ProviderRefs.PodUID == "" || s.ProviderRefs.PodName != "sandbox-"+c.SandboxID {
		return biz.ExperimentalPreviewRecord{}, biz.ErrDataPlaneUnavailable
	}
	var expires time.Time
	err = tx.QueryRow(ctx, `INSERT INTO sandbox.preview_ports(tenant_id,sandbox_id,preview_id,open_operation_id,idempotency_key,request_digest,sandbox_version,pod_uid,port,protocol,desired,state,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'open','queued',clock_timestamp()+$11::bigint*interval '1 second') RETURNING expires_at`, c.TenantID, c.SandboxID, c.PreviewID, c.OperationID, c.IdempotencyKey, digest, s.Version, s.ProviderRefs.PodUID, int32(c.Port), c.Protocol, int64(ttl/time.Second)).Scan(&expires)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return biz.ExperimentalPreviewRecord{}, biz.ErrConflict
		}
		return biz.ExperimentalPreviewRecord{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sandbox.preview_port_operations(tenant_id,sandbox_id,preview_id,operation_id,action,idempotency_key,request_digest,state) VALUES($1,$2,$3,$4,'open',$5,$6,'queued')`, c.TenantID, c.SandboxID, c.PreviewID, c.OperationID, c.IdempotencyKey, digest)
	if err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE sandbox.work SET due_at=LEAST(due_at,clock_timestamp()) WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.SandboxID)
	if err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	return biz.ExperimentalPreviewRecord{TenantID: c.TenantID, SandboxID: c.SandboxID, PreviewID: c.PreviewID, OpenOperationID: c.OperationID, OperationID: c.OperationID, Version: s.Version, PodUID: s.ProviderRefs.PodUID, Port: c.Port, Protocol: c.Protocol, Desired: "open", State: "queued", ExpiresAt: expires}, nil
}

func (p *Postgres) GetExperimentalPreview(ctx context.Context, tenant, sandbox, id string) (biz.ExperimentalPreviewRecord, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	if !biz.UUID(sandbox) || !biz.UUID(id) {
		return biz.ExperimentalPreviewRecord{}, biz.ErrInvalid
	}
	r, err := scanPreview(p.pool.QueryRow(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND preview_id=$3`, tenant, sandbox, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, biz.ErrNotFound
	}
	return r, err
}

// Close remains enabled even if the experimental capability was disabled.
// It journals intent only; the worker must prove the exact Service UID gone.
func (p *Postgres) AcceptExperimentalPreviewClose(ctx context.Context, c biz.ExperimentalPreviewClose) (biz.ExperimentalPreviewRecord, error) {
	if err := biz.RequireTenant(ctx, c.TenantID); err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	if !biz.UUID(c.SandboxID) || !biz.UUID(c.PreviewID) || !biz.UUID(c.OperationID) || c.ExpectedVersion < 1 || c.IdempotencyKey == "" || !utf8.ValidString(c.IdempotencyKey) || strings.ContainsRune(c.IdempotencyKey, 0) || len([]rune(c.IdempotencyKey)) > 256 {
		return biz.ExperimentalPreviewRecord{}, biz.ErrInvalid
	}
	b, _ := json.Marshal(struct {
		Preview string
		Version int64
	}{c.PreviewID, c.ExpectedVersion})
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	defer tx.Rollback(ctx)
	s, err := lockCodeRunSandbox(ctx, tx, c.TenantID, c.SandboxID)
	if err != nil {
		return biz.ExperimentalPreviewRecord{}, err
	}
	row, err := scanPreview(tx.QueryRow(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND preview_id=$3 FOR UPDATE`, c.TenantID, c.SandboxID, c.PreviewID))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, biz.ErrNotFound
	}
	if err != nil {
		return row, err
	}
	var old, oldOperationID string
	err = tx.QueryRow(ctx, `SELECT request_digest,operation_id::text FROM sandbox.preview_port_operations WHERE tenant_id=$1 AND preview_id=$2 AND action='close' AND idempotency_key=$3`, c.TenantID, c.PreviewID, c.IdempotencyKey).Scan(&old, &oldOperationID)
	if err == nil {
		if old != digest {
			return row, biz.ErrConflict
		}
		row.Replayed = true
		row.OperationID = oldOperationID
		return row, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	if s.Version != c.ExpectedVersion {
		return row, biz.ErrVersion
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sandbox.preview_port_operations(tenant_id,sandbox_id,preview_id,operation_id,action,idempotency_key,request_digest,state,completed_at) VALUES($1,$2,$3,$4,'close',$5,$6,CASE WHEN $7 THEN 'succeeded' ELSE 'queued' END,CASE WHEN $7 THEN clock_timestamp() ELSE NULL END)`, c.TenantID, c.SandboxID, c.PreviewID, c.OperationID, c.IdempotencyKey, digest, row.State == "closed"); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return row, biz.ErrConflict
		}
		return row, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET desired='closed',state=CASE WHEN state IN ('opening','ready') THEN 'closing' ELSE state END,address='',updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`, c.TenantID, c.PreviewID); err != nil {
		return row, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.work SET due_at=LEAST(due_at,clock_timestamp()) WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.SandboxID); err != nil {
		return row, err
	}
	row.Desired = "closed"
	row.Address = ""
	if row.State == "opening" || row.State == "ready" {
		row.State = "closing"
	}
	row.OperationID = c.OperationID
	return row, tx.Commit(ctx)
}

func (p *Postgres) ListExperimentalPreviews(ctx context.Context, tenant, sandbox string, page, size uint32) ([]biz.ExperimentalPreviewRecord, int64, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return nil, 0, err
	}
	if !biz.UUID(sandbox) || size > 100 {
		return nil, 0, biz.ErrInvalid
	}
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
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2)`, tenant, sandbox).Scan(&exists); err != nil {
		return nil, 0, err
	}
	if !exists {
		return nil, 0, biz.ErrNotFound
	}
	rows, err := tx.Query(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 ORDER BY created_at DESC,preview_id DESC LIMIT $3 OFFSET $4`, tenant, sandbox, int64(size), int64(page-1)*int64(size))
	if err != nil {
		return nil, 0, err
	}
	items := []biz.ExperimentalPreviewRecord{}
	for rows.Next() {
		r, e := scanPreview(rows)
		if e != nil {
			rows.Close()
			return nil, 0, e
		}
		items = append(items, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sandbox.preview_ports WHERE tenant_id=$1 AND sandbox_id=$2`, tenant, sandbox).Scan(&total); err != nil {
		return nil, 0, err
	}
	return items, total, tx.Commit(ctx)
}

// PreviewCleanupRequired is invoked by the internal worker, not a public RPC.
// Queued, dispatched and unknown records all retain the lifecycle barrier.
func (p *Postgres) PreviewCleanupRequired(ctx context.Context, tenant, sandbox string) (bool, error) {
	if !biz.UUID(tenant) || !biz.UUID(sandbox) {
		return false, biz.ErrInvalid
	}
	var needed bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.preview_ports WHERE tenant_id=$1 AND sandbox_id=$2 AND state<>'closed')`, tenant, sandbox).Scan(&needed)
	return needed, err
}

// ReconcilePreviewLifecycleFence cancels only work that was never dispatched.
// Once a pending mutation exists or a Service UID was recorded, it preserves
// the barrier until a real provider worker observes UID-checked cleanup.
func (p *Postgres) ReconcilePreviewLifecycleFence(ctx context.Context, work biz.Work) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = lockWork(ctx, tx, work, true); err != nil {
		return false, err
	}
	var version int64
	var desired string
	if err = tx.QueryRow(ctx, `SELECT version,desired FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, work.Sandbox.TenantID, work.Sandbox.ID).Scan(&version, &desired); err != nil {
		return false, err
	}
	if version != work.Sandbox.Version || (desired != biz.DesiredPaused && desired != biz.DesiredDeleted) {
		return false, biz.ErrConflict
	}
	rows, err := tx.Query(ctx, `UPDATE sandbox.preview_ports SET desired='closed',state='closed',reason='CANCELLED_BEFORE_DISPATCH',updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND state='queued' AND service_uid='' AND pending_mutation IS NULL RETURNING preview_id::text`, work.Sandbox.TenantID, work.Sandbox.ID)
	if err != nil {
		return false, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_port_operations SET state=CASE WHEN action='open' THEN 'failed' ELSE 'succeeded' END,reason=CASE WHEN action='open' THEN 'CANCELLED_BEFORE_DISPATCH' ELSE '' END,completed_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2 AND state='queued'`, work.Sandbox.TenantID, id); err != nil {
			return false, err
		}
	}
	var needed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.preview_ports WHERE tenant_id=$1 AND sandbox_id=$2 AND state<>'closed')`, work.Sandbox.TenantID, work.Sandbox.ID).Scan(&needed); err != nil {
		return false, err
	}
	return needed, tx.Commit(ctx)
}

func (p *Postgres) GetExperimentalPreviewOperation(ctx context.Context, tenant, sandbox, operation string) (biz.ExperimentalPreviewOperation, error) {
	var out biz.ExperimentalPreviewOperation
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return out, err
	}
	if !biz.UUID(sandbox) || !biz.UUID(operation) {
		return out, biz.ErrInvalid
	}
	err := p.pool.QueryRow(ctx, `SELECT tenant_id::text,sandbox_id::text,preview_id::text,operation_id::text,action,state,reason,created_at,completed_at FROM sandbox.preview_port_operations WHERE tenant_id=$1 AND sandbox_id=$2 AND operation_id=$3`, tenant, sandbox, operation).Scan(&out.TenantID, &out.SandboxID, &out.PreviewID, &out.OperationID, &out.Action, &out.State, &out.Reason, &out.CreatedAt, &out.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, biz.ErrNotFound
	}
	return out, err
}

// PrepareExperimentalPreviewMutation commits a single Provider intent before
// dispatch. A repeated call cannot grant a second create dispatch. Delete
// retries need a separately journaled attempt counter and are not implemented.
func (p *Postgres) PrepareExperimentalPreviewMutation(ctx context.Context, work biz.Work, previewID string, m biz.PreviewExperimentMutation, enabled bool) error {
	if !biz.UUID(work.Sandbox.TenantID) || !biz.UUID(work.Sandbox.ID) || !biz.UUID(previewID) || !biz.UUID(m.ID) || (m.Kind != "create" && m.Kind != "delete" && m.Kind != "create_slice" && m.Kind != "delete_slice") {
		return biz.ErrInvalid
	}
	if (m.Kind == "create" || m.Kind == "create_slice") && !enabled {
		return biz.ErrUnsupported
	}
	if m.Kind == "create" && m.TargetUID != "" {
		return biz.ErrInvalid
	}
	if m.Kind != "create_slice" && m.PodIP != "" {
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
	s, err := scanSandbox(tx.QueryRow(ctx, sandboxSelect+` WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s`, work.Sandbox.TenantID, work.Sandbox.ID))
	if err != nil {
		return err
	}
	if s.Version != work.Sandbox.Version || s.LastOperationID != work.Sandbox.LastOperationID {
		return biz.ErrConflict
	}
	r, err := scanPreview(tx.QueryRow(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND preview_id=$3 FOR UPDATE`, s.TenantID, s.ID, previewID))
	if err != nil {
		return err
	}
	if r.Pending != nil || r.State == "closed" {
		return biz.ErrConflict
	}
	if m.Kind == "create" || m.Kind == "create_slice" {
		if r.Desired != "open" || r.Version != s.Version || r.PodUID != s.ProviderRefs.PodUID || s.Desired != biz.DesiredRunning || s.Observed != biz.ObservedRunning || s.Pending != nil {
			return biz.ErrDataPlaneUnavailable
		}
		if m.Kind == "create" && (r.State != "queued" || r.ServiceUID != "") {
			return biz.ErrConflict
		}
		if m.Kind == "create_slice" {
			ip, e := netip.ParseAddr(m.PodIP)
			if e != nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.Is4In6() || r.ServiceUID == "" || m.TargetUID != r.ServiceUID || r.SliceUID != "" || r.State != "opening" {
				return biz.ErrConflict
			}
		}
		var live bool
		if err = tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM sandbox.preview_ports WHERE tenant_id=$1 AND preview_id=$2`, s.TenantID, previewID).Scan(&live); err != nil {
			return err
		}
		if !live {
			return biz.ErrDataPlaneUnavailable
		}
	} else {
		if r.Desired != "closed" {
			return biz.ErrConflict
		}
		if m.Kind == "delete_slice" && (r.SliceUID == "" || m.TargetUID != r.SliceUID) {
			return biz.ErrConflict
		}
		if m.Kind == "delete" && (r.ServiceUID == "" || m.TargetUID != r.ServiceUID || r.SliceUID != "") {
			return biz.ErrConflict
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	state := "opening"
	if m.Kind == "delete" || m.Kind == "delete_slice" {
		state = "closing"
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET state=$3,pending_mutation=$4,pod_ip=CASE WHEN $5<>'' THEN $5 ELSE pod_ip END,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`, s.TenantID, previewID, state, raw, m.PodIP); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_port_operations SET state='running' WHERE tenant_id=$1 AND preview_id=$2 AND action=$3 AND state='queued'`, s.TenantID, previewID, map[string]string{"create": "open", "create_slice": "open", "delete": "close", "delete_slice": "close"}[m.Kind]); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordExperimentalPreviewReceipt saves only a receipt for the exact
// journaled mutation. A delete acknowledgement is not proof of deletion.
// Errors or lost responses keep pending_mutation and remain unknown.
func (p *Postgres) RecordExperimentalPreviewReceipt(ctx context.Context, work biz.Work, previewID string, m biz.PreviewExperimentMutation, outcome, serviceUID string) error {
	if !biz.UUID(previewID) || !biz.UUID(m.ID) || (m.Kind != "create" && m.Kind != "delete" && m.Kind != "create_slice" && m.Kind != "delete_slice") || (outcome != "applied" && outcome != "unknown" && outcome != "not_sent" && outcome != "rejected") || (outcome == "not_sent" || outcome == "rejected") && m.Kind != "create" {
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
	var version int64
	if err = tx.QueryRow(ctx, `SELECT version FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, work.Sandbox.TenantID, work.Sandbox.ID).Scan(&version); err != nil {
		return err
	}
	r, err := scanPreview(tx.QueryRow(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND preview_id=$3 FOR UPDATE`, work.Sandbox.TenantID, work.Sandbox.ID, previewID))
	if err != nil {
		return err
	}
	if r.Pending == nil || *r.Pending != m {
		return biz.ErrConflict
	}
	if outcome == "not_sent" || outcome == "rejected" {
		// A later callback cannot resolve an earlier uncertain dispatch.
		if r.State == "unknown" {
			return biz.ErrConflict
		}
		if serviceUID != "" || r.ServiceUID != "" {
			return biz.ErrInvalid
		}
		reason := "PROVIDER_NOT_SENT"
		if outcome == "rejected" {
			reason = "PROVIDER_REJECTED"
		}
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET desired='closed',state='closed',reason=$3,address='',pending_mutation=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`, r.TenantID, r.PreviewID, reason); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_port_operations SET state=CASE WHEN action='open' THEN 'failed' ELSE 'succeeded' END,reason=CASE WHEN action='open' THEN $3 ELSE '' END,completed_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2 AND state IN ('queued','running','unknown')`, r.TenantID, r.PreviewID, reason); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if outcome == "unknown" {
		if serviceUID != "" {
			return biz.ErrInvalid
		}
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET state='unknown',reason='PROVIDER_OUTCOME_UNKNOWN',updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`, r.TenantID, r.PreviewID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_port_operations SET state='unknown',reason='PROVIDER_OUTCOME_UNKNOWN' WHERE tenant_id=$1 AND preview_id=$2 AND state='running'`, r.TenantID, r.PreviewID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if m.Kind == "create" || m.Kind == "create_slice" {
		if serviceUID == "" {
			return biz.ErrInvalid
		}
		query := `UPDATE sandbox.preview_ports SET state='opening',reason='SERVICE_CREATED',service_uid=$3,create_mutation_id=$4,pending_mutation=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`
		if m.Kind == "create_slice" {
			query = `UPDATE sandbox.preview_ports SET state='opening',reason='SLICE_CREATED',slice_uid=$3,slice_create_mutation_id=$4,pending_mutation=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`
		}
		if _, err = tx.Exec(ctx, query, r.TenantID, r.PreviewID, serviceUID, m.ID); err != nil {
			return err
		}
	} else {
		if serviceUID != "" && serviceUID != m.TargetUID {
			return biz.ErrOwnership
		}
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET state='closing',reason='WAITING_FOR_SERVICE_ABSENCE',updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`, r.TenantID, r.PreviewID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ObserveExperimentalPreviewMutation records only verified provider facts.
// A missing create stays unknown; a matching create may have arrived late.
// Delete reaches closed only when the exact UID is absent from a verified
// namespace and the caller has separately handled its EndpointSlice/gateway.
func (p *Postgres) ObserveExperimentalPreviewMutation(ctx context.Context, work biz.Work, previewID string, m biz.PreviewExperimentMutation, present bool, observedUID string, allEndpointsRevoked bool) error {
	if !biz.UUID(previewID) || !biz.UUID(m.ID) || (m.Kind != "create" && m.Kind != "delete" && m.Kind != "create_slice" && m.Kind != "delete_slice") {
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
	var version int64
	if err = tx.QueryRow(ctx, `SELECT version FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, work.Sandbox.TenantID, work.Sandbox.ID).Scan(&version); err != nil {
		return err
	}
	r, err := scanPreview(tx.QueryRow(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND preview_id=$3 FOR UPDATE`, work.Sandbox.TenantID, work.Sandbox.ID, previewID))
	if err != nil {
		return err
	}
	if r.Pending == nil || *r.Pending != m {
		return biz.ErrConflict
	}
	if m.Kind == "create" || m.Kind == "create_slice" {
		if !present {
			return tx.Commit(ctx)
		}
		if observedUID == "" {
			return biz.ErrInvalid
		}
		query := `UPDATE sandbox.preview_ports SET state='opening',reason='LATE_SERVICE_OBSERVED',service_uid=$3,create_mutation_id=$4,pending_mutation=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`
		if m.Kind == "create_slice" {
			query = `UPDATE sandbox.preview_ports SET state='opening',reason='LATE_SLICE_OBSERVED',slice_uid=$3,slice_create_mutation_id=$4,pending_mutation=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`
		}
		if _, err = tx.Exec(ctx, query, r.TenantID, r.PreviewID, observedUID, m.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if present {
		if observedUID != m.TargetUID {
			return biz.ErrOwnership
		}
		return tx.Commit(ctx)
	}
	if observedUID != "" || !allEndpointsRevoked {
		return biz.ErrDataPlaneUnavailable
	}
	if m.Kind == "delete_slice" {
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET state='closing',reason='SLICE_REVOKED',slice_uid='',slice_create_mutation_id='',pending_mutation=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`, r.TenantID, r.PreviewID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if r.SliceUID != "" {
		return biz.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET state='closed',desired='closed',reason='',service_uid='',pending_mutation=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`, r.TenantID, r.PreviewID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_port_operations SET state=CASE WHEN action='close' THEN 'succeeded' ELSE 'failed' END,reason=CASE WHEN action='close' THEN '' ELSE 'CLOSED_BEFORE_READY' END,completed_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2 AND state IN ('queued','running','unknown')`, r.TenantID, r.PreviewID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NextExperimentalPreviewWork uses the same worker lease and Sandbox lock as
// lifecycle reconciliation. It never sends a Kubernetes call itself.
func (p *Postgres) NextExperimentalPreviewWork(ctx context.Context, work biz.Work, enabled bool) (*biz.ExperimentalPreviewRecord, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockWork(ctx, tx, work, true); err != nil {
		return nil, err
	}
	var desired string
	var version int64
	if err = tx.QueryRow(ctx, `SELECT version,desired FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2 FOR UPDATE`, work.Sandbox.TenantID, work.Sandbox.ID).Scan(&version, &desired); err != nil {
		return nil, err
	}
	if version != work.Sandbox.Version {
		return nil, biz.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET desired='closed',state=CASE WHEN state IN ('opening','ready') THEN 'closing' ELSE state END,address='',updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND state<>'closed' AND (NOT $3 OR $4<>'running' OR expires_at<=clock_timestamp() OR pod_uid<>$5 OR $6<>'running')`, work.Sandbox.TenantID, work.Sandbox.ID, enabled, desired, work.Sandbox.ProviderRefs.PodUID, work.Sandbox.Observed); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `UPDATE sandbox.preview_ports SET state='closed',reason='CANCELLED_BEFORE_DISPATCH',updated_at=clock_timestamp() WHERE tenant_id=$1 AND sandbox_id=$2 AND desired='closed' AND state='queued' AND service_uid='' AND pending_mutation IS NULL RETURNING preview_id::text`, work.Sandbox.TenantID, work.Sandbox.ID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_port_operations SET state=CASE WHEN action='open' THEN 'failed' ELSE 'succeeded' END,reason=CASE WHEN action='open' THEN 'CANCELLED_BEFORE_DISPATCH' ELSE '' END,completed_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2 AND state='queued'`, work.Sandbox.TenantID, id); err != nil {
			return nil, err
		}
	}
	r, err := scanPreview(tx.QueryRow(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND state<>'closed' AND (desired='closed' OR pending_mutation IS NOT NULL OR ($3 AND state='queued') OR ($3 AND desired='open' AND service_uid<>'')) ORDER BY (desired='closed') DESC,updated_at,preview_id LIMIT 1 FOR UPDATE`, work.Sandbox.TenantID, work.Sandbox.ID, enabled))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	// Rotate blocked records so that an unknown outcome cannot starve other
	// cleanup work for the same Sandbox.
	if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_ports SET updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2`, r.TenantID, r.PreviewID); err != nil {
		return nil, err
	}
	return &r, tx.Commit(ctx)
}

// RecordExperimentalPreviewAddress stores only provider-observed facts under
// the same Sandbox intent and worker lease that authorized the open.
func (p *Postgres) RecordExperimentalPreviewAddress(ctx context.Context, work biz.Work, previewID, serviceUID, address string) error {
	if !biz.UUID(previewID) || serviceUID == "" {
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
	s, err := scanSandbox(tx.QueryRow(ctx, sandboxSelect+` WHERE s.tenant_id=$1 AND s.sandbox_id=$2 FOR UPDATE OF s`, work.Sandbox.TenantID, work.Sandbox.ID))
	if err != nil {
		return err
	}
	r, err := scanPreview(tx.QueryRow(ctx, previewSelect+` WHERE tenant_id=$1 AND sandbox_id=$2 AND preview_id=$3 FOR UPDATE`, s.TenantID, s.ID, previewID))
	if err != nil {
		return err
	}
	if s.Version != work.Sandbox.Version || s.LastOperationID != work.Sandbox.LastOperationID || r.Version != s.Version || r.Pending != nil || r.Desired != "open" || r.ServiceUID != serviceUID {
		return biz.ErrConflict
	}
	if address != "" && (s.Desired != biz.DesiredRunning || s.Observed != biz.ObservedRunning || s.Pending != nil || s.ProviderRefs.PodUID != r.PodUID) {
		return biz.ErrDataPlaneUnavailable
	}
	state, reason := "opening", "ENDPOINT_UNAVAILABLE"
	if address != "" {
		state, reason = "ready", ""
	}
	result, err := tx.Exec(ctx, `UPDATE sandbox.preview_ports SET state=$3,reason=$4,address=$5,updated_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2 AND expires_at>clock_timestamp() AND desired='open'`, s.TenantID, r.PreviewID, state, reason, address)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return biz.ErrConflict
	}
	if address != "" {
		if _, err = tx.Exec(ctx, `UPDATE sandbox.preview_port_operations SET state='succeeded',reason='',completed_at=clock_timestamp() WHERE tenant_id=$1 AND preview_id=$2 AND action='open' AND state IN ('queued','running','unknown')`, s.TenantID, r.PreviewID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
