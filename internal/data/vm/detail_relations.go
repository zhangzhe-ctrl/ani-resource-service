package vm

import (
	"context"

	network "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
)

type detailNetworkRepository interface {
	GetVPC(context.Context, string, string) (network.VPC, error)
	GetSubnet(context.Context, string, string) (network.Subnet, error)
	GetSecurityGroup(context.Context, string, string) (network.SecurityGroup, error)
}
type DetailRelations struct{ repo detailNetworkRepository }

func NewDetailRelations(repo detailNetworkRepository) *DetailRelations {
	return &DetailRelations{repo: repo}
}
func detailReference(id, name string, err error) biz.ResourceReference {
	state := "RESOLVED"
	if err != nil {
		name = ""
		state = "UNAVAILABLE"
		if network.ReasonOf(err) == network.ResourceNotFound {
			state = "NOT_FOUND"
		}
	}
	return biz.ResourceReference{ID: id, Name: name, State: state}
}
func (d *DetailRelations) ReadRelations(ctx context.Context, v biz.VM) biz.Relations {
	// Every lookup includes the VM's trusted tenant; names are current labels,
	// while the relationship IDs remain the frozen/current owner identities.
	vpc, e := d.repo.GetVPC(ctx, v.TenantID, v.Spec.Network.VPCID)
	out := biz.Relations{VPC: detailReference(v.Spec.Network.VPCID, vpc.Name, e)}
	subnet, e := d.repo.GetSubnet(ctx, v.TenantID, v.Spec.Network.SubnetID)
	out.Subnet = detailReference(v.Spec.Network.SubnetID, subnet.Name, e)
	ids := v.SecurityGroupIDs
	if !v.SecurityGroupsInitialized {
		ids = v.Spec.SecurityGroupIDs
	}
	for _, id := range ids {
		group, err := d.repo.GetSecurityGroup(ctx, v.TenantID, id)
		out.SecurityGroups = append(out.SecurityGroups, detailReference(id, group.Name, err))
	}
	return out
}
