package instancev1

import (
	"bytes"
	"testing"
)

func TestSecurityGroupIntentPreservesEmptyAndBindsCompleteReplacement(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	ids := []string{"sg_11111111111111111111111111111111", "sg_22222222222222222222222222222222"}
	raw, err := CanonicalSecurityGroupIntent(id, 9007199254740993, ids)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := ParseCommandIntent("set-security-groups", id, raw)
	if err != nil || intent.ExpectedVersion != 9007199254740993 || len(intent.SecurityGroupIDs) != 2 {
		t.Fatal(intent, err)
	}
	reversed, err := CanonicalSecurityGroupIntent(id, 9007199254740993, []string{ids[1], ids[0]})
	if err != nil || bytes.Equal(raw, reversed) {
		t.Fatal("selection was not bound", err)
	}
	empty, err := CanonicalSecurityGroupIntent(id, 1, nil)
	if err != nil || !bytes.Contains(empty, []byte(`[]]`)) {
		t.Fatal(string(empty), err)
	}
	if _, err = CanonicalSecurityGroupIntent(id, 1, []string{ids[0], ids[0]}); err == nil {
		t.Fatal("duplicate groups accepted")
	}
}
func TestSecurityGroupsCreateIntentDigestAndLegacyBytes(t *testing.T) {
	d := &CreateInstanceData{Name: "example"}
	original, err := CanonicalBusinessPayload(d)
	if err != nil {
		t.Fatal(err)
	}
	d.SecurityGroupIds = []string{"sg_11111111111111111111111111111111"}
	if _, err = CanonicalBusinessPayload(d); err == nil {
		t.Fatal("group without managed network accepted")
	}
	d.Network = &NetworkSelection{VpcId: "vpc", SubnetId: "subnet"}
	selected, err := CanonicalBusinessPayload(d)
	if err != nil || !bytes.Contains(selected, []byte(`"security_group_ids"`)) {
		t.Fatal(string(selected), err)
	}
	d.SecurityGroupIds = nil
	d.Network = nil
	again, err := CanonicalBusinessPayload(d)
	if err != nil || !bytes.Equal(original, again) {
		t.Fatal("legacy digest changed", err)
	}
}
