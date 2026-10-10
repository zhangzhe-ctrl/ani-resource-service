package biz

import (
	"slices"
	"testing"
)

func TestLBListenerSetContract(t *testing.T) {
	port := func(v uint32) *uint32 { return &v }
	request := lbTestRequest()
	request.LoadBalancerMutableInput = LoadBalancerMutableInput{Name: "two", Listeners: []LoadBalancerListenerInput{
		{Name: "first", Port: port(80), Backends: []LoadBalancerBackendInput{{SubnetID: request.SubnetID, Address: "10.2.1.2", Port: 8080}}, Health: LoadBalancerHealthInput{Port: port(8080)}},
		{Name: "second", Port: port(8081), Backends: []LoadBalancerBackendInput{{SubnetID: request.SubnetID, Address: "10.2.1.3", Port: 9000}}, Health: LoadBalancerHealthInput{Port: port(9000)}},
	}}
	l, repo, ctx := lbTestSetup(t)
	if _, err := l.Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	first := repo.accepted[0]
	if len(first.Listeners) != 2 || first.Listeners[0].Health.Port != 8080 || first.Listeners[1].Health.Port != 9000 {
		t.Fatal("listener health/backend scope was flattened", first)
	}
	slices.Reverse(request.Listeners)
	if _, err := l.Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != repo.accepted[1].Fingerprint() {
		t.Fatal("listener order changed set fingerprint")
	}
	for name, change := range map[string]func(*CreateLoadBalancer){
		"empty":                func(r *CreateLoadBalancer) { r.Listeners = []LoadBalancerListenerInput{} },
		"duplicate_name":       func(r *CreateLoadBalancer) { r.Listeners[1].Name = r.Listeners[0].Name },
		"duplicate_port":       func(r *CreateLoadBalancer) { r.Listeners[1].Port = r.Listeners[0].Port },
		"unsupported_protocol": func(r *CreateLoadBalancer) { r.Listeners[0].Protocol = "TCP" },
		"mixed_legacy":         func(r *CreateLoadBalancer) { r.Backends = lbTestRequest().Backends },
		"too_many":             func(r *CreateLoadBalancer) { r.Listeners = make([]LoadBalancerListenerInput, 65) },
	} {
		t.Run(name, func(t *testing.T) {
			r := request
			r.Listeners = slices.Clone(request.Listeners)
			change(&r)
			before := len(repo.accepted)
			if _, err := l.Create(ctx, r); ReasonOf(err) != InvalidArgument {
				t.Fatal("invalid collection was admitted", err)
			}
			if len(repo.accepted) != before {
				t.Fatal("invalid collection reached persistence")
			}
		})
	}
}

func TestLBLegacyFingerprintContract(t *testing.T) {
	l, repo, ctx := lbTestSetup(t)
	if _, err := l.Create(ctx, lbTestRequest()); err != nil {
		t.Fatal(err)
	}
	i := repo.accepted[0]
	// Original pre-collection field order and types, frozen for historic replay.
	legacy := struct {
		TenantID, ID, Kind, IdempotencyKey                                           string
		ExpectedVersion                                                              int64
		Name, Description, VPCID, SubnetID, Exposure, Flavor, PublicEIPID, PrivateIP string
		ListenerPort                                                                 uint32
		Backends                                                                     []LoadBalancerBackend
		Health                                                                       LoadBalancerHealth
	}{i.TenantID, i.ID, i.Kind, "", i.ExpectedVersion, i.Name, i.Description, i.VPCID, i.SubnetID, i.Exposure, i.Flavor, i.PublicEIPID, i.PrivateIP, i.ListenerPort, i.Backends, i.Health}
	if i.Fingerprint() != egressFingerprint(legacy) {
		t.Fatal("legacy acceptance fingerprint changed")
	}
}
