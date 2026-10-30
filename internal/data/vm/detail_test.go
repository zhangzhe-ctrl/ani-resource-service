package vm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

func TestRuntimeReadsOwnedGuestAndFencesRestart(t *testing.T) {
	for _, mode := range []string{"available", "no-agent", "stopped", "changed", "foreign-vm", "foreign-vmi", "query-error", "filesystem-error", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			v := biz.VM{TenantID: uuid.NewString(), ID: uuid.NewString(), Native: biz.ObjectRef{Name: "runtime-test", UID: uuid.NewString()}, Spec: biz.Spec{Namespace: "tenant-runtime", NamespaceUID: uuid.NewString(), Digest: stringOfDigest()}}
			ns := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": v.Spec.Namespace, "uid": v.Spec.NamespaceUID, "labels": map[string]any{"network.ani.io/tenant-id": v.TenantID, "network.ani.io/managed-by": "ani-network-service"}}}}
			root := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachine", "metadata": map[string]any{"name": v.Native.Name, "namespace": v.Spec.Namespace, "uid": v.Native.UID}}}
			root.SetLabels(labelVM(v))
			vmi := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachineInstance", "metadata": map[string]any{"name": v.Native.Name, "namespace": v.Spec.Namespace, "uid": "vmi-original"}, "status": map[string]any{"phase": "Running", "nodeName": "node-a", "conditions": []any{map[string]any{"type": "AgentConnected", "status": "True"}}, "phaseTransitionTimestamps": []any{map[string]any{"phase": "Running", "phaseTransitionTimestamp": "2026-10-30T02:00:00Z"}}, "interfaces": []any{map[string]any{"name": "primary", "mac": "52:54:00:00:00:01", "interfaceName": "eth0", "ipAddresses": []any{"10.0.0.2"}, "infoSource": "guest-agent"}}}}}
			vmi.SetOwnerReferences(ownReference(v))
			if mode == "foreign-vm" {
				root.SetUID("replaced-vm")
			}
			if mode == "foreign-vmi" {
				vmi.SetOwnerReferences(nil)
			}
			if mode == "no-agent" {
				_ = unstructured.SetNestedSlice(vmi.Object, []any{}, "status", "conditions")
			}
			if mode == "stopped" {
				_ = unstructured.SetNestedField(vmi.Object, "Succeeded", "status", "phase")
			}
			client := fake.NewSimpleDynamicClient(runtime.NewScheme(), ns, root, vmi)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/apis/subresources.kubevirt.io/v1/namespaces/tenant-runtime/virtualmachineinstances/runtime-test/") {
					t.Errorf("unexpected guest request %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "query-error" {
					http.Error(w, "dependency address and credential must not be exposed", 503)
					return
				}
				if strings.HasSuffix(r.URL.Path, "guestosinfo") {
					if mode == "unsupported" {
						_, _ = w.Write([]byte(`{"hostname":"guest-a","supportedCommands":[{"name":"guest-get-fsinfo","enabled":false}]}`))
						return
					}
					_, _ = w.Write([]byte(`{"hostname":"guest-a","guestAgentVersion":"10.0","os":{"prettyName":"Fedora Linux","kernelRelease":"6.18"},"supportedCommands":[{"name":"guest-get-fsinfo","enabled":true}]}`))
					return
				}
				if mode == "filesystem-error" {
					http.Error(w, "unavailable", 503)
					return
				}
				if mode == "changed" {
					replacement := vmi.DeepCopy()
					replacement.SetUID("vmi-replacement")
					_, err := client.Resource(vmiResource).Namespace(v.Spec.Namespace).Update(context.Background(), replacement, metav1.UpdateOptions{})
					if err != nil {
						t.Error(err)
					}
				}
				_, _ = w.Write([]byte(`{"items":[{"diskName":"vda1","mountPoint":"/","fileSystemType":"ext4","totalBytes":1073741824,"usedBytes":0},{"diskName":"tmpfs","mountPoint":"/tmp","fileSystemType":"tmpfs"}]}`))
			}))
			defer server.Close()
			n, err := NewNative(client, &rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			out, err := n.ReadRuntime(context.Background(), v)
			if mode == "foreign-vm" || mode == "foreign-vmi" {
				if !errors.Is(err, biz.ErrOwnership) || calls != 0 {
					t.Fatalf("ownership bypass: %+v %v calls=%d", out, err, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "no-agent":
				if out.State != "AGENT_NOT_CONNECTED" || calls != 0 {
					t.Fatalf("%+v calls=%d", out, calls)
				}
			case "stopped":
				if out.State != "NOT_RUNNING" || calls != 0 {
					t.Fatalf("%+v calls=%d", out, calls)
				}
			case "changed":
				if out.State != "CHANGED" || out.Hostname != "" || len(out.Interfaces) != 0 || len(out.Filesystems) != 0 {
					t.Fatalf("replacement guest data escaped: %+v", out)
				}
			case "query-error":
				if out.State != "UNAVAILABLE" || out.NodeName != "node-a" || out.Hostname != "" {
					t.Fatalf("%+v", out)
				}
			case "filesystem-error":
				if out.State != "AVAILABLE" || out.FilesystemsState != "UNAVAILABLE" || out.Hostname != "guest-a" {
					t.Fatalf("%+v", out)
				}
			case "unsupported":
				if out.FilesystemsState != "UNSUPPORTED" || calls != 1 {
					t.Fatalf("%+v", out)
				}
			default:
				if out.State != "AVAILABLE" || out.Hostname != "guest-a" || out.KernelRelease != "6.18" || len(out.Filesystems) != 2 || out.Filesystems[0].UsedBytes == nil || *out.Filesystems[0].UsedBytes != 0 || out.Filesystems[1].UsedBytes != nil || out.StartedAt.IsZero() || out.Interfaces[0].MAC == "" {
					t.Fatalf("lost real guest fields or missing/zero distinction: %+v", out)
				}
			}
		})
	}
}
