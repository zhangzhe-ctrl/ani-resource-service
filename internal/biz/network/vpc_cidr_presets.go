package biz

import (
	"context"
	"slices"
)

type VPCCIDRPresets struct{ cidrs []string }

func NewVPCCIDRPresets(values []string) (*VPCCIDRPresets, error) {
	seen := map[string]bool{}
	for _, value := range values {
		// Reuse CIDR syntax/RFC1918 validation without adding policy to Subnets.
		if _, err := NewVPCIntent("00000000-0000-0000-0000-000000000001", "preset", value, "", "preset"); err != nil {
			return nil, err
		}
		if seen[value] {
			return nil, Fail(InvalidArgument, "duplicate VPC CIDR preset")
		}
		seen[value] = true
	}
	return &VPCCIDRPresets{cidrs: slices.Clone(values)}, nil
}
func (p *VPCCIDRPresets) Values() []string {
	if p == nil {
		return []string{}
	}
	return slices.Clone(p.cidrs)
}
func (p *VPCCIDRPresets) Allows(cidr string) bool { return p != nil && slices.Contains(p.cidrs, cidr) }
func (n *Network) ListVPCCIDRPresets(ctx context.Context, tenant string) ([]string, error) {
	if _, err := ParseTenant(tenant); err != nil {
		return nil, err
	}
	repo, ok := n.repository.(interface {
		ListVPCCIDRPresets(context.Context) ([]string, error)
	})
	if !ok {
		return nil, Fail(DependencyUnavailable, "VPC CIDR policy is unavailable")
	}
	return repo.ListVPCCIDRPresets(ctx)
}
