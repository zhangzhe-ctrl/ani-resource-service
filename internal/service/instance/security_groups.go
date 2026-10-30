package instance

import (
	"context"
	"errors"
	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
)

func (s *Service) UseSecurityGroups(port biz.SecurityGroupPort) error {
	if port == nil {
		return biz.ErrInvalid
	}
	s.securityGroups = port
	return nil
}
func (s *Service) SetSecurityGroups(ctx context.Context, r *pb.SetSecurityGroupsRequest) (*pb.OperationResponse, error) {
	if r == nil || len(r.ProtoReflect().GetUnknown()) > 0 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	raw, err := pb.CanonicalSecurityGroupIntent(r.InstanceId, r.ExpectedVersion, r.SecurityGroupIds)
	if err != nil || !biz.UUID(r.IdempotencyKey) {
		return nil, rpcError(biz.ErrInvalid)
	}
	c := biz.Command{TenantID: r.TenantId, ID: r.InstanceId, Action: "set-security-groups", Key: r.IdempotencyKey, ExpectedVersion: r.ExpectedVersion, CanonicalIntent: raw, RequestHash: pb.CommandIntentDigest(raw), SecurityGroupIDs: append([]string{}, r.SecurityGroupIds...)}
	replay, ok := s.repo.(biz.CommandReplayRepository)
	if !ok {
		return nil, rpcError(biz.ErrNetworkUnavailable)
	}
	a, err := replay.LookupCommandReplay(ctx, c)
	if err == nil {
		return ack(a), nil
	}
	if !errors.Is(err, biz.ErrNotFound) {
		return nil, rpcError(err)
	}
	i, err := s.repo.Get(ctx, r.TenantId, r.InstanceId)
	if err != nil {
		return nil, rpcError(err)
	}
	if i.Spec.Network == nil {
		return nil, rpcError(biz.ErrNetworkConflict)
	}
	if s.securityGroups == nil {
		return nil, rpcError(biz.ErrNetworkUnavailable)
	}
	if _, err = s.securityGroups.ResolveSecurityGroups(ctx, r.TenantId, i.Spec.Network.VPCID, i.Spec.Namespace, r.SecurityGroupIds); err != nil {
		return nil, rpcError(err)
	}
	c.OperationID = uuid.NewString()
	a, err = s.repo.AcceptCommand(ctx, c)
	return ack(a), rpcError(err)
}
