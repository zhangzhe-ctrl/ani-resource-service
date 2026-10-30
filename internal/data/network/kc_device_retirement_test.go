package data

import (
	"context"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"testing"
)

func TestStandaloneDeviceRetirementCannotMutateKCN(t *testing.T) {
	p := &KCProvider{}
	if _, err := p.InspectLegacyDeviceRetirement(context.Background(), biz.LegacyDeviceRetirementWork{}); biz.ReasonOf(err) != biz.InvalidArgument {
		t.Fatal("retired interface unexpectedly accepted", err)
	}
	if err := p.ClearLegacyDeviceRetirement(context.Background(), biz.LegacyDeviceRetirementWork{}); biz.ReasonOf(err) != biz.InvalidArgument {
		t.Fatal("retired interface unexpectedly mutated KCN", err)
	}
}
