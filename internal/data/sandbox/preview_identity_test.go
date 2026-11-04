package sandbox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func previewFixture() (previewObjectIdentity, *corev1.Service, *discoveryv1.EndpointSlice) {
	tenant := "11111111-1111-4111-8111-111111111111"
	sandbox := "22222222-2222-4222-8222-222222222222"
	ns := "tenant-" + tenant
	pod := "sandbox-" + sandbox
	id := previewObjectIdentity{tenant: tenant, sandbox: sandbox, namespace: ns, podName: pod, podUID: "pod-uid", podIP: "10.5.1.2", serviceName: "preview-123", serviceUID: "service-uid", sliceName: "preview-123-a", sliceUID: "slice-uid", port: 8080}
	labels := map[string]string{providerOwnerLabel: sandbox, providerTenantLabel: tenant, providerServiceLabel: "ani-resource-service"}
	yes := true
	proto := corev1.ProtocolTCP
	name := "preview"
	port := int32(8080)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: id.serviceName, Namespace: ns, UID: types.UID(id.serviceUID), Labels: labels, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: pod, UID: types.UID(id.podUID), Controller: &yes}}}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.6.2.3", Ports: []corev1.ServicePort{{Name: name, Protocol: proto, Port: port, TargetPort: intstr.FromInt32(port)}}}}
	sliceLabels := map[string]string{providerOwnerLabel: sandbox, providerTenantLabel: tenant, providerServiceLabel: "ani-resource-service", discoveryv1.LabelServiceName: id.serviceName}
	slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: id.sliceName, Namespace: ns, UID: types.UID(id.sliceUID), Labels: sliceLabels, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Service", Name: id.serviceName, UID: types.UID(id.serviceUID), Controller: &yes}}}, AddressType: discoveryv1.AddressTypeIPv4, Ports: []discoveryv1.EndpointPort{{Name: &name, Protocol: &proto, Port: &port}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{id.podIP}, Conditions: discoveryv1.EndpointConditions{Ready: &yes}, TargetRef: &corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: ns, Name: pod, UID: types.UID(id.podUID)}}}}
	return id, service, slice
}

func TestPreviewEndpointRequiresBoundPodServiceAndSlice(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*previewObjectIdentity, *corev1.Service, *discoveryv1.EndpointSlice)
	}{
		{"valid", nil},
		{"cross tenant", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			s.Labels[providerTenantLabel] = "other"
		}},
		{"foreign namespace", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			e.Namespace = "tenant-other"
		}},
		{"old service UID", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) { s.UID = "replaced" }},
		{"old pod UID", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			e.Endpoints[0].TargetRef.UID = "replaced"
		}},
		{"pod IP reused", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			e.Endpoints[0].Addresses = []string{"10.5.1.3"}
		}},
		{"spoofable selector", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			s.Spec.Selector = map[string]string{providerOwnerLabel: i.sandbox}
		}},
		{"nodeport", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			s.Spec.Type = corev1.ServiceTypeNodePort
		}},
		{"external IP", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			s.Spec.ExternalIPs = []string{"192.0.2.1"}
		}},
		{"wrong service owner", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			s.OwnerReferences[0].UID = "other"
		}},
		{"wrong slice owner", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			e.OwnerReferences[0].UID = "other"
		}},
		{"wrong port", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			v := int32(9999)
			e.Ports[0].Port = &v
		}},
		{"additional endpoint", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			e.Endpoints = append(e.Endpoints, e.Endpoints[0])
		}},
		{"not ready", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			v := false
			e.Endpoints[0].Conditions.Ready = &v
		}},
		{"terminating", func(i *previewObjectIdentity, s *corev1.Service, e *discoveryv1.EndpointSlice) {
			v := true
			e.Endpoints[0].Conditions.Terminating = &v
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, s, e := previewFixture()
			if tc.mutate != nil {
				tc.mutate(&id, s, e)
			}
			err := previewEndpointObserved(id, s, []*discoveryv1.EndpointSlice{e})
			if (err == nil) != (tc.mutate == nil) {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

func TestPreviewEndpointRejectsAdditionalSlices(t *testing.T) {
	id, s, e := previewFixture()
	extra := e.DeepCopy()
	extra.UID = "foreign-slice"
	if err := previewEndpointObserved(id, s, []*discoveryv1.EndpointSlice{e, extra}); err == nil {
		t.Fatal("accepted an additional endpoint slice that kube-proxy could route")
	}
}
