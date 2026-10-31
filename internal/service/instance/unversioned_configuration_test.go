package instance

import (
	"context"
	"strings"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	compute "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/compute"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
)

func TestResolveConfigurationWithoutVersion(t *testing.T) {
	const tenant = "11111111-1111-4111-8111-111111111111"
	const preset = "22222222-2222-4222-8222-222222222222"
	for _, explicit := range []bool{false, true} {
		name := "defaults"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			repo := &cpuIntake{preset: compute.Preset{ID: preset, Code: "small", Name: "small", ComputeType: biz.TypeCPU, CPUMillis: 1000, MemoryMiB: 2048, Enabled: true}}
			s, err := NewService(repo, newTestComputeResolver(t, repo, tenant), &cpuImageBoundary{}, &cpuEnvironmentBoundary{}, Config{NewIntentsEnabled: true, NamespacePrefix: "tenant-", ClusterID: tenant, ServiceAccount: "workload", StorageClass: "ani-block", WorkspaceSize: "1Gi", Shell: "/bin/bash", UID: 1000, GID: 1000})
			if err != nil {
				t.Fatal(err)
			}
			input := &pb.CreateInstanceData{Name: "work", ImageId: "img_" + strings.Repeat("a", 32), ImageScope: "platform", PresetId: preset}
			wantPath := "/workspace"
			if explicit {
				wantPath = "/work"
				input.WorkspaceMountPath = &wantPath
				input.Labels = map[string]string{"team": "ml"}
				input.Env = []*pb.InstanceEnvVar{{Name: "MODE", Value: "  literal  "}}
			}
			ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: tenant, Actor: "governance:user:42"})
			resolved, err := s.ResolveCreateSpec(testContainerScope(ctx, tenant), &pb.ResolveCreateSpecRequest{TenantId: tenant, Data: input})
			if err != nil {
				t.Fatal("version-free configuration resolution failed", err)
			}
			if resolved.WorkspaceMountPath != wantPath || (explicit && (resolved.Labels["team"] != "ml" || len(resolved.Env) != 1 || resolved.Env[0].Value != "  literal  ")) {
				t.Fatal("configuration lost during resolution", resolved)
			}
		})
	}
}
