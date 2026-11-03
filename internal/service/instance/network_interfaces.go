package instance

import (
	"context"
	"errors"
	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	networkpb "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func networkChoice(c biz.NetworkInterfaceChoice) *networkpb.NetworkInterfaceChoice {
	return &networkpb.NetworkInterfaceChoice{Mode: c.Mode, VnicId: c.VNicID, VnicName: c.ProviderName, ProviderUid: c.ProviderUID, MacAddress: c.MACAddress, Ips: append([]string{}, c.IPs...)}
}
func (s *Service) GetNetworkInterfaces(ctx context.Context, r *pb.GetInstanceRequest) (*networkpb.NetworkInterfaces, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	i, err := s.repo.Get(ctx, r.TenantId, r.InstanceId)
	if err != nil {
		return nil, rpcError(err)
	}
	if i.Spec.Network == nil || i.Spec.InstanceKind != biz.KindContainer {
		return &networkpb.NetworkInterfaces{Version: i.Version, Supported: false, UnsupportedReason: "INSTANCE_NETWORK_NOT_SUPPORTED"}, nil
	}
	cfg := biz.PrimaryNetworkInterface(i)
	item := &networkpb.NetworkInterface{Slot: "primary", Desired: networkChoice(cfg.Desired), Applied: networkChoice(cfg.Applied), RequestedRevision: cfg.RequestedRevision, AppliedRevision: cfg.AppliedRevision, PendingRestart: cfg.PendingRestart(), ObservationReason: i.Reason}
	if !i.UpdatedAt.IsZero() {
		item.ObservedAt = timestamppb.New(i.UpdatedAt)
	}
	if i.Observed == biz.Running && i.Desired != biz.Deleted && i.Network != nil && i.PodUID != "" && i.Network.NetworkState == "attached" {
		item.CurrentVnicName = i.Network.CurrentVNicName
		item.CurrentMacAddress = i.Network.CurrentMACAddress
		item.CurrentIps = append([]string{}, i.Network.CurrentIPs...)
		item.EffectiveNow = !cfg.PendingRestart() && (cfg.RequestedRevision == 0 || cfg.VerifiedPodUID == i.PodUID) && i.Network.Reason == "" && len(item.CurrentIps) > 0
	}

	return &networkpb.NetworkInterfaces{Items: []*networkpb.NetworkInterface{item}, Version: i.Version, Supported: true}, nil
}
func (s *Service) SetNetworkInterface(ctx context.Context, r *pb.SetNetworkInterfaceRequest) (*pb.OperationResponse, error) {
	if r == nil || len(r.ProtoReflect().GetUnknown()) > 0 {
		return nil, rpcError(biz.ErrInvalid)
	}
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, rpcError(err)
	}
	raw, err := pb.CanonicalNetworkInterfaceIntent(r.InstanceId, r.Slot, r.Mode, r.VnicId, r.ExpectedVersion)
	if err != nil || !biz.UUID(r.IdempotencyKey) {
		return nil, rpcError(biz.ErrInvalid)
	}
	c := biz.Command{TenantID: r.TenantId, ID: r.InstanceId, Action: "set-network-interface", Key: r.IdempotencyKey, ExpectedVersion: r.ExpectedVersion, CanonicalIntent: raw, RequestHash: pb.CommandIntentDigest(raw), NetworkInterface: &biz.NetworkInterfaceIntent{Slot: r.Slot, Mode: r.Mode, VNicID: r.VnicId}}
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
	c.OperationID = uuid.NewString()
	a, err = s.repo.AcceptCommand(ctx, c)
	return ack(a), rpcError(err)
}
