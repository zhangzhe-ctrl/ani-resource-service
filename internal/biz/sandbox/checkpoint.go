package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// CheckpointSource records identities, rather than relying on reusable names.
// CSI fields are established by the provider only after source protection.
type CheckpointSource struct {
	ClusterID, Namespace, NamespaceUID                                                     string
	PVCName, PVCUID, PVName, PVUID                                                         string
	CSIDriver, VolumeHandle, SnapshotClass, SnapshotClassUID, SnapshotClassResourceVersion string
}

type Checkpoint struct {
	TenantID, SandboxID, ID, OperationID, Name                         string
	State, Phase, Reason, FailureReason                                string
	Source                                                             CheckpointSource
	SnapshotName, SnapshotUID, ContentName, ContentUID, SnapshotHandle string
	MutationID, MutationKind, MutationState                            string
	ProtectionMutationID, SnapshotMutationID, ReleaseMutationID        string
	CreatedAt                                                          time.Time
	CompletedAt                                                        *time.Time
	Replayed                                                           bool
}

type CheckpointCommand struct {
	TenantID, SandboxID, CheckpointID, OperationID string
	Name, IdempotencyKey                           string
	ExpectedVersion                                int64
}

func CheckpointRequestDigest(c CheckpointCommand) (string, error) {
	if !UUID(c.TenantID) || !UUID(c.SandboxID) || !UUID(c.CheckpointID) || !UUID(c.OperationID) || c.ExpectedVersion < 1 ||
		c.Name == "" || len([]rune(c.Name)) > 128 || !utf8.ValidString(c.Name) || strings.ContainsRune(c.Name, 0) ||
		c.IdempotencyKey == "" || len([]rune(c.IdempotencyKey)) > 256 || !utf8.ValidString(c.IdempotencyKey) || strings.ContainsRune(c.IdempotencyKey, 0) {
		return "", ErrInvalid
	}
	b, err := json.Marshal(struct {
		Version         int
		ExpectedVersion int64
		Name            string
	}{1, c.ExpectedVersion, c.Name})
	if err != nil {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

type CheckpointRepository interface {
	AcceptCheckpoint(context.Context, CheckpointCommand) (Checkpoint, error)
	GetCheckpoint(context.Context, string, string, string) (Checkpoint, error)
	ListCheckpoints(context.Context, string, string, uint32, uint32) ([]Checkpoint, int64, error)
}

// CheckpointObservation is a current provider observation of the precise
// snapshot and content UIDs persisted in the checkpoint ledger.
type CheckpointObservation struct {
	State, Reason string
}

type CheckpointObserver interface {
	ObserveRecordedCheckpoint(context.Context, Sandbox, Checkpoint) (CheckpointObservation, error)
}

// CheckpointWork is an internal worker lease. A provider mutation is sent only
// after PrepareCheckpointMutation returns dispatch=true, at most once.
type CheckpointWork struct {
	Checkpoint Checkpoint
	Sandbox    Sandbox
	LeaseOwner string
	LeaseEpoch int64
}

type CheckpointMutation struct {
	ID, Kind string
}

type CheckpointWorkerRepository interface {
	ClaimCheckpoint(context.Context, string, time.Duration) (*CheckpointWork, error)
	RecordCheckpointSource(context.Context, CheckpointWork, CheckpointSource) error
	FailCheckpointPreflight(context.Context, CheckpointWork, string) error
	RecordCheckpointPendingReason(context.Context, CheckpointWork, string) error
	PrepareCheckpointMutation(context.Context, CheckpointWork, CheckpointMutation) (bool, error)
	FinishCheckpointMutation(context.Context, CheckpointWork, CheckpointMutation, string, string) error
	RecordCheckpointReady(context.Context, CheckpointWork, string, string, string, string) error
	ReleaseCheckpointLease(context.Context, CheckpointWork, time.Duration) error
}
