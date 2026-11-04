package sandbox

import (
	"context"
	"testing"
	"time"
)

type testPreviewFenceRepo struct {
	*workerRepo
	needed bool
}

type testPreviewWorkRunner struct{ calls int }

func (r *testPreviewWorkRunner) StepExperimentalPreview(context.Context, Work) error {
	r.calls++
	return nil
}

func TestPreviewWorkerRunsBeforeLifecycleFence(t *testing.T) {
	base := &workerRepo{work: &Work{Sandbox: Sandbox{Desired: DesiredPaused, Version: 1, LastOperationID: "11111111-1111-4111-8111-111111111111"}}}
	repo := &testPreviewFenceRepo{workerRepo: base, needed: true}
	provider := &workerProvider{repo: base}
	w, err := NewWorker(repo, provider, "worker", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runner := &testPreviewWorkRunner{}
	w.ConfigurePreviewWorkRunner(runner)
	step(t, w)
	if runner.calls != 1 || base.progress.Sandbox.Reason != "PREVIEW_CLEANUP_PENDING" {
		t.Fatalf("calls=%d progress=%+v", runner.calls, base.progress)
	}
}

func (r *testPreviewFenceRepo) ReconcilePreviewLifecycleFence(context.Context, Work) (bool, error) {
	return r.needed, nil
}
func TestPreviewFenceBlocksLifecycleCompletionUntilCleanup(t *testing.T) {
	for _, desired := range []string{DesiredPaused, DesiredDeleted} {
		t.Run(desired, func(t *testing.T) {
			base := &workerRepo{work: &Work{Sandbox: Sandbox{Desired: desired, Version: 1, LastOperationID: "11111111-1111-4111-8111-111111111111"}}}
			repo := &testPreviewFenceRepo{workerRepo: base, needed: true}
			provider := &workerProvider{repo: base, observeCall: func(context.Context, Sandbox) (Observation, error) {
				t.Fatal("provider observed before preview cleanup")
				return Observation{}, nil
			}}
			worker, err := NewWorker(repo, provider, "worker", 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			step(t, worker)
			if base.progress.Sandbox.Observed != ObservedUnknown || base.progress.Sandbox.Reason != "PREVIEW_CLEANUP_PENDING" || len(provider.calls) != 0 {
				t.Fatalf("unsafe lifecycle completion: %+v", base.progress)
			}
		})
	}
}
