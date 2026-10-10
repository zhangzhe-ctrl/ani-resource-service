package biz

import (
	"context"
	"net/netip"
	"slices"
	"time"
)

// PlatformCIDRFacts reads assigned/reserved networks, never aggregate routes.
type PlatformCIDRFacts interface {
	PlatformCIDRs(context.Context) ([]string, time.Time, error)
}

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
func ValidateVPCPlatformCIDRs(cidr string, platform []string, observedAt, now time.Time) error {
	if len(platform) == 0 || observedAt.IsZero() || observedAt.After(now) || now.Sub(observedAt) > time.Minute {
		return Fail(DependencyUnavailable, "platform CIDR facts are unknown or expired")
	}
	wanted, err := netip.ParsePrefix(cidr)
	if err != nil {
		return Fail(InvalidArgument, "invalid VPC CIDR")
	}
	for _, value := range platform {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix != prefix.Masked() {
			return Fail(DependencyUnavailable, "platform CIDR facts are invalid")
		}
		if wanted.Overlaps(prefix) {
			return Fail(ResourceInUse, "VPC CIDR conflicts with a platform network")
		}
	}
	return nil
}
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
