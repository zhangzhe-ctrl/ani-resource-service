package sandbox

import (
	"context"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func recordSandboxPod(ctx context.Context, tx pgx.Tx, s biz.Sandbox, refs biz.ProviderRefs) error {
	if refs.PodUID == "" {
		return nil
	}
	result, err := tx.Exec(ctx, `INSERT INTO sandbox.pod_history
		(tenant_id,sandbox_id,cluster_id,namespace,namespace_uid,pod_name,pod_uid)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(tenant_id,sandbox_id,pod_uid) DO UPDATE SET pod_name=CASE
		  WHEN sandbox.pod_history.pod_name='' THEN EXCLUDED.pod_name ELSE sandbox.pod_history.pod_name END
		WHERE sandbox.pod_history.cluster_id=EXCLUDED.cluster_id
		AND sandbox.pod_history.namespace=EXCLUDED.namespace
		AND sandbox.pod_history.namespace_uid=EXCLUDED.namespace_uid
		AND (sandbox.pod_history.pod_name=EXCLUDED.pod_name OR sandbox.pod_history.pod_name='' OR EXCLUDED.pod_name='')`,
		s.TenantID, s.ID, s.Snapshot.ClusterID, s.Snapshot.Namespace, s.Snapshot.NamespaceUID, refs.PodName, refs.PodUID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return biz.ErrOwnership
	}
	return nil
}

func (p *Postgres) ListSandboxPodHistory(ctx context.Context, tenant, id string) ([]biz.PodHistory, error) {
	if err := biz.RequireTenant(ctx, tenant); err != nil {
		return nil, err
	}
	if !biz.UUID(id) {
		return nil, biz.ErrInvalid
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox.sandboxes WHERE tenant_id=$1 AND sandbox_id=$2)`, tenant, id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, biz.ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT cluster_id,namespace,namespace_uid,pod_name,pod_uid,first_recorded_at
		FROM sandbox.pod_history WHERE tenant_id=$1 AND sandbox_id=$2
		ORDER BY first_recorded_at,pod_uid LIMIT $3`, tenant, id, biz.PodHistoryLimit+1)
	if err != nil {
		return nil, err
	}
	items := make([]biz.PodHistory, 0)
	for rows.Next() {
		var item biz.PodHistory
		if err = rows.Scan(&item.ClusterID, &item.Namespace, &item.NamespaceUID, &item.PodName, &item.PodUID, &item.FirstRecordedAt); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(items) > biz.PodHistoryLimit {
		return nil, biz.ErrPodHistoryTooLarge
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}
