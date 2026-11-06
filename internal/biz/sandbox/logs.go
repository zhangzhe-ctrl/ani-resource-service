package sandbox

import (
	"context"
	"time"
)

// LogOptions selects the current Pod's container log. Since is inclusive:
// consumers must tolerate duplicate lines at the boundary on reconnect.
// A resume must bind the cursor to the same Pod UID returned by READY.
type LogOptions struct {
	TailLines uint32
	Follow    bool
	Since     *time.Time
	PodUID    string
}

type LogFrame struct {
	Kind, Reason, PodUID string
	Data                 []byte
}

// LogReader streams timestamped Kubernetes container stdout/stderr. It does
// not read code-run result output or retain logs after the Pod disappears.
type LogReader interface {
	StreamLogs(context.Context, Sandbox, LogOptions, func(LogFrame) error) error
}
