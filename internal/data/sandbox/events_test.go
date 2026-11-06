package sandbox

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func eventHistory(s biz.Sandbox, uid string) biz.PodHistory {
	return biz.PodHistory{ClusterID: s.Snapshot.ClusterID, Namespace: s.Snapshot.Namespace, NamespaceUID: s.Snapshot.NamespaceUID, PodName: "sandbox-" + s.ID, PodUID: uid}
}

func fixtureEvent(s biz.Sandbox, id string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Event", "metadata": map[string]any{"name": "event-" + id, "uid": id, "namespace": s.Snapshot.Namespace}, "involvedObject": map[string]any{"kind": "Pod", "apiVersion": "v1", "name": s.ProviderRefs.PodName, "namespace": s.Snapshot.Namespace, "uid": s.ProviderRefs.PodUID}, "type": "Warning", "reason": "Failed", "message": "token=secret-password", "lastTimestamp": "2026-01-02T03:04:05Z"}}
}

func eventList(items ...unstructured.Unstructured) *unstructured.UnstructuredList {
	list := &unstructured.UnstructuredList{Items: items}
	list.SetResourceVersion("100")
	return list
}

func TestReadEventsAcrossDeletedPodsUsesOneSnapshot(t *testing.T) {
	client, provider, s, ctx := environmentFixture(t)
	s.Desired, s.Observed = biz.DesiredDeleted, biz.ObservedDeleted
	old, current := eventHistory(s, "old-pod"), eventHistory(s, "new-pod")
	first, second, foreign := fixtureEvent(s, "event-1"), fixtureEvent(s, "event-2"), fixtureEvent(s, "foreign")
	first.Object["involvedObject"].(map[string]any)["uid"] = old.PodUID
	second.Object["involvedObject"].(map[string]any)["uid"] = current.PodUID
	foreign.Object["involvedObject"].(map[string]any)["uid"] = "unrelated"
	calls := 0
	client.PrependReactor("get", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		t.Fatal("historical event query must not require a current Pod")
		return true, nil, nil
	})
	client.PrependReactor("list", "events", func(action ktesting.Action) (bool, runtime.Object, error) {
		calls++
		opts := action.(interface{ GetListOptions() metav1.ListOptions }).GetListOptions()
		if calls == 1 {
			if opts.FieldSelector != "involvedObject.uid=old-pod" || opts.ResourceVersion != "" || opts.Continue != "" {
				t.Fatalf("first list options %+v", opts)
			}
			out := eventList(*first, *foreign)
			out.SetContinue("first-continuation")
			return true, out, nil
		}
		if calls == 2 {
			if opts.FieldSelector != "involvedObject.uid=old-pod" || opts.Continue != "first-continuation" || opts.ResourceVersion != "" {
				t.Fatalf("continuation options %+v", opts)
			}
			return true, eventList(), nil
		}
		if calls == 3 && opts.FieldSelector == "involvedObject.uid=new-pod" && opts.ResourceVersion == "100" && opts.ResourceVersionMatch == metav1.ResourceVersionMatchExact && opts.Continue == "" {
			out := eventList(*second)
			out.SetContinue("second-continuation")
			return true, out, nil
		}
		if calls == 4 && opts.FieldSelector == "involvedObject.uid=new-pod" && opts.Continue == "second-continuation" && opts.ResourceVersion == "" && opts.ResourceVersionMatch == "" {
			return true, eventList(), nil
		}
		t.Fatalf("unexpected list call %d: %+v", calls, opts)
		return true, nil, nil
	})
	got, err := provider.ReadEvents(ctx, s, []biz.PodHistory{old, current})
	if err != nil || calls != 4 || len(got) != 2 || got[0].ID != "event-1" || got[1].ID != "event-2" || got[0].Message != "容器操作失败" {
		t.Fatalf("events=%+v calls=%d err=%v", got, calls, err)
	}
}

func TestReadEventsRejectsInvalidHistoryAndNamespaceReplacement(t *testing.T) {
	for _, which := range []string{"empty", "cluster", "namespace", "namespace-uid", "pod-name", "pod-uid", "duplicate", "replaced-namespace"} {
		t.Run(which, func(t *testing.T) {
			client, provider, s, ctx := environmentFixture(t)
			history := []biz.PodHistory{eventHistory(s, "pod-uid")}
			switch which {
			case "empty":
				history = nil
			case "cluster":
				history[0].ClusterID = "foreign"
			case "namespace":
				history[0].Namespace = "foreign"
			case "namespace-uid":
				history[0].NamespaceUID = "foreign"
			case "pod-name":
				history[0].PodName = "foreign"
			case "pod-uid":
				history[0].PodUID = ""
			case "duplicate":
				history = append(history, history[0])
			}
			client.PrependReactor("list", "events", func(ktesting.Action) (bool, runtime.Object, error) { return true, eventList(), nil })
			gets := 0
			client.PrependReactor("get", "namespaces", func(ktesting.Action) (bool, runtime.Object, error) {
				gets++
				if which == "replaced-namespace" && gets == 2 {
					obj, err := client.Tracker().Get(namespaceGVR, "", s.Snapshot.Namespace)
					if err != nil {
						return true, nil, err
					}
					ns := obj.DeepCopyObject().(*unstructured.Unstructured)
					ns.SetUID("other")
					return true, ns, nil
				}
				return false, nil, nil
			})
			if got, err := provider.ReadEvents(ctx, s, history); err == nil || len(got) != 0 {
				t.Fatalf("events=%+v err=%v", got, err)
			}
		})
	}
}

func TestReadEventsRejectsSnapshotAndEventFailures(t *testing.T) {
	for _, which := range []string{"expired", "missing-rv", "changed-rv", "missing-time", "oversize"} {
		t.Run(which, func(t *testing.T) {
			client, provider, s, ctx := environmentFixture(t)
			event := fixtureEvent(s, "event-1")
			event.Object["involvedObject"].(map[string]any)["uid"] = "pod-uid"
			if which == "missing-time" {
				delete(event.Object, "lastTimestamp")
			}
			if which == "oversize" {
				event.Object["message"] = strings.Repeat("x", 4*1024*1024)
			}
			calls := 0
			client.PrependReactor("list", "events", func(ktesting.Action) (bool, runtime.Object, error) {
				calls++
				if calls == 2 && which == "expired" {
					return true, nil, fmt.Errorf("expired")
				}
				list := eventList(*event)
				if which == "missing-rv" {
					list.SetResourceVersion("")
				}
				if calls == 2 && which == "changed-rv" {
					list.SetResourceVersion("101")
				}
				return true, list, nil
			})
			history := []biz.PodHistory{eventHistory(s, "pod-uid")}
			if which == "expired" || which == "changed-rv" {
				history = append(history, eventHistory(s, "second-uid"))
			}
			got, err := provider.ReadEvents(ctx, s, history)
			if err == nil || len(got) != 0 {
				t.Fatalf("events=%+v err=%v", got, err)
			}
			if which == "oversize" && !errors.Is(err, biz.ErrDataPlaneUnavailable) {
				t.Fatal(err)
			}
		})
	}
}

func TestReadEventsAllowsOptionalReferenceFieldsAndSanitizesLabels(t *testing.T) {
	client, provider, s, ctx := environmentFixture(t)
	valid := fixtureEvent(s, "legacy")
	validRef := valid.Object["involvedObject"].(map[string]any)
	delete(validRef, "kind")
	delete(validRef, "apiVersion")
	delete(valid.Object, "type")
	delete(valid.Object, "reason")
	malicious := fixtureEvent(s, "malicious")
	malicious.Object["type"] = "secret-password"
	malicious.Object["reason"] = "secretpassword"
	conflict := fixtureEvent(s, "wrong-kind")
	conflict.Object["involvedObject"].(map[string]any)["kind"] = "Secret"
	wrongVersion := fixtureEvent(s, "wrong-version")
	wrongVersion.Object["involvedObject"].(map[string]any)["apiVersion"] = "v2"
	client.PrependReactor("list", "events", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, eventList(*valid, *malicious, *conflict, *wrongVersion), nil
	})
	got, err := provider.ReadEvents(ctx, s, []biz.PodHistory{eventHistory(s, s.ProviderRefs.PodUID)})
	if err != nil || len(got) != 2 {
		t.Fatalf("events=%+v err=%v", got, err)
	}
	for _, event := range got {
		if event.Type != "Unknown" || event.Reason != "Unknown" || event.Message != "Pod 运行事件" {
			t.Fatalf("unexpected unsafe event %+v", event)
		}
	}
}
