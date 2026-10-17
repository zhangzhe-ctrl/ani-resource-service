package instance

import (
	"context"
	"errors"
	"reflect"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func TestSchedulingNotebookFrozenProjectionAndPodVerification(t *testing.T) {
	for _, gpu := range []bool{false, true} {
		for _, placement := range []string{"NONE", "COLOCATE_PREFERRED", "SPREAD_PREFERRED"} {
			t.Run(map[bool]string{false: "cpu", true: "gpu"}[gpu]+"/"+placement, func(t *testing.T) {
				_, i, _, _ := diagnosticFixture(t, gpu)
				priority := "workload-normal"
				i.Spec.Scheduling = biz.Scheduling{PodPlacement: placement, PriorityClassName: &priority}
				notebook, err := RenderNotebook(i)
				if err != nil {
					t.Fatal(err)
				}
				template, _, _ := unstructured.NestedMap(notebook.Object, "spec", "template")
				pod := &unstructured.Unstructured{Object: template}
				pod.SetAnnotations(notebook.GetAnnotations())
				if err = verifyPod(pod, i); err != nil {
					t.Fatal("controller projection", err)
				}
				spec, _, _ := unstructured.NestedMap(pod.Object, "spec")
				first := runtime.DeepCopyJSONValue(spec)
				if err = ApplySchedulingOptions(spec, i); err != nil || !reflect.DeepEqual(first, spec) {
					t.Fatal("repeated render changed constraints", err)
				}
				selected, _ := placementFields(placement)
				if selected == "" {
					if _, exists, _ := unstructured.NestedMap(spec, "affinity"); exists {
						t.Fatal("NONE acquired affinity")
					}
				} else {
					rules, _, _ := unstructured.NestedSlice(spec, "affinity", selected, preferredPlacement)
					if len(rules) != 1 {
						t.Fatal("soft rule missing", rules)
					}
					weighted := rules[0].(map[string]any)
					term := weighted["podAffinityTerm"].(map[string]any)
					if weighted["weight"] != int64(100) || !reflect.DeepEqual(term, placementTerm(i)) {
						t.Fatal("scope/weight differs", rules)
					}
					if _, exists, _ := unstructured.NestedSlice(spec, "affinity", selected, requiredPlacement); exists {
						t.Fatal("new hard rule")
					}
				}
				if spec["priorityClassName"] != priority {
					t.Fatal("priority missing")
				}
				if gpu {
					if spec["schedulerName"] != i.Spec.GPU.Scheduler || !reflect.DeepEqual(spec["nodeSelector"], stringMap(i.Spec.GPU.NodeLabels)) {
						t.Fatal("GPU routing changed")
					}
					if notebook.GetAnnotations()["scheduling.volcano.sh/queue-name"] != i.Spec.GPU.Queue {
						t.Fatal("queue changed")
					}
					cs := spec["containers"].([]any)
					limits, _, _ := unstructured.NestedStringMap(cs[0].(map[string]any), "resources", "limits")
					for key, value := range i.Spec.GPU.Limits {
						if limits[key] != value {
							t.Fatal("GPU units changed", key)
						}
					}
				} else if _, exists := spec["schedulerName"]; exists {
					t.Fatal("CPU forced to Volcano")
				}
				wrong := pod.DeepCopy()
				_ = unstructured.SetNestedField(wrong.Object, "other", "spec", "priorityClassName")
				if err = verifyPod(wrong, i); !errors.Is(err, biz.ErrSchedulingConflict) {
					t.Fatal("priority mutation accepted", err)
				}
				if placement != "NONE" {
					wrong = pod.DeepCopy()
					unstructured.RemoveNestedField(wrong.Object, "spec", "affinity")
					if err = verifyPod(wrong, i); !errors.Is(err, biz.ErrSchedulingConflict) {
						t.Fatal("lost placement accepted", err)
					}
				}
			})
		}
	}
}

func TestSchedulingMergePreservesHardConstraintsAndRejectsConflicts(t *testing.T) {
	_, i, _, _ := diagnosticFixture(t, true)
	priority := "normal"
	i.Spec.Scheduling = biz.Scheduling{PodPlacement: "SPREAD_PREFERRED", PriorityClassName: &priority}
	hard := map[string]any{"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{"nodeSelectorTerms": []any{map[string]any{"matchExpressions": []any{map[string]any{"key": "managed-zone", "operator": "In", "values": []any{"zone-a"}}}}}}}
	unrelated := map[string]any{"weight": int64(7), "podAffinityTerm": map[string]any{"topologyKey": "topology.kubernetes.io/zone", "labelSelector": map[string]any{"matchLabels": map[string]any{"app": "other"}}}}
	pod := map[string]any{"schedulerName": "volcano", "nodeSelector": map[string]any{"gpu": "test"}, "runtimeClassName": "nvidia", "affinity": map[string]any{"nodeAffinity": hard, "podAntiAffinity": map[string]any{preferredPlacement: []any{unrelated}}}}
	before := runtime.DeepCopyJSONValue(pod).(map[string]any)
	if err := ApplySchedulingOptions(pod, i); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pod["nodeSelector"], before["nodeSelector"]) || pod["schedulerName"] != before["schedulerName"] || pod["runtimeClassName"] != before["runtimeClassName"] {
		t.Fatal("runtime overwritten")
	}
	affinity := pod["affinity"].(map[string]any)
	if !reflect.DeepEqual(affinity["nodeAffinity"], hard) {
		t.Fatal("node affinity overwritten")
	}
	rules, _, _ := unstructured.NestedSlice(pod, "affinity", "podAntiAffinity", preferredPlacement)
	if len(rules) != 2 || !reflect.DeepEqual(rules[0], unrelated) {
		t.Fatal("unrelated affinity overwritten")
	}
	if err := ApplySchedulingOptions(pod, i); err != nil {
		t.Fatal(err)
	}
	rules, _, _ = unstructured.NestedSlice(pod, "affinity", "podAntiAffinity", preferredPlacement)
	if len(rules) != 2 {
		t.Fatal("duplicate managed preference")
	}
	for _, field := range []string{preferredPlacement, requiredPlacement} {
		changed := runtime.DeepCopyJSONValue(pod).(map[string]any)
		term := any(placementTerm(i))
		if field == preferredPlacement {
			term = map[string]any{"weight": int64(100), "podAffinityTerm": term}
		}
		_ = unstructured.SetNestedSlice(changed, []any{term}, "affinity", "podAffinity", field)
		original := runtime.DeepCopyJSONValue(changed)
		if err := ApplySchedulingOptions(changed, i); !errors.Is(err, biz.ErrSchedulingConflict) || !reflect.DeepEqual(original, changed) {
			t.Fatal("opposite managed relationship accepted/mutated", field, err)
		}
	}
	changed := runtime.DeepCopyJSONValue(pod).(map[string]any)
	changed["priorityClassName"] = "fixed-other"
	original := runtime.DeepCopyJSONValue(changed)
	if err := ApplySchedulingOptions(changed, i); !errors.Is(err, biz.ErrSchedulingConflict) || !reflect.DeepEqual(original, changed) {
		t.Fatal("fixed priority overwritten", err)
	}
	i.Spec.Scheduling.PriorityClassName = nil
	if err := ApplySchedulingOptions(changed, i); err != nil || changed["priorityClassName"] != "fixed-other" {
		t.Fatal("absent priority cleared existing value", err)
	}
}

func TestSchedulingPodGroupPriorityGuardReadOnly(t *testing.T) {
	n, i, client, pod := diagnosticFixture(t, true)
	priority := "normal"
	i.Spec.Scheduling.PriorityClassName = &priority
	annotations := pod.GetAnnotations()
	annotations["scheduling.k8s.io/group-name"] = "managed-group"
	pod.SetAnnotations(annotations)
	group := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "scheduling.volcano.sh/v1beta1", "kind": "PodGroup", "metadata": map[string]any{"name": "managed-group", "namespace": i.Spec.Namespace}, "spec": map[string]any{"priorityClassName": priority, "queue": i.Spec.GPU.Queue}}}
	if _, err := client.Resource(podGroupGVR).Namespace(i.Spec.Namespace).Create(context.Background(), group, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	assertGroupRead := func() {
		t.Helper()
		actions := client.Actions()
		if len(actions) != 1 {
			t.Fatal("guard must read the annotated PodGroup exactly once", actions)
		}
		action, ok := actions[0].(ktesting.GetAction)
		if !ok || action.GetVerb() != "get" || action.GetNamespace() != i.Spec.Namespace || action.GetName() != "managed-group" || action.GetResource().Group != "scheduling.volcano.sh" || action.GetResource().Version != "v1beta1" || action.GetResource().Resource != "podgroups" {
			t.Fatal("guard read a different group/namespace or wrote an object", actions)
		}
	}
	if err := n.verifyPodGroupPriority(context.Background(), pod, i); err != nil {
		t.Fatal("matching group", err)
	}
	assertGroupRead()
	group.Object["spec"].(map[string]any)["priorityClassName"] = "other"
	if _, err := client.Resource(podGroupGVR).Namespace(i.Spec.Namespace).Update(context.Background(), group, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	client.ClearActions()
	if err := n.verifyPodGroupPriority(context.Background(), pod, i); !errors.Is(err, biz.ErrSchedulingConflict) {
		t.Fatal("group conflict silently accepted", err)
	}
	assertGroupRead()
	client.ClearActions()
	i.Spec.Scheduling.PriorityClassName = nil
	if err := n.verifyPodGroupPriority(context.Background(), pod, i); err != nil || len(client.Actions()) != 0 {
		t.Fatal("absent new reference changed existing path", err)
	}
}
