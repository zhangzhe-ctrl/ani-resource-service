package vm

import (
	"context"
	"encoding/json"
	"strconv"

	network "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func (n *Network) UseManualVNics(port *network.Network) error {
	if port == nil {
		return biz.ErrInvalid
	}
	n.vnics = port
	return nil
}
func (n *Network) ReserveNetworkInterface(ctx context.Context, v biz.VM, o biz.Operation, intent biz.NetworkInterfaceIntent) (biz.NetworkInterfaceChoice, error) {
	if err := intent.Validate(); err != nil {
		return biz.NetworkInterfaceChoice{}, err
	}
	if intent.Mode == "automatic" {
		return biz.NetworkInterfaceChoice{Mode: "automatic"}, nil
	}
	if n.vnics == nil {
		return biz.NetworkInterfaceChoice{}, biz.ErrDependency
	}
	x, err := n.vnics.ReserveVNic(ctx, network.VNicReservation{TenantID: v.TenantID, VNicID: intent.VNicID, ConsumerKind: "vm", ConsumerID: v.ID, Slot: intent.Slot, OperationID: o.ID, SubnetID: v.Spec.Network.SubnetID, Revision: o.IntentVersion})
	if err != nil {
		return biz.NetworkInterfaceChoice{}, err
	}
	if x.ClusterID != v.Spec.ClusterID || x.Namespace != v.Spec.Namespace || x.NamespaceUID != v.Spec.NamespaceUID || x.ProviderUID == "" {
		return biz.NetworkInterfaceChoice{}, biz.ErrChanged
	}
	return biz.NetworkInterfaceChoice{Mode: "manual", VNicID: x.ID, ProviderName: x.ProviderName, ProviderUID: x.ProviderUID, MACAddress: x.MACAddress, IPs: append([]string{}, x.IPs...)}, nil
}
func (n *Network) SyncNetworkInterfaces(ctx context.Context, v biz.VM, ids []string) error {
	for _, id := range biz.PrimaryNetworkInterface(v).KeepVNicIDs() {
		if n.vnics == nil {
			return biz.ErrDependency
		}
		if _, err := n.vnics.SyncVNicSecurityGroups(ctx, v.TenantID, id, "vm", v.ID, ids); err != nil {
			return err
		}
	}
	return nil
}
func (n *Network) ReleaseNetworkInterfaces(ctx context.Context, v biz.VM, keep []string) error {
	if n.vnics == nil {
		if len(v.NetworkInterfaces) > 0 {
			return biz.ErrDependency
		}
		return nil
	}
	cfg := biz.PrimaryNetworkInterface(v)
	if !cfg.PendingRestart() {
		if err := n.vnics.ApplyVNicBinding(ctx, v.TenantID, "vm", v.ID, cfg.Applied.VNicID, cfg.AppliedRevision); err != nil {
			return err
		}
	}
	return n.vnics.ReleaseVNicBindings(ctx, v.TenantID, "vm", v.ID, keep)
}

// SetNetworkInterface is a UID/RV-conditional template mutation while stopped.
// It never patches a VMI or launcher Pod.
func (n *Native) SetNetworkInterface(ctx context.Context, v biz.VM, c biz.NetworkInterfaceChoice, revision int64) error {
	before, err := n.observe(ctx, v, false)
	if err != nil {
		return err
	}
	if !before.Stopped || before.VMI != nil || before.Launcher != nil || len(before.StateChangeRequests) > 0 {
		return biz.ErrDependency
	}
	root, err := n.client.Resource(vmResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !matches(root, v) || string(root.GetUID()) != v.Native.UID || root.GetDeletionTimestamp() != nil {
		return biz.ErrOwnership
	}
	ann, _, err := unstructured.NestedStringMap(root.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return err
	}
	if ann == nil {
		ann = map[string]string{}
	}
	delete(ann, biz.VNicAnnotation)
	if c.Mode == "manual" {
		if c.ProviderName == "" || c.ProviderUID == "" {
			return biz.ErrChanged
		}
		ann[biz.VNicAnnotation] = c.ProviderName
	}
	ann["network.ani.io/interface-revision"] = strconv.FormatInt(revision, 10)
	raw, err := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": string(root.GetUID())}, {"op": "test", "path": "/metadata/resourceVersion", "value": root.GetResourceVersion()}, {"op": "add", "path": "/spec/template/metadata/annotations", "value": ann}})
	if err != nil {
		return err
	}
	_, err = n.client.Resource(vmResource).Namespace(v.Spec.Namespace).Patch(ctx, v.Native.Name, types.JSONPatchType, raw, metav1.PatchOptions{})
	return err
}
