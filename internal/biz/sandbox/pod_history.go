package sandbox

import (
	"context"
	"errors"
	"time"
)

// PodHistoryLimit bounds a complete identity snapshot. An overflow is an error,
// never a silently truncated list that consumers could interpret as complete.
const PodHistoryLimit = 1000

var ErrPodHistoryTooLarge = errors.New("SANDBOX_POD_HISTORY_TOO_LARGE")

// PodHistory is an attributable, durable provider identity. FirstRecordedAt
// is the database recording time, not a Pod start or termination timestamp.
type PodHistory struct {
	ClusterID, Namespace, NamespaceUID, PodName, PodUID string
	FirstRecordedAt                                     time.Time
}

type PodHistoryRepository interface {
	ListSandboxPodHistory(context.Context, string, string) ([]PodHistory, error)
}
