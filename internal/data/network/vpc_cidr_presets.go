package data

import (
	"context"
	"net/netip"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network/sqlcgen"
)

// Configured once by the composition root; no cluster request at startup.
func (p *Postgres) ConfigureVPCCIDRPresets(values []string, facts biz.PlatformCIDRFacts) error {
	policy, err := biz.NewVPCCIDRPresets(values)
	if err != nil {
		return err
	}
	p.vpcCIDRPresets, p.platformCIDRFacts = policy, facts
	return nil
}
func (p *Postgres) vpcCIDRFacts(ctx context.Context) ([]string, time.Time, error) {
	if p.platformCIDRFacts == nil {
		return nil, time.Time{}, biz.Fail(biz.DependencyUnavailable, "platform CIDR facts are unavailable")
	}
	values, at, err := p.platformCIDRFacts.PlatformCIDRs(ctx)
	if err != nil {
		return nil, at, &biz.Error{Reason: biz.DependencyUnavailable, Message: "platform CIDR facts are unavailable", Cause: err}
	}
	return values, at, nil
}
func (p *Postgres) validateVPCPlatformCIDR(ctx context.Context, q *sqlcgen.Queries, cidr string) error {
	values, at, err := p.vpcCIDRFacts(ctx)
	if err != nil {
		return err
	}
	now, err := q.DatabaseTime(ctx)
	if err != nil {
		return databaseFailure(err)
	}
	if err = biz.ValidateVPCPlatformCIDRs(cidr, values, at, now); err != nil {
		return err
	}
	count, err := q.PublicPoolOverlaps(ctx, sqlcgen.PublicPoolOverlapsParams{ClusterID: p.placement.ClusterID, Cidr: netip.MustParsePrefix(cidr)})
	if err != nil {
		return databaseFailure(err)
	}
	if count > 0 {
		return biz.Fail(biz.ResourceInUse, "VPC CIDR conflicts with an accepted platform pool")
	}
	return nil
}
func (p *Postgres) ListVPCCIDRPresets(ctx context.Context) ([]string, error) {
	result := []string{}
	if len(p.vpcCIDRPresets.Values()) == 0 {
		return result, nil
	}
	// The same cluster lock protects pool admission in both directions.
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, databaseFailure(err)
	}
	defer tx.Rollback(ctx)
	q := p.queries.WithTx(tx)
	if err = q.LockPlatformCluster(ctx, sqlcgen.LockPlatformClusterParams{ClusterID: p.placement.ClusterID}); err != nil {
		return nil, databaseFailure(err)
	}
	values, at, err := p.vpcCIDRFacts(ctx)
	if err != nil {
		return nil, err
	}
	now, err := q.DatabaseTime(ctx)
	if err != nil {
		return nil, databaseFailure(err)
	}
	for _, cidr := range p.vpcCIDRPresets.Values() {
		err = biz.ValidateVPCPlatformCIDRs(cidr, values, at, now)
		if biz.ReasonOf(err) == biz.ResourceInUse {
			continue
		}
		if err != nil {
			return nil, err
		}
		count, err := q.PublicPoolOverlaps(ctx, sqlcgen.PublicPoolOverlapsParams{ClusterID: p.placement.ClusterID, Cidr: netip.MustParsePrefix(cidr)})
		if err != nil {
			return nil, databaseFailure(err)
		}
		if count == 0 {
			result = append(result, cidr)
		}
	}
	return result, nil
}
