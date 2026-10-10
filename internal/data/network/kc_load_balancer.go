package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network/sqlcgen"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var lbGateways = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
var lbRoutes = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
var lbBackends = schema.GroupVersionResource{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Resource: "backends"}
var lbPolicies = schema.GroupVersionResource{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Resource: "backendtrafficpolicies"}
var lbDeployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
var lbReplicaSets = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
var lbEndpointSlices = schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}
var lbGatewayClasses = schema.GroupVersionResource{Group: lbGateways.Group, Version: "v1", Resource: "gatewayclasses"}
var lbEnvoyProxies = schema.GroupVersionResource{Group: lbBackends.Group, Version: "v1alpha1", Resource: "envoyproxies"}

const lbVersionAnnotation = "network.ani.io/configuration-version"
const lbController = "gateway.envoyproxy.io/gatewayclass-controller"

func lbGVR(kind string) schema.GroupVersionResource {
	switch kind {
	case "gateway":
		return lbGateways
	case "route":
		return lbRoutes
	case "policy":
		return lbPolicies
	case "backend":
		return lbBackends
	}
	return schema.GroupVersionResource{}
}
func lbKind(kind string) string {
	switch kind {
	case "gateway":
		return "Gateway"
	case "route":
		return "HTTPRoute"
	case "policy":
		return "BackendTrafficPolicy"
	case "backend":
		return "Backend"
	}
	return ""
}

type lbResolved struct {
	work                      biz.Work
	binding, vpc, subnet, eip sqlcgen.NetworkProviderBinding
	objects                   map[schema.GroupVersionResource][]unstructured.Unstructured
	dependencies              bool
}

func (p *KCProvider) resolveLB(ctx context.Context, w biz.Work) (lbResolved, error) {
	r := lbResolved{work: w, objects: map[schema.GroupVersionResource][]unstructured.Unstructured{}}
	if w.Resource.LoadBalancer == nil {
		return r, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	if _, ok := biz.FindLoadBalancerFlavor(w.Resource.LoadBalancer.LoadBalancer.Flavor); !ok {
		return r, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	var err error
	r.binding, err = p.binding(ctx, biz.ProviderTarget{Kind: "load_balancer", TenantID: w.Resource.TenantID, ResourceID: w.Resource.ID, BindingID: w.BindingID, KnownIdentity: w.KnownIdentity})
	if err != nil {
		return r, err
	}
	for id, out := range map[string]*sqlcgen.NetworkProviderBinding{w.Resource.VPCID: &r.vpc, w.Resource.LoadBalancer.LoadBalancer.SubnetID: &r.subnet} {
		*out, err = p.repository.queries.GetBinding(ctx, sqlcgen.GetBindingParams{TenantID: w.Resource.TenantID, ResourceID: id})
		if err != nil {
			return r, readFailure(err)
		}
		if out.ClusterID != r.binding.ClusterID || out.Namespace != r.binding.Namespace || out.ProviderUid == "" {
			return r, &biz.ProviderError{Kind: biz.ProviderConflict}
		}
	}
	if id := w.Resource.LoadBalancer.LoadBalancer.PublicEIPID; id != "" {
		r.eip, err = p.repository.queries.GetBinding(ctx, sqlcgen.GetBindingParams{TenantID: w.Resource.TenantID, ResourceID: id})
		if err != nil {
			return r, readFailure(err)
		}
		if r.eip.Namespace != r.binding.Namespace || r.eip.ClusterID != r.binding.ClusterID || r.eip.ProviderUid == "" {
			return r, &biz.ProviderError{Kind: biz.ProviderConflict}
		}
	}
	now, err := p.repository.queries.DatabaseTime(ctx)
	if err != nil {
		return r, readFailure(err)
	}
	r.dependencies = p.repository.requireBaseConnectivity(ctx, p.repository.queries, w.Resource.TenantID, w.Resource.VPCID, now, p.repository.baseFreshnessLimit()) == nil
	cap, capErr := p.repository.queries.GetLBCapability(ctx, sqlcgen.GetLBCapabilityParams{ClusterID: r.binding.ClusterID})
	r.dependencies = r.dependencies && capErr == nil && cap.Ready && now.Sub(cap.ObservedAt) <= p.repository.baseFreshnessLimit()
	return r, nil
}
func lbComponentName(w biz.Work, kind string) string {
	for _, c := range w.Resource.LoadBalancer.Components {
		if c.Kind == kind {
			return c.Name
		}
	}
	return ""
}
func lbMemberEligible(o biz.LoadBalancerObservation, id string) bool {
	for _, m := range o.Members {
		if m.ID == id {
			return m.Eligible
		}
	}
	return false
}
func lbDesiredObject(r lbResolved, c biz.LoadBalancerComponent, o biz.LoadBalancerObservation) *unstructured.Unstructured {
	l := r.work.Resource.LoadBalancer.LoadBalancer
	spec := map[string]any{}
	version := l.DesiredVersion
	switch c.Kind {
	case "gateway":
		flavor, ok := biz.FindLoadBalancerFlavor(l.Flavor)
		if !ok {
			return nil
		}
		class := flavor.GatewayClass(l.Exposure)
		vip := l.PrivateIP
		if vip == "" {
			vip = "disable"
		}
		a := map[string]any{"networking.kubercloud.com/lb_vpc": r.vpc.Namespace + "/" + r.vpc.ProviderName, "networking.kubercloud.com/subnet": r.subnet.Namespace + "/" + r.subnet.ProviderName, "networking.kubercloud.com/lb_vip_address": vip}
		if l.PublicEIPID != "" {
			a["networking.kubercloud.com/lb_eips"] = r.eip.ProviderName
		}
		listeners := []any{}
		for _, listener := range biz.EffectiveLoadBalancerListeners(l) {
			listeners = append(listeners, map[string]any{"name": listener.Name, "protocol": "HTTP", "port": int64(listener.Port), "allowedRoutes": map[string]any{"namespaces": map[string]any{"from": "Same"}}})
		}
		spec = map[string]any{"gatewayClassName": class, "infrastructure": map[string]any{"annotations": a}, "listeners": listeners}
	case "backend":
		version = 1
		for _, m := range r.work.Resource.LoadBalancer.Members {
			if m.ID == c.MemberID {
				spec = map[string]any{"type": "Endpoints", "endpoints": []any{map[string]any{"ip": map[string]any{"address": m.Address, "port": int64(m.Port)}}}}
			}
		}
	case "route":
		listener := lbComponentListener(r.work, c)
		refs := []any{}
		for _, m := range listener.Backends {
			if lbMemberEligible(o, m.ID) {
				for _, backend := range r.work.Resource.LoadBalancer.Components {
					if backend.Kind == "backend" && backend.MemberID == m.ID {
						refs = append(refs, map[string]any{"group": lbBackends.Group, "kind": "Backend", "name": backend.Name, "port": int64(m.Port), "weight": int64(m.Weight)})
					}
				}
			}
		}
		rule := map[string]any{"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}}}
		// No legitimate targets produces the Gateway API's no-backend response.
		// It never falls back to the old IP or a default service.
		if len(refs) > 0 {
			rule["backendRefs"] = refs
		}
		spec = map[string]any{"parentRefs": []any{map[string]any{"group": lbGateways.Group, "kind": "Gateway", "name": r.binding.ProviderName, "sectionName": listener.Name}}, "rules": []any{rule}}
	case "policy":
		h := lbComponentListener(r.work, c).Health
		spec = map[string]any{"targetRefs": []any{map[string]any{"group": lbRoutes.Group, "kind": "HTTPRoute", "name": lbListenerRouteName(r.work, c.ListenerID)}}, "loadBalancer": map[string]any{"type": "RoundRobin"}, "healthCheck": map[string]any{"panicThreshold": int64(0), "active": map[string]any{"type": "TCP", "interval": fmt.Sprintf("%ds", h.IntervalSeconds), "timeout": fmt.Sprintf("%ds", h.TimeoutSeconds), "unhealthyThreshold": int64(h.UnhealthyThreshold), "healthyThreshold": int64(h.HealthyThreshold), "tcp": map[string]any{}}}}
		if h.Port != 0 {
			spec["healthCheck"].(map[string]any)["active"].(map[string]any)["overrides"] = map[string]any{"port": int64(h.Port)}
		}
	}
	gvr := lbGVR(c.Kind)
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": gvr.Group + "/" + gvr.Version, "kind": lbKind(c.Kind), "metadata": map[string]any{"name": c.Name, "namespace": r.binding.Namespace, "labels": map[string]any{ownerLabel: "ani-network-service", tenantLabel: r.work.Resource.TenantID, resourceLabel: r.work.Resource.ID, bindingLabel: c.ID}, "annotations": map[string]any{lbVersionAnnotation: strconv.FormatInt(version, 10)}}, "spec": spec}}
}
func lbConditions(raw []any, generation int64, kinds ...string) bool {
	if generation <= 0 {
		return false
	}
	good := map[string]bool{}
	for _, item := range raw {
		c, ok := item.(map[string]any)
		if !ok {
			continue
		}
		g, _, _ := unstructured.NestedInt64(c, "observedGeneration")
		kind, _ := c["type"].(string)
		good[kind] = c["status"] == "True" && g == generation
	}
	for _, kind := range kinds {
		if !good[kind] {
			return false
		}
	}
	return true
}
func lbObjectReady(obj *unstructured.Unstructured, c biz.LoadBalancerComponent, gateway, namespace string) bool {
	if obj.GetDeletionTimestamp() != nil {
		return false
	}
	if c.Kind == "gateway" || c.Kind == "backend" {
		conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if c.Kind == "backend" {
			return lbConditions(conditions, obj.GetGeneration(), "Accepted")
		}
		listeners, _, _ := unstructured.NestedSlice(obj.Object, "status", "listeners")
		wanted, _, _ := unstructured.NestedSlice(obj.Object, "spec", "listeners")
		if len(listeners) != len(wanted) || len(wanted) == 0 {
			return false
		}
		names := map[string]bool{}
		for _, raw := range wanted {
			l, _ := raw.(map[string]any)
			name, _ := l["name"].(string)
			names[name] = true
		}
		for _, raw := range listeners {
			l, _ := raw.(map[string]any)
			name, _ := l["name"].(string)
			lc, _, _ := unstructured.NestedSlice(l, "conditions")
			if !names[name] || !lbConditions(lc, obj.GetGeneration(), "Accepted", "Programmed", "ResolvedRefs") {
				return false
			}
			delete(names, name)
		}
		return len(names) == 0 && lbConditions(conditions, obj.GetGeneration(), "Accepted", "Programmed")
	}
	field, refField := "parents", "parentRef"
	if c.Kind == "policy" {
		field, refField = "ancestors", "ancestorRef"
	}
	entries, _, _ := unstructured.NestedSlice(obj.Object, "status", field)
	for _, raw := range entries {
		e, ok := raw.(map[string]any)
		if !ok || e["controllerName"] != lbController {
			continue
		}
		ref, _, _ := unstructured.NestedMap(e, refField)
		ns, _ := ref["namespace"].(string)
		if ns == "" {
			ns = namespace
		}
		if ref["name"] != gateway || ns != namespace || ref["kind"] != "Gateway" || ref["group"] != lbGateways.Group {
			continue
		}
		if c.Kind == "route" && c.ListenerName != "" && ref["sectionName"] != c.ListenerName {
			continue
		}
		conditions, _, _ := unstructured.NestedSlice(e, "conditions")
		if c.Kind == "policy" {
			return lbConditions(conditions, obj.GetGeneration(), "Accepted")
		}
		return lbConditions(conditions, obj.GetGeneration(), "Accepted", "ResolvedRefs")
	}
	return false
}
func lbInspect(obj, desired *unstructured.Unstructured, c biz.LoadBalancerComponent, r lbResolved) (biz.LoadBalancerComponentObservation, error) {
	v := biz.LoadBalancerComponentObservation{ID: c.ID}
	if obj == nil {
		return v, nil
	}
	l := obj.GetLabels()
	if obj.GetName() != c.Name || obj.GetNamespace() != r.binding.Namespace || obj.GetUID() == "" || obj.GetResourceVersion() == "" || obj.GetAPIVersion() != desired.GetAPIVersion() || obj.GetKind() != desired.GetKind() || l[ownerLabel] != "ani-network-service" || l[tenantLabel] != r.work.Resource.TenantID || l[resourceLabel] != r.work.Resource.ID || l[bindingLabel] != c.ID || (c.Identity != "" && c.Identity != string(obj.GetUID())) {
		return v, &biz.ProviderError{Kind: biz.ProviderConflict}
	}
	if c.Kind == "gateway" {
		actual, _, _ := unstructured.NestedMap(obj.Object, "spec")
		expected, _, _ := unstructured.NestedMap(desired.Object, "spec")
		delete(actual, "listeners")
		delete(expected, "listeners")
		if contentHash(actual) != contentHash(expected) {
			return v, &biz.ProviderError{Kind: biz.ProviderConflict}
		}
	}
	v.Exists, v.Identity, v.Deleting = true, string(obj.GetUID()), obj.GetDeletionTimestamp() != nil
	v.Matches = contentHash(obj.Object["spec"]) == contentHash(desired.Object["spec"]) && obj.GetAnnotations()[lbVersionAnnotation] == desired.GetAnnotations()[lbVersionAnnotation]
	v.Ready = v.Matches && lbObjectReady(obj, c, r.binding.ProviderName, r.binding.Namespace)
	return v, nil
}

var errLBAuditInvalid = errors.New("LB audit needs a fresh collection")

func (p *KCProvider) ObserveLoadBalancer(ctx context.Context, w biz.Work) (biz.LoadBalancerObservation, error) {
	refresh := false
	for {
		if err := ctx.Err(); err != nil {
			return biz.LoadBalancerObservation{}, readFailure(err)
		}
		o, err := p.observeLoadBalancerOnce(ctx, w, refresh)
		if err != errLBAuditInvalid {
			if ctx.Err() != nil {
				return biz.LoadBalancerObservation{}, readFailure(ctx.Err())
			}
			return o, err
		}
		// A Watch renewal invalidates the audit, not the Provider facts. Each
		// retry collects after a new database boundary within the original
		// deadline. Actual read errors and identity conflicts are not retried.
		// Unbounded callers retain only one fresh-collection fallback.
		if refresh {
			if _, bounded := ctx.Deadline(); !bounded {
				return biz.LoadBalancerObservation{}, readFailure(err)
			}
		}
		refresh = true
	}
}

func (p *KCProvider) observeLoadBalancerOnce(ctx context.Context, w biz.Work, refresh bool) (biz.LoadBalancerObservation, error) {
	o := biz.LoadBalancerObservation{}
	r, err := p.resolveLB(ctx, w)
	if err != nil {
		return o, err
	}
	now, err := p.repository.queries.DatabaseTime(ctx)
	if err != nil {
		return o, readFailure(err)
	}
	resources := []schema.GroupVersionResource{kcVPCs, kcSubnets, pods, observationGVRs[3], observationGVRs[4], kcEIPs, kcSnats, kcNats, kcServices, lbGateways, lbBackends, lbRoutes, lbPolicies, lbDeployments, lbReplicaSets, lbEndpointSlices}
	var view *auditView
	keys := []string{"load-balancer:" + r.binding.Namespace + "/" + w.Resource.ID, "uid:" + r.binding.ProviderUid, "lb-capability"}
	if p.observation != nil && p.loadBalancerInstallation != nil {
		after := time.Time{}
		if refresh || w.ActiveOperation || w.PendingAction != "" || w.Resource.State == biz.Deleted {
			after = now
		}
		view, err = p.observation.snapshot(ctx, after)
		if err != nil {
			return o, readFailure(err)
		}
		for key, targets := range view.targets {
			for _, target := range targets {
				if target.tenant == w.Resource.TenantID && target.id == w.Resource.ID && target.kind == "load_balancer" {
					keys = append(keys, key)
					break
				}
			}
		}
	}
	for _, gvr := range resources {
		if view != nil {
			index := view.indices[gvr]
			if index == nil {
				return o, readFailure(fmt.Errorf("LB audit source unavailable"))
			}
			for _, item := range index.List() {
				r.objects[gvr] = append(r.objects[gvr], *item.(*unstructured.Unstructured))
			}
		} else {
			items, err := p.listAll(ctx, gvr)
			if err != nil {
				return o, readFailure(err)
			}
			r.objects[gvr] = items
		}
	}
	o.Proof = biz.ObservationProof{CollectedAt: now, Hash: contentHash(candidatesForHash(r.objects)), CoveredGeneration: w.Requirement.RequestedGeneration}
	if view != nil {
		o.Proof.CollectedAt = view.collected
		o.Proof.CoveredGeneration = view.generations[observationTarget{w.Resource.TenantID, w.Resource.ID, "load_balancer"}.key()]
		if !o.Proof.Covers(w.Requirement) || !p.observation.valid(view, keys) {
			return biz.LoadBalancerObservation{}, errLBAuditInvalid
		}
	}
	o.DependenciesReady = r.dependencies
	for _, parent := range []sqlcgen.NetworkProviderBinding{r.vpc, r.subnet} {
		obj := lbFind(r.objects[kcResource(parent)], parent.Namespace, parent.ProviderName)
		if obj == nil || string(obj.GetUID()) != parent.ProviderUid || !goodConditions(obj, "Valid", "Initialized", "Ready") || crInt(obj, "status", "observedGeneration") != obj.GetGeneration() {
			o.DependenciesReady = false
		}
	}
	for _, m := range w.Resource.LoadBalancer.Members {
		desired := false
		for _, b := range w.Resource.LoadBalancer.LoadBalancer.Backends {
			if b.ID == m.ID {
				desired = true
			}
		}
		if !desired {
			continue
		}
		eligible := p.lbMemberIdentity(ctx, r, m)
		reason := biz.Reason("")
		if !eligible {
			reason = biz.BackendIdentityMismatch
		}
		o.Members = append(o.Members, biz.LoadBalancerMemberObservation{ID: m.ID, Eligible: eligible, Reason: reason})
	}
	for _, c := range w.Resource.LoadBalancer.Components {
		obj := lbFind(r.objects[lbGVR(c.Kind)], r.binding.Namespace, c.Name)
		v, err := lbInspect(obj, lbDesiredObject(r, c, o), c, r)
		if err != nil {
			// Preserve the other observations so the domain worker can first
			// withdraw an invalid backend from an independently owned Route.
			v = biz.LoadBalancerComponentObservation{ID: c.ID, Identity: c.Identity, Conflict: true}
		}
		o.Components = append(o.Components, v)
	}
	if err = p.lbGenerated(ctx, r, &o); err != nil {
		if providerErr, ok := err.(*biz.ProviderError); ok && providerErr.Kind == biz.ProviderConflict {
			o.GeneratedConflict = true
			o.DependenciesReady, o.GeneratedReady, o.GeneratedReleased, o.AddressReleased = false, false, false, false
		} else {
			return o, err
		}
	}
	if view != nil && !p.observation.valid(view, keys) {
		return biz.LoadBalancerObservation{}, errLBAuditInvalid
	}
	return o, nil
}
func lbFind(items []unstructured.Unstructured, namespace, name string) *unstructured.Unstructured {
	for i := range items {
		if items[i].GetNamespace() == namespace && items[i].GetName() == name {
			return &items[i]
		}
	}
	return nil
}
func lbOwnedBy(o *unstructured.Unstructured, kind, name, uid string) bool {
	if o == nil || uid == "" {
		return false
	}
	for _, ref := range o.GetOwnerReferences() {
		if ref.Kind == kind && ref.Name == name && string(ref.UID) == uid && ref.APIVersion == lbOwnerAPIVersion(kind) {
			return true
		}
	}
	return false
}
func lbOwnerAPIVersion(kind string) string {
	switch kind {
	case "Gateway":
		return "gateway.networking.k8s.io/v1"
	case "Pod", "Service":
		return "v1"
	case "Deployment", "ReplicaSet":
		return "apps/v1"
	case "VNic":
		return "networking.kubercloud.com/v1"
	}
	return ""
}
func (p *KCProvider) lbMemberIdentity(ctx context.Context, r lbResolved, m biz.LoadBalancerMemberIdentity) bool {
	a, err := p.repository.queries.GetAttachment(ctx, sqlcgen.GetAttachmentParams{TenantID: r.work.Resource.TenantID, AttachmentID: m.AttachmentID})
	if err != nil {
		return false
	}
	// Attachment observation can advance while this LB waits for its audit.
	// Read database time after the row, rather than treating a renewal after
	// work claim as a future timestamp. Stale and truly future facts still fail.
	now, err := p.repository.queries.DatabaseTime(ctx)
	if err != nil || a.State != "attached" || a.ProtocolBlocked || a.Reason != "" || a.PodUid != m.PodUID || a.SubnetID != m.SubnetID || a.VpcID != r.work.Resource.VPCID || a.Namespace != r.binding.Namespace || a.ClusterID != r.binding.ClusterID || !freshTime(a.ObservedAt, now, p.repository.baseFreshnessLimit()) {
		return false
	}
	parent, err := p.repository.queries.GetBinding(ctx, sqlcgen.GetBindingParams{TenantID: a.TenantID, ResourceID: m.SubnetID})
	if err != nil || parent.ClusterID != r.binding.ClusterID || parent.Namespace != r.binding.Namespace || parent.ProviderUid == "" {
		return false
	}
	subnet := lbFind(r.objects[kcSubnets], a.Namespace, parent.ProviderName)
	if subnet == nil || string(subnet.GetUID()) != parent.ProviderUid || subnet.GetDeletionTimestamp() != nil {
		return false
	}
	ref := parent.Namespace + "/" + parent.ProviderName
	pod := lbFind(r.objects[pods], a.Namespace, m.PodName)
	nic := lbFind(r.objects[observationGVRs[3]], a.Namespace, m.VNicName)
	ip := lbFind(r.objects[observationGVRs[4]], a.Namespace, m.VNicIPName)
	if pod == nil || nic == nil || ip == nil || string(pod.GetUID()) != m.PodUID || string(nic.GetUID()) != m.VNicUID || string(ip.GetUID()) != m.VNicIPUID || pod.GetDeletionTimestamp() != nil || nic.GetDeletionTimestamp() != nil || ip.GetDeletionTimestamp() != nil {
		return false
	}
	return pod.GetLabels()[attachmentLabel] == a.AttachmentID && crString(pod, "status", "podIP") == m.Address && lbOwnedBy(nic, "Pod", m.PodName, m.PodUID) && lbOwnedBy(ip, "VNic", m.VNicName, m.VNicUID) && kcRef(crString(nic, "spec", "subnet"), a.Namespace) == ref && kcRef(crString(ip, "spec", "subnet"), a.Namespace) == ref && kcRef(crString(ip, "spec", "vNic"), a.Namespace) == a.Namespace+"/"+m.VNicName && kcRef(crString(ip, "status", "vNic"), a.Namespace) == a.Namespace+"/"+m.VNicName && crString(ip, "spec", "ipAddress") == m.Address
}

func (p *KCProvider) MutateLoadBalancer(ctx context.Context, w biz.Work, c biz.LoadBalancerComponent, action string, observed biz.LoadBalancerObservation) (biz.LoadBalancerComponentObservation, error) {
	v := biz.LoadBalancerComponentObservation{ID: c.ID}
	r, err := p.resolveLB(ctx, w)
	if err != nil {
		return v, err
	}
	desired := lbDesiredObject(r, c, observed)
	endpoint := p.client.Resource(lbGVR(c.Kind)).Namespace(r.binding.Namespace)
	obj, err := endpoint.Get(ctx, c.Name, metav1.GetOptions{})
	if action == "create" {
		if err == nil {
			return lbInspect(obj, desired, c, r)
		}
		if !apierrors.IsNotFound(err) {
			return v, readFailure(err)
		}
		if c.Identity != "" {
			return v, &biz.ProviderError{Kind: biz.ProviderConflict}
		}
		obj, err = endpoint.Create(ctx, desired, metav1.CreateOptions{FieldManager: "ani-network-service", FieldValidation: "Strict"})
		if apierrors.IsAlreadyExists(err) {
			obj, err = endpoint.Get(ctx, c.Name, metav1.GetOptions{})
			if err != nil {
				return v, &biz.ProviderError{Kind: biz.ProviderUncertain, Cause: err}
			}
		}
	} else {
		if apierrors.IsNotFound(err) && action == "delete" {
			return v, nil
		}
		if err != nil {
			return v, readFailure(err)
		}
		v, err = lbInspect(obj, desired, c, r)
		if err != nil {
			return v, err
		}
		if v.Identity == "" {
			return v, &biz.ProviderError{Kind: biz.ProviderConflict}
		}
		if action == "delete" {
			uid := obj.GetUID()
			rv := obj.GetResourceVersion()
			foreground := metav1.DeletePropagationForeground
			err = endpoint.Delete(ctx, c.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &foreground})
			if apierrors.IsNotFound(err) {
				err = nil
			}
			if err != nil {
				return v, mutationFailure(err)
			}
			v.Deleting = true
			return v, nil
		}
		if action != "update" || (c.Kind != "route" && c.Kind != "policy" && c.Kind != "gateway") {
			return v, &biz.ProviderError{Kind: biz.ProviderConflict}
		}
		annotations := obj.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[lbVersionAnnotation] = desired.GetAnnotations()[lbVersionAnnotation]
		patch, _ := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": string(obj.GetUID())}, {"op": "test", "path": "/metadata/resourceVersion", "value": obj.GetResourceVersion()}, {"op": "replace", "path": "/spec", "value": desired.Object["spec"]}, {"op": "add", "path": "/metadata/annotations", "value": annotations}})
		obj, err = endpoint.Patch(ctx, c.Name, types.JSONPatchType, patch, metav1.PatchOptions{FieldManager: "ani-network-service", FieldValidation: "Strict"})
	}
	if err != nil {
		return v, mutationFailure(err)
	}
	v, err = lbInspect(obj, desired, c, r)
	if err != nil {
		return v, &biz.ProviderError{Kind: biz.ProviderUncertain, Cause: err}
	}
	return v, nil
}

// kc publishes address ranges from its own IPAM. Counts or absence of the
// Service alone are insufficient to prove a particular VIP was released.
func lbIPInRange(address, ranges string) (bool, bool) {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return false, false
	}
	if ranges == "" {
		return false, true
	}
	contains := false
	for _, part := range strings.Split(ranges, ",") {
		lo, hi, rangeValue := strings.Cut(strings.TrimSpace(part), "-")
		if !rangeValue {
			hi = lo
		}
		a, ea := netip.ParseAddr(lo)
		b, eb := netip.ParseAddr(hi)
		if ea != nil || eb != nil || !a.Is4() || !b.Is4() || a.Compare(b) > 0 {
			return false, false
		}
		contains = contains || (ip.Compare(a) >= 0 && ip.Compare(b) <= 0)
	}
	return contains, true
}

func lbComponentListener(w biz.Work, c biz.LoadBalancerComponent) biz.LoadBalancerListener {
	for _, l := range biz.EffectiveLoadBalancerListeners(w.Resource.LoadBalancer.LoadBalancer) {
		if l.ID == c.ListenerID || c.ListenerID == "" {
			return l
		}
	}
	return biz.LoadBalancerListener{ID: c.ListenerID, Name: c.ListenerName, Protocol: "HTTP"}
}
func lbListenerRouteName(w biz.Work, id string) string {
	for _, c := range w.Resource.LoadBalancer.Components {
		if c.Kind == "route" && c.ListenerID == id && !c.Deleted {
			return c.Name
		}
	}
	return ""
}
