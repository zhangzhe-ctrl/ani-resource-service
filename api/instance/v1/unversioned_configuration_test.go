package instancev1

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestConfigurationContractHasNoVersionSelector(t *testing.T) {
	for _, message := range []proto.Message{&ResolveCreateSpecRequest{}, &CreateSpec{}, &InstanceConfiguration{}} {
		if message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name("instance_contract_version")) != nil {
			t.Errorf("%s exposes a retired configuration version", message.ProtoReflect().Descriptor().FullName())
		}
	}
}

func TestConfigurationCanonicalIsUnversionedAndRejectsUnknownFields(t *testing.T) {
	spec := currentBusiness()
	raw, err := CanonicalBusinessPayload(spec)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["instance_contract_version"]; exists {
		t.Error("canonical business still includes a configuration version")
	}
	restored, err := DecodeBusinessPayload(raw)
	if err != nil || !proto.Equal(spec, restored) {
		t.Fatal("current configuration did not round trip", err)
	}
	for _, name := range []string{"instance_contract_version", "unknown_configuration_field"} {
		fields[name] = json.RawMessage(`2`)
		invalid, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodeBusinessPayload(invalid); err == nil {
			t.Errorf("unknown configuration field %s accepted", name)
		}
		delete(fields, name)
	}
}
