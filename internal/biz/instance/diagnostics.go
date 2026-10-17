package instance

import (
	"context"
	"time"
)

var DefaultLogLimits = LogLimits{FrameBytes: 16 * 1024, BufferBytes: 256 * 1024, OpenTimeoutSeconds: 15, WriteTimeoutSeconds: 10, LifetimeSeconds: 600, TargetPollSeconds: 2, ProcessConnections: 100, TenantConnections: 10, InstanceConnections: 3}

type LogLimits struct {
	FrameBytes, BufferBytes                                                     int
	OpenTimeoutSeconds, WriteTimeoutSeconds, LifetimeSeconds, TargetPollSeconds int
	ProcessConnections, TenantConnections, InstanceConnections                  int
}

func (v LogLimits) Defaults() LogLimits {
	d := DefaultLogLimits
	if v.FrameBytes > 0 {
		d.FrameBytes = v.FrameBytes
	}
	if v.BufferBytes > 0 {
		d.BufferBytes = v.BufferBytes
	}
	if v.OpenTimeoutSeconds > 0 {
		d.OpenTimeoutSeconds = v.OpenTimeoutSeconds
	}
	if v.WriteTimeoutSeconds > 0 {
		d.WriteTimeoutSeconds = v.WriteTimeoutSeconds
	}
	if v.LifetimeSeconds > 0 {
		d.LifetimeSeconds = v.LifetimeSeconds
	}
	if v.TargetPollSeconds > 0 {
		d.TargetPollSeconds = v.TargetPollSeconds
	}
	if v.ProcessConnections > 0 {
		d.ProcessConnections = v.ProcessConnections
	}
	if v.TenantConnections > 0 {
		d.TenantConnections = v.TenantConnections
	}
	if v.InstanceConnections > 0 {
		d.InstanceConnections = v.InstanceConnections
	}
	return d
}

type EnvironmentSource struct{ Name, SourceType string }
type RuntimeMount struct {
	Name, VolumeType, MountPath string
	ReadOnly                    bool
}
type RuntimePod struct {
	Name, UID, NodeName, PodIP, Phase, Reason, Message string
	Ready                                              bool
	EnvironmentSources                                 []EnvironmentSource
	Mounts                                             []RuntimeMount
}
type RuntimeStatefulSet struct{ DesiredReplicas, ReadyReplicas *int32 }
type Runtime struct {
	ObservedAt  time.Time
	Pod         *RuntimePod
	StatefulSet *RuntimeStatefulSet
}
type PodEvent struct {
	UID, Type, Reason, Message string
	LastOccurredAt             *time.Time
	Count                      *int64
}
type Events struct {
	PodUID     *string
	ObservedAt time.Time
	Items      []PodEvent
	Truncated  bool
}
type LogFrame struct {
	Kind   string
	Data   []byte
	Reason string
}
type Diagnostics interface {
	ReadRuntime(context.Context, Instance) (Runtime, error)
	ReadEvents(context.Context, Instance) (Events, error)
	StreamLogs(context.Context, Instance, uint32, bool, func(LogFrame) error) error
}

func ValidateLogLimits(v LogLimits) error {
	d := v.Defaults()
	if d.FrameBytes < 1 || d.FrameBytes > d.BufferBytes/3 {
		return ErrInvalid
	}
	return nil
}
