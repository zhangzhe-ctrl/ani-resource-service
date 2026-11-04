package sandbox

import (
	"context"
	"github.com/google/uuid"
	"time"
)

// CheckpointCleanupWork holds a lease on an original Sandbox's checkpoint.
// The repository must atomically fence new transfer references before dispatch.
type CheckpointCleanupWork struct {
	Checkpoint Checkpoint
	Sandbox    Sandbox
	Owner      string
	Epoch      int64
	MutationID string
	Outcome    string
}
type CheckpointCleanupRepository interface {
	ClaimCheckpointCleanup(context.Context, string, time.Duration) (*CheckpointCleanupWork, error)
	PrepareCheckpointCleanup(context.Context, CheckpointCleanupWork, string) (bool, error)
	FinishCheckpointCleanup(context.Context, CheckpointCleanupWork, string, string) error
	ReleaseCheckpointCleanup(context.Context, CheckpointCleanupWork, time.Duration) error
}
type CheckpointCleanupProvider interface {
	DeleteCheckpointSnapshot(context.Context, Sandbox, Checkpoint) (string, error)
	ObserveCheckpointCleanup(context.Context, Sandbox, Checkpoint) (bool, error)
}
type CheckpointCleanupWorker struct {
	repo         CheckpointCleanupRepository
	provider     CheckpointCleanupProvider
	owner        string
	lease, retry time.Duration
}

func NewCheckpointCleanupWorker(repo CheckpointCleanupRepository, provider CheckpointCleanupProvider, owner string, lease, retry time.Duration) (*CheckpointCleanupWorker, error) {
	if repo == nil || provider == nil || owner == "" || lease <= 5*time.Second || retry <= 0 {
		return nil, ErrInvalid
	}
	return &CheckpointCleanupWorker{repo, provider, owner, lease, retry}, nil
}
func (w *CheckpointCleanupWorker) Step(ctx context.Context) (worked bool, result error) {
	work, err := w.repo.ClaimCheckpointCleanup(ctx, w.owner, w.lease)
	if err != nil || work == nil {
		return false, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := w.repo.ReleaseCheckpointCleanup(c, *work, w.retry); result == nil && err != nil {
			result = err
		}
	}()
	call, cancel := context.WithTimeout(ctx, w.lease-5*time.Second)
	defer cancel()
	persist := func(id, outcome string) error {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return w.repo.FinishCheckpointCleanup(c, *work, id, outcome)
	}
	if work.MutationID != "" {
		done, err := w.provider.ObserveCheckpointCleanup(call, work.Sandbox, work.Checkpoint)
		if err != nil || !done {
			return true, nil
		}
		return true, persist(work.MutationID, MutationApplied)
	}
	id := uuid.NewString()
	dispatch, err := w.repo.PrepareCheckpointCleanup(call, *work, id)
	if err != nil {
		return true, err
	}
	if !dispatch {
		return true, nil
	}
	outcome, err := w.provider.DeleteCheckpointSnapshot(call, work.Sandbox, work.Checkpoint)
	// An accepted DELETE is only a dispatch receipt; cleanup is confirmed by
	// later observation that exact Snapshot and Content have both disappeared.
	if outcome != MutationApplied && outcome != MutationNotSent && outcome != MutationRejected && outcome != MutationUnknown {
		outcome = MutationUnknown
	}
	if err != nil && outcome == MutationApplied {
		outcome = MutationUnknown
	}
	if outcome == MutationApplied {
		outcome = MutationUnknown
	}
	return true, persist(id, outcome)
}
