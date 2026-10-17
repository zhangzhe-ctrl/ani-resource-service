package instance

import (
	"context"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strconv"
)

func gpuReference(g *pb.GpuPresetReference) *biz.GPUReference {
	if g == nil {
		return nil
	}
	return &biz.GPUReference{ProfileID: g.ProfileId, ProfileVersion: g.ProfileVersion, SpecDigest: g.SpecDigest, DevicesPerInstance: g.DevicesPerInstance, ClusterID: g.ClusterId, PoolID: g.PoolId, Mode: g.Mode.String(), ModelKey: g.ModelKey, MemoryMiBPerDevice: g.MemoryMibPerDevice, CoreLimitPercent: g.CoreLimitPercent}
}
func wireReference(g *biz.GPUReference) *pb.GpuPresetReference {
	if g == nil {
		return nil
	}
	return &pb.GpuPresetReference{ProfileId: g.ProfileID, ProfileVersion: g.ProfileVersion, SpecDigest: g.SpecDigest, DevicesPerInstance: g.DevicesPerInstance, ClusterId: g.ClusterID, PoolId: g.PoolID, Mode: acc.SupplyMode(acc.SupplyMode_value[g.Mode]), ModelKey: g.ModelKey, MemoryMibPerDevice: g.MemoryMiBPerDevice, CoreLimitPercent: g.CoreLimitPercent}
}
func wireSummary(g *biz.GPUSummary, execution biz.GPU) *pb.GpuSummary {
	if g == nil {
		return nil
	}
	n, _ := strconv.ParseUint(execution.Limits["volcano.sh/vgpu-number"], 10, 32)
	m, _ := strconv.ParseInt(execution.Limits["volcano.sh/vgpu-memory"], 10, 64)
	c, _ := strconv.ParseUint(execution.Limits["volcano.sh/vgpu-cores"], 10, 32)
	return &pb.GpuSummary{ProfileId: g.ProfileID, ProfileVersion: g.ProfileVersion, Mode: acc.SupplyMode(acc.SupplyMode_value[g.Mode]), ModelKey: g.ModelKey, DevicesPerInstance: uint32(n), MemoryMibPerDevice: m, MemoryMibTotal: m * int64(n), CoreLimitPercent: uint32(c)}
}
func domainScheduling(s *pb.SchedulingOptions) biz.Scheduling {
	return biz.Scheduling{PodPlacement: s.GetPodPlacement().String(), PriorityClassName: s.PriorityClassName}
}
func wireScheduling(s biz.Scheduling) *pb.SchedulingOptions {
	return &pb.SchedulingOptions{PodPlacement: pb.SchedulingOptions_PodPlacement(pb.SchedulingOptions_PodPlacement_value[s.PodPlacement]).Enum(), PriorityClassName: s.PriorityClassName}
}
func presetData(d *pb.PresetData) biz.Preset {
	return biz.Preset{Code: d.Code, Name: d.Name, Description: d.Description, ComputeType: d.ComputeType, CPUMillis: d.CpuMillis, MemoryMiB: d.MemoryMib, Enabled: d.GetEnabled(), GPU: gpuReference(d.Gpu)}
}
func wirePreset(p biz.Preset) *pb.Preset {
	reason := ""
	if !p.Enabled {
		reason = "COMPUTE_PRESET_DISABLED"
	}
	return &pb.Preset{Selectable: p.Enabled, UnavailableReason: reason, Id: p.ID, Code: p.Code, Name: p.Name, Description: p.Description, ComputeType: p.ComputeType, CpuMillis: p.CPUMillis, MemoryMib: p.MemoryMiB, Enabled: p.Enabled, Gpu: wireReference(p.GPU), CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt)}
}
func (s *Service) checkedPresetData(d *pb.PresetData) error {
	if d == nil {
		return biz.ErrInvalid
	}
	if d.Gpu != nil && d.Gpu.ClusterId != s.config.ClusterID {
		return biz.ErrInvalid
	}
	return nil
}
func (s *Service) ListPresets(ctx context.Context, r *pb.ListPresetsRequest) (*pb.ListPresetsResponse, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if e := biz.RequireTenant(ctx, r.TenantId); e != nil {
		return nil, rpcError(e)
	}
	f := biz.PresetFilter{Page: r.Page, PageSize: r.PageSize, Name: r.Name, Code: r.Code, ComputeType: r.ComputeType, GPUMode: r.GpuMode, Tenant: true}
	for _, g := range r.AllowedGpuProfiles {
		if g == nil || g.ClusterId != s.config.ClusterID || !biz.UUID(g.ProfileId) || !biz.UUID(g.PoolId) || g.ProfileVersion == 0 || len(g.SpecDigest) != 64 {
			return nil, rpcError(biz.ErrInvalid)
		}
		f.AllowedGPUProfiles = append(f.AllowedGPUProfiles, *gpuReference(g))
	}
	return s.listPresets(ctx, f)
}
func (s *Service) listPresets(ctx context.Context, f biz.PresetFilter) (*pb.ListPresetsResponse, error) {
	items, total, has, e := s.presets.ListPresets(ctx, f)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &pb.ListPresetsResponse{Items: []*pb.Preset{}, Total: total, HasGpuCandidates: has}
	for _, p := range items {
		out.Items = append(out.Items, wirePreset(p))
	}
	return out, nil
}
func (s *Service) AdminListPresets(ctx context.Context, r *pb.AdminListPresetsRequest) (*pb.ListPresetsResponse, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if _, e := biz.RequirePlatform(ctx); e != nil {
		return nil, rpcError(e)
	}
	return s.listPresets(ctx, biz.PresetFilter{Page: r.Page, PageSize: r.PageSize, Name: r.Name, Code: r.Code, ComputeType: r.ComputeType, GPUMode: r.GpuMode, Enabled: r.Enabled})
}
func (s *Service) AdminGetPreset(ctx context.Context, r *pb.AdminGetPresetRequest) (*pb.Preset, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if _, e := biz.RequirePlatform(ctx); e != nil {
		return nil, rpcError(e)
	}
	v, e := s.presets.GetPreset(ctx, r.Id)
	if e != nil {
		return nil, rpcError(e)
	}
	return wirePreset(v), nil
}
func (s *Service) AdminCreatePreset(ctx context.Context, r *pb.AdminCreatePresetRequest) (*pb.Preset, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if _, e := biz.RequirePlatform(ctx); e != nil {
		return nil, rpcError(e)
	}
	if e := s.checkedPresetData(r.Data); e != nil {
		return nil, rpcError(e)
	}
	v, e := s.presets.CreatePreset(ctx, presetData(r.Data))
	if e != nil {
		return nil, rpcError(e)
	}
	return wirePreset(v), nil
}
func (s *Service) AdminUpdatePreset(ctx context.Context, r *pb.AdminUpdatePresetRequest) (*pb.Preset, error) {
	if r == nil || r.Data == nil || r.UpdateMask == nil || biz.ValidatePresetMask(r.UpdateMask.Paths) != nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if _, e := biz.RequirePlatform(ctx); e != nil {
		return nil, rpcError(e)
	}
	for _, path := range r.UpdateMask.Paths {
		if path == "enabled" && r.Data.Enabled == nil {
			return nil, rpcError(biz.ErrInvalid)
		}
		if path == "gpu" && r.Data.Gpu != nil && r.Data.Gpu.ClusterId != s.config.ClusterID {
			return nil, rpcError(biz.ErrInvalid)
		}
	}
	v, e := s.presets.UpdatePreset(ctx, r.Id, presetData(r.Data), r.UpdateMask.Paths)
	if e != nil {
		return nil, rpcError(e)
	}
	return wirePreset(v), nil
}
func (s *Service) AdminSetPresetEnabled(ctx context.Context, r *pb.AdminSetPresetEnabledRequest) (*pb.Preset, error) {
	if r == nil || r.Enabled == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if _, e := biz.RequirePlatform(ctx); e != nil {
		return nil, rpcError(e)
	}
	v, e := s.presets.SetPresetEnabled(ctx, r.Id, *r.Enabled)
	if e != nil {
		return nil, rpcError(e)
	}
	return wirePreset(v), nil
}
func (s *Service) AdminDeletePreset(ctx context.Context, r *pb.AdminDeletePresetRequest) (*emptypb.Empty, error) {
	if r == nil {
		return nil, rpcError(biz.ErrInvalid)
	}
	if _, e := biz.RequirePlatform(ctx); e != nil {
		return nil, rpcError(e)
	}
	if e := s.presets.DeletePreset(ctx, r.Id); e != nil {
		return nil, rpcError(e)
	}
	return &emptypb.Empty{}, nil
}
