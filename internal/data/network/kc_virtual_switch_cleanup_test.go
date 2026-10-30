package data

import (
	"testing"

	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSwitchDeletionWaitsForKCNReleaseWithoutHostProbe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		clean bool
	}{
		{"clean_without_facts", true}, {"empty_acknowledgement", true}, {"unrelated_group", true}, {"daemon_feedback", false}, {"provider_vlan", false}, {"owned_label", false}, {"owned_marker", false}, {"replacement_node", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw, nodes, _, _, _, now := switchFixture(t)
			member := biz.VirtualSwitchNode{NodeName: "n1", NodeUID: "uid1", Priority: 100, Allocated: true}
			var vlans []unstructured.Unstructured
			switch tc.name {
			case "daemon_feedback":
				nodes[0].SetAnnotations(map[string]string{virtualSwitchGroupPrefix + sw.GroupName: "ens35"})
			case "empty_acknowledgement":
				nodes[0].SetAnnotations(map[string]string{virtualSwitchGroupPrefix + sw.GroupName: ""})
			case "unrelated_group":
				nodes[0].SetAnnotations(map[string]string{virtualSwitchGroupPrefix + "another": "ens35"})
			case "provider_vlan":
				vlans = []unstructured.Unstructured{{Object: map[string]any{"spec": map[string]any{"vClusterName": sw.GroupName, "devName": "ens35"}}}}
			case "owned_label":
				nodes[0].SetLabels(map[string]string{virtualSwitchGroupPrefix + sw.GroupName: "100"})
			case "owned_marker":
				nodes[0].SetAnnotations(map[string]string{VirtualSwitchOwnerKey(sw.GroupName): "binding1"})
			case "replacement_node":
				nodes[0].SetUID("replacement")
			}
			clean, err := switchNodeCleanupConfirmed(sw, member, &nodes[0], nil, vlans, nil, now)
			if clean != tc.clean || (tc.clean && err != nil) {
				t.Fatal(clean, err)
			}
		})
	}
}
