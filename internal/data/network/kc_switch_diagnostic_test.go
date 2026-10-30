package data

import (
	"encoding/json"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
)

func TestSwitchLogicalDeviceIdentityIgnoresHistoricalPhysicalSnapshot(t *testing.T) {
	var frozen []biz.PreparedDeviceNode
	if err := json.Unmarshal([]byte(`[{"NodeName":"node-a","NodeUID":"uid-a","Name":"prepared0","MAC":"old-mac","Carrier":false,"Management":true}]`), &frozen); err != nil {
		t.Fatal(err)
	}
	current := []biz.PreparedDeviceNode{{NodeName: "node-a", NodeUID: "uid-a", Name: "prepared0"}}
	if !sameSwitchDeviceIdentity(frozen, current) {
		t.Fatal("historical host snapshot still gates prepared device intent")
	}
	for _, changed := range []biz.PreparedDeviceNode{
		{NodeName: "node-a", NodeUID: "uid-b", Name: "prepared0"},
		{NodeName: "node-b", NodeUID: "uid-a", Name: "prepared0"},
		{NodeName: "node-a", NodeUID: "uid-a", Name: "prepared1"},
	} {
		if sameSwitchDeviceIdentity(frozen, []biz.PreparedDeviceNode{changed}) {
			t.Fatal("logical device identity changed", changed)
		}
	}
}

func TestSwitchMembershipRequiresFrozenUIDsAndPermitsEmptyKCNFeedback(t *testing.T) {
	sw, nodes, _, _, _, _ := switchFixture(t)
	sw.Nodes = []biz.VirtualSwitchNode{{NodeName: "n1", NodeUID: "uid1", Priority: 100, Allocated: true}}
	nodes[0].SetAnnotations(map[string]string{virtualSwitchGroupPrefix + sw.GroupName: ""})
	if err := validateSwitchMembers(sw, nodes); err != nil {
		t.Fatal("empty acknowledgement blocked logical group reuse", err)
	}
	nodes[0].SetUID("replacement")
	if err := validateSwitchMembers(sw, nodes); err == nil {
		t.Fatal("same node name adopted a replacement UID")
	}
}
