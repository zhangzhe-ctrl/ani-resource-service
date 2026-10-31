package instance

import (
	"encoding/json"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"testing"
)

func TestCurrentConfigurationPreservesCleanupCompletion(t *testing.T) {
	_, current, _, _ := diagnosticFixture(t, false)
	for _, completedAt := range []string{"0001-01-01T00:00:00Z", "2026-10-27T07:00:00Z"} {
		t.Run(completedAt, func(t *testing.T) {
			raw, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]json.RawMessage
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			snapshot["CleanupCompletedAt"], _ = json.Marshal(completedAt)
			raw, err = json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var restored biz.Instance
			if err := decodeSnapshot(raw, &restored); err != nil {
				t.Fatalf("decode current shared-worker snapshot: %v", err)
			}
			raw, err = json.Marshal(restored)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := json.Unmarshal(snapshot["CleanupCompletedAt"], &got); err != nil || got != completedAt {
				t.Fatalf("cleanup completion lost: got %q, want %q; error %v", got, completedAt, err)
			}
			snapshot["UnrecognizedRuntimeField"] = json.RawMessage(`true`)
			raw, err = json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err := decodeSnapshot(raw, &restored); err == nil {
				t.Fatal("unrecognized runtime field accepted")
			}
		})
	}
}

func TestConfigurationRejectsIncompleteSnapshotsAndNotebook(t *testing.T) {
	_, current, _, _ := diagnosticFixture(t, false)
	current.Spec.WorkspaceMountPath = ""
	if _, err := RenderNotebook(current); err == nil {
		t.Fatal("incomplete configuration rendered a Notebook")
	}
	for _, hash := range []string{current.CreateRequestHash, ""} {
		current.CreateRequestHash = hash
		raw, err := json.Marshal(current)
		if err != nil {
			t.Fatal(err)
		}
		var restored biz.Instance
		if err := decodeSnapshot(raw, &restored); err == nil {
			t.Fatal("incomplete configuration decoded from persisted snapshot")
		}
	}
}
