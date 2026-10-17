package instance

import (
	"context"
	"fmt"
	"reflect"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const preferredPlacement = "preferredDuringSchedulingIgnoredDuringExecution"
const requiredPlacement = "requiredDuringSchedulingIgnoredDuringExecution"
const podGroupAnnotation = "scheduling.k8s.io/group-name"

var podGroupGVR = schema.GroupVersionResource{Group: "scheduling.volcano.sh", Version: "v1beta1", Resource: "podgroups"}

func placementTerm(i biz.Instance) map[string]any {
	return map[string]any{"topologyKey": "kubernetes.io/hostname", "namespaces": []any{i.Spec.Namespace}, "labelSelector": map[string]any{"matchLabels": map[string]any{"ani.io/tenant-id": i.TenantID, "ani.io/instance-id": i.ID}}}
}
func placementFields(placement string) (selected, opposite string) {
	switch placement {
	case "COLOCATE_PREFERRED":
		return "podAffinity", "podAntiAffinity"
	case "SPREAD_PREFERRED":
		return "podAntiAffinity", "podAffinity"
	default:
		return "", ""
	}
}
func schedulingConflict(field string) error {
	return fmt.Errorf("%w: %s", biz.ErrSchedulingConflict, field)
}

// ApplySchedulingOptions merges only frozen placement and an explicitly frozen
// priority reference. GPU routing, node affinity and unrelated terms survive.
// Work on a copy so a conflict cannot partially alter the caller's template.
func ApplySchedulingOptions(pod map[string]any, i biz.Instance) error {
	if err := biz.ValidateScheduling(i.Spec.Scheduling); err != nil {
		return err
	}
	out := runtime.DeepCopyJSONValue(pod).(map[string]any)
	if requested := i.Spec.Scheduling.PriorityClassName; requested != nil {
		fixed, exists, err := unstructured.NestedString(out, "priorityClassName")
		if err != nil || (exists && fixed != "" && fixed != *requested) {
			return schedulingConflict("priorityClassName")
		}
		out["priorityClassName"] = *requested
	}
	selected, opposite := placementFields(i.Spec.Scheduling.PodPlacement)
	if selected != "" {
		expected := placementTerm(i)
		oppositePreferred, _, err := unstructured.NestedSlice(out, "affinity", opposite, preferredPlacement)
		if err != nil {
			return schedulingConflict("affinity." + opposite)
		}
		for _, value := range oppositePreferred {
			weighted, ok := value.(map[string]any)
			if !ok {
				return schedulingConflict("affinity." + opposite)
			}
			if reflect.DeepEqual(weighted["podAffinityTerm"], expected) {
				return schedulingConflict("affinity." + opposite)
			}
		}
		oppositeRequired, _, err := unstructured.NestedSlice(out, "affinity", opposite, requiredPlacement)
		if err != nil {
			return schedulingConflict("affinity." + opposite)
		}
		for _, term := range oppositeRequired {
			if reflect.DeepEqual(term, expected) {
				return schedulingConflict("affinity." + opposite)
			}
		}
		rules, _, err := unstructured.NestedSlice(out, "affinity", selected, preferredPlacement)
		if err != nil {
			return schedulingConflict("affinity." + selected)
		}
		merged := make([]any, 0, len(rules)+1)
		found := false
		for _, value := range rules {
			weighted, ok := value.(map[string]any)
			if !ok {
				return schedulingConflict("affinity." + selected)
			}
			if reflect.DeepEqual(weighted["podAffinityTerm"], expected) {
				weight, _, err := unstructured.NestedInt64(weighted, "weight")
				if err != nil || weight != 100 {
					return schedulingConflict("affinity." + selected + ".weight")
				}
				if found {
					continue
				}
				found = true
			}
			merged = append(merged, value)
		}
		if !found {
			merged = append(merged, map[string]any{"weight": int64(100), "podAffinityTerm": expected})
		}
		if err = unstructured.SetNestedSlice(out, merged, "affinity", selected, preferredPlacement); err != nil {
			return schedulingConflict("affinity")
		}
	}
	for key := range pod {
		delete(pod, key)
	}
	for key, value := range out {
		pod[key] = value
	}
	return nil
}

func verifySchedulingOptions(p *unstructured.Unstructured, i biz.Instance) error {
	if err := biz.ValidateScheduling(i.Spec.Scheduling); err != nil {
		return err
	}
	if requested := i.Spec.Scheduling.PriorityClassName; requested != nil {
		actual, exists, err := unstructured.NestedString(p.Object, "spec", "priorityClassName")
		if err != nil || !exists || actual != *requested {
			return schedulingConflict("Pod.spec.priorityClassName")
		}
	}
	selected, opposite := placementFields(i.Spec.Scheduling.PodPlacement)
	if selected == "" {
		return nil
	}
	expected := placementTerm(i)
	rules, _, err := unstructured.NestedSlice(p.Object, "spec", "affinity", selected, preferredPlacement)
	if err != nil {
		return schedulingConflict("Pod.spec.affinity")
	}
	found := 0
	for _, value := range rules {
		weighted, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if reflect.DeepEqual(weighted["podAffinityTerm"], expected) {
			weight, _, err := unstructured.NestedInt64(weighted, "weight")
			if err != nil || weight != 100 {
				return schedulingConflict("Pod.spec.affinity.weight")
			}
			found++
		}
	}
	if found != 1 {
		return schedulingConflict("Pod.spec.affinity." + selected)
	}
	for _, field := range []string{preferredPlacement, requiredPlacement} {
		rules, _, err := unstructured.NestedSlice(p.Object, "spec", "affinity", opposite, field)
		if err != nil {
			return schedulingConflict("Pod.spec.affinity." + opposite)
		}
		for _, value := range rules {
			term := value
			if field == preferredPlacement {
				if weighted, ok := value.(map[string]any); ok {
					term = weighted["podAffinityTerm"]
				}
			}
			if reflect.DeepEqual(term, expected) {
				return schedulingConflict("Pod.spec.affinity." + opposite)
			}
		}
	}
	return nil
}

// Volcano's admission/controller owns PodGroups. Read only the group named by
// this verified Pod; do not create or rewrite group priority to hide a conflict.
// No new priority means no new lookup or change to the existing Runtime path.
func (n *Notebook) verifyPodGroupPriority(ctx context.Context, p *unstructured.Unstructured, i biz.Instance) error {
	if i.Spec.ComputeType != biz.TypeGPU || i.Spec.Scheduling.PriorityClassName == nil {
		return nil
	}
	group := p.GetAnnotations()[podGroupAnnotation]
	if group == "" {
		return nil
	}
	obj, err := n.client.Resource(podGroupGVR).Namespace(i.Spec.Namespace).Get(ctx, group, metav1.GetOptions{})
	if err != nil {
		return err
	}
	actual, exists, err := unstructured.NestedString(obj.Object, "spec", "priorityClassName")
	if err != nil || !exists || actual != *i.Spec.Scheduling.PriorityClassName {
		return schedulingConflict("PodGroup.spec.priorityClassName")
	}
	return nil
}
