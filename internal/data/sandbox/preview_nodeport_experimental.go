package sandbox

import (
	"context"
	"errors"
	"net/netip"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
)

var previewServiceGVR = schema.GroupVersionResource{Version: "v1", Resource: "services"}
var previewEndpointSliceGVR = schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}
var errPreviewOutcomeUnknown = errors.New("sandbox preview provider outcome unknown")
var errPreviewNotSent = errors.New("sandbox preview provider request not sent")

type previewTargetVerifier func(context.Context, biz.Sandbox, previewObjectIdentity) error
type previewScopeVerifier func(context.Context, biz.Sandbox, previewObjectIdentity) error

// experimentalNodePortProvider is not wired by the composition root. Its
// zero value rejects creates. Enabling it before an ingress access policy,
// EndpointSlice journal, lifecycle cleanup worker, and RBAC are integrated
// would expose unauthenticated TCP traffic and is unsupported.
type experimentalNodePortProvider struct {
	client        dynamic.Interface
	enabled       bool
	verify        previewTargetVerifier
	verifyScope   previewScopeVerifier
	verifyIngress previewScopeVerifier
}

// createService issues exactly one Provider write. The caller must have
// persisted this exact pending create mutation while holding the work and
// Sandbox locks; retrying an absent object after an unknown response is unsafe.
// A created Service has no selector or endpoints and is not an open preview.
func (p experimentalNodePortProvider) createService(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, m biz.PreviewExperimentMutation) (string, error) {
	if !p.enabled {
		return "", biz.ErrUnsupported
	}
	if p.client == nil || p.verify == nil || m.Kind != "create" || !biz.UUID(m.ID) || m.TargetUID != "" || !biz.UUID(id.tenant) || !biz.UUID(id.sandbox) || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != "tenant-"+id.tenant || id.namespace != s.Snapshot.Namespace || id.podName != "sandbox-"+id.sandbox || id.podName != s.ProviderRefs.PodName || id.podUID == "" || id.podUID != s.ProviderRefs.PodUID || id.serviceName == "" || id.port < 1 || id.port > 65535 {
		return "", biz.ErrInvalid
	}
	if err := p.verify(ctx, s, id); err != nil {
		return "", err
	}
	yes := true
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: id.serviceName, Namespace: id.namespace, Labels: map[string]string{providerTenantLabel: id.tenant, providerOwnerLabel: id.sandbox, providerServiceLabel: "ani-resource-service", providerMutationID: m.ID}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: id.podName, UID: types.UID(id.podUID), Controller: &yes}}}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Ports: []corev1.ServicePort{{Name: "preview", Protocol: corev1.ProtocolTCP, Port: id.port, TargetPort: intstr.FromInt32(id.port)}}}}
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(service)
	if err != nil {
		return "", err
	}
	created, err := p.client.Resource(previewServiceGVR).Namespace(id.namespace).Create(ctx, &unstructured.Unstructured{Object: raw}, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return "", errPreviewOutcomeUnknown
		}
		created, err = p.client.Resource(previewServiceGVR).Namespace(id.namespace).Get(ctx, id.serviceName, metav1.GetOptions{})
		if err != nil {
			return "", errPreviewOutcomeUnknown
		}
	}
	if !previewPendingServiceMatches(created, id, m) {
		return "", biz.ErrOwnership
	}
	return string(created.GetUID()), nil
}

// deleteService only sends a UID-preconditioned request. Success is not a
// deletion receipt; a separate fresh observation must establish absence.
func (p experimentalNodePortProvider) deleteService(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, m biz.PreviewExperimentMutation) error {
	if p.client == nil || p.verifyScope == nil || !biz.UUID(id.tenant) || !biz.UUID(id.sandbox) || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != s.Snapshot.Namespace || id.namespace != "tenant-"+id.tenant || id.podName != "sandbox-"+id.sandbox || id.podUID == "" || id.serviceName == "" || id.serviceUID == "" || m.Kind != "delete" || !biz.UUID(m.ID) || m.TargetUID != id.serviceUID {
		return biz.ErrInvalid
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return err
	}
	obj, err := p.client.Resource(previewServiceGVR).Namespace(id.namespace).Get(ctx, id.serviceName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return errPreviewOutcomeUnknown
	}
	if string(obj.GetUID()) != id.serviceUID || !previewOwned(obj.GetLabels(), id) || !biz.UUID(obj.GetLabels()[providerMutationID]) || !previewOneOwner(obj.GetOwnerReferences(), "v1", "Pod", id.podName, id.podUID) {
		return biz.ErrOwnership
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return err
	}
	uid := types.UID(id.serviceUID)
	err = p.client.Resource(previewServiceGVR).Namespace(id.namespace).Delete(ctx, id.serviceName, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	if apierrors.IsConflict(err) {
		return biz.ErrOwnership
	}
	return errPreviewOutcomeUnknown
}

// observeService checks the recorded UID and exact create-attempt marker.
// A negative observation is not by itself proof that a timed-out create can
// no longer appear; the durable state machine retains that barrier.
// This only observes Service topology, not EndpointSlices or reachability.
func (p experimentalNodePortProvider) observeService(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, create biz.PreviewExperimentMutation) (bool, error) {
	if p.client == nil || p.verifyScope == nil || create.Kind != "create" || !biz.UUID(create.ID) || create.TargetUID != "" || !biz.UUID(id.tenant) || !biz.UUID(id.sandbox) || id.serviceUID == "" || id.serviceName == "" || id.podUID == "" || id.podName != "sandbox-"+id.sandbox || id.namespace != "tenant-"+id.tenant || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != s.Snapshot.Namespace {
		return false, biz.ErrInvalid
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return false, err
	}
	obj, err := p.client.Resource(previewServiceGVR).Namespace(id.namespace).Get(ctx, id.serviceName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, errPreviewOutcomeUnknown
	}
	if err == nil && (string(obj.GetUID()) != id.serviceUID || !previewPendingServiceMatches(obj, id, create)) {
		return false, biz.ErrOwnership
	}
	if err = p.verifyScope(ctx, s, id); err != nil {
		return false, err
	}
	return obj != nil, nil
}

// observePendingCreate can recover the exact late object while its create
// mutation remains journaled. Absence never clears the pending mutation.
func (p experimentalNodePortProvider) observePendingCreate(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, create biz.PreviewExperimentMutation) (string, error) {
	if p.client == nil || p.verifyScope == nil || create.Kind != "create" || !biz.UUID(create.ID) || create.TargetUID != "" || !biz.UUID(id.tenant) || !biz.UUID(id.sandbox) || id.serviceName == "" || id.podUID == "" || id.podName != "sandbox-"+id.sandbox || id.namespace != "tenant-"+id.tenant || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != s.Snapshot.Namespace {
		return "", biz.ErrInvalid
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return "", err
	}
	obj, err := p.client.Resource(previewServiceGVR).Namespace(id.namespace).Get(ctx, id.serviceName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return "", errPreviewOutcomeUnknown
	}
	if err == nil && !previewPendingServiceMatches(obj, id, create) {
		return "", biz.ErrOwnership
	}
	if err = p.verifyScope(ctx, s, id); err != nil {
		return "", err
	}
	if obj == nil {
		return "", nil
	}
	return string(obj.GetUID()), nil
}

func previewPendingServiceMatches(obj *unstructured.Unstructured, id previewObjectIdentity, m biz.PreviewExperimentMutation) bool {
	if obj == nil || obj.GetName() != id.serviceName || obj.GetNamespace() != id.namespace || obj.GetUID() == "" || obj.GetDeletionTimestamp() != nil {
		return false
	}
	labels := obj.GetLabels()
	if !previewOwned(labels, id) || labels[providerMutationID] != m.ID || !previewOneOwner(obj.GetOwnerReferences(), "v1", "Pod", id.podName, id.podUID) {
		return false
	}
	var svc corev1.Service
	if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &svc) != nil {
		return false
	}
	if svc.Spec.Type != corev1.ServiceTypeNodePort || len(svc.Spec.Selector) != 0 || len(svc.Spec.ExternalIPs) != 0 || svc.Spec.ExternalName != "" || svc.Spec.LoadBalancerIP != "" || svc.Spec.LoadBalancerClass != nil || len(svc.Spec.Ports) != 1 {
		return false
	}
	port := svc.Spec.Ports[0]
	return port.Name == "preview" && port.Protocol == corev1.ProtocolTCP && port.Port == id.port && port.TargetPort == intstr.FromInt32(id.port) && port.NodePort >= 1 && port.NodePort <= 65535
}

// createSlice creates routable data-plane state. An operator-controlled ingress
// verifier is mandatory and must establish actual enforcement; a mere policy
// manifest or a caller-supplied boolean is not sufficient. No verifier is
// wired in this candidate, so production composition cannot expose a port.
func (p experimentalNodePortProvider) createSlice(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, serviceCreate, m biz.PreviewExperimentMutation) (string, error) {
	if !p.enabled || p.verifyIngress == nil {
		return "", biz.ErrUnsupported
	}
	if p.client == nil || p.verify == nil || p.verifyScope == nil || m.Kind != "create_slice" || !biz.UUID(m.ID) || m.TargetUID != id.serviceUID || id.serviceUID == "" || serviceCreate.Kind != "create" || !biz.UUID(serviceCreate.ID) || id.sliceName == "" || id.port < 1 || id.port > 65535 || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != s.Snapshot.Namespace || id.namespace != "tenant-"+id.tenant || id.podName != "sandbox-"+id.sandbox || id.podUID == "" || id.podUID != s.ProviderRefs.PodUID {
		return "", biz.ErrInvalid
	}
	ip, err := netip.ParseAddr(id.podIP)
	if err != nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.Is4In6() {
		return "", biz.ErrInvalid
	}
	if err = p.verify(ctx, s, id); err != nil {
		return "", err
	}
	if err = p.verifyScope(ctx, s, id); err != nil {
		return "", err
	}
	if err = p.verifyIngress(ctx, s, id); err != nil {
		return "", err
	}
	service, err := p.client.Resource(previewServiceGVR).Namespace(id.namespace).Get(ctx, id.serviceName, metav1.GetOptions{})
	if err != nil {
		return "", errPreviewOutcomeUnknown
	}
	if string(service.GetUID()) != id.serviceUID || !previewPendingServiceMatches(service, id, serviceCreate) {
		return "", biz.ErrOwnership
	}
	yes := true
	name := "preview"
	port := id.port
	proto := corev1.ProtocolTCP
	addressType := discoveryv1.AddressTypeIPv4
	if ip.Is6() {
		addressType = discoveryv1.AddressTypeIPv6
	}
	slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: id.sliceName, Namespace: id.namespace, Labels: map[string]string{providerTenantLabel: id.tenant, providerOwnerLabel: id.sandbox, providerServiceLabel: "ani-resource-service", providerMutationID: m.ID, discoveryv1.LabelServiceName: id.serviceName, discoveryv1.LabelManagedBy: "ani-resource-sandbox"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Service", Name: id.serviceName, UID: types.UID(id.serviceUID), Controller: &yes}}}, AddressType: addressType, Ports: []discoveryv1.EndpointPort{{Name: &name, Port: &port, Protocol: &proto}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{ip.String()}, Conditions: discoveryv1.EndpointConditions{Ready: &yes}, TargetRef: &corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: id.namespace, Name: id.podName, UID: types.UID(id.podUID)}}}}
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(slice)
	if err != nil {
		return "", err
	}
	created, err := p.client.Resource(previewEndpointSliceGVR).Namespace(id.namespace).Create(ctx, &unstructured.Unstructured{Object: raw}, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return "", errPreviewOutcomeUnknown
		}
		created, err = p.client.Resource(previewEndpointSliceGVR).Namespace(id.namespace).Get(ctx, id.sliceName, metav1.GetOptions{})
		if err != nil {
			return "", errPreviewOutcomeUnknown
		}
	}
	if !previewPendingSliceMatches(created, id, m) {
		return "", biz.ErrOwnership
	}
	return string(created.GetUID()), nil
}

func previewPendingSliceMatches(obj *unstructured.Unstructured, id previewObjectIdentity, m biz.PreviewExperimentMutation) bool {
	if obj == nil || obj.GetName() != id.sliceName || obj.GetNamespace() != id.namespace || obj.GetUID() == "" || obj.GetDeletionTimestamp() != nil {
		return false
	}
	labels := obj.GetLabels()
	if !previewOwned(labels, id) || labels[providerMutationID] != m.ID || labels[discoveryv1.LabelServiceName] != id.serviceName || labels[discoveryv1.LabelManagedBy] != "ani-resource-sandbox" || !previewOneOwner(obj.GetOwnerReferences(), "v1", "Service", id.serviceName, id.serviceUID) {
		return false
	}
	var slice discoveryv1.EndpointSlice
	if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &slice) != nil {
		return false
	}
	expected, err := netip.ParseAddr(id.podIP)
	if err != nil || (expected.Is4() && slice.AddressType != discoveryv1.AddressTypeIPv4) || (expected.Is6() && slice.AddressType != discoveryv1.AddressTypeIPv6) || len(slice.Ports) != 1 || len(slice.Endpoints) != 1 {
		return false
	}
	port := slice.Ports[0]
	if port.Name == nil || *port.Name != "preview" || port.Port == nil || *port.Port != id.port || port.Protocol == nil || *port.Protocol != corev1.ProtocolTCP {
		return false
	}
	endpoint := slice.Endpoints[0]
	if len(endpoint.Addresses) != 1 || endpoint.Conditions.Ready == nil || !*endpoint.Conditions.Ready || endpoint.Conditions.Terminating != nil && *endpoint.Conditions.Terminating || endpoint.TargetRef == nil {
		return false
	}
	actual, err := netip.ParseAddr(endpoint.Addresses[0])
	if err != nil || actual != expected {
		return false
	}
	ref := endpoint.TargetRef
	return ref.APIVersion == "v1" && ref.Kind == "Pod" && ref.Namespace == id.namespace && ref.Name == id.podName && string(ref.UID) == id.podUID
}

// observeSlice accepts only the exact journaled object. An empty result is a
// point-in-time observation and cannot cancel an uncertain create.
func (p experimentalNodePortProvider) observeSlice(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, create biz.PreviewExperimentMutation) (string, error) {
	if p.client == nil || p.verifyScope == nil || create.Kind != "create_slice" || !biz.UUID(create.ID) || create.TargetUID != id.serviceUID || id.serviceUID == "" || id.sliceName == "" || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != s.Snapshot.Namespace || id.namespace != "tenant-"+id.tenant || id.podUID == "" || id.podName != "sandbox-"+id.sandbox {
		return "", biz.ErrInvalid
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return "", err
	}
	obj, err := p.client.Resource(previewEndpointSliceGVR).Namespace(id.namespace).Get(ctx, id.sliceName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return "", errPreviewOutcomeUnknown
	}
	if err == nil && (!previewPendingSliceMatches(obj, id, create) || id.sliceUID != "" && string(obj.GetUID()) != id.sliceUID) {
		return "", biz.ErrOwnership
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return "", err
	}
	if obj == nil {
		return "", nil
	}
	return string(obj.GetUID()), nil
}

func (p experimentalNodePortProvider) deleteSlice(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, create, m biz.PreviewExperimentMutation) error {
	if p.client == nil || p.verifyScope == nil || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != s.Snapshot.Namespace || id.namespace != "tenant-"+id.tenant || id.sliceUID == "" || m.Kind != "delete_slice" || !biz.UUID(m.ID) || m.TargetUID != id.sliceUID || create.Kind != "create_slice" || !biz.UUID(create.ID) {
		return biz.ErrInvalid
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return err
	}
	obj, err := p.client.Resource(previewEndpointSliceGVR).Namespace(id.namespace).Get(ctx, id.sliceName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return errPreviewOutcomeUnknown
	}
	if string(obj.GetUID()) != id.sliceUID || !previewPendingSliceMatches(obj, id, create) {
		return biz.ErrOwnership
	}
	if err = p.verifyScope(ctx, s, id); err != nil {
		return err
	}
	uid := types.UID(id.sliceUID)
	err = p.client.Resource(previewEndpointSliceGVR).Namespace(id.namespace).Delete(ctx, id.sliceName, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	if apierrors.IsConflict(err) {
		return biz.ErrOwnership
	}
	return errPreviewOutcomeUnknown
}

// noPreviewSlices proves an API snapshot currently has no Slice referring to
// this Service; it cannot cancel a prior unknown create, which remains fenced.
func (p experimentalNodePortProvider) noPreviewSlices(ctx context.Context, s biz.Sandbox, id previewObjectIdentity) (bool, error) {
	if p.client == nil || p.verifyScope == nil || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != s.Snapshot.Namespace || id.namespace != "tenant-"+id.tenant || id.serviceName == "" {
		return false, biz.ErrInvalid
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return false, err
	}
	opts := metav1.ListOptions{Limit: 500, LabelSelector: discoveryv1.LabelServiceName + "=" + id.serviceName}
	version := ""
	for page := 0; page < 32; page++ {
		list, err := p.client.Resource(previewEndpointSliceGVR).Namespace(id.namespace).List(ctx, opts)
		if err != nil {
			return false, errPreviewOutcomeUnknown
		}
		if list.GetResourceVersion() == "" || version != "" && list.GetResourceVersion() != version {
			return false, errPreviewOutcomeUnknown
		}
		version = list.GetResourceVersion()
		if len(list.Items) > 0 {
			return false, nil
		}
		next := list.GetContinue()
		if next == "" {
			if err = p.verifyScope(ctx, s, id); err != nil {
				return false, err
			}
			return true, nil
		}
		if next == opts.Continue {
			return false, errPreviewOutcomeUnknown
		}
		opts.Continue = next
	}
	return false, errPreviewOutcomeUnknown
}
