package instancev1

import (
	"strings"
	"testing"
)

func TestRuntimeClassOnlyFromExecution(t *testing.T) {
	for _, name := range []string{"", "Nvidia", "nvidia/runtime", "-nvidia", "nvidia.", strings.Repeat("a", 64)} {
		if ValidateGPURuntimeClassName(name) == nil {
			t.Fatal("invalid RuntimeClass accepted", name)
		}
	}
	b := currentBusiness()
	b.GpuExecution = executionFixture()
	if ValidateCreateSpec(b, true) == nil {
		t.Fatal("CPU accepted GPU execution")
	}
}
