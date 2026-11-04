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

// Transfer is an isolated candidate; it must not be exposed until the ordinary
// Sandbox lifecycle and provider enforce its active-work and generation fences.
type CheckpointTransfer struct {
	TenantID, ID, OperationID, CheckpointID, SourceSandboxID, TargetSandboxID, Kind string
	State, Reason                                                                   string
	ExpectedVersion                                                                 int64
	SourceExpectedVersion                                                           int64
	SourceSnapshotName, SourceSnapshotUID, SourceContentUID, SourceSnapshotHandle   string
	OriginalRefs                                                                    ProviderRefs
	CandidateName, CandidateUID, CandidatePVName, CandidatePVUID, MutationID        string
	CreatedAt                                                                       time.Time
	CompletedAt                                                                     *time.Time
	Replayed                                                                        bool
}
type CheckpointTransferCommand struct {
	TenantID, TransferID, OperationID, CheckpointID, SourceSandboxID, TargetSandboxID, Kind, IdempotencyKey string
	ExpectedVersion                                                                                         int64
}

// CheckpointWorkspaceName preserves the complete Sandbox identity in the PVC
// name so the existing per-Sandbox Pod admission can enforce ownership. The
// database enforces uniqueness for the shortened transfer suffix.
func CheckpointWorkspaceName(sandboxID, transferID string) (string, error) {
	if !UUID(sandboxID) || !UUID(transferID) {
		return "", ErrInvalid
	}
	return "workspace-" + sandboxID + "-" + strings.ReplaceAll(transferID, "-", "")[:16], nil
}

func ValidWorkspaceName(sandboxID, name string) bool {
	if !UUID(sandboxID) {
		return false
	}
	base := "workspace-" + sandboxID
	if name == base {
		return true
	}
	if len(name) != len(base)+17 || !strings.HasPrefix(name, base+"-") {
		return false
	}
	for _, ch := range name[len(base)+1:] {
		if !(ch >= '0' && ch <= '9') && !(ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func CheckpointTransferDigest(c CheckpointTransferCommand) (string, error) {
	if !UUID(c.TenantID) || !UUID(c.TransferID) || !UUID(c.OperationID) || !UUID(c.CheckpointID) || !UUID(c.SourceSandboxID) || !UUID(c.TargetSandboxID) || c.ExpectedVersion < 1 || (c.Kind != "restore" && c.Kind != "clone") || (c.Kind == "restore" && c.SourceSandboxID != c.TargetSandboxID) || (c.Kind == "clone" && c.SourceSandboxID == c.TargetSandboxID) || c.IdempotencyKey == "" || !utf8.ValidString(c.IdempotencyKey) || strings.ContainsRune(c.IdempotencyKey, 0) || len([]rune(c.IdempotencyKey)) > 256 {
		return "", ErrInvalid
	}
	raw, err := json.Marshal(struct {
		Version                             int
		Kind, CheckpointID, SourceSandboxID string
		ExpectedVersion                     int64
	}{1, c.Kind, c.CheckpointID, c.SourceSandboxID, c.ExpectedVersion})
	if err != nil {
		return "", ErrInvalid
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}

type CheckpointTransferWork struct {
	Transfer   CheckpointTransfer
	Sandbox    Sandbox
	LeaseOwner string
	LeaseEpoch int64
}
type CheckpointTransferReplayReader interface {
	LookupCheckpointTransfer(context.Context, CheckpointTransferCommand) (CheckpointTransfer, error)
}
type CheckpointTransferRejector interface {
	RejectCheckpointTransferWFFC(context.Context, CheckpointTransferCommand) (CheckpointTransfer, error)
}
type CheckpointTransferRepository interface {
	AcceptCheckpointTransfer(context.Context, CheckpointTransferCommand) (CheckpointTransfer, error)
	GetCheckpointTransfer(context.Context, string, string, string) (CheckpointTransfer, error)
	ClaimCheckpointTransfer(context.Context, string, time.Duration) (*CheckpointTransferWork, error)
	PrepareCheckpointTransferCreate(context.Context, CheckpointTransferWork, string) (bool, error)
	FinishCheckpointTransferCreate(context.Context, CheckpointTransferWork, string, string, string) error
	RecordCheckpointTransferPending(context.Context, CheckpointTransferWork, string) error
	SwitchCheckpointTransfer(context.Context, CheckpointTransferWork, string, string, string, string) error
	ReleaseCheckpointTransferLease(context.Context, CheckpointTransferWork, time.Duration) error
}
