package data

import (
	"context"

	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
)

// Administrators submit a prepared device name. Resource does not discover or
// approve physical interfaces on the host.
func (p *KCProvider) ListVirtualSwitchInterfaces(context.Context, string, biz.VirtualSwitch) (biz.InterfaceInventory, error) {
	return biz.InterfaceInventory{}, biz.Fail(biz.InvalidArgument, "interface discovery is unavailable; specify the prepared device name")
}
