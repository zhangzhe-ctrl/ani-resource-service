package biz

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestEgressAuthorizationRequiresTrustedContextAndExplicitDelegation(t *testing.T) {
	a := ContextEgressAuthorization{}
	tenant := "11111111-1111-4111-8111-111111111111"
	other := "22222222-2222-4222-8222-222222222222"
	if _, _, err := a.Tenant(context.Background(), tenant); ReasonOf(err) != PermissionDenied {
		t.Fatal(err)
	}
	ctx := WithEgressCaller(context.Background(), EgressCaller{TenantID: tenant, PlatformAdministrator: true})
	if got, _, err := a.Tenant(ctx, ""); err != nil || got != tenant {
		t.Fatalf("default tenant: %s %v", got, err)
	}
	if _, _, err := a.Tenant(ctx, other); ReasonOf(err) != PermissionDenied {
		t.Fatalf("platform admin is not tenant delegation: %v", err)
	}
	if _, err := a.Platform(ctx); err != nil {
		t.Fatal(err)
	}
	ctx = WithEgressCaller(context.Background(), EgressCaller{TenantID: tenant, DelegatedTenants: []string{other}})
	if got, _, err := a.Tenant(ctx, other); err != nil || got != other {
		t.Fatalf("delegation: %s %v", got, err)
	}
	if _, err := a.Platform(ctx); ReasonOf(err) != PermissionDenied {
		t.Fatal("delegation must not grant platform access")
	}
	if _, _, err := a.Tenant(ctx, "not-a-tenant"); ReasonOf(err) != TenantRequired {
		t.Fatal(err)
	}
}
func TestPublicPoolTopologyAndReservations(t *testing.T) {
	base := PublicPoolConfig{Mode: "overlay", GatewayID: "egw_" + strings.Repeat("1", 32), CIDR: "198.51.100.0/24", OVNGatewayIP: "198.51.100.1"}
	p, err := NormalizePublicPool(base)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.ExcludedIPs, []string{"198.51.100.1"}) {
		t.Fatal(p)
	}
	underlay := base
	underlay.Mode = "underlay"
	underlay.VlanNetworkID = "vlan_" + strings.Repeat("2", 32)
	underlay.UpstreamGatewayIP = "198.51.100.254"
	p, err = NormalizePublicPool(underlay)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.ExcludedIPs, []string{"198.51.100.1", "198.51.100.254"}) {
		t.Fatal(p)
	}
	for name, mutate := range map[string]func(*PublicPoolConfig){
		"overlay_has_vlan":             func(v *PublicPoolConfig) { v.VlanNetworkID = underlay.VlanNetworkID },
		"overlay_has_physical_gateway": func(v *PublicPoolConfig) { v.UpstreamGatewayIP = "198.51.100.254" },
		"missing_underlay":             func(v *PublicPoolConfig) { v.Mode = "underlay" },
		"unknown_mode":                 func(v *PublicPoolConfig) { v.Mode = "VLAN" },
		"noncanonical_cidr":            func(v *PublicPoolConfig) { v.CIDR = "198.51.100.1/24" },
		"ipv6":                         func(v *PublicPoolConfig) { v.CIDR = "2001:db8::/64" },
		"network_gateway":              func(v *PublicPoolConfig) { v.OVNGatewayIP = "198.51.100.0" },
		"broadcast_gateway":            func(v *PublicPoolConfig) { v.OVNGatewayIP = "198.51.100.255" },
		"reversed_range":               func(v *PublicPoolConfig) { v.ExcludedIPs = []string{"198.51.100.9..198.51.100.2"} },
		"out_of_pool":                  func(v *PublicPoolConfig) { v.ExcludedIPs = []string{"203.0.113.1"} },
	} {
		t.Run(name, func(t *testing.T) {
			v := base
			mutate(&v)
			if _, err := NormalizePublicPool(v); ReasonOf(err) != InvalidArgument {
				t.Fatal("accepted illegal topology", err)
			}
		})
	}
	underlay.UpstreamGatewayIP = underlay.OVNGatewayIP
	if _, err := NormalizePublicPool(underlay); err == nil {
		t.Fatal("same physical and OVN gateway accepted")
	}
}
func TestEgressFingerprintsPreserveIntentAndIgnoreInfrastructureRefresh(t *testing.T) {
	i := EgressIntent{TenantID: "tenant", Kind: "create_eip", Name: "eip", IdempotencyKey: "one"}
	h := i.Fingerprint()
	i.IdempotencyKey = "two"
	if h != i.Fingerprint() {
		t.Fatal("key in payload fingerprint")
	}
	i.Name = "different"
	if h == i.Fingerprint() {
		t.Fatal("different intent same fingerprint")
	}
	p := PlatformIntent{Kind: "create_egress_gateway", Name: "gateway", IdempotencyKey: "one"}
	h = p.Fingerprint()
	p.IdempotencyKey = "two"
	if h != p.Fingerprint() {
		t.Fatal("platform idempotency key leaked into fingerprint")
	}
	p.Name = "different"
	if h == p.Fingerprint() {
		t.Fatal("different platform intent shared a fingerprint")
	}
}

func TestPlatformFingerprintsRetainAcceptedV1AndSwitchV2Encoding(t *testing.T) {
	legacy := PlatformIntent{Kind: "create_egress_gateway", Name: "gateway", IdempotencyKey: "ignored"}
	if got := legacy.VersionedFingerprint(); got != "88fea9209b1b35455ac99adecf7d3e38ec6c4fb700d22dc7449549a820b3b12c" || legacy.FingerprintVersion() != 1 {
		t.Fatal("accepted v1 wire fingerprint changed", got)
	}
	legacyVlan := PlatformIntent{Kind: "create_vlan", Name: "legacy", Vlan: &VlanConfig{DeviceID: "device_" + strings.Repeat("b", 32)}}
	if got := legacyVlan.VersionedFingerprint(); got != "4f5300600d0edc706b5b85fdaa641b90053f9c0d797b9121c0a732f6e6f805b8" {
		t.Fatal("accepted legacy VLAN replay fingerprint changed", got)
	}
	legacyPool := PlatformIntent{Kind: "create_public_pool", Name: "pool", Pool: &PublicPoolConfig{Mode: "overlay", GatewayID: "egw_" + strings.Repeat("c", 32), CIDR: "192.0.2.0/24", OVNGatewayIP: "192.0.2.1"}}
	if got := legacyPool.VersionedFingerprint(); got != "ea8611db2d991c34d6a8fc24daf837967ac12e57ff4a298b4f604338e0e03c6d" {
		t.Fatal("accepted public pool replay fingerprint changed", got)
	}
	current := PlatformIntent{Kind: "create_vlan", Name: "prepared", IdempotencyKey: "ignored", Vlan: &VlanConfig{ClusterID: "test-cluster", VirtualSwitchID: "vsw_" + strings.Repeat("a", 32), DeviceName: "ens35", VlanID: 0}}
	if got := current.VersionedFingerprint(); got != "49db9268cea6fa0f6b030411e22e5a8f159dd5a84ee54dffd1b3239cf079b301" || current.FingerprintVersion() != 2 {
		t.Fatal("accepted v2 wire fingerprint changed", got)
	}
	before := current.VersionedFingerprint()
	current.Vlan.DeviceID = "device_server_generated"
	current.Vlan.Nodes = []VirtualSwitchNode{{NodeName: "node", NodeUID: "uid", Prepared: true}}
	if current.VersionedFingerprint() != before {
		t.Fatal("server-generated binding facts changed v2 replay identity")
	}
}

func TestCreateEIPLegacyReplayFingerprintWithoutSecurityGroups(t *testing.T) {
	intent := EgressIntent{TenantID: "tenant", Kind: "create_eip", Name: "eip", IdempotencyKey: "ignored"}
	if got := intent.Fingerprint(); got != "5e019ff9fdc1228f41e5f3f870c774c2f1ef4943db70b09d8370375cf45d5b4e" {
		t.Fatal("legacy create EIP fingerprint changed", got)
	}
	intent.SecurityGroupIDs = []string{}
	if got := intent.Fingerprint(); got != "5e019ff9fdc1228f41e5f3f870c774c2f1ef4943db70b09d8370375cf45d5b4e" {
		t.Fatal("empty initial SG list changed legacy replay", got)
	}
	intent.SecurityGroupIDs = []string{"sg_11111111111111111111111111111111"}
	if intent.Fingerprint() == "5e019ff9fdc1228f41e5f3f870c774c2f1ef4943db70b09d8370375cf45d5b4e" {
		t.Fatal("initial security group selection missing from replay identity")
	}
}
