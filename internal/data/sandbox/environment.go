package sandbox

import (
	"context"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ReadEnvironment uses the Pod spec, never process execution or Secret reads.
// Existing Sandbox admission only permits a downward API Pod UID or no env.
func (p *KubernetesProvider) ReadEnvironment(ctx context.Context, s biz.Sandbox) ([]biz.EnvironmentVariable, error) {
	if err := biz.RequireTenant(ctx, s.TenantID); err != nil {
		return nil, err
	}
	if !biz.UUID(s.ID) || s.Version < 1 || s.Desired != biz.DesiredRunning || s.Observed != biz.ObservedRunning || s.Pending != nil ||
		s.ProviderRefs.PodName != "sandbox-"+s.ID || s.ProviderRefs.PodUID == "" {
		return nil, biz.ErrDataPlaneUnavailable
	}
	if err := p.requireCluster(s); err != nil {
		return nil, err
	}
	uid, err := p.verifyTenantNamespace(ctx, s, "")
	if err != nil {
		return nil, err
	}
	pod, err := p.client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Get(ctx, s.ProviderRefs.PodName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, biz.ErrDataPlaneUnavailable
	}
	if err != nil {
		return nil, err
	}
	if string(pod.GetUID()) != s.ProviderRefs.PodUID || !owned(pod, s) || !p.matchesPod(pod, s) || !podReady(pod) {
		return nil, biz.ErrDataPlaneUnavailable
	}
	containers, found, err := unstructured.NestedSlice(pod.Object, "spec", "containers")
	if err != nil || !found || len(containers) != 1 {
		return nil, biz.ErrDataPlaneUnavailable
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		return nil, biz.ErrDataPlaneUnavailable
	}
	result := []biz.EnvironmentVariable{}
	if !emptyOptionalList(container, "env") {
		if !podUIDEnvironment(container) {
			return nil, biz.ErrDataPlaneUnavailable
		}
		result = append(result, biz.EnvironmentVariable{Name: "ANI_SANDBOX_POD_UID", Source: "downward_api", FieldRef: "metadata.uid"})
	}
	if _, err = p.verifyTenantNamespace(ctx, s, uid); err != nil {
		return nil, err
	}
	return result, nil
}
