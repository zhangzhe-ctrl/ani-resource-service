package sandboxv1

import (
	"strings"
	"testing"
)

func TestEgressIntentVersionAndCanonicalHosts(t *testing.T) {
	old := &CreateSandboxData{Name: "demo", ImageReference: "reg.example/a@sha256:" + strings.Repeat("a", 64), PresetId: "33333333-3333-4333-8333-333333333333", WorkspaceSize: "5Gi"}
	legacy, err := CreateIntentDigest("", old)
	if err != nil || legacy != "e67cbb7b00fcdab555d2441c867715a1c1972e64a89ea4ed7ae6e15676aeb142" {
		t.Fatalf("legacy %s %v", legacy, err)
	}
	old.EgressIntent = &SandboxEgressIntent{NetworkEgressPolicy: "allowlist", EgressAllowlist: []string{"B.Example.", "*.a.example"}}
	a, err := CreateIntentDigest("", old)
	if err != nil || a == legacy {
		t.Fatalf("a %s %v", a, err)
	}
	old.EgressIntent.EgressAllowlist = []string{"*.a.example", "b.example"}
	b, err := CreateIntentDigest("", old)
	if err != nil || a != b {
		t.Fatalf("canonical %s %s %v", a, b, err)
	}
	encoded, err := EncodeEgressIntent(old.EgressIntent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeEgressIntent(encoded); err != nil {
		t.Fatal(err)
	}
	old.EgressIntent.EgressAllowlist = []string{}
	empty, err := CreateIntentDigest("", old)
	if err != nil || empty == a || empty == legacy {
		t.Fatalf("empty semantics %s %v", empty, err)
	}
	old.EgressIntent.EgressAllowlist = []string{"EXAMPLE.COM", "example.com."}
	if _, err = CreateIntentDigest("", old); err == nil {
		t.Fatal("duplicate accepted")
	}
	old.EgressIntent.EgressAllowlist = []string{"https://example.com"}
	if _, err = CreateIntentDigest("", old); err == nil {
		t.Fatal("URL accepted")
	}
	old.EgressIntent = &SandboxEgressIntent{NetworkEgressPolicy: "internet", EgressAllowlist: []string{"x.test"}}
	if _, err = CreateIntentDigest("", old); err == nil {
		t.Fatal("inapplicable host accepted")
	}
	if _, err = DecodeEgressIntent(`{"network_egress_policy":"allowlist","egress_allowlist":["X.TEST"]}`); err == nil {
		t.Fatal("noncanonical snapshot accepted")
	}
}
