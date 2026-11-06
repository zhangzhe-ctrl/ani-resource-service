package sandbox

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The existing lifecycle creates empty workspaces. A PVC sourced from a
// snapshot or a different claim must not be adopted as that workspace.
func TestOrdinaryWorkspaceRejectsUnfrozenDataSource(t *testing.T) {
	_, _, sandbox := providerFixture(t)
	name, _ := names(sandbox)
	for _, tc := range []struct {
		name   string
		field  string
		value  any
		accept bool
	}{
		{"absent", "", nil, true},
		{"explicit null", "dataSource", nil, true},
		{"snapshot", "dataSource", map[string]any{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "foreign"}, false},
		{"claim", "dataSource", map[string]any{"kind": "PersistentVolumeClaim", "name": "foreign"}, false},
		{"snapshot ref", "dataSourceRef", map[string]any{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "foreign"}, false},
		{"malformed", "dataSource", "foreign", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim := renderWorkspace(sandbox, name)
			if tc.field != "" {
				if err := unstructured.SetNestedField(claim.Object, tc.value, "spec", tc.field); err != nil {
					t.Fatal(err)
				}
			}
			if got := pvcMatches(claim, sandbox); got != tc.accept {
				t.Fatalf("pvcMatches=%v, want %v", got, tc.accept)
			}
		})
	}
}
