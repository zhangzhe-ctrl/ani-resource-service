package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// These errors certify a rejection committed in the Resource journal. They are
// distinct from ordinary preflight errors, which may not end a Gov intent.
var (
	ErrCloneRejectedNotFound = errors.New("SANDBOX_CLONE_REJECTED_NOT_FOUND")
	ErrCloneRejectedVersion  = errors.New("SANDBOX_CLONE_REJECTED_VERSION")
	ErrCloneRejectedConflict = errors.New("SANDBOX_CLONE_REJECTED_CONFLICT")
)

type CheckpointCloneRepository interface {
	LookupCheckpointClone(context.Context, CheckpointCloneCommand) (CheckpointTransfer, error)
	AcceptCheckpointClone(context.Context, CheckpointCloneCommand) (CheckpointTransfer, error)
}

type CheckpointCloneRejector interface {
	RejectCheckpointCloneWFFC(context.Context, CheckpointCloneCommand) (CheckpointTransfer, error)
}

// CheckpointClonePreflightRejector persists a deterministic post-journal
// admission failure after serializing with a competing acceptance.
type CheckpointClonePreflightRejector interface {
	RejectCheckpointClonePreflight(context.Context, CheckpointCloneCommand) (CheckpointTransfer, error)
}

// CheckpointCloneCommand carries a new independently owned Sandbox and the
// original caller intent digest. The inbound use case must resolve and authorize
// its image, compute and namespace exactly like normal Sandbox creation.
type CheckpointCloneCommand struct {
	Transfer              CheckpointTransferCommand
	Sandbox               Create
	SourceExpectedVersion int64
}

func CheckpointCloneDigest(c CheckpointCloneCommand) (string, error) {
	if _, err := CheckpointTransferDigest(c.Transfer); err != nil {
		return "", err
	}
	if c.Transfer.Kind != "clone" || c.Sandbox.TenantID != c.Transfer.TenantID || c.Sandbox.ID != c.Transfer.TargetSandboxID || c.Sandbox.InstanceID != "" || !UUID(c.Sandbox.OperationID) || c.Sandbox.OperationID == c.Transfer.OperationID || c.Transfer.ExpectedVersion != 1 || c.SourceExpectedVersion < 1 || c.Sandbox.IntentDigest == "" {
		return "", ErrInvalid
	}
	raw, err := json.Marshal(struct {
		Version                                                                                                                             int
		SourceSandboxID, CheckpointID, TargetSandboxID, TransferID, OperationID, CreateOperationID, CommandDigest, SpecDigest, IntentDigest string
		SourceExpectedVersion                                                                                                               int64
	}{1, c.Transfer.SourceSandboxID, c.Transfer.CheckpointID, c.Sandbox.ID, c.Transfer.TransferID, c.Transfer.OperationID, c.Sandbox.OperationID, c.Sandbox.RequestDigest, c.Sandbox.Spec.SpecDigest, c.Sandbox.IntentDigest, c.SourceExpectedVersion})
	if err != nil {
		return "", ErrInvalid
	}
	h := sha256.Sum256(append([]byte("sandbox-clone-accept-v1\n"), raw...))
	return hex.EncodeToString(h[:]), nil
}
