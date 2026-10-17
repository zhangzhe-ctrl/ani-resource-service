package instancev1

import (
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"google.golang.org/protobuf/proto"
	"testing"
)

func executionFixture() *acc.GpuExecution {
	g := &acc.GpuExecution{SchedulerName: "volcano", QueueName: "default", RuntimeClassName: "nvidia", NodeSelector: map[string]string{"accelerator.ani.io/supply-group": "group", "accelerator.ani.io/model-key": "t4", "accelerator.ani.io/baseline-id": "config"}, PodAnnotations: map[string]string{"volcano.sh/vgpu-mode": "hami-core"}, Resources: map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "8192", "volcano.sh/vgpu-cores": "25"}}
	g.ExecutionDigest, _ = ExecutionDigest(g)
	return g
}
func TestExecutionAbsoluteMemoryAndWhitelist(t *testing.T) {
	g := executionFixture()
	if e := ValidateGpuExecution(g); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*acc.GpuExecution){"percentage": func(g *acc.GpuExecution) {
		delete(g.Resources, "volcano.sh/vgpu-memory")
		g.Resources["volcano.sh/vgpu-memory-percentage"] = "100"
	}, "resource injection": func(g *acc.GpuExecution) { g.Resources["cpu"] = "100" }, "unknown selector": func(g *acc.GpuExecution) { g.NodeSelector["unknown"] = "x" }, "missing runtime": func(g *acc.GpuExecution) { g.RuntimeClassName = "" }, "noncanonical integer": func(g *acc.GpuExecution) { g.Resources["volcano.sh/vgpu-memory"] = "08192" }} {
		t.Run(name, func(t *testing.T) {
			v := proto.Clone(g).(*acc.GpuExecution)
			change(v)
			v.ExecutionDigest, _ = ExecutionDigest(v)
			if ValidateGpuExecution(v) == nil {
				t.Fatal("unsafe execution accepted")
			}
		})
	}
	v := proto.Clone(g).(*acc.GpuExecution)
	v.Resources["volcano.sh/vgpu-memory"] = "1"
	if ValidateGpuExecution(v) == nil {
		t.Fatal("stale digest accepted")
	}
	v.ExecutionDigest, _ = ExecutionDigest(v)
	if e := ValidateGpuExecution(v); e != nil {
		t.Fatal("1MiB step rejected", e)
	}
}

func TestFixedExecutionCanonicalVector(t *testing.T) {
	// Independently generated on Fedora with sorted, compact UTF-8 JSON.
	g := executionFixture()
	if g.ExecutionDigest != "e320e19ba66a3c3da30b0649b37625ec322385853aa0cdfbace5f4807b62e679" {
		t.Fatal("wire digest vector changed", g.ExecutionDigest)
	}
}
