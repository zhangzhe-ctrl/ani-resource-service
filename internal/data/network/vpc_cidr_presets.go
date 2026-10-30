package data

import (
	"context"
	"net/netip"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network/sqlcgen"
)

// The platform publishes preplanned networks that do not overlap its infrastructure.
// Admission checks the configured whitelist and accepted address pools in SQL.
func (p *Postgres) ConfigureVPCCIDRPresets(values []string) error {
	policy, err := biz.NewVPCCIDRPresets(values)
	if err != nil {
		return err
	}
	p.vpcCIDRPresets = policy
	return nil
}

func (p *Postgres) validateVPCPlatformCIDR(ctx context.Context, q *sqlcgen.Queries, cidr string) error {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return biz.Fail(biz.InvalidArgument, "invalid VPC CIDR")
	}
	count, err := q.PublicPoolOverlaps(ctx, sqlcgen.PublicPoolOverlapsParams{ClusterID: p.placement.ClusterID, Cidr: prefix})
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
	for _, cidr := range p.vpcCIDRPresets.Values() {
		err = p.validateVPCPlatformCIDR(ctx, q, cidr)
		if biz.ReasonOf(err) == biz.ResourceInUse {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, cidr)
	}
	return result, nil
}
