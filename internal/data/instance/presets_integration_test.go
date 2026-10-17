//go:build instanceintegration

package instance_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	service "github.com/zhangzhe-ctrl/ani-resource-service/internal/service/instance"
)

func platformContext() context.Context {
	return biz.WithCaller(context.Background(), biz.Caller{Platform: true, Actor: "governance:user:42"})
}
func TestComputePresetTransactionsAndIsolation(t *testing.T) {
	f := database(t)
	ctx := platformContext()
	p, e := f.repo.CreatePreset(ctx, biz.Preset{Code: "cpu-4c", Name: "4 cores", ComputeType: biz.TypeCPU, CPUMillis: 4000, MemoryMiB: 8192})
	if e != nil {
		t.Fatal(e)
	}
	if !biz.UUID(p.ID) || p.Enabled || p.CreatedAt.IsZero() || p.CreatedBy != "governance:user:42" {
		t.Fatal("server managed fields", p)
	}
	if _, e = f.repo.CreatePreset(ctx, biz.Preset{Code: p.Code, Name: "duplicate", ComputeType: biz.TypeCPU, CPUMillis: 1000, MemoryMiB: 1024}); !errors.Is(e, biz.ErrPresetConflict) {
		t.Fatal("duplicate code", e)
	}
	tenantCtx := biz.WithCaller(context.Background(), biz.Caller{TenantID: uuid.NewString(), Actor: "governance:user:42"})
	if _, e = f.repo.CreatePreset(tenantCtx, p); !errors.Is(e, biz.ErrDenied) {
		t.Fatal("tenant wrote platform catalog", e)
	}
	g := &biz.GPUReference{ProfileID: uuid.NewString(), ProfileVersion: 1, SpecDigest: strings.Repeat("a", 64), DevicesPerInstance: 2, ClusterID: uuid.NewString(), PoolID: uuid.NewString(), Mode: "WHOLE_EXCLUSIVE", ModelKey: "A100", MemoryMiBPerDevice: 15360, CoreLimitPercent: 100}
	gpu, e := f.repo.UpdatePreset(ctx, p.ID, biz.Preset{ComputeType: biz.TypeGPU, GPU: g}, []string{"compute_type", "gpu"})
	if e != nil {
		t.Fatal(e)
	}
	t.Run("whole quantity SQL range", func(t *testing.T) {
		for _, n := range []uint32{4, math.MaxUint32} {
			if _, e := f.runtime.Exec(ctx, "UPDATE instance.compute_presets SET gpu=jsonb_set(gpu,'{devices_per_instance}',$2::jsonb) WHERE id=$1", gpu.ID, fmt.Sprint(n)); e != nil {
				t.Fatal("positive uint32 SQL quantity rejected", n, e)
			}
			row, e := f.repo.GetPreset(ctx, gpu.ID)
			if e != nil || row.GPU == nil || row.GPU.DevicesPerInstance != n {
				t.Fatal("uint32 quantity not preserved", n, row.GPU, e)
			}
		}
		for _, raw := range []string{"0", "-1", "1.5", "4.0", "4294967296", `"4"`, "null"} {
			if _, e := f.runtime.Exec(ctx, "UPDATE instance.compute_presets SET gpu=jsonb_set(gpu,'{devices_per_instance}',$2::jsonb) WHERE id=$1", gpu.ID, raw); e == nil {
				t.Fatal("non-positive/non-integer/out-of-range quantity accepted", raw)
			}
			row, e := f.repo.GetPreset(ctx, gpu.ID)
			if e != nil || row.GPU == nil || row.GPU.DevicesPerInstance != math.MaxUint32 {
				t.Fatal("rejected quantity mutated preset", raw, row.GPU, e)
			}
		}
	})
	if _, e = f.repo.UpdatePreset(ctx, p.ID, biz.Preset{ComputeType: biz.TypeCPU}, []string{"compute_type"}); !errors.Is(e, biz.ErrInvalid) {
		t.Fatal("invalid final row committed", e)
	}
	unchanged, e := f.repo.GetPreset(ctx, p.ID)
	if e != nil || unchanged.ComputeType != biz.TypeGPU || unchanged.GPU == nil {
		t.Fatal("rollback failed", unchanged, e)
	}
	cpu, e := f.repo.UpdatePreset(ctx, p.ID, biz.Preset{ComputeType: biz.TypeCPU, Name: "ignored"}, []string{"compute_type", "gpu"})
	if e != nil || cpu.GPU != nil || cpu.Name != p.Name {
		t.Fatal("whole gpu clear or mask preservation", cpu, e)
	}
	enabled, e := f.repo.SetPresetEnabled(ctx, p.ID, true)
	if e != nil || !enabled.Enabled || enabled.CPUMillis != 4000 {
		t.Fatal("set enabled overwritten resources", e)
	}
	disabled, e := f.repo.UpdatePreset(ctx, p.ID, biz.Preset{Enabled: false}, []string{"enabled"})
	if e != nil || disabled.Enabled || disabled.Name != p.Name {
		t.Fatal("false or unmasked field lost", e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, change := range []struct {
		v    biz.Preset
		mask []string
	}{{biz.Preset{Name: "concurrent"}, []string{"name"}}, {biz.Preset{CPUMillis: 7000}, []string{"cpu_millis"}}} {
		wg.Add(1)
		go func(v biz.Preset, mask []string) {
			defer wg.Done()
			_, e := f.repo.UpdatePreset(ctx, p.ID, v, mask)
			errs <- e
		}(change.v, change.mask)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	got, e := f.repo.GetPreset(ctx, p.ID)
	if e != nil || got.Name != "concurrent" || got.CPUMillis != 7000 {
		t.Fatal("concurrent disjoint edits lost", got, e)
	}
	if e = f.repo.DeletePreset(ctx, gpu.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.repo.GetPreset(ctx, p.ID); !errors.Is(e, biz.ErrNotFound) {
		t.Fatal("physical delete", e)
	}
}
func TestComputePresetAuthorizedPages(t *testing.T) {
	f := database(t)
	admin := platformContext()
	tenant := biz.WithCaller(context.Background(), biz.Caller{TenantID: uuid.NewString(), Actor: "governance:user:42"})
	var allowed []biz.GPUReference
	for n := 0; n < 9; n++ {
		p := biz.Preset{Code: fmt.Sprintf("preset-%d", n), Name: "literal%_Name", ComputeType: biz.TypeCPU, CPUMillis: 1000, MemoryMiB: 1024, Enabled: true}
		if n >= 5 {
			g := biz.GPUReference{ProfileID: uuid.NewString(), ProfileVersion: 2, SpecDigest: strings.Repeat("b", 64), DevicesPerInstance: 1, ClusterID: uuid.NewString(), PoolID: uuid.NewString(), Mode: "WHOLE_EXCLUSIVE", ModelKey: "A100", MemoryMiBPerDevice: 15360, CoreLimitPercent: 100}
			p.ComputeType = biz.TypeGPU
			p.GPU = &g
			if n%2 == 0 {
				allowed = append(allowed, g)
			}
		}
		if _, e := f.repo.CreatePreset(admin, p); e != nil {
			t.Fatal(e)
		}
	}
	first, total, has, e := f.repo.ListPresets(tenant, biz.PresetFilter{Tenant: true, Page: 3, PageSize: 2, Name: "%_"})
	if e != nil || len(first) != 1 || total != 5 || !has {
		t.Fatal("safe CPU page", len(first), total, has, e)
	}
	final, total, has, e := f.repo.ListPresets(tenant, biz.PresetFilter{Tenant: true, Page: 3, PageSize: 2, AllowedGPUProfiles: allowed})
	if e != nil || len(final) != 2 || total != 7 || !has {
		t.Fatal("authorized GPU intersection before page/count", len(final), total, has, e)
	}
	empty, total, _, e := f.repo.ListPresets(tenant, biz.PresetFilter{Tenant: true, Page: 100, PageSize: 2, AllowedGPUProfiles: allowed})
	if e != nil || empty == nil || len(empty) != 0 || total != 7 {
		t.Fatal("empty page total", empty, total, e)
	}
	_, total, _, e = f.repo.ListPresets(tenant, biz.PresetFilter{Tenant: true, Name: "wildcard not present"})
	if e != nil || total != 0 {
		t.Fatal("literal name filtering", total, e)
	}
	forged := append([]biz.GPUReference(nil), allowed...)
	forged[0].PoolID = uuid.NewString()
	_, total, _, e = f.repo.ListPresets(tenant, biz.PresetFilter{Tenant: true, ComputeType: biz.TypeGPU, AllowedGPUProfiles: forged})
	if e != nil || total != 1 {
		t.Fatal("pool mismatch exposed GPU", total, e)
	}
}
func TestDatabaseCPUFreezeSurvivesPresetMutationAndDeletion(t *testing.T) {
	f := database(t)
	admin := platformContext()
	tenantID := uuid.NewString()
	ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: tenantID, Actor: "governance:user:42"})
	p, e := f.repo.CreatePreset(admin, biz.Preset{Code: "cpu-freeze", Name: "Frozen CPU", ComputeType: biz.TypeCPU, CPUMillis: 1250, MemoryMiB: 1536, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	s, e := service.NewService(f.repo, fixtureImage{}, fixtureEnvironment{}, service.Config{NamespacePrefix: "tenant-", ClusterID: uuid.NewString(), ServiceAccount: "workload", StorageClass: "ani-block", WorkspaceSize: "1Gi", Shell: "/bin/sh", UID: 1000, GID: 1000})
	if e != nil {
		t.Fatal(e)
	}
	raw := &pb.CreateInstanceData{Name: "literal%_Workspace", ImageId: "img_" + strings.Repeat("a", 32), ImageScope: "platform", PresetId: p.ID}
	priorityBefore, priorityAfter := "before-priority", "after-priority"
	frozen, e := s.ResolveCreateSpec(ctx, &pb.ResolveCreateSpecRequest{TenantId: tenantID, Data: raw, SchedulingDefaults: &pb.SchedulingOptions{PodPlacement: pb.SchedulingOptions_SPREAD_PREFERRED.Enum(), PriorityClassName: &priorityBefore}})
	if e != nil {
		t.Fatal(e)
	}
	if frozen.Cpu != "1250m" || frozen.Memory != "1536Mi" || frozen.Scheduling.GetPodPlacement() != pb.SchedulingOptions_SPREAD_PREFERRED || frozen.Scheduling.GetPriorityClassName() != priorityBefore || frozen.GpuExecution != nil {
		t.Fatal("incorrect CPU freeze", frozen)
	}
	request := &pb.AcceptCreateRequest{Context: &pb.CommandContext{TenantId: tenantID, OperationId: uuid.NewString(), ResourceId: uuid.NewString()}, Spec: frozen}
	request.Context.RequestDigest, e = pb.CommandDigest(request.Context, request.Spec)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptCreate(ctx, request); e != nil {
		t.Fatal(e)
	}
	if _, e = f.repo.UpdatePreset(admin, p.ID, biz.Preset{CPUMillis: 3000, Name: "new CPU"}, []string{"cpu_millis", "name"}); e != nil {
		t.Fatal(e)
	}
	next, e := s.ResolveCreateSpec(ctx, &pb.ResolveCreateSpecRequest{TenantId: tenantID, Data: raw, SchedulingDefaults: &pb.SchedulingOptions{PodPlacement: pb.SchedulingOptions_COLOCATE_PREFERRED.Enum(), PriorityClassName: &priorityAfter}})
	if e != nil || next.Cpu != "3000m" || next.Scheduling.GetPodPlacement() != pb.SchedulingOptions_COLOCATE_PREFERRED || next.Scheduling.GetPriorityClassName() != priorityAfter {
		t.Fatal("new resolution ignored update", next, e)
	}
	if e = f.repo.DeletePreset(admin, p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptCreate(ctx, request); e != nil {
		t.Fatal("accepted replay queried deleted preset", e)
	}
	got, e := s.GetInstance(ctx, &pb.GetInstanceRequest{TenantId: tenantID, InstanceId: request.Context.ResourceId})
	if e != nil || got.Cpu != "1250m" || got.PresetName != "Frozen CPU" || got.CreatedAt == nil || got.Scheduling.GetPodPlacement() != pb.SchedulingOptions_SPREAD_PREFERRED || got.Scheduling.GetPriorityClassName() != priorityBefore {
		t.Fatal("static snapshot drifted", got, e)
	}
	page, e := s.ListInstances(ctx, &pb.ListInstancesRequest{TenantId: tenantID, Page: 1, PageSize: 20, InstanceKind: "container", ComputeType: "cpu", Name: "%_"})
	if e != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal("instance filters/count", page, e)
	}
	foreign := biz.WithCaller(context.Background(), biz.Caller{TenantID: uuid.NewString(), Actor: "governance:user:42"})
	if _, e = s.GetInstance(foreign, &pb.GetInstanceRequest{TenantId: tenantID, InstanceId: got.Id}); e == nil {
		t.Fatal("cross tenant query accepted")
	}
	if _, e = s.ResolveCreateSpec(ctx, &pb.ResolveCreateSpecRequest{TenantId: tenantID, Data: raw}); e == nil {
		t.Fatal("deleted preset allowed new resolution")
	}
}
