package biz

import "testing"

func TestSecurityGroupRulesDefaultIngressAndValidation(t *testing.T) {
	valid := SecurityGroupRule{Direction: "ingress", Protocol: "tcp", Action: "allow", Priority: 3000, RemoteAddresses: []string{"192.0.2.0/24"}, PortRanges: []SecurityGroupPortRange{{Min: 443, Max: 443}}}
	if err := ValidateSecurityGroupRules([]SecurityGroupRule{valid, DefaultSecurityGroupRule()}); err != nil {
		t.Fatal(err)
	}
	otherPort := valid
	otherPort.PortRanges = []SecurityGroupPortRange{{Min: 80, Max: 80}}
	if err := ValidateSecurityGroupRules([]SecurityGroupRule{valid, otherPort}); err != nil {
		t.Fatal("same-priority allow rules must compose", err)
	}
	otherPort.Action = "drop"
	if ValidateSecurityGroupRules([]SecurityGroupRule{valid, otherPort}) == nil {
		t.Fatal("opposite actions at the same priority were accepted")
	}
	cases := []struct {
		name   string
		mutate func(*SecurityGroupRule)
	}{
		{"priority-reserved", func(r *SecurityGroupRule) { r.Priority = 1 }},
		{"priority-over-limit", func(r *SecurityGroupRule) { r.Priority = 4001 }},
		{"ipv6", func(r *SecurityGroupRule) { r.RemoteAddresses = []string{"::/0"} }},
		{"host-bits", func(r *SecurityGroupRule) { r.RemoteAddresses = []string{"192.0.2.1/24"} }},
		{"duplicate-cidr", func(r *SecurityGroupRule) { r.RemoteAddresses = []string{"192.0.2.0/24", "192.0.2.0/24"} }},
		{"overlapping-ports", func(r *SecurityGroupRule) {
			r.PortRanges = []SecurityGroupPortRange{{Min: 80, Max: 100}, {Min: 100, Max: 200}}
		}},
		{"ports-for-icmp", func(r *SecurityGroupRule) { r.Protocol = "icmp" }},
		{"invalid-action", func(r *SecurityGroupRule) { r.Action = "deny" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.mutate(&r)
			if ValidateSecurityGroupRules([]SecurityGroupRule{r}) == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
	def := DefaultSecurityGroupRule()
	def.Direction = "egress"
	if ValidateSecurityGroupRules([]SecurityGroupRule{def}) == nil {
		t.Fatal("outbound default drop accepted")
	}
}
