//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"sync"
	"testing"
)

func fileFixture(t *testing.T) (*sandboxFixture, context.Context, biz.FileCommand) {
	f, ctx, c := codeRunFixture(t)
	return f, ctx, biz.FileCommand{TenantID: c.TenantID, SandboxID: c.SandboxID, OperationID: uuid.NewString(), IdempotencyKey: "file-key", ExpectedVersion: 1, Request: biz.FileRequest{Action: "write", Path: "hello", ContentBase64: "aGVsbG8="}}
}
func TestFileDurableDispatchAndTenantScope(t *testing.T) {
	f, ctx, c := fileFixture(t)
	const n = 10
	type result struct {
		intent biz.FileIntent
		err    error
	}
	ch := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := c
			copy.OperationID = uuid.NewString()
			v, e := f.repo.AcceptFileOperation(ctx, copy)
			ch <- result{v, e}
		}()
	}
	wg.Wait()
	close(ch)
	id := ""
	created := 0
	for r := range ch {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if id == "" {
			id = r.intent.OperationID
		}
		if id != r.intent.OperationID {
			t.Fatal("different operations for same key")
		}
		if !r.intent.Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	c.OperationID = id
	copy := c
	copy.Request.ContentBase64 = "b3RoZXI="
	if _, err := f.repo.AcceptFileOperation(ctx, copy); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("conflicting replay=%v", err)
	}
	sends := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, send, err := f.repo.BeginFileOperation(ctx, c)
			if err != nil {
				t.Errorf("begin: %v", err)
			}
			sends <- send
		}()
	}
	wg.Wait()
	close(sends)
	count := 0
	for sent := range sends {
		if sent {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("dispatch count=%d", count)
	}
	if err := f.repo.FinishFileOperation(ctx, c.TenantID, c.SandboxID, id, "unknown", biz.FileResult{}); err != nil {
		t.Fatal(err)
	}
	v, send, err := f.repo.BeginFileOperation(ctx, c)
	if err != nil || send || v.State != "unknown" {
		t.Fatalf("unknown resent: %+v %v %v", v, send, err)
	}
	var unknownHasCompletion bool
	if err := f.runtime.QueryRow(ctx, `SELECT completed_at IS NOT NULL FROM sandbox.file_operations WHERE tenant_id=$1 AND operation_id=$2`, c.TenantID, id).Scan(&unknownHasCompletion); err != nil || unknownHasCompletion {
		t.Fatalf("unknown completion=%v err=%v", unknownHasCompletion, err)
	}
	other := uuid.NewString()
	otherCtx := biz.WithCaller(context.Background(), biz.Caller{TenantID: other, Actor: "user"})
	if _, err = f.repo.GetFileOperation(otherCtx, other, c.SandboxID, id); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("cross-tenant: %v", err)
	}
}
func TestFilePauseBlocksDispatch(t *testing.T) {
	f, ctx, c := fileFixture(t)
	if _, err := f.repo.AcceptFileOperation(ctx, c); err != nil {
		t.Fatal(err)
	}
	pause := biz.Command{TenantID: c.TenantID, ID: c.SandboxID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "pause", RequestDigest: biz.ActionDigest("", 1, "pause"), ExpectedVersion: 1}
	if _, err := f.repo.Change(ctx, pause); err != nil {
		t.Fatal(err)
	}
	v, send, err := f.repo.BeginFileOperation(ctx, c)
	if err != nil || send || v.State != "cancelled" {
		t.Fatalf("pause: %+v %v %v", v, send, err)
	}
	var cancelledHasCompletion bool
	if err := f.runtime.QueryRow(ctx, `SELECT completed_at IS NOT NULL FROM sandbox.file_operations WHERE tenant_id=$1 AND operation_id=$2`, c.TenantID, c.OperationID).Scan(&cancelledHasCompletion); err != nil || !cancelledHasCompletion {
		t.Fatalf("cancelled completion=%v err=%v", cancelledHasCompletion, err)
	}
	c.OperationID = uuid.NewString()
	c.IdempotencyKey = "after-pause"
	c.ExpectedVersion = 0
	if _, err = f.repo.AcceptFileOperation(ctx, c); !errors.Is(err, biz.ErrDataPlaneUnavailable) {
		t.Fatalf("new after pause: %v", err)
	}
}

func TestFileConcurrentAcceptanceAndPause(t *testing.T) {
	f, ctx, c := fileFixture(t)
	pause := biz.Command{TenantID: c.TenantID, ID: c.SandboxID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "pause", RequestDigest: biz.ActionDigest("", 1, "pause"), ExpectedVersion: 1}
	start := make(chan struct{})
	accepted := make(chan error, 1)
	paused := make(chan error, 1)
	go func() { <-start; _, err := f.repo.AcceptFileOperation(ctx, c); accepted <- err }()
	go func() { <-start; _, err := f.repo.Change(ctx, pause); paused <- err }()
	close(start)
	a, p := <-accepted, <-paused
	if p != nil || (a != nil && !errors.Is(a, biz.ErrVersion) && !errors.Is(a, biz.ErrDataPlaneUnavailable)) {
		t.Fatalf("accept=%v pause=%v", a, p)
	}
	if a == nil {
		intent, send, err := f.repo.BeginFileOperation(ctx, c)
		if err != nil || send || intent.State != "cancelled" {
			t.Fatalf("after pause: %+v %v %v", intent, send, err)
		}
	}
	c.OperationID = uuid.NewString()
	c.IdempotencyKey = "after"
	c.ExpectedVersion = 0
	if _, err := f.repo.AcceptFileOperation(ctx, c); !errors.Is(err, biz.ErrDataPlaneUnavailable) {
		t.Fatalf("new after pause: %v", err)
	}
}

func TestFileOperationHistoryPersistsActionAndConfirmedCompletion(t *testing.T) {
	f, ctx, c := fileFixture(t)
	for _, action := range []string{"write", "delete"} {
		c.OperationID = uuid.NewString()
		c.IdempotencyKey = action
		c.Request.Action = action
		if action == "delete" {
			c.Request.ContentBase64 = ""
		}
		if _, err := f.repo.AcceptFileOperation(ctx, c); err != nil {
			t.Fatal(action, err)
		}
		if _, send, err := f.repo.BeginFileOperation(ctx, c); err != nil || !send {
			t.Fatal(action, send, err)
		}
		if err := f.repo.FinishFileOperation(ctx, c.TenantID, c.SandboxID, c.OperationID, "", biz.FileResult{}); err != nil {
			t.Fatal(action, err)
		}
		var stored string
		var completed bool
		if err := f.runtime.QueryRow(ctx, `SELECT action,completed_at IS NOT NULL FROM sandbox.file_operations WHERE tenant_id=$1 AND operation_id=$2`, c.TenantID, c.OperationID).Scan(&stored, &completed); err != nil || stored != action || !completed {
			t.Fatalf("action=%s stored=%s completed=%v err=%v", action, stored, completed, err)
		}
	}
	items, total, err := f.repo.ListOperations(ctx, c.TenantID, c.SandboxID, 1, 20)
	if err != nil || total != 3 {
		t.Fatal(items, total, err)
	}
	seen := make(map[string]bool)
	for _, op := range items {
		if op.Action == "file_write" || op.Action == "file_delete" {
			seen[op.Action] = op.CompletedAt != nil
		}
	}
	if !seen["file_write"] || !seen["file_delete"] {
		t.Fatalf("file actions missing or uncompleted: %+v", items)
	}
}
