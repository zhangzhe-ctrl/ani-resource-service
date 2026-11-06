package sandbox

import (
	"context"
	"fmt"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var sandboxEventGVR = schema.GroupVersionResource{Version: "v1", Resource: "events"}

// ReadEvents lists one Kubernetes resource-version snapshot for recorded Pod
// identities. Pod absence is expected after pause or deletion. It never
// returns a truncated snapshot or provider-supplied messages.
func (p *KubernetesProvider) ReadEvents(ctx context.Context, s biz.Sandbox, history []biz.PodHistory) ([]biz.Event, error) {
	if err := biz.RequireTenant(ctx, s.TenantID); err != nil {
		return nil, err
	}
	if !biz.UUID(s.ID) || s.Version < 1 || len(history) == 0 || len(history) > biz.PodHistoryLimit {
		return nil, biz.ErrDataPlaneUnavailable
	}
	if err := p.requireCluster(s); err != nil {
		return nil, err
	}
	uid, err := p.verifyTenantNamespace(ctx, s, "")
	if err != nil {
		return nil, err
	}
	seenPods := map[string]bool{}
	for _, pod := range history {
		if pod.ClusterID != s.Snapshot.ClusterID || pod.Namespace != s.Snapshot.Namespace || pod.NamespaceUID == "" || pod.NamespaceUID != s.Snapshot.NamespaceUID || pod.PodName != "sandbox-"+s.ID || pod.PodUID == "" || len(pod.PodUID) > 128 || seenPods[pod.PodUID] {
			return nil, biz.ErrDataPlaneUnavailable
		}
		seenPods[pod.PodUID] = true
	}
	result := []biz.Event{}
	seen := map[string]bool{}
	size := 0
	pages := 0
	resourceVersion := ""
	for _, pod := range history {
		opts := metav1.ListOptions{Limit: 500, FieldSelector: fields.OneTermEqualSelector("involvedObject.uid", pod.PodUID).String()}
		if resourceVersion != "" {
			opts.ResourceVersion = resourceVersion
			opts.ResourceVersionMatch = metav1.ResourceVersionMatchExact
		}
		for {
			pages++
			if pages > biz.PodHistoryLimit+21 {
				return nil, fmt.Errorf("%w: event snapshot exceeds limit", biz.ErrDataPlaneUnavailable)
			}
			list, err := p.client.Resource(sandboxEventGVR).Namespace(s.Snapshot.Namespace).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			if list.GetResourceVersion() == "" || (resourceVersion != "" && list.GetResourceVersion() != resourceVersion) {
				return nil, biz.ErrDataPlaneUnavailable
			}
			resourceVersion = list.GetResourceVersion()
			for i := range list.Items {
				obj := &list.Items[i]
				raw, err := obj.MarshalJSON()
				if err != nil {
					return nil, biz.ErrDataPlaneUnavailable
				}
				size += len(raw)
				if size > 4*1024*1024 {
					return nil, fmt.Errorf("%w: event snapshot exceeds size limit", biz.ErrDataPlaneUnavailable)
				}
				ref, found, err := unstructured.NestedMap(obj.Object, "involvedObject")
				if err != nil || !found {
					return nil, biz.ErrDataPlaneUnavailable
				}
				// The selector is not a security boundary; verify every returned reference.
				kind, _, kindErr := unstructured.NestedString(ref, "kind")
				apiVersion, _, versionErr := unstructured.NestedString(ref, "apiVersion")
				if kindErr != nil || versionErr != nil || ref["uid"] != pod.PodUID || ref["name"] != pod.PodName || ref["namespace"] != s.Snapshot.Namespace || (kind != "" && kind != "Pod") || (apiVersion != "" && apiVersion != "v1") || obj.GetNamespace() != s.Snapshot.Namespace {
					continue
				}
				id := string(obj.GetUID())
				if id == "" || len(id) > 128 || seen[id] {
					return nil, biz.ErrDataPlaneUnavailable
				}
				seen[id] = true
				typ, _, _ := unstructured.NestedString(obj.Object, "type")
				reason, _, _ := unstructured.NestedString(obj.Object, "reason")
				// Legacy Kubernetes Events can have no type/reason. A safe
				// placeholder keeps one malformed or optional label from hiding
				// every other event and avoids reflecting provider-supplied text.
				if typ != "Normal" && typ != "Warning" {
					typ = "Unknown"
				}
				reason = safeEventReason(reason)
				var occurred time.Time
				seriesTime, _, _ := unstructured.NestedString(obj.Object, "series", "lastObservedTime")
				if seriesTime != "" {
					t, e := time.Parse(time.RFC3339Nano, seriesTime)
					if e != nil {
						return nil, biz.ErrDataPlaneUnavailable
					}
					if !t.IsZero() {
						occurred = t
					}
				}
				for _, key := range []string{"lastTimestamp", "eventTime", "firstTimestamp"} {
					if !occurred.IsZero() {
						break
					}
					val, _, _ := unstructured.NestedString(obj.Object, key)
					if val != "" {
						t, e := time.Parse(time.RFC3339Nano, val)
						if e != nil {
							return nil, biz.ErrDataPlaneUnavailable
						}
						if !t.IsZero() {
							occurred = t
							break
						}
					}
				}
				if occurred.IsZero() {
					occurred = obj.GetCreationTimestamp().Time
				}
				if occurred.IsZero() {
					return nil, biz.ErrDataPlaneUnavailable
				}
				result = append(result, biz.Event{ID: id, Type: typ, Reason: reason, Message: safeEventMessage(reason), OccurredAt: occurred})
				if len(result) > 10000 {
					return nil, fmt.Errorf("%w: event snapshot exceeds count limit", biz.ErrDataPlaneUnavailable)
				}
			}
			next := list.GetContinue()
			if next == "" {
				break
			}
			if next == opts.Continue {
				return nil, biz.ErrDataPlaneUnavailable
			}
			opts.Continue = next
			// The continuation token carries the original snapshot version;
			// Kubernetes rejects a non-empty resourceVersion on continuation.
			opts.ResourceVersion = ""
			opts.ResourceVersionMatch = ""
		}
	}
	if _, err = p.verifyTenantNamespace(ctx, s, uid); err != nil {
		return nil, err
	}
	return result, nil
}
func safeEventReason(reason string) string {
	switch reason {
	case "Scheduled", "Pulling", "Pulled", "Created", "Started", "Killing", "Failed", "BackOff", "Unhealthy", "FailedScheduling", "FailedMount":
		return reason
	default:
		return "Unknown"
	}
}

func safeEventMessage(reason string) string {
	switch reason {
	case "Scheduled":
		return "Pod 已调度"
	case "Pulling":
		return "正在拉取容器镜像"
	case "Pulled":
		return "容器镜像已拉取"
	case "Created":
		return "容器已创建"
	case "Started":
		return "容器已启动"
	case "Killing":
		return "容器正在停止"
	case "Failed":
		return "容器操作失败"
	case "BackOff":
		return "容器操作正在退避重试"
	case "Unhealthy":
		return "容器健康检查失败"
	case "FailedScheduling":
		return "Pod 调度失败"
	case "FailedMount":
		return "Pod 存储挂载失败"
	default:
		return "Pod 运行事件"
	}
}
