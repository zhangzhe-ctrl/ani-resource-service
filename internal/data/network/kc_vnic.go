package data

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"

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
func (p *KCProvider) syncVNicSecurityGroups(ctx context.Context, tenant, id, kind, consumer string, ids []string) (biz.VNic, error) {
	v, e := p.repository.GetVNic(ctx, tenant, id)
	if e != nil {
		return v, e
	}
	owned := false
	for _, b := range v.Bindings {
		if b.ConsumerKind == kind && b.ConsumerID == consumer {
			owned = true
		}
	}
	if !owned {
		return v, biz.Fail(biz.ResourceBusy, "VNIC reservation does not belong to consumer")
	}
	names := make([]string, 0, len(ids))
	// Reserve SG references before the provider mutation, retaining them on uncertain outcomes.
	tx, e := p.repository.pool.Begin(ctx)
	if e != nil {
		return v, databaseFailure(e)
	}
	defer tx.Rollback(ctx)
	q := p.repository.queries.WithTx(tx)
	targetPayload, _ := json.Marshal(biz.SecurityGroupBinding{TenantID: tenant, TargetType: "vnic", TargetID: id, VPCID: v.VPCID, Namespace: v.Namespace, State: "available", SecurityGroupIDs: ids, Version: v.Version})
	if e = q.UpsertSecurityGroupTarget(ctx, sqlcgen.UpsertSecurityGroupTargetParams{TenantID: tenant, TargetType: "vnic", TargetID: id, Version: v.Version, Payload: targetPayload}); e != nil {
		return v, databaseFailure(e)
	}
	for _, id := range ids {
		g, _, e := p.readMappedSecurityGroup(ctx, tenant, v.Namespace, id)
		if e != nil {
			return v, e
		}
		if g.State != biz.Available || g.ClusterID != v.ClusterID {
			return v, biz.Fail(biz.ProviderNotReady, "security group unavailable")
		}
		if _, e = q.LockSecurityGroupRow(ctx, sqlcgen.LockSecurityGroupRowParams{TenantID: tenant, SecurityGroupID: id}); e != nil {
			return v, databaseFailure(e)
		}
		if e = q.ReserveSecurityGroupReference(ctx, sqlcgen.ReserveSecurityGroupReferenceParams{TenantID: tenant, TargetType: "vnic", TargetID: v.ID, SecurityGroupID: id}); e != nil {
			return v, databaseFailure(e)
		}
		names = append(names, g.ProviderName)
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
	actual, _, _ := unstructured.NestedStringSlice(o.Object, "spec", "securityGroups")
	if !sameStrings(actual, names) {
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"uid": v.ProviderUID, "resourceVersion": o.GetResourceVersion()}, "spec": map[string]any{"securityGroups": names}})
		_, e = r.Patch(ctx, v.ProviderName, types.MergePatchType, patch, metav1.PatchOptions{})
		if e != nil {
			return v, mutationFailure(e)
		}
	}
	// Read only the affected old/new groups, including removals, rather than listing the tenant's whole SG inventory.
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
	applied, _, _ := unstructured.NestedMap(o.Object, "status", "appliedSecurityGroups")
	appliedNames := make([]string, 0, len(applied))
	for name := range applied {
		appliedNames = append(appliedNames, strings.TrimPrefix(name, v.Namespace+"/"))
	}
	if !sameStrings(appliedNames, names) {
		return v, &biz.ProviderError{Kind: biz.ProviderTemporary}
	}
	tx, e = p.repository.pool.Begin(ctx)
	if e != nil {
		return v, databaseFailure(e)
	}
	defer tx.Rollback(ctx)
	q = p.repository.queries.WithTx(tx)
	_, e = tx.Exec(ctx, `UPDATE network_vnics SET security_group_ids=$3 WHERE tenant_id=$1 AND vnic_id=$2`, tenant, id, ids)
	if e != nil {
		return v, databaseFailure(e)
	}
	if e = q.ReleaseSecurityGroupReferences(ctx, sqlcgen.ReleaseSecurityGroupReferencesParams{TenantID: tenant, TargetType: "vnic", TargetID: id, KeepIds: ids}); e != nil {
		return v, databaseFailure(e)
	}
	if e = tx.Commit(ctx); e != nil {
		return v, databaseFailure(e)
	}
	return p.repository.GetVNic(ctx, tenant, id)
}
