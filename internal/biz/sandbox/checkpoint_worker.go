package sandbox

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// The provider must preserve the source guard until snapshot readiness has
// been positively established. Observation never treats a missing object as
// evidence that an outstanding create/protect call was not sent.
type CheckpointProvider interface {
	PreflightCheckpoint(context.Context, Sandbox, Checkpoint) (CheckpointSource, error)
	ApplyCheckpointMutation(context.Context, Sandbox, Checkpoint, CheckpointMutation) (CheckpointMutationReceipt, error)
	ObserveCheckpointMutation(context.Context, Sandbox, Checkpoint, CheckpointMutation) (CheckpointMutationReceipt, error)
	ObserveCheckpointReady(context.Context, Sandbox, Checkpoint) (CheckpointReadyObservation, error)
}
type CheckpointMutationReceipt struct {
	Mutation     CheckpointMutation
	Outcome, UID string
}
type CheckpointReadyObservation struct {
	Ready                                                bool
	SnapshotUID, ContentName, ContentUID, SnapshotHandle string
	Reason                                               string
}

func checkpointObservationErrorReason(err error) string {
	switch {
	case errors.Is(err, ErrDenied):
		return "SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED"
	case errors.Is(err, ErrOwnership), errors.Is(err, ErrClusterMismatch):
		return "SANDBOX_CHECKPOINT_PROVIDER_IDENTITY_MISMATCH"
	default:
		return ""
	}
}

type CheckpointWorker struct {
	repo         CheckpointWorkerRepository
	provider     CheckpointProvider
	owner        string
	lease, retry time.Duration
}

func NewCheckpointWorker(repo CheckpointWorkerRepository, provider CheckpointProvider, owner string, lease, retry time.Duration) (*CheckpointWorker, error) {
	if repo == nil || provider == nil || owner == "" || lease <= 5*time.Second || retry <= 0 {
		return nil, ErrInvalid
	}
	return &CheckpointWorker{repo: repo, provider: provider, owner: owner, lease: lease, retry: retry}, nil
}

// Step executes at most one provider action per lease. It never redispatches a
// previously journaled write. Unknown state remains durable across restarts.
func (w *CheckpointWorker) Step(ctx context.Context) (worked bool, result error) {
	work, err := w.repo.ClaimCheckpoint(ctx, w.owner, w.lease)
	if err != nil || work == nil {
		return false, err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if releaseErr := w.repo.ReleaseCheckpointLease(releaseCtx, *work, w.retry); result == nil && releaseErr != nil {
			result = releaseErr
		}
	}()
	callCtx, cancel := context.WithTimeout(ctx, w.lease-5*time.Second)
	defer cancel()
	persist := func(f func(context.Context) error) error {
		pctx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		return f(pctx)
	}
	c := work.Checkpoint
	if c.MutationID != "" {
		m := CheckpointMutation{ID: c.MutationID, Kind: c.MutationKind}
		receipt, observeErr := w.provider.ObserveCheckpointMutation(callCtx, work.Sandbox, c, m)
		if observeErr != nil {
			if reason := checkpointObservationErrorReason(observeErr); reason != "" {
				return true, persist(func(pctx context.Context) error { return w.repo.RecordCheckpointPendingReason(pctx, *work, reason) })
			}
			return true, nil
		}
		if receipt.Mutation != m {
			return true, nil
		}
		if receipt.Outcome != MutationApplied || receipt.UID == "" {
			return true, persist(func(pctx context.Context) error { return w.repo.RecordCheckpointPendingReason(pctx, *work, "") })
		}
		return true, persist(func(pctx context.Context) error {
			return w.repo.FinishCheckpointMutation(pctx, *work, m, MutationApplied, receipt.UID)
		})
	}
	switch c.Phase {
	case "preflight":
		source, err := w.provider.PreflightCheckpoint(callCtx, work.Sandbox, c)
		if err != nil {
			if reason := checkpointObservationErrorReason(err); reason == "SANDBOX_CHECKPOINT_PROVIDER_ACCESS_DENIED" {
				return true, persist(func(pctx context.Context) error { return w.repo.RecordCheckpointPendingReason(pctx, *work, reason) })
			}
			reason := ""
			if errors.Is(err, ErrUnsupported) {
				reason = "SANDBOX_CHECKPOINT_UNSUPPORTED"
			} else if errors.Is(err, ErrOwnership) || errors.Is(err, ErrClusterMismatch) {
				reason = "SANDBOX_CHECKPOINT_OWNERSHIP_CONFLICT"
			}
			if reason == "" {
				return true, nil
			}
			return true, persist(func(pctx context.Context) error { return w.repo.FailCheckpointPreflight(pctx, *work, reason) })
		}
		return true, persist(func(pctx context.Context) error { return w.repo.RecordCheckpointSource(pctx, *work, source) })
	case "observe":
		obs, err := w.provider.ObserveCheckpointReady(callCtx, work.Sandbox, c)
		if err != nil {
			if reason := checkpointObservationErrorReason(err); reason != "" {
				return true, persist(func(pctx context.Context) error { return w.repo.RecordCheckpointPendingReason(pctx, *work, reason) })
			}
			return true, nil
		}
		if !obs.Ready {
			if obs.Reason == "CSI_SNAPSHOT_LAST_ERROR" {
				return true, persist(func(pctx context.Context) error { return w.repo.RecordCheckpointPendingReason(pctx, *work, obs.Reason) })
			}
			if obs.Reason == "" {
				return true, persist(func(pctx context.Context) error { return w.repo.RecordCheckpointPendingReason(pctx, *work, "") })
			}
			return true, nil
		}
		if obs.SnapshotUID == "" || obs.ContentName == "" || obs.ContentUID == "" || obs.SnapshotHandle == "" {
			return true, nil
		}
		return true, persist(func(pctx context.Context) error {
			return w.repo.RecordCheckpointReady(pctx, *work, obs.SnapshotUID, obs.ContentName, obs.ContentUID, obs.SnapshotHandle)
		})
	case "protect", "create", "release":
		m := CheckpointMutation{ID: uuid.NewString(), Kind: c.Phase}
		dispatch, err := w.repo.PrepareCheckpointMutation(callCtx, *work, m)
		if err != nil {
			return true, err
		}
		if !dispatch {
			return true, nil
		}
		receipt := CheckpointMutationReceipt{Mutation: m, Outcome: MutationNotSent}
		var providerErr error
		if callCtx.Err() == nil {
			receipt, providerErr = w.provider.ApplyCheckpointMutation(callCtx, work.Sandbox, c, m)
		}
		if receipt.Mutation != m || (receipt.Outcome != MutationApplied && receipt.Outcome != MutationNotSent && receipt.Outcome != MutationRejected && receipt.Outcome != MutationUnknown) || (receipt.Outcome == MutationApplied && (receipt.UID == "" || providerErr != nil)) {
			receipt = CheckpointMutationReceipt{Mutation: m, Outcome: MutationUnknown}
		}
		return true, persist(func(pctx context.Context) error {
			return w.repo.FinishCheckpointMutation(pctx, *work, m, receipt.Outcome, receipt.UID)
		})
	default:
		return true, ErrInvalid
	}
}
