package vm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (n *Native) runtimeVMI(ctx context.Context, v biz.VM) (*unstructured.Unstructured, error) {
	if _, err := n.snapshotRoot(ctx, v); err != nil {
		return nil, err
	}
	vmi, err := n.client.Resource(vmiResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if vmi.GetAPIVersion() != "kubevirt.io/v1" || vmi.GetKind() != "VirtualMachineInstance" || vmi.GetUID() == "" || !owned(vmi, "VirtualMachine", v.Native.UID) || vmi.GetDeletionTimestamp() != nil {
		return nil, biz.ErrOwnership
	}
	return vmi, nil
}

// KubeVirt's guest responses have no UID. Fence both ends of these name-based
// calls so an intervening restart cannot attribute another VMI's guest data.
func (n *Native) ReadRuntime(ctx context.Context, v biz.VM) (biz.Runtime, error) {
	out := biz.Runtime{State: "UNAVAILABLE", AgentState: "UNKNOWN", FilesystemsState: "NOT_QUERIED"}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	vmi, err := n.runtimeVMI(ctx, v)
	if apierrors.IsNotFound(err) {
		out.State = "NOT_RUNNING"
		out.ObservedAt = time.Now().UTC()
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.NodeName = snapshotString(vmi, "status", "nodeName")
	transitions, _, _ := unstructured.NestedSlice(vmi.Object, "status", "phaseTransitionTimestamps")
	for _, item := range transitions {
		x, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if x["phase"] == "Running" {
			if value, ok := x["phaseTransitionTimestamp"].(string); ok {
				out.StartedAt, _ = time.Parse(time.RFC3339Nano, value)
			}
		}
	}
	interfaces, _, _ := unstructured.NestedSlice(vmi.Object, "status", "interfaces")
	for _, item := range interfaces {
		x, ok := item.(map[string]any)
		if !ok {
			continue
		}
		u := &unstructured.Unstructured{Object: x}
		addresses, _, _ := unstructured.NestedStringSlice(x, "ipAddresses")
		if len(addresses) == 0 {
			if ip := snapshotString(u, "ipAddress"); ip != "" {
				addresses = []string{ip}
			}
		}
		out.Interfaces = append(out.Interfaces, biz.GuestInterface{Name: snapshotString(u, "name"), GuestName: snapshotString(u, "interfaceName"), MAC: snapshotString(u, "mac"), InfoSource: snapshotString(u, "infoSource"), Addresses: addresses})
	}
	out.ObservedAt = time.Now().UTC()
	if snapshotString(vmi, "status", "phase") != "Running" {
		out.State = "NOT_RUNNING"
		return n.fencedRuntime(ctx, v, vmi, out)
	}
	connected := false
	conditions, _, _ := unstructured.NestedSlice(vmi.Object, "status", "conditions")
	for _, item := range conditions {
		if x, ok := item.(map[string]any); ok && x["type"] == "AgentConnected" && x["status"] == "True" {
			connected = true
		}
	}
	if !connected {
		out.State = "AGENT_NOT_CONNECTED"
		out.AgentState = "NOT_CONNECTED"
		return n.fencedRuntime(ctx, v, vmi, out)
	}
	out.AgentState = "CONNECTED"
	var guest struct {
		Hostname string                                                                   `json:"hostname"`
		Version  string                                                                   `json:"guestAgentVersion"`
		OS       struct{ Name, Version, PrettyName, KernelRelease, KernelVersion string } `json:"os"`
		Commands []struct {
			Name    string
			Enabled bool
		} `json:"supportedCommands"`
	}
	if err = n.readGuestSubresource(ctx, v, "guestosinfo", &guest); err != nil {
		return n.fencedRuntime(ctx, v, vmi, out)
	}
	out.State = "AVAILABLE"
	out.Hostname, out.AgentVersion = guest.Hostname, guest.Version
	out.OSName = guest.OS.PrettyName
	if out.OSName == "" {
		out.OSName = guest.OS.Name
	}
	out.OSVersion, out.KernelRelease, out.KernelVersion = guest.OS.Version, guest.OS.KernelRelease, guest.OS.KernelVersion
	out.FilesystemsState = "UNSUPPORTED"
	if len(guest.Commands) == 0 {
		out.FilesystemsState = "UNAVAILABLE"
	}
	for _, command := range guest.Commands {
		if command.Name != "guest-get-fsinfo" || !command.Enabled {
			continue
		}
		var files struct {
			Items []struct {
				DiskName   string  `json:"diskName"`
				MountPoint string  `json:"mountPoint"`
				Type       string  `json:"fileSystemType"`
				Total      *uint64 `json:"totalBytes"`
				Used       *uint64 `json:"usedBytes"`
			} `json:"items"`
		}
		out.FilesystemsState = "UNAVAILABLE"
		if n.readGuestSubresource(ctx, v, "filesystemlist", &files) == nil {
			out.FilesystemsState = "AVAILABLE"
			for _, fs := range files.Items {
				out.Filesystems = append(out.Filesystems, biz.GuestFilesystem{DiskName: fs.DiskName, MountPoint: fs.MountPoint, FileSystemType: fs.Type, TotalBytes: fs.Total, UsedBytes: fs.Used})
			}
		}
		break
	}
	return n.fencedRuntime(ctx, v, vmi, out)
}

func (n *Native) fencedRuntime(ctx context.Context, v biz.VM, initial *unstructured.Unstructured, out biz.Runtime) (biz.Runtime, error) {
	current, err := n.runtimeVMI(ctx, v)
	if err != nil || current.GetUID() != initial.GetUID() {
		state := "CHANGED"
		if err != nil && !apierrors.IsNotFound(err) && !errors.Is(err, biz.ErrOwnership) {
			state = "UNAVAILABLE"
		}
		return biz.Runtime{State: state, AgentState: "UNKNOWN", FilesystemsState: "NOT_QUERIED", ObservedAt: time.Now().UTC()}, nil
	}
	out.ObservedAt = time.Now().UTC()
	return out, nil
}

func (n *Native) readGuestSubresource(ctx context.Context, v biz.VM, subresource string, out any) error {
	if n.control == nil {
		return biz.ErrDependency
	}
	stream, err := n.control.Get().SetHeader("Accept", "application/json").Namespace(v.Spec.Namespace).Resource("virtualmachineinstances").Name(v.Native.Name).SubResource(subresource).Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	const limit = 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(stream, limit+1))
	if err != nil {
		return err
	}
	if len(raw) > limit {
		return biz.ErrDependency
	}
	return json.Unmarshal(raw, out)
}
