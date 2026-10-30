package instance

import (
	"context"
	"errors"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func TestSecurityGroupsNotebookCurrentPodAndTemplateConvergence(t *testing.T) {
	n, i, client, pod := diagnosticFixture(t, false)
	ctx := context.Background()
	root, _ := client.Resource(notebookGVR).Namespace(i.Spec.Namespace).Get(ctx, i.Spec.NotebookName, metav1.GetOptions{})
	root.SetResourceVersion("1")
	_, _ = client.Resource(notebookGVR).Namespace(i.Spec.Namespace).Update(ctx, root, metav1.UpdateOptions{})
	pod.SetResourceVersion("2")
	_, _ = client.Resource(podGVR).Namespace(i.Spec.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
	names := []string{"sg-first", "sg-second"}
	// A successful API patch is insufficient until the generated controller's
	// actual Pod template has caught up.
	if err := n.SetSecurityGroups(ctx, i, names); !errors.Is(err, biz.ErrVersion) {
		t.Fatal("missing template convergence accepted", err)
	}
	current, _ := client.Resource(podGVR).Namespace(i.Spec.Namespace).Get(ctx, pod.GetName(), metav1.GetOptions{})
	if current.GetAnnotations()[biz.SecurityGroupsAnnotation] != "sg-first,sg-second" {
		t.Fatal("live Pod not updated")
	}
	set, _ := client.Resource(stsGVR).Namespace(i.Spec.Namespace).Get(ctx, i.Spec.NotebookName, metav1.GetOptions{})
	_ = unstructured.SetNestedStringMap(set.Object, map[string]string{biz.SecurityGroupsAnnotation: "sg-first,sg-second"}, "spec", "template", "metadata", "annotations")
	_, _ = client.Resource(stsGVR).Namespace(i.Spec.Namespace).Update(ctx, set, metav1.UpdateOptions{})
	if err := n.SetSecurityGroups(ctx, i, names); err != nil {
		t.Fatal(err)
	}
	// Explicit empty annotation must remain present in both paths.
	_ = unstructured.SetNestedStringMap(set.Object, map[string]string{biz.SecurityGroupsAnnotation: ""}, "spec", "template", "metadata", "annotations")
	_, _ = client.Resource(stsGVR).Namespace(i.Spec.Namespace).Update(ctx, set, metav1.UpdateOptions{})
	if err := n.SetSecurityGroups(ctx, i, nil); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []struct {
		kind string
		name string
	}{{"root", i.Spec.NotebookName}, {"pod", pod.GetName()}} {
		r := notebookGVR
		if resource.kind == "pod" {
			r = podGVR
		}
		got, _ := client.Resource(r).Namespace(i.Spec.Namespace).Get(ctx, resource.name, metav1.GetOptions{})
		if value, present := got.GetAnnotations()[biz.SecurityGroupsAnnotation]; !present || value != "" {
			t.Fatal("empty detach lost", got.GetAnnotations())
		}
	}
}
func TestSecurityGroupsRejectChangedNotebookUIDBeforeMutation(t *testing.T) {
	n, i, client, _ := diagnosticFixture(t, false)
	client.PrependReactor("get", "notebooks", func(a ktesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": i.Spec.NotebookName, "namespace": i.Spec.Namespace, "uid": "foreign"}}}, nil
	})
	if err := n.SetSecurityGroups(context.Background(), i, []string{"sg-web"}); !errors.Is(err, biz.ErrOwnership) {
		t.Fatal(err)
	}
	for _, a := range client.Actions() {
		if a.GetVerb() == "patch" {
			t.Fatal("foreign root was mutated")
		}
	}
}
