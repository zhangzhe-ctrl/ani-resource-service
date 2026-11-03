package instance

import (
	"context"
	"encoding/json"
	"strconv"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	network "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func (n *InstanceNetwork) ReserveNetworkInterface(ctx context.Context, i biz.Instance, o biz.Operation, intent biz.NetworkInterfaceIntent) (biz.NetworkInterfaceChoice, error) {
	if err := intent.Validate(); err != nil {
		return biz.NetworkInterfaceChoice{}, err
	}
	if intent.Mode == "automatic" {
		return biz.NetworkInterfaceChoice{Mode: "automatic"}, nil
	}
	if i.Spec.Network == nil {
		return biz.NetworkInterfaceChoice{}, biz.ErrNetworkConflict
	}
	x, err := n.network.ReserveVNic(ctx, network.VNicReservation{TenantID: i.TenantID, VNicID: intent.VNicID, ConsumerKind: "container", ConsumerID: i.ID, Slot: intent.Slot, OperationID: o.ID, SubnetID: i.Spec.Network.SubnetID, Revision: o.Version})
	if err != nil {
		return biz.NetworkInterfaceChoice{}, instanceNetworkError(err)
	}
	if x.ClusterID != i.Spec.ClusterID || x.Namespace != i.Spec.Namespace || x.ProviderUID == "" {
		return biz.NetworkInterfaceChoice{}, biz.ErrNetworkConflict
	}
	return biz.NetworkInterfaceChoice{Mode: "manual", VNicID: x.ID, ProviderName: x.ProviderName, ProviderUID: x.ProviderUID, MACAddress: x.MACAddress, IPs: append([]string{}, x.IPs...)}, nil
}
func (n *InstanceNetwork) SyncNetworkInterfaces(ctx context.Context, i biz.Instance, ids []string) error {
	for _, id := range biz.PrimaryNetworkInterface(i).KeepVNicIDs() {
		if _, err := n.network.SyncVNicSecurityGroups(ctx, i.TenantID, id, "container", i.ID, ids); err != nil {
			return instanceNetworkError(err)
		}
	}
	return nil
}
func (n *InstanceNetwork) ReleaseNetworkInterfaces(ctx context.Context, i biz.Instance, keep []string) error {
	cfg := biz.PrimaryNetworkInterface(i)
	if !cfg.PendingRestart() && len(keep) > 0 {
		if err := n.network.ApplyVNicBinding(ctx, i.TenantID, "container", i.ID, cfg.Applied.VNicID, cfg.AppliedRevision); err != nil {
			return instanceNetworkError(err)
		}
	}
	return instanceNetworkError(n.network.ReleaseVNicBindings(ctx, i.TenantID, "container", i.ID, keep))
}

func (n *Notebook) SetNetworkInterface(ctx context.Context, i biz.Instance, c biz.NetworkInterfaceChoice, revision int64) error {
	obs, err := n.ObserveIdentity(ctx, i)
	if err != nil {
		return err
	}
	if !obs.SafeStopped || obs.PodExists {
		return biz.ErrNetworkUnavailable
	}
	root, err := n.lifecycleGet(ctx, notebookGVR, i.Spec.Namespace, i.Spec.NotebookName)
	if err != nil {
		return err
	}
	if !owned(root, i, obs.NotebookUID) || string(root.GetUID()) != i.NotebookUID || root.GetDeletionTimestamp() != nil {
		return biz.ErrOwnership
	}
	ann := root.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	value := ""
	if c.Mode == "manual" {
		if c.ProviderName == "" || c.ProviderUID == "" {
			return biz.ErrNetworkConflict
		}
		value = c.ProviderName
	}
	revisionText := strconv.FormatInt(revision, 10)
	if ann[biz.VNicAnnotation] != value || ann["network.ani.io/interface-revision"] != revisionText {
		delete(ann, biz.VNicAnnotation)
		if value != "" {
			ann[biz.VNicAnnotation] = value
		}
		ann["network.ani.io/interface-revision"] = revisionText
		raw, err := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": string(root.GetUID())}, {"op": "test", "path": "/metadata/resourceVersion", "value": root.GetResourceVersion()}, {"op": "add", "path": "/metadata/annotations", "value": ann}})
		if err != nil {
			return err
		}
		if _, err = n.client.Resource(notebookGVR).Namespace(i.Spec.Namespace).Patch(ctx, i.Spec.NotebookName, types.JSONPatchType, raw, metav1.PatchOptions{}); err != nil {
			return err
		}
		return biz.ErrNetworkUnavailable
	}
	// Notebook owns StatefulSet propagation. Do not start before that template
	// carries this exact revision, including removal when returning to automatic.
	set, err := n.lifecycleGet(ctx, stsGVR, i.Spec.Namespace, i.Spec.NotebookName)
	if err != nil {
		return err
	}
	if !controlledBy(set, "kubeflow.org", "Notebook", i.Spec.NotebookName, i.NotebookUID) || string(set.GetUID()) != obs.StatefulSetUID {
		return biz.ErrOwnership
	}
	template, _, err := unstructured.NestedStringMap(set.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return err
	}
	if template[biz.VNicAnnotation] != value || template["network.ani.io/interface-revision"] != revisionText {
		return biz.ErrNetworkUnavailable
	}
	after, err := n.ObserveIdentity(ctx, i)
	if err != nil {
		return err
	}
	if !after.SafeStopped || after.PodExists || after.NotebookUID != obs.NotebookUID || after.StatefulSetUID != obs.StatefulSetUID {
		return biz.ErrNetworkUnavailable
	}
	return nil
}
