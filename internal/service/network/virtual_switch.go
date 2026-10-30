package service

import (
	"context"
	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func wireSwitchNodes(nodes []biz.VirtualSwitchNode) []*networkv1.VirtualSwitchNode {
	result := make([]*networkv1.VirtualSwitchNode, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, &networkv1.VirtualSwitchNode{NodeName: n.NodeName, NodeUid: n.NodeUID, Priority: n.Priority, State: n.State, Reason: n.Reason})
	}
	return result
}
func wireVirtualSwitch(v biz.VirtualSwitch) *networkv1.VirtualSwitch {
	return &networkv1.VirtualSwitch{Id: v.ID, Name: v.Name, State: statesToWire[v.State], Reason: string(v.Reason), ReasonMessage: v.Reason.Message(), Version: v.Version, CreatedAt: timestamppb.New(v.CreatedAt), UpdatedAt: timestamppb.New(v.UpdatedAt), LastOperationId: v.LastOperationID, ClusterId: v.ClusterID, GroupName: v.GroupName, BindingState: v.BindingState, BindingVersion: v.BindingVersion, MembershipFingerprint: v.MembershipFingerprint, Nodes: wireSwitchNodes(v.Nodes)}
}
func (s *PlatformNetworkService) CreateVirtualSwitch(ctx context.Context, r *networkv1.CreateVirtualSwitchRequest) (*networkv1.CreateVirtualSwitchResponse, error) {
	v, err := s.egress.CreateVirtualSwitch(ctx, biz.VirtualSwitchIntent{Name: r.GetName(), IdempotencyKey: r.GetIdempotencyKey()})
	if err != nil {
		return nil, rpcError(err)
	}
	return &networkv1.CreateVirtualSwitchResponse{Resource: wireVirtualSwitch(v)}, nil
}
func (s *PlatformNetworkService) GetVirtualSwitch(ctx context.Context, r *networkv1.GetVirtualSwitchRequest) (*networkv1.GetVirtualSwitchResponse, error) {
	v, err := s.egress.GetVirtualSwitch(ctx, r.GetVirtualSwitchId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &networkv1.GetVirtualSwitchResponse{Resource: wireVirtualSwitch(v)}, nil
}
func (s *PlatformNetworkService) ListVirtualSwitches(ctx context.Context, r *networkv1.ListVirtualSwitchesRequest) (*networkv1.ListVirtualSwitchesResponse, error) {
	state, ok := statesFromWire[r.GetState()]
	if !ok {
		return nil, rpcError(biz.Fail(biz.InvalidArgument, "invalid state filter"))
	}
	rows, total, err := s.egress.ListVirtualSwitches(ctx, biz.VirtualSwitchFilter{Name: r.GetName(), State: string(state), Page: r.GetPage(), PageSize: r.GetPageSize()})
	if err != nil {
		return nil, rpcError(err)
	}
	reply := &networkv1.ListVirtualSwitchesResponse{Items: make([]*networkv1.VirtualSwitch, 0, len(rows)), Total: total}
	for _, v := range rows {
		reply.Items = append(reply.Items, wireVirtualSwitch(v))
	}
	return reply, nil
}
func (s *PlatformNetworkService) DeleteVirtualSwitch(ctx context.Context, r *networkv1.DeleteVirtualSwitchRequest) (*networkv1.DeleteVirtualSwitchResponse, error) {
	v, err := s.egress.DeleteVirtualSwitch(ctx, r.GetVirtualSwitchId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &networkv1.DeleteVirtualSwitchResponse{Resource: wireVirtualSwitch(v)}, nil
}
