package sandbox

import (
	"fmt"
	"net/netip"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// previewObjectIdentity is the frozen identity recorded by an accepted port
// operation. It is an observation contract, not an externally reachable URL.
// The caller must also check the Namespace UID, live Pod and Sandbox version
// and arrange durable lifecycle fencing before relying on this observation.
type previewObjectIdentity struct {
	tenant, sandbox, namespace string
	podName, podUID, podIP     string
	serviceName, serviceUID    string
	sliceName, sliceUID        string
	port                       int32
}

// previewEndpointObserved verifies a selectorless ClusterIP Service and its
// single EndpointSlice against recorded identities. slices must contain the
// complete, consistently paginated service-name selection; checking only the
// expected Slice would allow another Slice to add destinations. Kubernetes forwarding
// uses addresses, not TargetRef UID: this observation alone does not prove
// that the endpoint stays safe after a Pod deletion/IP reuse. A later runtime
// must revoke the gateway and delete/observe these objects through a durable
// Sandbox lifecycle fence before reporting closure or releasing identities.
func previewEndpointObserved(id previewObjectIdentity, service *corev1.Service, slices []*discoveryv1.EndpointSlice) error {
	invalid := func() error { return fmt.Errorf("sandbox preview endpoint identity mismatch") }
	if id.tenant == "" || id.sandbox == "" || id.namespace != "tenant-"+id.tenant || id.podName != "sandbox-"+id.sandbox || id.podUID == "" || id.serviceName == "" || id.serviceUID == "" || id.sliceName == "" || id.sliceUID == "" || id.port < 1 || id.port > 65535 || service == nil || len(slices) != 1 || slices[0] == nil {
		return invalid()
	}
	slice := slices[0]
	podIP, err := netip.ParseAddr(id.podIP)
	if err != nil || podIP.IsUnspecified() || podIP.IsLoopback() || podIP.IsMulticast() || podIP.IsLinkLocalUnicast() || podIP.Is4In6() {
		return invalid()
	}
	if service.Name != id.serviceName || service.Namespace != id.namespace || string(service.UID) != id.serviceUID || service.DeletionTimestamp != nil || !previewOwned(service.Labels, id) || !previewOneOwner(service.OwnerReferences, "v1", "Pod", id.podName, id.podUID) {
		return invalid()
	}
	spec := service.Spec
	if spec.Type != corev1.ServiceTypeClusterIP || spec.ClusterIP == "" || spec.ClusterIP == corev1.ClusterIPNone || len(spec.Selector) != 0 || len(spec.ExternalIPs) != 0 || spec.ExternalName != "" || spec.LoadBalancerIP != "" || spec.LoadBalancerClass != nil || len(service.Status.LoadBalancer.Ingress) != 0 || len(spec.Ports) != 1 {
		return invalid()
	}
	serviceIP, err := netip.ParseAddr(spec.ClusterIP)
	if err != nil || serviceIP.IsUnspecified() || serviceIP.IsLoopback() || serviceIP.IsMulticast() || serviceIP.IsLinkLocalUnicast() || serviceIP.Is4In6() || serviceIP == podIP {
		return invalid()
	}
	p := spec.Ports[0]
	if p.Name != "preview" || p.Protocol != corev1.ProtocolTCP || p.Port != id.port || p.TargetPort != intstr.FromInt32(id.port) || p.NodePort != 0 {
		return invalid()
	}
	if slice.Name != id.sliceName || slice.Namespace != id.namespace || string(slice.UID) != id.sliceUID || slice.DeletionTimestamp != nil || !previewOwned(slice.Labels, id) || slice.Labels[discoveryv1.LabelServiceName] != id.serviceName || !previewOneOwner(slice.OwnerReferences, "v1", "Service", id.serviceName, id.serviceUID) {
		return invalid()
	}
	if (podIP.Is4() && slice.AddressType != discoveryv1.AddressTypeIPv4) || (podIP.Is6() && slice.AddressType != discoveryv1.AddressTypeIPv6) || len(slice.Ports) != 1 || len(slice.Endpoints) != 1 {
		return invalid()
	}
	ep := slice.Ports[0]
	if ep.Name == nil || *ep.Name != "preview" || ep.Protocol == nil || *ep.Protocol != corev1.ProtocolTCP || ep.Port == nil || *ep.Port != id.port {
		return invalid()
	}
	endpoint := slice.Endpoints[0]
	if len(endpoint.Addresses) != 1 || endpoint.Conditions.Ready == nil || !*endpoint.Conditions.Ready || (endpoint.Conditions.Terminating != nil && *endpoint.Conditions.Terminating) || (endpoint.Conditions.Serving != nil && !*endpoint.Conditions.Serving) || endpoint.TargetRef == nil {
		return invalid()
	}
	actual, err := netip.ParseAddr(endpoint.Addresses[0])
	if err != nil || actual != podIP {
		return invalid()
	}
	ref := endpoint.TargetRef
	if ref.APIVersion != "v1" || ref.Kind != "Pod" || ref.Namespace != id.namespace || ref.Name != id.podName || string(ref.UID) != id.podUID {
		return invalid()
	}
	return nil
}

func previewOwned(labels map[string]string, id previewObjectIdentity) bool {
	return labels[providerOwnerLabel] == id.sandbox && labels[providerTenantLabel] == id.tenant && labels[providerServiceLabel] == "ani-resource-service"
}
func previewOneOwner(refs []metav1.OwnerReference, api, kind, name, uid string) bool {
	return len(refs) == 1 && refs[0].APIVersion == api && refs[0].Kind == kind && refs[0].Name == name && string(refs[0].UID) == uid && refs[0].Controller != nil && *refs[0].Controller
}
