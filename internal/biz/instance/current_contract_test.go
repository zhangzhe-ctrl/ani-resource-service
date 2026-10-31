package instance

import "testing"

func TestConfigurationRequiresWorkspacePath(t *testing.T) {
	if err := ValidateConfiguration(Spec{}); err == nil {
		t.Fatal("incomplete configuration accepted")
	}
	if err := ValidateConfiguration(Spec{WorkspaceMountPath: "/workspace"}); err != nil {
		t.Fatal("configuration defaults rejected", err)
	}
}
