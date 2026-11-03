package vm

import (
	"context"
	networkpb "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/vm/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func choice(c biz.NetworkInterfaceChoice) *networkpb.NetworkInterfaceChoice {
	return &networkpb.NetworkInterfaceChoice{Mode: c.Mode, VnicId: c.VNicID, VnicName: c.ProviderName, ProviderUid: c.ProviderUID, MacAddress: c.MACAddress, Ips: append([]string{}, c.IPs...)}
}
func (s *Service) GetNetworkInterfaces(ctx context.Context, r *pb.GetVMRequest) (*networkpb.NetworkInterfaces, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	v, err := s.repo.Get(ctx, r.TenantId, r.VmId)
	if err != nil {
		return nil, rpcError(err)
	}
	cfg := biz.PrimaryNetworkInterface(v)
	item := &networkpb.NetworkInterface{Slot: "primary", Desired: choice(cfg.Desired), Applied: choice(cfg.Applied), RequestedRevision: cfg.RequestedRevision, AppliedRevision: cfg.AppliedRevision, PendingRestart: cfg.PendingRestart(), ObservationReason: v.Reason}
	if !v.ObservedAt.IsZero() {
		item.ObservedAt = timestamppb.New(v.ObservedAt)
	}
	// Only persisted runtime evidence populates current addresses. Desired IPs
	// cannot overwrite the currently attached interface in this read projection.
	if v.Observed == "Running" && !v.Closing {
		for _, a := range v.Addresses {
			item.CurrentIps = append(item.CurrentIps, a.Address)
		}
		if cfg.Applied.Mode == "manual" && cfg.VerifiedPodUID != "" {
			item.CurrentVnicName = cfg.Applied.ProviderName
			item.CurrentMacAddress = cfg.Applied.MACAddress
		}
		item.EffectiveNow = !cfg.PendingRestart() && (cfg.RequestedRevision == 0 || cfg.VerifiedPodUID != "") && len(item.CurrentIps) > 0 && item.ObservationReason == ""
	}
	return &networkpb.NetworkInterfaces{Items: []*networkpb.NetworkInterface{item}, Version: v.Version, Supported: true}, nil
}
func (s *Service) SetNetworkInterface(ctx context.Context, r *pb.SetNetworkInterfaceRequest) (*pb.Operation, error) {
	if r == nil || (biz.NetworkInterfaceIntent{Slot: r.Slot, Mode: r.Mode, VNicID: r.VnicId}).Validate() != nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	return s.command(ctx, r, "set-network-interface")
}
