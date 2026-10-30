package biz

import "testing"

func TestVPCCIDRPresetConfiguration(t *testing.T) {
	for _, values := range [][]string{{"10.61.1.1/16"}, {"8.8.0.0/16"}, {"10.61.0.0/16", "10.61.0.0/16"}, {"bad"}} {
		if _, err := NewVPCCIDRPresets(values); ReasonOf(err) != InvalidArgument {
			t.Fatal("invalid startup policy was accepted", values, err)
		}
	}
	p, err := NewVPCCIDRPresets(nil)
	if err != nil || p.Allows("10.61.0.0/16") || len(p.Values()) != 0 {
		t.Fatal("empty startup policy is open", p, err)
	}
	values := []string{"10.61.0.0/16"}
	p, err = NewVPCCIDRPresets(values)
	if err != nil || !p.Allows(values[0]) || p.Allows("10.62.0.0/16") {
		t.Fatal("configured whitelist was not enforced", p, err)
	}
	values[0] = "10.62.0.0/16"
	returned := p.Values()
	returned[0] = "10.63.0.0/16"
	if !p.Allows("10.61.0.0/16") || p.Allows(values[0]) || p.Allows(returned[0]) {
		t.Fatal("caller changed the configured whitelist", p.Values())
	}
}
