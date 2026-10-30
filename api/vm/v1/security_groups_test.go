package vmv1

import (
	"bytes"
	"testing"
)

func TestSecurityGroupsFrozenIntentDigestAndLegacyCompatibility(t *testing.T) {
	spec := fixedSpec(t)
	original, err := CanonicalCreateSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Intent.SecurityGroupIds = []string{"sg_11111111111111111111111111111111"}
	if _, err = CanonicalCreateSpec(spec); err == nil {
		t.Fatal("unfrozen security groups accepted")
	}
	spec.SecurityGroupIds = append([]string(nil), spec.Intent.SecurityGroupIds...)
	selected, err := CanonicalCreateSpec(spec)
	if err != nil || bytes.Equal(original, selected) {
		t.Fatal("group selection absent from digest", err)
	}
	decoded, err := DecodeCreateSpec(selected)
	if err != nil || len(decoded.SecurityGroupIds) != 1 {
		t.Fatal(decoded, err)
	}
	spec.Intent.SecurityGroupIds = nil
	spec.SecurityGroupIds = nil
	again, err := CanonicalCreateSpec(spec)
	if err != nil || !bytes.Equal(original, again) {
		t.Fatal("legacy digest changed", err)
	}
	command, err := CanonicalCommandIntent(&SetSecurityGroupsRequest{VmId: "id", ExpectedVersion: 2, SecurityGroupIds: []string{"sg_11111111111111111111111111111111"}})
	if err != nil || !bytes.Contains(command, []byte(`"security_group_ids"`)) {
		t.Fatal(string(command), err)
	}
}
