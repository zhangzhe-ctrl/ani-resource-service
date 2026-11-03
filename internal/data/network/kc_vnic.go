package data

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network/sqlcgen"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var kcManualVNics = schema.GroupVersionResource{Group: "networking.kubercloud.com", Version: "v1", Resource: "vnics"}

func (p *KCProvider) inspectVNic(ctx context.Context, o *unstructured.Unstructured, b sqlcgen.NetworkProviderBinding, t biz.ProviderTarget) (biz.ProviderObservation, error) {
	l := o.GetLabels()
	if o.GetAPIVersion() != "networking.kubercloud.com/v1" || o.GetKind() != "VNic" || o.GetName() != b.ProviderName || o.GetNamespace() != b.Namespace || o.GetUID() == "" || o.GetResourceVersion() == "" || l[ownerLabel] != "ani-network-service" || l[tenantLabel] != t.TenantID || l[resourceLabel] != t.ResourceID || l[bindingLabel] != b.BindingID || len(o.GetOwnerReferences()) > 0 || (b.ProviderUid != "" && string(o.GetUID()) != b.ProviderUid) || (t.KnownIdentity != "" && string(o.GetUID()) != t.KnownIdentity) {
		return biz.ProviderObservation{}, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	row, e := p.repository.queries.GetVNicRow(ctx, sqlcgen.GetVNicRowParams{TenantID: t.TenantID, VnicID: t.ResourceID})
	if e != nil {
		return biz.ProviderObservation{}, readFailure(e)
	}
	sb, e := p.repository.queries.GetBinding(ctx, sqlcgen.GetBindingParams{TenantID: t.TenantID, ResourceID: row.SubnetID})
	if e != nil {
		return biz.ProviderObservation{}, readFailure(e)
	}
	subnet, _, _ := unstructured.NestedString(o.Object, "spec", "subnet")
	kind, _, _ := unstructured.NestedString(o.Object, "spec", "type")
	if o.GetAnnotations()["networking.kubercloud.com/allocate_ip_with_dhcp"] != "false" || kind != "VETH" || subnet != sb.Namespace+"/"+sb.ProviderName || sb.Namespace != b.Namespace || sb.ClusterID != b.ClusterID {
		return biz.ProviderObservation{}, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	mac, _, _ := unstructured.NestedString(o.Object, "spec", "macAddress")
	ip, _, _ := unstructured.NestedString(o.Object, "status", "primaryIP")
	ipMap, _, _ := unstructured.NestedStringMap(o.Object, "status", "ips")
	ips := make([]string, 0, len(ipMap))
	for _, addr := range ipMap {
		ips = append(ips, addr)
	}
	sort.Strings(ips)
	if ip != "" && !slices.Contains(ips, ip) {
		ips = append(ips, ip)
	}
	good := map[string]bool{}
	conditions, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	for _, r := range conditions {
		if c, ok := r.(map[string]any); ok {
			typ, _ := c["type"].(string)
			good[typ] = c["status"] == "True"
		}
	}
	return biz.ProviderObservation{Exists: true, Identity: string(o.GetUID()), Ready: o.GetDeletionTimestamp() == nil && mac != "" && ip != "" && good["Valid"] && good["Initialized"] && good["Ready"], VNic: &biz.VNicFacts{MACAddress: mac, PrimaryIP: ip, IPs: ips}}, nil
}
func (p *KCProvider) EnsureVNic(ctx context.Context, t biz.ProviderTarget) (biz.ProviderObservation, error) {
	b, e := p.binding(ctx, t)
	if e != nil {
		return biz.ProviderObservation{}, e
	}
	r := p.client.Resource(kcManualVNics).Namespace(b.Namespace)
	existing, e := r.Get(ctx, b.ProviderName, metav1.GetOptions{})
	if e == nil {
		return p.inspectVNic(ctx, existing, b, t)
	}
	if !apierrors.IsNotFound(e) {
		return biz.ProviderObservation{}, readFailure(e)
	}
	if b.ProviderUid != "" || t.KnownIdentity != "" {
		return biz.ProviderObservation{}, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	row, e := p.repository.queries.GetVNicRow(ctx, sqlcgen.GetVNicRowParams{TenantID: t.TenantID, VnicID: t.ResourceID})
	if e != nil {
		return biz.ProviderObservation{}, readFailure(e)
	}
	sb, e := p.repository.queries.GetBinding(ctx, sqlcgen.GetBindingParams{TenantID: t.TenantID, ResourceID: row.SubnetID})
	if e != nil {
		return biz.ProviderObservation{}, readFailure(e)
	}
	if sb.Namespace != b.Namespace || sb.ClusterID != b.ClusterID || sb.ProviderUid == "" {
		return biz.ProviderObservation{}, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	sub, e := p.client.Resource(kcSubnets).Namespace(sb.Namespace).Get(ctx, sb.ProviderName, metav1.GetOptions{})
	if e != nil {
		return biz.ProviderObservation{}, readFailure(e)
	}
	if string(sub.GetUID()) != sb.ProviderUid || sub.GetDeletionTimestamp() != nil {
		return biz.ProviderObservation{}, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	o := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "VNic", "metadata": map[string]any{"name": b.ProviderName, "namespace": b.Namespace, "labels": map[string]any{ownerLabel: "ani-network-service", tenantLabel: t.TenantID, resourceLabel: t.ResourceID, bindingLabel: b.BindingID, "network.ani.io/vnic-id": t.ResourceID}, "annotations": map[string]any{"networking.kubercloud.com/allocate_ip_with_dhcp": "false"}}, "spec": map[string]any{"type": "VETH", "subnet": sb.Namespace + "/" + sb.ProviderName}}}
	o, e = r.Create(ctx, o, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(e) {
		o, e = r.Get(ctx, b.ProviderName, metav1.GetOptions{})
	}
	if e != nil {
		return biz.ProviderObservation{}, mutationFailure(e)
	}
	return p.inspectVNic(ctx, o, b, t)
}
func (p *KCProvider) deleteVNic(ctx context.Context, t biz.ProviderTarget) error {
	b, e := p.binding(ctx, t)
	if e != nil {
		return e
	}
	if t.KnownIdentity == "" {
		return &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	r := p.client.Resource(kcManualVNics).Namespace(b.Namespace)
	o, e := r.Get(ctx, b.ProviderName, metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		return nil
	}
	if e != nil {
		return readFailure(e)
	}
	if _, e = p.inspectVNic(ctx, o, b, t); e != nil {
		return e
	}
	uid, rv := types.UID(t.KnownIdentity), o.GetResourceVersion()
	e = r.Delete(ctx, b.ProviderName, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
	if apierrors.IsNotFound(e) {
		return nil
	}
	if e != nil {
		return mutationFailure(e)
	}
	return nil
}

const vnicConsumerAnnotation = "network.ani.io/vnic-consumer"
const vnicSGVersionAnnotation = "network.ani.io/vnic-security-groups-version"

// Lock only Network's own selection and reservation. The transaction is
// committed before any provider I/O; no owner-domain SQL or provider lock spans.
func (p *KCProvider) lockVNicSGSelection(ctx context.Context, tx pgx.Tx, v biz.VNic, kind, consumer string, ids []string, initialize bool) (biz.SecurityGroupBinding, error) {
	q := p.repository.queries.WithTx(tx)
	if e := sgTenantLock(ctx, q, v.TenantID); e != nil {
		return biz.SecurityGroupBinding{}, e
	}
	if e := q.LockSecurityGroupRequest(ctx, sqlcgen.LockSecurityGroupRequestParams{TenantID: v.TenantID, Action: kind, RequestKey: consumer}); e != nil {
		return biz.SecurityGroupBinding{}, databaseFailure(e)
	}
	row, e := q.LockSecurityGroupTarget(ctx, sqlcgen.LockSecurityGroupTargetParams{TenantID: v.TenantID, TargetType: kind, TargetID: consumer})
	selection := biz.SecurityGroupBinding{}
	if errors.Is(e, pgx.ErrNoRows) && initialize && len(ids) == 0 {
		selection = biz.SecurityGroupBinding{TenantID: v.TenantID, TargetType: kind, TargetID: consumer, VPCID: v.VPCID, Namespace: v.Namespace, Version: 1, State: "configured", SecurityGroupIDs: []string{}}
		if e = sgSaveTarget(ctx, q, selection); e != nil {
			return selection, e
		}
	} else if e != nil {
		return selection, databaseFailure(e)
	} else {
		selection, e = sgTarget(row)
		if e != nil {
			return selection, e
		}
	}
	if selection.State == "released" || selection.VPCID != v.VPCID || selection.Namespace != v.Namespace || !sameStrings(selection.SecurityGroupIDs, ids) {
		return selection, biz.Fail(biz.VersionConflict, "consumer security group selection changed")
	}
	resource, e := q.LockVNicRow(ctx, sqlcgen.LockVNicRowParams{TenantID: v.TenantID, VnicID: v.ID})
	if e != nil {
		return selection, databaseFailure(e)
	}
	binding, e := q.GetBinding(ctx, sqlcgen.GetBindingParams{TenantID: v.TenantID, ResourceID: v.ID})
	if e != nil {
		return selection, databaseFailure(e)
	}
	if resource.State == "deleting" || resource.State == "deleted" || binding.ProviderUid != v.ProviderUID || binding.ProviderName != v.ProviderName || binding.Namespace != v.Namespace {
		return selection, biz.Fail(biz.ResourceBusy, "manual VNIC identity is no longer available")
	}
	var active bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM network_vnic_bindings WHERE tenant_id=$1 AND vnic_id=$2 AND consumer_kind=$3 AND consumer_id=$4 AND released_at IS NULL)`, v.TenantID, v.ID, kind, consumer).Scan(&active)
	if e != nil {
		return selection, databaseFailure(e)
	}
	if !active {
		return selection, biz.Fail(biz.ResourceBusy, "VNIC reservation does not belong to consumer")
	}
	return selection, nil
}

func (p *KCProvider) syncVNicSecurityGroups(ctx context.Context, tenant, id, kind, consumer string, ids []string) (biz.VNic, error) {
	v, e := p.repository.GetVNic(ctx, tenant, id)
	if e != nil {
		return v, e
	}
	tx, e := p.repository.pool.Begin(ctx)
	if e != nil {
		return v, databaseFailure(e)
	}
	defer tx.Rollback(ctx)
	selection, e := p.lockVNicSGSelection(ctx, tx, v, kind, consumer, ids, true)
	if e != nil {
		return v, e
	}
	q := p.repository.queries.WithTx(tx)
	names, e := resolveSecurityGroups(ctx, q, tenant, v.ClusterID, v.Namespace, ids)
	if e != nil {
		return v, e
	}
	target := biz.SecurityGroupBinding{TenantID: tenant, TargetType: "vnic", TargetID: id, VPCID: v.VPCID, Namespace: v.Namespace, State: "applying", SecurityGroupIDs: slices.Clone(ids), AppliedSecurityGroupIDs: slices.Clone(v.SecurityGroupIDs), Version: selection.Version}
	if e = sgSaveTarget(ctx, q, target); e != nil {
		return v, e
	}
	for _, gid := range ids {
		if e = q.ReserveSecurityGroupReference(ctx, sqlcgen.ReserveSecurityGroupReferenceParams{TenantID: tenant, TargetType: "vnic", TargetID: id, SecurityGroupID: gid}); e != nil {
			return v, databaseFailure(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return v, databaseFailure(e)
	}
	r := p.client.Resource(kcManualVNics).Namespace(v.Namespace)
	o, e := r.Get(ctx, v.ProviderName, metav1.GetOptions{})
	if e != nil {
		return v, readFailure(e)
	}
	if string(o.GetUID()) != v.ProviderUID || o.GetDeletionTimestamp() != nil {
		return v, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	consumerKey := kind + ":" + consumer
	version := strconv.FormatInt(selection.Version, 10)
	annotations := o.GetAnnotations()
	if annotations[vnicConsumerAnnotation] == consumerKey && annotations[vnicSGVersionAnnotation] != "" {
		previous, parseErr := strconv.ParseInt(annotations[vnicSGVersionAnnotation], 10, 64)
		if parseErr != nil || previous > selection.Version {
			return v, biz.Fail(biz.VersionConflict, "VNIC already declares a newer consumer security group selection")
		}
	}
	// A stalled worker must still own the same Network selection and reservation
	// immediately before its conditional provider request.
	check, e := p.repository.pool.Begin(ctx)
	if e != nil {
		return v, databaseFailure(e)
	}
	defer check.Rollback(ctx)
	current, e := p.lockVNicSGSelection(ctx, check, v, kind, consumer, ids, false)
	if e != nil {
		return v, e
	}
	if current.Version != selection.Version {
		return v, biz.Fail(biz.VersionConflict, "consumer security group version changed")
	}
	if e = check.Commit(ctx); e != nil {
		return v, databaseFailure(e)
	}
	actual, _, _ := unstructured.NestedStringSlice(o.Object, "spec", "securityGroups")
	if !sameStrings(actual, names) || annotations[vnicConsumerAnnotation] != consumerKey || annotations[vnicSGVersionAnnotation] != version {
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[vnicConsumerAnnotation] = consumerKey
		annotations[vnicSGVersionAnnotation] = version
		patch, _ := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": v.ProviderUID}, {"op": "test", "path": "/metadata/resourceVersion", "value": o.GetResourceVersion()}, {"op": "add", "path": "/metadata/annotations", "value": annotations}, {"op": "add", "path": "/spec/securityGroups", "value": names}})
		if _, e = r.Patch(ctx, v.ProviderName, types.JSONPatchType, patch, metav1.PatchOptions{}); e != nil {
			return v, mutationFailure(e)
		}
	}
	// KCN confirms application through each SG's appliedVNics membership.
	// Read only affected old/new groups, including removals; VNIC status is not an additional receipt.
	all := append(append([]string{}, v.SecurityGroupIDs...), ids...)
	for _, gid := range all {
		g, sg, e := p.readMappedSecurityGroup(ctx, tenant, v.Namespace, gid)
		if e != nil {
			return v, e
		}
		applied, _, _ := unstructured.NestedStringSlice(sg.Object, "status", "appliedVNics")
		if slices.Contains(applied, v.ProviderName) != slices.Contains(ids, gid) || slices.Contains(ids, gid) && (!securityGroupReady(sg) || g.State != biz.Available) {
			return v, &biz.ProviderError{Kind: biz.ProviderTemporary}
		}
	}
	o, e = r.Get(ctx, v.ProviderName, metav1.GetOptions{})
	if e != nil {
		return v, readFailure(e)
	}
	if string(o.GetUID()) != v.ProviderUID || o.GetAnnotations()[vnicConsumerAnnotation] != consumerKey || o.GetAnnotations()[vnicSGVersionAnnotation] != version {
		return v, biz.Fail(biz.VersionConflict, "VNIC consumer security group declaration changed")
	}
	tx, e = p.repository.pool.Begin(ctx)
	if e != nil {
		return v, databaseFailure(e)
	}
	defer tx.Rollback(ctx)
	current, e = p.lockVNicSGSelection(ctx, tx, v, kind, consumer, ids, false)
	if e != nil {
		return v, e
	}
	if current.Version != selection.Version {
		return v, biz.Fail(biz.VersionConflict, "consumer security group version changed before completion")
	}
	q = p.repository.queries.WithTx(tx)
	if _, e = tx.Exec(ctx, `UPDATE network_vnics SET security_group_ids=$3 WHERE tenant_id=$1 AND vnic_id=$2`, tenant, id, ids); e != nil {
		return v, databaseFailure(e)
	}
	target.State = "applied"
	target.AppliedSecurityGroupIDs = slices.Clone(ids)
	if e = sgSaveTarget(ctx, q, target); e != nil {
		return v, e
	}
	if e = q.ReleaseSecurityGroupReferences(ctx, sqlcgen.ReleaseSecurityGroupReferencesParams{TenantID: tenant, TargetType: "vnic", TargetID: id, KeepIds: ids}); e != nil {
		return v, databaseFailure(e)
	}
	if e = tx.Commit(ctx); e != nil {
		return v, databaseFailure(e)
	}
	return p.repository.GetVNic(ctx, tenant, id)
}
