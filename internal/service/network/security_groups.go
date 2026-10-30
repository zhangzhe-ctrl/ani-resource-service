package service

import (
	"context"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NetworkSecurityGroupService exposes the tenant-owned use cases. Provider
// names and persisted execution remain behind the Network business boundary.
type NetworkSecurityGroupService struct {
	pb.UnimplementedNetworkSecurityGroupServiceServer
	groups *biz.SecurityGroups
}

func NewNetworkSecurityGroupService(groups *biz.SecurityGroups) *NetworkSecurityGroupService {
	return &NetworkSecurityGroupService{groups: groups}
}

func securityGroupRuleFromWire(rule *pb.SecurityGroupRule) biz.SecurityGroupRule {
	if rule == nil {
		return biz.SecurityGroupRule{}
	}
	v := biz.SecurityGroupRule{ID: rule.GetId(), Direction: rule.GetDirection(), Protocol: rule.GetProtocol(), Action: rule.GetAction(), Priority: rule.GetPriority(), Description: rule.GetDescription(), IsDefault: rule.GetIsDefault(), RemoteAddresses: append([]string{}, rule.GetRemoteAddresses()...)}
	for _, ports := range rule.GetPortRanges() {
		v.PortRanges = append(v.PortRanges, biz.SecurityGroupPortRange{Min: ports.GetMin(), Max: ports.GetMax()})
	}
	return v
}

func securityGroupRulesFromWire(rules []*pb.SecurityGroupRule) []biz.SecurityGroupRule {
	values := make([]biz.SecurityGroupRule, 0, len(rules))
	for _, rule := range rules {
		values = append(values, securityGroupRuleFromWire(rule))
	}
	return values
}

func securityGroupRuleToWire(rule biz.SecurityGroupRule) *pb.SecurityGroupRule {
	v := &pb.SecurityGroupRule{Id: rule.ID, Direction: rule.Direction, Protocol: rule.Protocol, RemoteAddresses: append([]string{}, rule.RemoteAddresses...), Action: rule.Action, Priority: rule.Priority, Description: rule.Description, IsDefault: rule.IsDefault, PortRanges: make([]*pb.SecurityGroupPortRange, 0, len(rule.PortRanges))}
	for _, ports := range rule.PortRanges {
		v.PortRanges = append(v.PortRanges, &pb.SecurityGroupPortRange{Min: ports.Min, Max: ports.Max})
	}
	return v
}

func securityGroupToWire(group biz.SecurityGroup) *pb.SecurityGroup {
	v := &pb.SecurityGroup{Id: group.ID, TenantId: group.TenantID, Name: group.Name, Description: group.Description, State: string(group.State), Reason: string(group.Reason), Version: group.Version, LastOperationId: group.LastOperationID, BoundResourceCount: group.BoundResourceCount, CreatedAt: timestamppb.New(group.CreatedAt), UpdatedAt: timestamppb.New(group.UpdatedAt), ObservedAt: optionalTime(group.ObservedAt), Rules: make([]*pb.SecurityGroupRule, 0, len(group.Rules))}
	for _, rule := range group.Rules {
		v.Rules = append(v.Rules, securityGroupRuleToWire(rule))
	}
	return v
}

func securityGroupBindingToWire(binding biz.SecurityGroupBinding) *pb.SecurityGroupBinding {
	return &pb.SecurityGroupBinding{TargetType: binding.TargetType, TargetId: binding.TargetID, SecurityGroupIds: append([]string{}, binding.SecurityGroupIDs...), AppliedSecurityGroupIds: append([]string{}, binding.AppliedSecurityGroupIDs...), State: binding.State, Reason: binding.Reason, Version: binding.Version, LastOperationId: binding.LastOperationID, ObservedAt: optionalTime(binding.ObservedAt)}
}

func sgWireOperation(operation biz.Operation) *pb.Operation {
	if operation.ID == "" {
		return nil
	}
	v := wireOperation(operation)
	if operation.ResourceType == "security_group" {
		v.ResourceType = pb.ResourceType_RESOURCE_TYPE_SECURITY_GROUP
	}
	switch operation.Kind {
	case "create_security_group":
		v.Kind = pb.OperationKind_OPERATION_KIND_CREATE_SECURITY_GROUP
	case "update_security_group":
		v.Kind = pb.OperationKind_OPERATION_KIND_UPDATE_SECURITY_GROUP
	case "delete_security_group":
		v.Kind = pb.OperationKind_OPERATION_KIND_DELETE_SECURITY_GROUP
	case "set_eip_security_groups":
		v.Kind = pb.OperationKind_OPERATION_KIND_SET_EIP_SECURITY_GROUPS
	}
	return v
}

func optionalSecurityGroupRule(rule biz.SecurityGroupRule) *pb.SecurityGroupRule {
	if rule.ID == "" {
		return nil
	}
	return securityGroupRuleToWire(rule)
}

func (s *NetworkSecurityGroupService) CreateSecurityGroup(ctx context.Context, r *pb.CreateSecurityGroupRequest) (*pb.CreateSecurityGroupResponse, error) {
	v, err := s.groups.Mutate(ctx, biz.SecurityGroupIntent{TenantID: r.GetTargetTenantId(), Action: "create", Name: r.GetName(), Description: r.GetDescription(), IdempotencyKey: r.GetIdempotencyKey(), Rules: securityGroupRulesFromWire(r.GetRules())})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.CreateSecurityGroupResponse{SecurityGroup: securityGroupToWire(v.SecurityGroup), Rule: optionalSecurityGroupRule(v.Rule), Operation: sgWireOperation(v.Operation)}, nil
}

func (s *NetworkSecurityGroupService) GetSecurityGroup(ctx context.Context, r *pb.GetSecurityGroupRequest) (*pb.GetSecurityGroupResponse, error) {
	v, err := s.groups.Get(ctx, r.GetTargetTenantId(), r.GetSecurityGroupId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.GetSecurityGroupResponse{SecurityGroup: securityGroupToWire(v)}, nil
}

func (s *NetworkSecurityGroupService) ListSecurityGroups(ctx context.Context, r *pb.ListSecurityGroupsRequest) (*pb.ListSecurityGroupsResponse, error) {
	values, total, err := s.groups.List(ctx, biz.SecurityGroupFilter{TenantID: r.GetTargetTenantId(), Name: r.GetName(), State: r.GetState(), Page: r.GetPage(), PageSize: r.GetPageSize()})
	if err != nil {
		return nil, rpcError(err)
	}
	result := &pb.ListSecurityGroupsResponse{Items: make([]*pb.SecurityGroup, 0, len(values)), Total: total}
	for _, value := range values {
		result.Items = append(result.Items, securityGroupToWire(value))
	}
	return result, nil
}

func (s *NetworkSecurityGroupService) UpdateSecurityGroup(ctx context.Context, r *pb.UpdateSecurityGroupRequest) (*pb.UpdateSecurityGroupResponse, error) {
	v, err := s.groups.Mutate(ctx, biz.SecurityGroupIntent{TenantID: r.GetTargetTenantId(), ID: r.GetSecurityGroupId(), Action: "update", Name: r.GetName(), Description: r.GetDescription(), ExpectedVersion: r.GetExpectedVersion(), IdempotencyKey: r.GetIdempotencyKey()})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.UpdateSecurityGroupResponse{SecurityGroup: securityGroupToWire(v.SecurityGroup), Rule: optionalSecurityGroupRule(v.Rule), Operation: sgWireOperation(v.Operation)}, nil
}

func (s *NetworkSecurityGroupService) DeleteSecurityGroup(ctx context.Context, r *pb.DeleteSecurityGroupRequest) (*pb.DeleteSecurityGroupResponse, error) {
	v, err := s.groups.Mutate(ctx, biz.SecurityGroupIntent{TenantID: r.GetTargetTenantId(), ID: r.GetSecurityGroupId(), Action: "delete"})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.DeleteSecurityGroupResponse{SecurityGroup: securityGroupToWire(v.SecurityGroup), Rule: optionalSecurityGroupRule(v.Rule), Operation: sgWireOperation(v.Operation)}, nil
}

func (s *NetworkSecurityGroupService) ListSecurityGroupRules(ctx context.Context, r *pb.ListSecurityGroupRulesRequest) (*pb.ListSecurityGroupRulesResponse, error) {
	if direction := r.GetDirection(); direction != "" && direction != "ingress" && direction != "egress" {
		return nil, rpcError(biz.Fail(biz.InvalidArgument, "direction must be ingress or egress"))
	}
	group, err := s.groups.Get(ctx, r.GetTargetTenantId(), r.GetSecurityGroupId())
	if err != nil {
		return nil, rpcError(err)
	}
	result := &pb.ListSecurityGroupRulesResponse{Items: make([]*pb.SecurityGroupRule, 0, len(group.Rules)), Version: group.Version}
	for _, rule := range group.Rules {
		if r.GetDirection() == "" || rule.Direction == r.GetDirection() {
			result.Items = append(result.Items, securityGroupRuleToWire(rule))
		}
	}
	result.Total = int64(len(result.Items))
	return result, nil
}

func (s *NetworkSecurityGroupService) CreateSecurityGroupRule(ctx context.Context, r *pb.CreateSecurityGroupRuleRequest) (*pb.CreateSecurityGroupRuleResponse, error) {
	v, err := s.groups.Mutate(ctx, biz.SecurityGroupIntent{TenantID: r.GetTargetTenantId(), ID: r.GetSecurityGroupId(), Action: "create_rule", Rule: securityGroupRuleFromWire(r.GetRule()), ExpectedVersion: r.GetExpectedVersion(), IdempotencyKey: r.GetIdempotencyKey()})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.CreateSecurityGroupRuleResponse{SecurityGroup: securityGroupToWire(v.SecurityGroup), Rule: optionalSecurityGroupRule(v.Rule), Operation: sgWireOperation(v.Operation)}, nil
}

func (s *NetworkSecurityGroupService) UpdateSecurityGroupRule(ctx context.Context, r *pb.UpdateSecurityGroupRuleRequest) (*pb.UpdateSecurityGroupRuleResponse, error) {
	v, err := s.groups.Mutate(ctx, biz.SecurityGroupIntent{TenantID: r.GetTargetTenantId(), ID: r.GetSecurityGroupId(), RuleID: r.GetRuleId(), Action: "update_rule", Rule: securityGroupRuleFromWire(r.GetRule()), ExpectedVersion: r.GetExpectedVersion(), IdempotencyKey: r.GetIdempotencyKey()})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.UpdateSecurityGroupRuleResponse{SecurityGroup: securityGroupToWire(v.SecurityGroup), Rule: optionalSecurityGroupRule(v.Rule), Operation: sgWireOperation(v.Operation)}, nil
}

func (s *NetworkSecurityGroupService) DeleteSecurityGroupRule(ctx context.Context, r *pb.DeleteSecurityGroupRuleRequest) (*pb.DeleteSecurityGroupRuleResponse, error) {
	v, err := s.groups.Mutate(ctx, biz.SecurityGroupIntent{TenantID: r.GetTargetTenantId(), ID: r.GetSecurityGroupId(), RuleID: r.GetRuleId(), Action: "delete_rule"})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.DeleteSecurityGroupRuleResponse{SecurityGroup: securityGroupToWire(v.SecurityGroup), Rule: optionalSecurityGroupRule(v.Rule), Operation: sgWireOperation(v.Operation)}, nil
}

func (s *NetworkSecurityGroupService) ListSecurityGroupBindings(ctx context.Context, r *pb.ListSecurityGroupBindingsRequest) (*pb.ListSecurityGroupBindingsResponse, error) {
	values, total, err := s.groups.Bindings(ctx, biz.SecurityGroupFilter{TenantID: r.GetTargetTenantId(), ID: r.GetSecurityGroupId(), TargetType: r.GetTargetType(), Page: r.GetPage(), PageSize: r.GetPageSize()})
	if err != nil {
		return nil, rpcError(err)
	}
	result := &pb.ListSecurityGroupBindingsResponse{Items: make([]*pb.SecurityGroupBinding, 0, len(values)), Total: total}
	for _, value := range values {
		result.Items = append(result.Items, securityGroupBindingToWire(value))
	}
	return result, nil
}

func (s *NetworkSecurityGroupService) SetEIPSecurityGroups(ctx context.Context, r *pb.SetEIPSecurityGroupsRequest) (*pb.SetEIPSecurityGroupsResponse, error) {
	v, err := s.groups.SetEIP(ctx, biz.SecurityGroupIntent{TenantID: r.GetTargetTenantId(), ID: r.GetEipId(), SecurityGroupIDs: append([]string{}, r.GetSecurityGroupIds()...), ExpectedVersion: r.GetExpectedVersion(), IdempotencyKey: r.GetIdempotencyKey()})
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.SetEIPSecurityGroupsResponse{Binding: securityGroupBindingToWire(v.Binding), Operation: sgWireOperation(v.Operation)}, nil
}

func (s *NetworkSecurityGroupService) GetEIPSecurityGroups(ctx context.Context, r *pb.GetEIPSecurityGroupsRequest) (*pb.GetEIPSecurityGroupsResponse, error) {
	v, err := s.groups.GetEIP(ctx, r.GetTargetTenantId(), r.GetEipId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.GetEIPSecurityGroupsResponse{Binding: securityGroupBindingToWire(v)}, nil
}

func (s *NetworkSecurityGroupService) GetSecurityGroupOperation(ctx context.Context, r *pb.GetSecurityGroupOperationRequest) (*pb.GetSecurityGroupOperationResponse, error) {
	v, err := s.groups.Operation(ctx, r.GetTargetTenantId(), r.GetOperationId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &pb.GetSecurityGroupOperationResponse{Operation: sgWireOperation(v)}, nil
}

var _ pb.NetworkSecurityGroupServiceServer = (*NetworkSecurityGroupService)(nil)
