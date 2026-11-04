package sandbox

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type WorkspaceGenerationCleanup struct {
	TenantID, SandboxID, GenerationID, State, MutationID, Driver, VolumeHandle string
	Refs                                                                       ProviderRefs
}
type WorkspaceGenerationCleanupWork struct {
	Generation WorkspaceGenerationCleanup
	Sandbox    Sandbox
	LeaseOwner string
	LeaseEpoch int64
}
type WorkspaceGenerationObservation struct {
	PVCExists, PVExists, PodExists bool
	Driver, VolumeHandle           string
}
type WorkspaceGenerationCleanupRepository interface {
	ClaimWorkspaceGenerationCleanup(context.Context, string, time.Duration) (*WorkspaceGenerationCleanupWork, error)
	RecordWorkspaceGenerationSource(context.Context, WorkspaceGenerationCleanupWork, string, string) error
	PrepareWorkspaceGenerationDelete(context.Context, WorkspaceGenerationCleanupWork, string) (bool, error)
	FinishWorkspaceGenerationDelete(context.Context, WorkspaceGenerationCleanupWork, string, string) error
	FinishWorkspaceGenerationReclaim(context.Context, WorkspaceGenerationCleanupWork) error
	ReleaseWorkspaceGenerationCleanup(context.Context, WorkspaceGenerationCleanupWork, time.Duration) error
}
type WorkspaceGenerationCleanupProvider interface {
	ObserveWorkspaceGeneration(context.Context, Sandbox, WorkspaceGenerationCleanup) (WorkspaceGenerationObservation, error)
	DeleteWorkspaceGenerationPVC(context.Context, Sandbox, WorkspaceGenerationCleanup, string) (string, error)
}
type WorkspaceGenerationCleanupWorker struct {
	repo         WorkspaceGenerationCleanupRepository
	provider     WorkspaceGenerationCleanupProvider
	owner        string
	lease, retry time.Duration
}

func NewWorkspaceGenerationCleanupWorker(repo WorkspaceGenerationCleanupRepository, provider WorkspaceGenerationCleanupProvider, owner string, lease, retry time.Duration) (*WorkspaceGenerationCleanupWorker, error) {
	if repo == nil || provider == nil || owner == "" || lease <= 5*time.Second || retry <= 0 {
		return nil, ErrInvalid
	}
	return &WorkspaceGenerationCleanupWorker{repo, provider, owner, lease, retry}, nil
}
func (w *WorkspaceGenerationCleanupWorker) Step(ctx context.Context) (worked bool, result error) {
	work, err := w.repo.ClaimWorkspaceGenerationCleanup(ctx, w.owner, w.lease)
	if err != nil || work == nil {
		return false, err
	}
	defer func() {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e := w.repo.ReleaseWorkspaceGenerationCleanup(pctx, *work, w.retry); result == nil && e != nil {
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
	g := work.Generation
	obs, err := w.provider.ObserveWorkspaceGeneration(callCtx, work.Sandbox, g)
	if err != nil || obs.PodExists {
		return true, nil
	}
	if g.State == "" {
		// Before the first provider write both identities and CSI provenance
		// must be observable. Missing resources are not a cleanup receipt.
		if !obs.PVCExists || !obs.PVExists || obs.Driver == "" || obs.VolumeHandle == "" {
			return true, nil
		}
		return true, persist(func(pctx context.Context) error {
			return w.repo.RecordWorkspaceGenerationSource(pctx, *work, obs.Driver, obs.VolumeHandle)
		})
	}
	if g.Driver == "" || g.VolumeHandle == "" {
		return true, ErrConflict
	}
	if g.State == "ready" {
		if !obs.PVCExists {
			return true, nil
		}
		id := uuid.NewString()
		dispatch, err := w.repo.PrepareWorkspaceGenerationDelete(callCtx, *work, id)
		if err != nil {
			return true, err
		}
		if !dispatch {
			return true, nil
		}
		outcome := MutationNotSent
		var callErr error
		if callCtx.Err() == nil {
			outcome, callErr = w.provider.DeleteWorkspaceGenerationPVC(callCtx, work.Sandbox, g, id)
		}
		if (outcome != MutationApplied && outcome != MutationNotSent && outcome != MutationRejected && outcome != MutationUnknown) || (outcome == MutationApplied && callErr != nil) {
			outcome = MutationUnknown
		}
		return true, persist(func(pctx context.Context) error {
			return w.repo.FinishWorkspaceGenerationDelete(pctx, *work, id, outcome)
		})
	}
	if g.State == "unknown" || g.State == "observing" {
		// A late UID-preconditioned delete is harmless after that UID is gone,
		// but the backing PV must also be confirmed gone before reclaim.
		if !obs.PVCExists && !obs.PVExists {
			return true, persist(func(pctx context.Context) error { return w.repo.FinishWorkspaceGenerationReclaim(pctx, *work) })
		}
		return true, nil
	}
	return true, ErrConflict
}
