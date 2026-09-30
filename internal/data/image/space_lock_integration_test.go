//go:build imageintegration

package data_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
)

type spaceLockBarrierRepository struct {
	biz.LifecycleRepository
	entered chan struct{}
	release chan struct{}
}

func (r *spaceLockBarrierRepository) WithSpaceWriteLock(ctx context.Context, space string, fn func(context.Context) error) error {
	return r.LifecycleRepository.WithSpaceWriteLock(ctx, space, func(locked context.Context) error {
		r.entered <- struct{}{}
		select {
		case <-r.release:
			return fn(locked)
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

func assertNoSpaceLocks(t *testing.T, f *fixture) {
	t.Helper()
	var locks int
	err := f.Runtime.QueryRow(context.Background(), `SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND a.usename=current_user`).Scan(&locks)
	if err != nil || locks != 0 {
		t.Fatalf("residual advisory locks=%d err=%v", locks, err)
	}
}

func TestSpaceLockEightSpacesLifecycleProgress(t *testing.T) {
	f, registry, cfg, ring, _ := lifecycleFixture(t)
	const writers = 8 // Equal to the existing connection budget, not a larger pool.
	barrier := &spaceLockBarrierRepository{LifecycleRepository: f.Repo, entered: make(chan struct{}, writers), release: make(chan struct{})}
	l := lifecycle(t, barrier, registry, ring, cfg, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tenant := uuid.NewString()
			caller := biz.WithCaller(ctx, biz.Caller{Kind: biz.GovernanceCaller, Subject: "governance", Actor: "user:test", TenantID: tenant})
			input := biz.EnableSpace{TenantID: tenant, Slug: fmt.Sprintf("eight-%02d", i), IdempotencyKey: "eight-enable"}
			space, err := l.EnsureImageSpace(caller, input)
			if err == nil && (space.State != "available" || space.ProjectID <= 0) {
				err = fmt.Errorf("space did not become available")
			}
			if err == nil {
				// A fresh use case verifies durable replay after the first write.
				plain, e := biz.NewLifecycle(f.Repo, registry, ring, cfg, nil)
				if e == nil {
					_, e = plain.EnsureImageSpace(caller, input)
				}
				err = e
			}
			results <- err
		}()
	}
	entered := 0
	for entered < writers && ctx.Err() == nil {
		select {
		case <-barrier.entered:
			entered++
		case <-ctx.Done():
		}
	}
	// No query in any callback can run until all eight dedicated lock
	// connections are held. The old pool cannot then supply a ninth connection.
	close(barrier.release)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			t.Logf("writer result: %v", err)
		}
	}
	assertNoSpaceLocks(t, f)
	registry.mu.Lock()
	creates, robots, sets := registry.creates, registry.robotCreates, registry.secretSets
	registry.mu.Unlock()
	if entered != writers || success != writers || creates != writers || robots != writers || sets != writers {
		t.Fatalf("eight-space progress: entered=%d succeeded=%d projects=%d robots=%d secrets=%d", entered, success, creates, robots, sets)
	}
	var connections int
	if err := f.Runtime.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE usename=current_user AND pid<>pg_backend_pid()`).Scan(&connections); err != nil || connections > writers {
		t.Fatalf("connection budget exceeded: %d %v", connections, err)
	}
	// Close must return and leave no runtime connection other than this
	// independently owned observer; an unreturned connection would hang Close.
	f.Repo.Close()
	if err := f.Runtime.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE usename=current_user AND pid<>pg_backend_pid()`).Scan(&connections); err != nil || connections != 0 {
		t.Fatalf("connections remain after close: %d %v", connections, err)
	}
}

func TestSpaceLockCallbackFailureAndCancellation(t *testing.T) {
	for _, scenario := range []string{"callback_error", "request_cancel", "connection_lost"} {
		t.Run(scenario, func(t *testing.T) {
			f := database(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			space := reserve(t, f, uuid.NewString(), "t-lock-failure")
			failure := errors.New("injected callback failure")
			err := f.Repo.WithSpaceWriteLock(ctx, space.ID, func(locked context.Context) error {
				if _, err := f.Repo.FindTenantSpace(locked, space.TenantID); err != nil {
					return err
				}
				switch scenario {
				case "callback_error":
					return failure
				case "request_cancel":
					cancel()
					return locked.Err()
				case "connection_lost":
					// The runtime role is unique to this fixture. Terminate only
					// its one backend currently holding this test's advisory lock.
					var terminated bool
					return f.Runtime.QueryRow(context.Background(), `SELECT pg_terminate_backend(a.pid) FROM pg_stat_activity a WHERE a.usename=current_user AND a.pid<>pg_backend_pid() AND EXISTS(SELECT 1 FROM pg_locks l WHERE l.pid=a.pid AND l.locktype='advisory' AND l.granted)`).Scan(&terminated)
				}
				return nil
			})
			if err == nil || (scenario == "callback_error" && !errors.Is(err, failure)) || (scenario == "request_cancel" && !errors.Is(err, context.Canceled)) {
				t.Fatal("failure was swallowed", err)
			}
			assertNoSpaceLocks(t, f)
			if err = f.Repo.WithSpaceWriteLock(context.Background(), space.ID, func(locked context.Context) error {
				_, err := f.Repo.FindTenantSpace(locked, space.TenantID)
				return err
			}); err != nil {
				t.Fatal("follow-up operation could not progress", err)
			}
			assertNoSpaceLocks(t, f)
		})
	}
}
