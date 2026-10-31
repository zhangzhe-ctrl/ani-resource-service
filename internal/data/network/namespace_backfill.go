package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network/sqlcgen"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/tenantnamespace"
)

type NamespaceUIDBackfillRow struct {
	TenantID, ClusterID, Namespace string
	OriginalUID                    *string
	LiveUID                        string
}
type NamespaceUIDBackfillPlan struct {
	ClusterID string
	Rows      []NamespaceUIDBackfillRow
	NullCount int
	SHA256    string
}

// PlanNamespaceUIDBackfill is read-only. Only already registered rows in the
// configured cluster are considered; live objects must match the standard
// tenant identity. Existing non-NULL UIDs are compared, never replaced.
func (p *Postgres) PlanNamespaceUIDBackfill(ctx context.Context) (NamespaceUIDBackfillPlan, error) {
	plan := NamespaceUIDBackfillPlan{ClusterID: p.placement.ClusterID, Rows: []NamespaceUIDBackfillRow{}}
	rows, err := p.queries.ListTenantNamespaceIdentities(ctx)
	if err != nil {
		return plan, databaseFailure(err)
	}
	for _, row := range rows {
		if row.ClusterID != p.placement.ClusterID {
			continue
		}
		live, err := p.ResolveTenantNamespace(ctx, row.TenantID)
		if err != nil {
			return plan, err
		}
		if live.Name != row.Namespace {
			return plan, fmt.Errorf("registered namespace differs for tenant %s", row.TenantID)
		}
		if row.NamespaceUid != nil && *row.NamespaceUid != live.UID {
			return plan, fmt.Errorf("original namespace UID differs for tenant %s", row.TenantID)
		}
		if row.NamespaceUid == nil {
			plan.NullCount++
		}
		plan.Rows = append(plan.Rows, NamespaceUIDBackfillRow{TenantID: row.TenantID, ClusterID: row.ClusterID, Namespace: row.Namespace, OriginalUID: row.NamespaceUid, LiveUID: live.UID})
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return plan, err
	}
	sum := sha256.Sum256(body)
	plan.SHA256 = hex.EncodeToString(sum[:])
	return plan, nil
}

// ApplyNamespaceUIDBackfill re-reads the complete reviewed plan, then updates
// exact NULL rows in one transaction. A race or identity drift rolls it back.
func (p *Postgres) ApplyNamespaceUIDBackfill(ctx context.Context, reviewedSHA256 string) (NamespaceUIDBackfillPlan, error) {
	plan, err := p.PlanNamespaceUIDBackfill(ctx)
	if err != nil {
		return plan, err
	}
	if reviewedSHA256 == "" || reviewedSHA256 != plan.SHA256 {
		return plan, fmt.Errorf("namespace UID plan digest differs")
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return plan, databaseFailure(err)
	}
	defer tx.Rollback(ctx)
	q := p.queries.WithTx(tx)
	for _, row := range plan.Rows {
		if row.OriginalUID != nil {
			continue
		}
		live, err := p.ResolveTenantNamespace(ctx, row.TenantID)
		if err != nil {
			return plan, err
		}
		if live.Name != row.Namespace || live.UID != row.LiveUID {
			return plan, tenantnamespace.ErrUIDChanged
		}
		changed, err := q.BackfillTenantNamespaceUID(ctx, sqlcgen.BackfillTenantNamespaceUIDParams{TenantID: row.TenantID, ClusterID: row.ClusterID, Namespace: row.Namespace, NamespaceUid: row.LiveUID})
		if err != nil {
			return plan, databaseFailure(err)
		}
		if changed != 1 {
			return plan, fmt.Errorf("namespace UID row changed during backfill for tenant %s", row.TenantID)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return plan, databaseFailure(err)
	}
	return p.PlanNamespaceUIDBackfill(ctx)
}
