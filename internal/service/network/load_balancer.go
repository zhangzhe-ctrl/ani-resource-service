package service

import (
	"context"

	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type TenantLoadBalancerService struct {
	networkv1.UnimplementedTenantLoadBalancerServiceServer
	lbs *biz.LoadBalancers
}

func NewTenantLoadBalancerService(lbs *biz.LoadBalancers) *TenantLoadBalancerService {
	return &TenantLoadBalancerService{lbs: lbs}
}
func wireOperation(v biz.Operation) *networkv1.Operation {
	return &networkv1.Operation{Id: v.ID, TenantId: v.TenantID, ResourceId: v.ResourceID, ResourceType: resourceTypesToWire[v.ResourceType], Kind: kindsToWire[v.Kind], State: operationsToWire[v.State], Reason: string(v.Reason), ReasonMessage: v.Reason.Message(), CreatedAt: timestamppb.New(v.CreatedAt), UpdatedAt: timestamppb.New(v.UpdatedAt), CompletedAt: optionalTime(v.CompletedAt), NextAttemptAt: optionalTime(v.NextAttemptAt)}
}

var lbExposureToWire = map[string]networkv1.LoadBalancerExposure{
	"private":        networkv1.LoadBalancerExposure_LOAD_BALANCER_EXPOSURE_PRIVATE,
	"public":         networkv1.LoadBalancerExposure_LOAD_BALANCER_EXPOSURE_PUBLIC,
	"public_private": networkv1.LoadBalancerExposure_LOAD_BALANCER_EXPOSURE_PUBLIC_PRIVATE,
}

func lbExposure(v networkv1.LoadBalancerExposure) (string, error) {
	if v == networkv1.LoadBalancerExposure_LOAD_BALANCER_EXPOSURE_UNSPECIFIED {
		return "", nil
	}
	for k, w := range lbExposureToWire {
		if v == w {
			return k, nil
		}
	}
	return "", biz.Fail(biz.InvalidArgument, "invalid load balancer exposure")
}
func lbMutable(name, description string, backends []*networkv1.LoadBalancerBackendInput, h *networkv1.LoadBalancerHealthCheck) (biz.LoadBalancerMutableInput, error) {
	r := biz.LoadBalancerMutableInput{Name: name, Description: description}
	if h != nil {
		if h.Protocol != networkv1.LoadBalancerHealthCheckProtocol_LOAD_BALANCER_HEALTH_CHECK_PROTOCOL_UNSPECIFIED && h.Protocol != networkv1.LoadBalancerHealthCheckProtocol_LOAD_BALANCER_HEALTH_CHECK_PROTOCOL_TCP {
			return r, biz.Fail(biz.InvalidArgument, "invalid health protocol")
		}
		r.Health = biz.LoadBalancerHealthInput{Port: h.Port, Protocol: "TCP", IntervalSeconds: h.IntervalSeconds, TimeoutSeconds: h.TimeoutSeconds, UnhealthyThreshold: h.UnhealthyThreshold, HealthyThreshold: h.HealthyThreshold}
	}
	for _, b := range backends {
		if b == nil {
			return r, biz.Fail(biz.InvalidArgument, "missing backend")
		}
		r.Backends = append(r.Backends, biz.LoadBalancerBackendInput{ID: b.Id, SubnetID: b.SubnetId, Address: b.Address, Port: b.Port, Weight: b.Weight})
	}
	return r, nil
}
func wireLoadBalancer(v biz.LoadBalancer) *networkv1.LoadBalancer {
	cfg := map[string]networkv1.LoadBalancerConfigurationState{
		"pending":    networkv1.LoadBalancerConfigurationState_LOAD_BALANCER_CONFIGURATION_STATE_PENDING,
		"applying":   networkv1.LoadBalancerConfigurationState_LOAD_BALANCER_CONFIGURATION_STATE_APPLYING,
		"configured": networkv1.LoadBalancerConfigurationState_LOAD_BALANCER_CONFIGURATION_STATE_CONFIGURED,
		"degraded":   networkv1.LoadBalancerConfigurationState_LOAD_BALANCER_CONFIGURATION_STATE_DEGRADED,
		"unknown":    networkv1.LoadBalancerConfigurationState_LOAD_BALANCER_CONFIGURATION_STATE_UNKNOWN,
	}
	data := networkv1.LoadBalancerDataPlaneState_LOAD_BALANCER_DATA_PLANE_STATE_UNKNOWN
	if v.DataPlaneObservedAt != nil {
		switch v.DataPlaneState {
		case "healthy":
			data = networkv1.LoadBalancerDataPlaneState_LOAD_BALANCER_DATA_PLANE_STATE_HEALTHY
		case "unhealthy":
			data = networkv1.LoadBalancerDataPlaneState_LOAD_BALANCER_DATA_PLANE_STATE_UNHEALTHY
		}
	}
	state, ok := cfg[v.ConfigurationState]
	if !ok {
		state = networkv1.LoadBalancerConfigurationState_LOAD_BALANCER_CONFIGURATION_STATE_UNKNOWN
	}
	r := &networkv1.LoadBalancer{Id: v.ID, TenantId: v.TenantID, VpcId: v.VPCID, SubnetId: v.SubnetID, Name: v.Name, Description: v.Description, Exposure: lbExposureToWire[v.Exposure], Flavor: v.Flavor, PublicEipId: v.PublicEIPID, PrivateIp: v.PrivateIP,
		Listener:    &networkv1.LoadBalancerListener{Id: v.Listener.ID, Protocol: networkv1.LoadBalancerListenerProtocol_LOAD_BALANCER_LISTENER_PROTOCOL_HTTP, Port: v.Listener.Port},
		HealthCheck: &networkv1.LoadBalancerHealthCheck{Protocol: networkv1.LoadBalancerHealthCheckProtocol_LOAD_BALANCER_HEALTH_CHECK_PROTOCOL_TCP, IntervalSeconds: &v.Health.IntervalSeconds, TimeoutSeconds: &v.Health.TimeoutSeconds, UnhealthyThreshold: &v.Health.UnhealthyThreshold, HealthyThreshold: &v.Health.HealthyThreshold},
		Algorithm:   networkv1.LoadBalancerAlgorithm_LOAD_BALANCER_ALGORITHM_ROUND_ROBIN, State: statesToWire[v.State], Reason: string(v.Reason), ReasonMessage: v.Reason.Message(), Version: v.Version, DesiredVersion: v.DesiredVersion, AppliedVersion: v.AppliedVersion, ConfigurationState: state, DataPlaneState: data, ObservedAt: optionalTime(v.ObservedAt), ObservationStale: v.ObservationStale, CreatedAt: timestamppb.New(v.CreatedAt), UpdatedAt: timestamppb.New(v.UpdatedAt), LastOperationId: v.LastOperationID, PublicAddress: v.PublicAddress, DataPlaneObservedAt: optionalTime(v.DataPlaneObservedAt)}
	if v.Health.Port != 0 {
		r.HealthCheck.Port = &v.Health.Port
	}
	for _, b := range v.Backends {
		r.Backends = append(r.Backends, &networkv1.LoadBalancerBackendMember{Id: b.ID, SubnetId: b.SubnetID, Address: b.Address, Port: b.Port, Weight: b.Weight, AttachmentId: b.AttachmentID, State: b.State, Reason: string(b.Reason), ObservedAt: optionalTime(b.ObservedAt), ObservationStale: b.ObservationStale})
	}
	return r
}
func (s *TenantLoadBalancerService) CreateLoadBalancer(ctx context.Context, r *networkv1.CreateLoadBalancerRequest) (*networkv1.CreateLoadBalancerResponse, error) {
	exposure, err := lbExposure(r.GetExposure())
	if err != nil {
		return nil, rpcError(err)
	}
	mutable, err := lbMutable(r.GetName(), r.GetDescription(), r.GetBackends(), r.GetHealthCheck())
	if err != nil {
		return nil, rpcError(err)
	}
	i := biz.CreateLoadBalancer{LoadBalancerMutableInput: mutable, TenantID: r.GetTargetTenantId(), VPCID: r.GetVpcId(), SubnetID: r.GetSubnetId(), Exposure: exposure, Flavor: r.GetFlavor(), PublicEIPID: r.GetPublicEipId(), PrivateIP: r.GetPrivateIp(), IdempotencyKey: r.GetIdempotencyKey()}
	if v := r.GetListener(); v != nil {
		if v.Protocol != networkv1.LoadBalancerListenerProtocol_LOAD_BALANCER_LISTENER_PROTOCOL_UNSPECIFIED && v.Protocol != networkv1.LoadBalancerListenerProtocol_LOAD_BALANCER_LISTENER_PROTOCOL_HTTP {
			return nil, rpcError(biz.Fail(biz.InvalidArgument, "invalid listener protocol"))
		}
		i.ListenerProtocol, i.ListenerPort = "HTTP", v.Port
	}
	v, err := s.lbs.Create(ctx, i)
	if err != nil {
		return nil, rpcError(err)
	}
	return &networkv1.CreateLoadBalancerResponse{LoadBalancer: wireLoadBalancer(v.LoadBalancer), Operation: wireOperation(v.Operation)}, nil
}
func (s *TenantLoadBalancerService) UpdateLoadBalancer(ctx context.Context, r *networkv1.UpdateLoadBalancerRequest) (*networkv1.UpdateLoadBalancerResponse, error) {
	i, err := lbMutable(r.GetName(), r.GetDescription(), r.GetBackends(), r.GetHealthCheck())
	if err != nil {
		return nil, rpcError(err)
	}
	v, err := s.lbs.Update(ctx, biz.UpdateLoadBalancer{LoadBalancerMutableInput: i, TenantID: r.GetTargetTenantId(), ID: r.GetLoadBalancerId(), ExpectedVersion: r.GetExpectedVersion(), IdempotencyKey: r.GetIdempotencyKey()})
	if err != nil {
		return nil, rpcError(err)
	}
	return &networkv1.UpdateLoadBalancerResponse{LoadBalancer: wireLoadBalancer(v.LoadBalancer), Operation: wireOperation(v.Operation)}, nil
}
func (s *TenantLoadBalancerService) GetLoadBalancer(ctx context.Context, r *networkv1.GetLoadBalancerRequest) (*networkv1.GetLoadBalancerResponse, error) {
	v, err := s.lbs.Get(ctx, r.GetTargetTenantId(), r.GetLoadBalancerId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &networkv1.GetLoadBalancerResponse{LoadBalancer: wireLoadBalancer(v)}, nil
}
func (s *TenantLoadBalancerService) ListLoadBalancers(ctx context.Context, r *networkv1.ListLoadBalancersRequest) (*networkv1.ListLoadBalancersResponse, error) {
	exposure, err := lbExposure(r.GetExposure())
	if err != nil {
		return nil, rpcError(err)
	}
	state, ok := statesFromWire[r.GetState()]
	if !ok && r.GetState() != networkv1.ResourceState_RESOURCE_STATE_UNSPECIFIED {
		return nil, rpcError(biz.Fail(biz.InvalidArgument, "invalid resource state"))
	}
	rows, next, total, err := s.lbs.List(ctx, biz.ListLoadBalancers{ListVPCs: biz.ListVPCs{TenantID: r.GetTargetTenantId(), Name: r.GetName(), State: string(state), Limit: int(r.GetLimit()), Cursor: r.GetCursor()}, VPCID: r.GetVpcId(), SubnetID: r.GetSubnetId(), Exposure: exposure})
	if err != nil {
		return nil, rpcError(err)
	}
	out := &networkv1.ListLoadBalancersResponse{Total: total, NextCursor: next, Items: make([]*networkv1.LoadBalancer, 0, len(rows))}
	for _, v := range rows {
		out.Items = append(out.Items, wireLoadBalancer(v))
	}
	return out, nil
}
func (s *TenantLoadBalancerService) DeleteLoadBalancer(ctx context.Context, r *networkv1.DeleteLoadBalancerRequest) (*networkv1.DeleteLoadBalancerResponse, error) {
	v, err := s.lbs.Delete(ctx, r.GetTargetTenantId(), r.GetLoadBalancerId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &networkv1.DeleteLoadBalancerResponse{LoadBalancer: wireLoadBalancer(v.LoadBalancer), Operation: wireOperation(v.Operation)}, nil
}
func (s *TenantLoadBalancerService) GetLoadBalancerOperation(ctx context.Context, r *networkv1.GetLoadBalancerOperationRequest) (*networkv1.GetLoadBalancerOperationResponse, error) {
	v, err := s.lbs.GetOperation(ctx, r.GetTargetTenantId(), r.GetOperationId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &networkv1.GetLoadBalancerOperationResponse{Operation: wireOperation(v)}, nil
}
