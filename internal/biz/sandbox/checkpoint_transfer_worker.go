package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// WFFC cannot bind a paused sandbox without a consumer. Until a safe
// consumer protocol exists, reject before acceptance and before PVC writes.
var ErrCheckpointWFFCUnsupported = fmt.Errorf("SANDBOX_TRANSFER_WFFC_UNSUPPORTED: %w", ErrUnsupported)

const MutationWFFCUnsupported = "wffc_unsupported"

type CheckpointTransferPreflight interface {
	ValidateCheckpointTransferStorage(context.Context, string) error
}

type CheckpointTransferObservation struct {
	Exists, Bound                           bool
	PVCName, PVCUID, PVName, PVUID          string
	SnapshotUID, ContentUID, SnapshotHandle string
	Reason                                  string
}

// The provider must verify tenant, immutable Namespace/PVC/PV identities,
// snapshot datasource, CSI driver and source before returning an observation.
type CheckpointTransferProvider interface {
	CreateCheckpointTransferPVC(context.Context, Sandbox, CheckpointTransfer, string) (string, string, error)
	ObserveCheckpointTransferPVC(context.Context, Sandbox, CheckpointTransfer) (CheckpointTransferObservation, error)
}
type CheckpointTransferWorker struct {
	repo         CheckpointTransferRepository
	provider     CheckpointTransferProvider
	owner        string
	lease, retry time.Duration
}

func NewCheckpointTransferWorker(repo CheckpointTransferRepository, provider CheckpointTransferProvider, owner string, lease, retry time.Duration) (*CheckpointTransferWorker, error) {
	if repo == nil || provider == nil || owner == "" || lease <= 5*time.Second || retry <= 0 {
		return nil, ErrInvalid
	}
	return &CheckpointTransferWorker{repo: repo, provider: provider, owner: owner, lease: lease, retry: retry}, nil
}
func (w *CheckpointTransferWorker) Step(ctx context.Context) (worked bool, result error) {
	work, err := w.repo.ClaimCheckpointTransfer(ctx, w.owner, w.lease)
	if err != nil || work == nil {
		return false, err
	}
	defer func() {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e := w.repo.ReleaseCheckpointTransferLease(pctx, *work, w.retry); result == nil && e != nil {
			result = e
		}
	}()
	callCtx, cancel := context.WithTimeout(ctx, w.lease-5*time.Second)
	defer cancel()
	persist := func(f func(context.Context) error) error {
		pctx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		return f(pctx)
	}
	t := work.Transfer
	if t.State == "accepted" {
		mutation := uuid.NewString()
		dispatch, err := w.repo.PrepareCheckpointTransferCreate(callCtx, *work, mutation)
		if err != nil {
			return true, err
		}
		if !dispatch {
			return true, nil
		}
		outcome, uid := MutationNotSent, ""
		var callErr error
		if callCtx.Err() == nil {
			outcome, uid, callErr = w.provider.CreateCheckpointTransferPVC(callCtx, work.Sandbox, t, mutation)
		}
		if outcome == MutationNotSent && errors.Is(callErr, ErrCheckpointWFFCUnsupported) {
			outcome = MutationWFFCUnsupported
		}
		if (outcome != MutationApplied && outcome != MutationNotSent && outcome != MutationRejected && outcome != MutationUnknown && outcome != MutationWFFCUnsupported) || (outcome == MutationApplied && (uid == "" || callErr != nil)) {
			outcome, uid = MutationUnknown, ""
		}
		return true, persist(func(pctx context.Context) error {
			return w.repo.FinishCheckpointTransferCreate(pctx, *work, mutation, outcome, uid)
		})
	}
	if t.State != "unknown" && t.State != "creating" && t.State != "observing" {
		return true, ErrInvalid
	}
	obs, err := w.provider.ObserveCheckpointTransferPVC(callCtx, work.Sandbox, t)
	if err != nil || !obs.Exists {
		return true, nil
	}
	if obs.PVCName != t.CandidateName || obs.PVCUID == "" || obs.SnapshotUID != t.SourceSnapshotUID || obs.ContentUID != t.SourceContentUID || obs.SnapshotHandle != t.SourceSnapshotHandle {
		return true, nil
	}
	if t.State == "unknown" || t.State == "creating" {
		return true, persist(func(pctx context.Context) error {
			return w.repo.FinishCheckpointTransferCreate(pctx, *work, t.MutationID, MutationApplied, obs.PVCUID)
		})
	}
	if !obs.Bound || obs.PVCUID != t.CandidateUID || obs.PVName == "" || obs.PVUID == "" {
		if obs.Reason == "SANDBOX_TRANSFER_WAIT_FOR_FIRST_CONSUMER" || obs.Reason == "SANDBOX_TRANSFER_PVC_PENDING" {
			return true, persist(func(pctx context.Context) error {
				return w.repo.RecordCheckpointTransferPending(pctx, *work, obs.Reason)
			})
		}
		return true, nil
	}
	return true, persist(func(pctx context.Context) error {
		return w.repo.SwitchCheckpointTransfer(pctx, *work, obs.PVCName, obs.PVCUID, obs.PVName, obs.PVUID)
	})
}
