package sandbox

import (
	"context"
	"errors"
	"net"
	"net/http/httptrace"
	"net/netip"
	"strconv"
	"sync/atomic"

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
)

var errPreviewRejected = errors.New("sandbox preview provider rejected request")

// createSelectorService is an experimental alternative to manual slices.
// Kubernetes owns its EndpointSlices and tracks Pod readiness and removal.
// NodePort has no authentication and a deployment must explicitly opt in.
func (p experimentalNodePortProvider) createSelectorService(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, m biz.PreviewExperimentMutation) (string, error) {
	if !p.enabled {
		return "", errors.Join(errPreviewNotSent, biz.ErrUnsupported)
	}
	if p.client == nil || p.verify == nil || p.verifyScope == nil || m.Kind != "create" || !biz.UUID(m.ID) || m.TargetUID != "" || !biz.UUID(id.tenant) || !biz.UUID(id.sandbox) || id.tenant != s.TenantID || id.sandbox != s.ID || id.namespace != "tenant-"+id.tenant || id.namespace != s.Snapshot.Namespace || id.podName != "sandbox-"+id.sandbox || id.podName != s.ProviderRefs.PodName || id.podUID == "" || id.podUID != s.ProviderRefs.PodUID || id.serviceName == "" || id.port < 1 || id.port > 65535 {
		return "", errors.Join(errPreviewNotSent, biz.ErrInvalid)
	}
	if err := p.verify(ctx, s, id); err != nil {
		return "", errors.Join(errPreviewNotSent, err)
	}
	if err := p.verifyScope(ctx, s, id); err != nil {
		return "", errors.Join(errPreviewNotSent, err)
	}
	selector := map[string]string{providerTenantLabel: id.tenant, providerOwnerLabel: id.sandbox, providerServiceLabel: "ani-resource-service"}
	yes := true
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: id.serviceName, Namespace: id.namespace, Labels: map[string]string{providerTenantLabel: id.tenant, providerOwnerLabel: id.sandbox, providerServiceLabel: "ani-resource-service", providerMutationID: m.ID}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: id.podName, UID: types.UID(id.podUID), Controller: &yes}}}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Selector: selector, Ports: []corev1.ServicePort{{Name: "preview", Protocol: corev1.ProtocolTCP, Port: id.port, TargetPort: intstr.FromInt32(id.port)}}}}
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(svc)
	if err != nil {
		return "", errors.Join(errPreviewNotSent, err)
	}
	// client-go may retry a write after a server Retry-After response. A later
	// rejection cannot prove that an earlier request was not committed.
	var attempts, writes atomic.Int32
	var writeFailed atomic.Bool
	requestCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { attempts.Add(1) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				writeFailed.Store(true)
			}
			writes.Add(1)
		},
	})
	obj, err := p.client.Resource(previewServiceGVR).Namespace(id.namespace).Create(requestCtx, &unstructured.Unstructured{Object: raw}, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			if attempts.Load() == 1 && writes.Load() == 1 && !writeFailed.Load() && (apierrors.IsForbidden(err) || apierrors.IsInvalid(err)) {
				return "", errors.Join(errPreviewRejected, err)
			}
			return "", errPreviewOutcomeUnknown
		}
		obj, err = p.client.Resource(previewServiceGVR).Namespace(id.namespace).Get(ctx, id.serviceName, metav1.GetOptions{})
		if err != nil {
			return "", errPreviewOutcomeUnknown
		}
	}
	if !previewSelectorServiceMatches(obj, id, m) {
		return "", biz.ErrOwnership
	}
	return string(obj.GetUID()), nil
}

// selectorAddress reads only API-server facts. It verifies the Pod and the
// controller-published target before returning the Pod node's InternalIP and
// allocated nodePort. This is not an authentication or reachability check.
func (p experimentalNodePortProvider) selectorAddress(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, create biz.PreviewExperimentMutation, protocol string) (string, error) {
	if protocol != "http" && protocol != "tcp" || p.verify == nil || p.verifyScope == nil || id.serviceUID == "" {
		return "", biz.ErrInvalid
	}
	if err := p.verify(ctx, s, id); err != nil {
		return "", err
	}
	uid, err := p.observeSelectorService(ctx, s, id, create)
	if err != nil {
		return "", err
	}
	if uid == "" {
		return "", biz.ErrDataPlaneUnavailable
	}
	svcObj, err := p.client.Resource(previewServiceGVR).Namespace(id.namespace).Get(ctx, id.serviceName, metav1.GetOptions{})
	if err != nil || !previewSelectorServiceMatches(svcObj, id, create) || string(svcObj.GetUID()) != id.serviceUID {
		return "", biz.ErrDataPlaneUnavailable
	}
	var svc corev1.Service
	if runtime.DefaultUnstructuredConverter.FromUnstructured(svcObj.Object, &svc) != nil {
		return "", biz.ErrDataPlaneUnavailable
	}
	podObj, err := p.client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Namespace(id.namespace).Get(ctx, id.podName, metav1.GetOptions{})
	if err != nil {
		return "", biz.ErrDataPlaneUnavailable
	}
	var pod corev1.Pod
	if runtime.DefaultUnstructuredConverter.FromUnstructured(podObj.Object, &pod) != nil || string(pod.UID) != id.podUID || pod.DeletionTimestamp != nil || pod.Spec.NodeName == "" || pod.Status.Phase != corev1.PodRunning || !previewOwned(pod.Labels, id) {
		return "", biz.ErrDataPlaneUnavailable
	}
	ready := false
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			ready = true
		}
	}
	podIP, err := netip.ParseAddr(pod.Status.PodIP)
	if err != nil || podIP.IsUnspecified() || !ready {
		return "", biz.ErrDataPlaneUnavailable
	}
	list, err := p.client.Resource(previewEndpointSliceGVR).Namespace(id.namespace).List(ctx, metav1.ListOptions{LabelSelector: discoveryv1.LabelServiceName + "=" + id.serviceName, Limit: 500})
	if err != nil || list.GetResourceVersion() == "" || list.GetContinue() != "" || len(list.Items) != 1 {
		return "", biz.ErrDataPlaneUnavailable
	}
	var slice discoveryv1.EndpointSlice
	if runtime.DefaultUnstructuredConverter.FromUnstructured(list.Items[0].Object, &slice) != nil || slice.DeletionTimestamp != nil || slice.Labels[discoveryv1.LabelManagedBy] != "endpointslice-controller.k8s.io" || !previewOneOwner(slice.OwnerReferences, "v1", "Service", id.serviceName, id.serviceUID) || len(slice.Endpoints) != 1 || len(slice.Ports) != 1 {
		return "", biz.ErrDataPlaneUnavailable
	}
	e := slice.Endpoints[0]
	if e.TargetRef == nil || (e.TargetRef.APIVersion != "" && e.TargetRef.APIVersion != "v1") || e.TargetRef.Kind != "Pod" || e.TargetRef.Namespace != id.namespace || e.TargetRef.Name != id.podName || string(e.TargetRef.UID) != id.podUID || e.Conditions.Ready == nil || !*e.Conditions.Ready || e.Conditions.Terminating != nil && *e.Conditions.Terminating || len(e.Addresses) != 1 || e.Addresses[0] != podIP.String() {
		return "", biz.ErrDataPlaneUnavailable
	}
	ep := slice.Ports[0]
	if ep.Name == nil || *ep.Name != "preview" || ep.Port == nil || *ep.Port != id.port || ep.Protocol == nil || *ep.Protocol != corev1.ProtocolTCP {
		return "", biz.ErrDataPlaneUnavailable
	}
	nodeObj, err := p.client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "nodes"}).Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		return "", biz.ErrDataPlaneUnavailable
	}
	var node corev1.Node
	if runtime.DefaultUnstructuredConverter.FromUnstructured(nodeObj.Object, &node) != nil || node.Name != pod.Spec.NodeName || node.UID == "" || node.DeletionTimestamp != nil {
		return "", biz.ErrDataPlaneUnavailable
	}
	var address netip.Addr
	for _, a := range node.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			ip, e := netip.ParseAddr(a.Address)
			if e == nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() {
				if address.IsValid() && address != ip {
					return "", biz.ErrDataPlaneUnavailable
				}
				address = ip
			}
		}
	}
	if !address.IsValid() {
		return "", biz.ErrDataPlaneUnavailable
	}
	if err = p.verify(ctx, s, id); err != nil {
		return "", err
	}
	if err = p.verifyScope(ctx, s, id); err != nil {
		return "", err
	}
	return protocol + "://" + net.JoinHostPort(address.String(), strconv.Itoa(int(svc.Spec.Ports[0].NodePort))), nil
}

func previewSelectorServiceMatches(obj *unstructured.Unstructured, id previewObjectIdentity, m biz.PreviewExperimentMutation) bool {
	if obj == nil || obj.GetName() != id.serviceName || obj.GetNamespace() != id.namespace || obj.GetUID() == "" || obj.GetDeletionTimestamp() != nil || !previewOwned(obj.GetLabels(), id) || obj.GetLabels()[providerMutationID] != m.ID || !previewOneOwner(obj.GetOwnerReferences(), "v1", "Pod", id.podName, id.podUID) {
		return false
	}
	var svc corev1.Service
	if runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &svc) != nil {
		return false
	}
	if svc.Spec.Type != corev1.ServiceTypeNodePort || len(svc.Spec.Selector) != 3 || svc.Spec.Selector[providerTenantLabel] != id.tenant || svc.Spec.Selector[providerOwnerLabel] != id.sandbox || svc.Spec.Selector[providerServiceLabel] != "ani-resource-service" || len(svc.Spec.ExternalIPs) != 0 || svc.Spec.ExternalName != "" || svc.Spec.LoadBalancerIP != "" || svc.Spec.LoadBalancerClass != nil || len(svc.Spec.Ports) != 1 {
		return false
	}
	port := svc.Spec.Ports[0]
	return port.Name == "preview" && port.Protocol == corev1.ProtocolTCP && port.Port == id.port && port.TargetPort == intstr.FromInt32(id.port) && port.NodePort >= 1 && port.NodePort <= 65535
}

// observeSelectorService checks the precise journaled Service. An empty UID
// is only a current absence; it never resolves a timed-out create by itself.
func (p experimentalNodePortProvider) observeSelectorService(ctx context.Context, s biz.Sandbox, id previewObjectIdentity, create biz.PreviewExperimentMutation) (string, error) {
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
	if err == nil && (!previewSelectorServiceMatches(obj, id, create) || id.serviceUID != "" && string(obj.GetUID()) != id.serviceUID) {
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
