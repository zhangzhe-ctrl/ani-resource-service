package data

import (
	"context"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
)

func retirementMarker(device string) string {
	return "network.ani.io/device-retirement-" + contentHash(device)[:16]
}
func (p *KCProvider) InspectLegacyDeviceRetirement(context.Context, biz.LegacyDeviceRetirementWork) (biz.LegacyDeviceRetirementObservation, error) {
	return biz.LegacyDeviceRetirementObservation{}, biz.Fail(biz.InvalidArgument, "standalone device registration has been removed")
}
func (p *KCProvider) ClearLegacyDeviceRetirement(context.Context, biz.LegacyDeviceRetirementWork) error {
	return biz.Fail(biz.InvalidArgument, "standalone device registration has been removed")
}
