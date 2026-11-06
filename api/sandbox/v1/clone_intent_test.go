package sandboxv1

import "testing"

func TestCloneDigestsBindSourceVersionAndFrozenCommand(t *testing.T) {
	data := &CreateSandboxData{Name: "clone", PresetId: "preset", ImageReference: "image"}
	intent, err := CloneIntentDigest("source", "checkpoint", 1, data)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := CloneIntentDigest("source", "checkpoint", 2, data)
	if err != nil || intent == changed {
		t.Fatal("source version not bound")
	}
	changed, err = CloneIntentDigest("other", "checkpoint", 1, data)
	if err != nil || intent == changed {
		t.Fatal("source identity not bound")
	}
	c := &CommandContext{TenantId: "tenant", ResourceId: "target", OperationId: "operation"}
	spec := &ResolveCreateSpecResponse{Name: "clone"}
	first, err := CloneCommandDigest(c, "source", "checkpoint", 1, spec, intent)
	if err != nil {
		t.Fatal(err)
	}
	c.RequestDigest = "self-reference ignored"
	again, err := CloneCommandDigest(c, "source", "checkpoint", 1, spec, intent)
	if err != nil || first != again {
		t.Fatal("digest changed with its own value")
	}
	other, err := CloneCommandDigest(c, "source", "checkpoint", 2, spec, intent)
	if err != nil || first == other {
		t.Fatal("source version not bound in command")
	}
	c.ResourceId = "other"
	other, err = CloneCommandDigest(c, "source", "checkpoint", 1, spec, intent)
	if err != nil || first == other {
		t.Fatal("target not bound in command")
	}
}
