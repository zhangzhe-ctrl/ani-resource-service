package sandbox

import (
	"context"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Restricted policies must never be accepted as if they were enforced.  The
// current provider records intent only and has no verified FQDN data plane.
func requireEnforceableEgress(intent *pb.SandboxEgressIntent) error {
	if intent != nil && (intent.NetworkEgressPolicy == "allowlist" || intent.NetworkEgressPolicy == "deny_all") {
		return status.Error(codes.Unimplemented, "SANDBOX_EGRESS_ENFORCEMENT_UNSUPPORTED")
	}
	return nil
}

func (s *Service) GetSandboxEgress(ctx context.Context, r *pb.GetSandboxEgressRequest) (*pb.GetSandboxEgressResponse, error) {
	if r == nil || hasUnknown(r.ProtoReflect()) || !biz.UUID(r.SandboxId) {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	sandbox, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	intent, err := pb.DecodeEgressIntent(sandbox.Snapshot.EgressIntentJSON)
	if err != nil {
		return nil, rpcError(biz.ErrDependencyUnavailable)
	}
	observation := biz.EgressDeclaration{Declaration: "unknown", Rules: []biz.EgressRule{}}
	source := "unavailable"
	if sandbox.Desired == biz.DesiredRunning && sandbox.Observed == biz.ObservedRunning && sandbox.Pending == nil {
		if s.config.EgressReader == nil {
			return nil, rpcError(biz.ErrDependencyUnavailable)
		}
		observation, err = s.config.EgressReader.ReadEgressDeclaration(ctx, sandbox)
		if err != nil {
			return nil, rpcError(err)
		}
		source = "kubernetes_network_policy"
	} else if intent == nil {
		return nil, rpcError(biz.ErrDataPlaneUnavailable)
	}
	current, err := s.repo.Get(ctx, r.TenantId, r.SandboxId)
	if err != nil {
		return nil, rpcError(err)
	}
	if current.ID != sandbox.ID || current.TenantID != sandbox.TenantID || current.Version != sandbox.Version || current.Desired != sandbox.Desired || current.Observed != sandbox.Observed || (current.Pending == nil) != (sandbox.Pending == nil) || current.Snapshot.EgressIntentJSON != sandbox.Snapshot.EgressIntentJSON || current.ProviderRefs.PodUID != sandbox.ProviderRefs.PodUID || current.ProviderRefs.PodName != sandbox.ProviderRefs.PodName || current.Snapshot.ClusterID != sandbox.Snapshot.ClusterID || current.Snapshot.Namespace != sandbox.Snapshot.Namespace || current.Snapshot.NamespaceUID != sandbox.Snapshot.NamespaceUID {
		return nil, rpcError(biz.ErrDataPlaneUnavailable)
	}
	out := &pb.GetSandboxEgressResponse{Source: source, Declaration: observation.Declaration, Enforcement: "unknown", HostAllowlistKnown: false, HostAllowlistStatus: "unknown", Isolated: observation.Isolated, Rules: []*pb.SandboxEgressRule{}, Intent: intent}
	if intent != nil {
		out.IntentEnforcement = "not_applied_by_sandbox"
		if intent.NetworkEgressPolicy == "allowlist" {
			out.HostAllowlistKnown = true
			out.HostAllowlistStatus = "known"
		} else {
			out.HostAllowlistStatus = "not_applicable"
		}
	}
	for _, rule := range observation.Rules {
		value := &pb.SandboxEgressRule{Peers: []*pb.SandboxEgressPeer{}, Ports: []*pb.SandboxEgressPort{}}
		for _, peer := range rule.Peers {
			v := &pb.SandboxEgressPeer{PodSelector: egressSelectorProto(peer.PodSelector), NamespaceSelector: egressSelectorProto(peer.NamespaceSelector)}
			if peer.IPBlock != nil {
				v.IpBlock = &pb.SandboxEgressIPBlock{Cidr: peer.IPBlock.CIDR, Except: peer.IPBlock.Except}
			}
			value.Peers = append(value.Peers, v)
		}
		for _, port := range rule.Ports {
			value.Ports = append(value.Ports, &pb.SandboxEgressPort{Protocol: port.Protocol, PortType: port.PortType, Port: port.Port, EndPort: port.EndPort})
		}
		out.Rules = append(out.Rules, value)
	}
	return out, nil
}

func egressSelectorProto(selector *biz.EgressLabelSelector) *pb.SandboxEgressSelector {
	if selector == nil {
		return nil
	}
	out := &pb.SandboxEgressSelector{MatchLabels: selector.MatchLabels, MatchExpressions: []*pb.SandboxEgressLabelExpression{}}
	for _, expr := range selector.MatchExpressions {
		out.MatchExpressions = append(out.MatchExpressions, &pb.SandboxEgressLabelExpression{Key: expr.Key, Operator: expr.Operator, Values: expr.Values})
	}
	return out
}
