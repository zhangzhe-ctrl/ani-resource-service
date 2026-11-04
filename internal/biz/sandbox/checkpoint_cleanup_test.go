package sandbox

import (
	"context"
	"io"
	"testing"
	"time"
)

type cleanupFakeRepo struct {
	work                        *CheckpointCleanupWork
	prepared, finished, release int
	outcome                     string
}

func (r *cleanupFakeRepo) ClaimCheckpointCleanup(context.Context, string, time.Duration) (*CheckpointCleanupWork, error) {
	return r.work, nil
}
func (r *cleanupFakeRepo) PrepareCheckpointCleanup(_ context.Context, _ CheckpointCleanupWork, id string) (bool, error) {
	r.prepared++
	r.work.MutationID = id
	return true, nil
}
func (r *cleanupFakeRepo) FinishCheckpointCleanup(_ context.Context, _ CheckpointCleanupWork, id, outcome string) error {
	r.finished++
	r.outcome = outcome
	return nil
}
func (r *cleanupFakeRepo) ReleaseCheckpointCleanup(context.Context, CheckpointCleanupWork, time.Duration) error {
	r.release++
	return nil
}

type cleanupFakeProvider struct {
	writes, reads int
	done          bool
}

func (p *cleanupFakeProvider) DeleteCheckpointSnapshot(context.Context, Sandbox, Checkpoint) (string, error) {
	p.writes++
	return MutationUnknown, io.ErrUnexpectedEOF
}
func (p *cleanupFakeProvider) ObserveCheckpointCleanup(context.Context, Sandbox, Checkpoint) (bool, error) {
	p.reads++
	return p.done, nil
}
func TestCheckpointCleanupWorkerUnknownNeverResends(t *testing.T) {
	repo := &cleanupFakeRepo{work: &CheckpointCleanupWork{}}
	provider := &cleanupFakeProvider{}
	worker, err := NewCheckpointCleanupWorker(repo, provider, "worker", time.Minute, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 1 || repo.outcome != MutationUnknown || repo.work.MutationID == "" {
		t.Fatalf("first write=%d outcome=%s", provider.writes, repo.outcome)
	}
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 1 || provider.reads != 1 || repo.finished != 1 {
		t.Fatal("unknown cleanup was resent or marked complete")
	}
	provider.done = true
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 1 || repo.outcome != MutationApplied || repo.finished != 2 {
		t.Fatal("cleanup receipt not confirmed")
	}
}
