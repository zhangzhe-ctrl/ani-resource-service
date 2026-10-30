package vm

import (
	"context"
	"time"
)

// RuntimePort reads the currently owned VMI without changing guest state.
type RuntimePort interface {
	ReadRuntime(context.Context, VM) (Runtime, error)
}

type Runtime struct {
	State, AgentState, FilesystemsState             string
	ObservedAt, StartedAt                           time.Time
	NodeName, Hostname, AgentVersion                string
	OSName, OSVersion, KernelRelease, KernelVersion string
	Interfaces                                      []GuestInterface
	Filesystems                                     []GuestFilesystem
}
type GuestInterface struct {
	Name, GuestName, MAC, InfoSource string
	Addresses                        []string
}
type GuestFilesystem struct {
	DiskName, MountPoint, FileSystemType string
	TotalBytes, UsedBytes                *uint64
}
type ResourceReference struct{ ID, Name, State string }
type Relations struct {
	VPC, Subnet    ResourceReference
	SecurityGroups []ResourceReference
}
type RelationsPort interface {
	ReadRelations(context.Context, VM) Relations
}
