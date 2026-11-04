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
func TestCheckpointCleanupWorkerRetriesJournaledUIDDelete(t *testing.T) {
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
	if provider.writes != 2 || provider.reads != 1 || repo.finished != 1 {
		t.Fatal("unknown cleanup was not retried or was marked complete")
	}
	provider.done = true
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 2 || repo.outcome != MutationApplied || repo.finished != 2 {
		t.Fatal("cleanup receipt not confirmed")
	}
}

// The original process may die after Prepare commits and before DELETE.
func TestCheckpointCleanupRestartAfterPrepare(t *testing.T) {
	repo := &cleanupFakeRepo{work: &CheckpointCleanupWork{MutationID: "journaled-delete"}}
	provider := &cleanupFakeProvider{}
	worker, err := NewCheckpointCleanupWorker(repo, provider, "restarted", time.Minute, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.writes != 1 || repo.prepared != 0 || repo.finished != 0 {
		t.Fatalf("recovery did not reuse journal: %+v %+v", provider, repo)
	}
	provider.done = true
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.outcome != MutationApplied || provider.writes != 1 {
		t.Fatalf("recovery not confirmed: %+v %+v", provider, repo)
	}
}
