package biz

import (
	"testing"
	"time"
)

func TestVPCCIDRPresetConfigurationAndFreshness(t *testing.T) {
	for _, values := range [][]string{{"10.61.1.1/16"}, {"8.8.0.0/16"}, {"10.61.0.0/16", "10.61.0.0/16"}, {"bad"}} {
		if _, err := NewVPCCIDRPresets(values); ReasonOf(err) != InvalidArgument {
			t.Fatal("invalid startup policy was accepted", values, err)
		}
	}
	p, err := NewVPCCIDRPresets(nil)
	if err != nil || p.Allows("10.61.0.0/16") || len(p.Values()) != 0 {
		t.Fatal("empty startup policy is open", p, err)
	}
	now := time.Now()
	for _, at := range []time.Time{{}, now.Add(-2 * time.Minute), now.Add(time.Second)} {
		if err := ValidateVPCPlatformCIDRs("10.61.0.0/16", []string{"10.96.0.0/12"}, at, now); ReasonOf(err) != DependencyUnavailable {
			t.Fatal("unknown/expired/future facts were accepted", err)
		}
	}
	if err := ValidateVPCPlatformCIDRs("10.96.1.0/24", []string{"10.96.0.0/12"}, now, now); ReasonOf(err) != ResourceInUse {
		t.Fatal("full Service CIDR conflict was missed", err)
	}
}
