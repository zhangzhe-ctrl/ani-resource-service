package instancev1

import (
	"google.golang.org/protobuf/encoding/protojson"
	"testing"
)

func TestConfigurationRejectsMissingRequiredPath(t *testing.T) {
	spec := currentBusiness()
	spec.WorkspaceMountPath = ""
	if err := ValidateCreateSpec(spec, false); err == nil {
		t.Fatal("missing configuration path accepted")
	}
	if _, err := CommandDigest(&CommandContext{}, spec); err == nil {
		t.Fatal("missing configuration produced a command digest")
	}
	if _, err := CanonicalBusinessPayload(spec); err == nil {
		t.Fatal("incomplete configuration was projected")
	}
	raw, err := protojson.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeBusinessPayload(raw); err == nil {
		t.Fatal("incomplete frozen configuration was decoded")
	}
}
