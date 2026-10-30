package vm

import (
	"context"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/vm/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
)

func (s *Service) UseSecurityGroups(port biz.SecurityGroupPort) error {
	if port == nil {
		return biz.ErrInvalid
	}
	s.securityGroups = port
	return nil
}
func (s *Service) validateSecurityGroups(ctx context.Context, c biz.Command, ids []string) error {
	if biz.ValidateSecurityGroupIDs(ids) != nil {
		return biz.ErrInvalid
	}
	v, err := s.repo.Get(ctx, c.TenantID, c.VMID)
	if err != nil {
		return err
	}
	if s.securityGroups == nil {
		return biz.ErrDependency
	}
	_, err = s.securityGroups.ResolveSecurityGroups(ctx, c.TenantID, v.Spec.Network.VPCID, v.Spec.Namespace, ids)
	return err
}
func (s *Service) SetSecurityGroups(ctx context.Context, r *pb.SetSecurityGroupsRequest) (*pb.Operation, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	return s.command(ctx, r, "set-security-groups")
}
