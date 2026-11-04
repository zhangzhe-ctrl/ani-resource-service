//go:build sandboxintegration

package sandbox_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func TestExperimentalPreviewLedgerReplayAndLifecycleFence(t *testing.T) {
	f, ctx, base := codeRunFixture(t)
	c := biz.ExperimentalPreviewOpen{TenantID: base.TenantID, SandboxID: base.SandboxID, PreviewID: uuid.NewString(), OperationID: uuid.NewString(), IdempotencyKey: "open-key", ExpectedVersion: 1, Port: 8080, Protocol: "http", TTL: time.Minute}
	if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, c, false); !errors.Is(err, biz.ErrUnsupported) {
		t.Fatalf("disabled: %v", err)
	}
	const n = 8
	var wg sync.WaitGroup
	type outcome struct {
		r   biz.ExperimentalPreviewRecord
		err error
	}
	ch := make(chan outcome, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := c
			cmd.PreviewID = uuid.NewString()
			cmd.OperationID = uuid.NewString()
			r, err := f.repo.AcceptExperimentalPreviewOpen(ctx, cmd, true)
			ch <- outcome{r, err}
		}()
	}
	wg.Wait()
	close(ch)
	id := ""
	created := 0
	for result := range ch {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if id == "" {
			id = result.r.PreviewID
		}
		if result.r.PreviewID != id {
			t.Fatal("duplicate open")
		}
		if !result.r.Replayed {
			created++
		}
		if result.r.State != "queued" {
			t.Fatalf("premature status: %+v", result.r)
		}
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	changed := c
	changed.Port = 8081
	if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, changed, true); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("different input replay: %v", err)
	}
	other := c
	other.IdempotencyKey = "other"
	other.PreviewID = uuid.NewString()
	other.OperationID = uuid.NewString()
	if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, other, true); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("duplicate port: %v", err)
	}
	closeCmd := biz.ExperimentalPreviewClose{TenantID: c.TenantID, SandboxID: c.SandboxID, PreviewID: id, OperationID: uuid.NewString(), IdempotencyKey: "close-key", ExpectedVersion: 1}
	r, err := f.repo.AcceptExperimentalPreviewClose(ctx, closeCmd)
	if err != nil || r.Desired != "closed" || r.State != "queued" {
		t.Fatalf("close: %+v %v", r, err)
	}
	op, err := f.repo.GetExperimentalPreviewOperation(ctx, c.TenantID, c.SandboxID, closeCmd.OperationID)
	if err != nil || op.State != "queued" || op.Action != "close" || op.CompletedAt != nil {
		t.Fatalf("operation: %+v %v", op, err)
	}
	closeCmd.OperationID = uuid.NewString()
	r, err = f.repo.AcceptExperimentalPreviewClose(ctx, closeCmd)
	if err != nil || !r.Replayed {
		t.Fatalf("close replay: %+v %v", r, err)
	}
	items, total, err := f.repo.ListExperimentalPreviews(ctx, c.TenantID, c.SandboxID, 1, 20)
	if err != nil || total != 1 || len(items) != 1 || items[0].Desired != "closed" {
		t.Fatalf("list: %+v %d %v", items, total, err)
	}
	cross := biz.WithCaller(ctx, biz.Caller{TenantID: uuid.NewString(), Actor: "governance:user:9"})
	if _, err = f.repo.GetExperimentalPreview(cross, c.TenantID, c.SandboxID, id); !errors.Is(err, biz.ErrDenied) {
		t.Fatalf("cross tenant: %v", err)
	}
	second := c
	second.PreviewID, second.OperationID, second.IdempotencyKey, second.Port = uuid.NewString(), uuid.NewString(), "second", 8083
	if _, err = f.repo.AcceptExperimentalPreviewOpen(ctx, second, true); err != nil {
		t.Fatal(err)
	}
	pause := biz.Command{TenantID: c.TenantID, ID: c.SandboxID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "pause", ExpectedVersion: 1, RequestDigest: biz.ActionDigest("", 1, "pause")}
	if _, err = f.repo.Change(ctx, pause); err != nil {
		t.Fatal(err)
	}
	secondState, err := f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, second.PreviewID)
	if err != nil || secondState.Desired != "closed" {
		t.Fatalf("pause did not fence preview: %+v %v", secondState, err)
	}
	needed, err := f.repo.PreviewCleanupRequired(ctx, c.TenantID, c.SandboxID)
	if err != nil || !needed {
		t.Fatalf("lifecycle barrier missing: %t %v", needed, err)
	}
	after := c
	after.PreviewID = uuid.NewString()
	after.OperationID = uuid.NewString()
	after.IdempotencyKey = "after-pause"
	after.Port = 8082
	if _, err = f.repo.AcceptExperimentalPreviewOpen(ctx, after, true); !errors.Is(err, biz.ErrVersion) {
		t.Fatalf("stale version accepted: %v", err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.preview_ports SET state='unknown',pending_mutation=$3::jsonb WHERE tenant_id=$1 AND preview_id=$2`, c.TenantID, id, `{"ID":"late-create","Kind":"create","TargetUID":""}`); err != nil {
		t.Fatal(err)
	}
	work, err := f.repo.Claim(ctx, "preview-test-worker", time.Minute)
	if err != nil || work == nil {
		t.Fatalf("claim: %+v %v", work, err)
	}
	needed, err = f.repo.ReconcilePreviewLifecycleFence(ctx, *work)
	if err != nil || !needed {
		t.Fatalf("unknown create lost its safety barrier: %t %v", needed, err)
	}
	secondState, err = f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, second.PreviewID)
	if err != nil || secondState.State != "closed" {
		t.Fatalf("undispatched preview not closed: %+v %v", secondState, err)
	}
}

func TestExperimentalPreviewMutationJournal(t *testing.T) {
	f, ctx, base := codeRunFixture(t)
	c := biz.ExperimentalPreviewOpen{TenantID: base.TenantID, SandboxID: base.SandboxID, PreviewID: uuid.NewString(), OperationID: uuid.NewString(), IdempotencyKey: "open-journal", ExpectedVersion: 1, Port: 9090, Protocol: "tcp", TTL: time.Minute}
	if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	work, err := f.repo.Claim(ctx, "journal-test", time.Minute)
	if err != nil || work == nil {
		t.Fatalf("claim: %+v %v", work, err)
	}
	create := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, create, false); !errors.Is(err, biz.ErrUnsupported) {
		t.Fatalf("disabled prepare: %v", err)
	}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, create, true); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, create, true); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("double dispatch: %v", err)
	}
	if err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, create, "unknown", ""); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.ObserveExperimentalPreviewMutation(ctx, *work, c.PreviewID, create, false, "", false); err != nil {
		t.Fatal(err)
	}
	r, err := f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
	if err != nil || r.State != "unknown" || r.Pending == nil {
		t.Fatalf("unknown create lost: %+v %v", r, err)
	}
	if err = f.repo.ObserveExperimentalPreviewMutation(ctx, *work, c.PreviewID, create, true, "service-uid", false); err != nil {
		t.Fatal(err)
	}
	r, err = f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
	if err != nil || r.ServiceUID != "service-uid" || r.CreateMutationID != create.ID || r.Pending != nil {
		t.Fatalf("late receipt: %+v %v", r, err)
	}
	sliceCreate := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create_slice", TargetUID: "service-uid", PodIP: "10.5.1.2"}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, sliceCreate, true); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, sliceCreate, "unknown", ""); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.ObserveExperimentalPreviewMutation(ctx, *work, c.PreviewID, sliceCreate, false, "", false); err != nil {
		t.Fatal(err)
	}
	r, err = f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
	if err != nil || r.Pending == nil || r.PodIP != "10.5.1.2" {
		t.Fatalf("slice unknown lost: %+v %v", r, err)
	}
	if err = f.repo.ObserveExperimentalPreviewMutation(ctx, *work, c.PreviewID, sliceCreate, true, "slice-uid", false); err != nil {
		t.Fatal(err)
	}
	closeCmd := biz.ExperimentalPreviewClose{TenantID: c.TenantID, SandboxID: c.SandboxID, PreviewID: c.PreviewID, OperationID: uuid.NewString(), IdempotencyKey: "close-journal", ExpectedVersion: 1}
	if _, err = f.repo.AcceptExperimentalPreviewClose(ctx, closeCmd); err != nil {
		t.Fatal(err)
	}
	del := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "delete", TargetUID: "service-uid"}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, del, false); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("service deleted before slice: %v", err)
	}
	delSlice := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "delete_slice", TargetUID: "slice-uid"}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, delSlice, false); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, delSlice, "applied", ""); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.ObserveExperimentalPreviewMutation(ctx, *work, c.PreviewID, delSlice, false, "", false); !errors.Is(err, biz.ErrDataPlaneUnavailable) {
		t.Fatalf("slice cleared without receipt: %v", err)
	}
	if err = f.repo.ObserveExperimentalPreviewMutation(ctx, *work, c.PreviewID, delSlice, false, "", true); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, del, false); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, del, "applied", ""); err != nil {
		t.Fatal(err)
	}
	r, err = f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
	if err != nil || r.State != "closing" || r.Pending == nil {
		t.Fatalf("delete ack incorrectly completed: %+v %v", r, err)
	}
	if err = f.repo.ObserveExperimentalPreviewMutation(ctx, *work, c.PreviewID, del, false, "", false); !errors.Is(err, biz.ErrDataPlaneUnavailable) {
		t.Fatalf("closed without endpoint receipt: %v", err)
	}
	if err = f.repo.ObserveExperimentalPreviewMutation(ctx, *work, c.PreviewID, del, false, "", true); err != nil {
		t.Fatal(err)
	}
	op, err := f.repo.GetExperimentalPreviewOperation(ctx, c.TenantID, c.SandboxID, closeCmd.OperationID)
	if err != nil || op.State != "succeeded" || op.CompletedAt == nil {
		t.Fatalf("close operation: %+v %v", op, err)
	}
}

func TestExperimentalPreviewSchedulingAndExpiry(t *testing.T) {
	f, ctx, base := codeRunFixture(t)
	c := biz.ExperimentalPreviewOpen{TenantID: base.TenantID, SandboxID: base.SandboxID, PreviewID: uuid.NewString(), OperationID: uuid.NewString(), IdempotencyKey: "schedule", ExpectedVersion: 1, Port: 9091, Protocol: "tcp", TTL: time.Minute}
	if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	work, err := f.repo.Claim(ctx, "preview-schedule", time.Minute)
	if err != nil || work == nil {
		t.Fatalf("claim %v", err)
	}
	pending, err := f.repo.NextExperimentalPreviewWork(ctx, *work, true)
	if err != nil || pending == nil || pending.PreviewID != c.PreviewID {
		t.Fatalf("schedule: %+v %v", pending, err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.preview_ports SET expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND preview_id=$2`, c.TenantID, c.PreviewID); err != nil {
		t.Fatal(err)
	}
	pending, err = f.repo.NextExperimentalPreviewWork(ctx, *work, true)
	if err != nil || pending != nil {
		t.Fatalf("expired undispatched: %+v %v", pending, err)
	}
	state, err := f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
	if err != nil || state.State != "closed" || state.Reason != "CANCELLED_BEFORE_DISPATCH" {
		t.Fatalf("state: %+v %v", state, err)
	}
	op, err := f.repo.GetExperimentalPreviewOperation(ctx, c.TenantID, c.SandboxID, c.OperationID)
	if err != nil || op.State != "failed" {
		t.Fatalf("op: %+v %v", op, err)
	}
}

func TestExperimentalPreviewAddressUnderLifecycleLock(t *testing.T) {
	f, ctx, base := codeRunFixture(t)
	c := biz.ExperimentalPreviewOpen{TenantID: base.TenantID, SandboxID: base.SandboxID, PreviewID: uuid.NewString(), OperationID: uuid.NewString(), IdempotencyKey: "address", ExpectedVersion: 1, Port: 8080, Protocol: "http", TTL: time.Minute}
	if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	work, err := f.repo.Claim(ctx, "preview-address", time.Minute)
	if err != nil || work == nil {
		t.Fatalf("claim %v", err)
	}
	m := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, m, true); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, m, "applied", "svc-uid"); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.RecordExperimentalPreviewAddress(ctx, *work, c.PreviewID, "svc-uid", "http://10.10.1.66:30123"); err != nil {
		t.Fatal(err)
	}
	state, err := f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
	if err != nil || state.State != "ready" || state.Address == "" {
		t.Fatalf("state %+v %v", state, err)
	}
	op, err := f.repo.GetExperimentalPreviewOperation(ctx, c.TenantID, c.SandboxID, c.OperationID)
	if err != nil || op.State != "succeeded" || op.CompletedAt == nil {
		t.Fatalf("op %+v %v", op, err)
	}
	closeCmd := biz.ExperimentalPreviewClose{TenantID: c.TenantID, SandboxID: c.SandboxID, PreviewID: c.PreviewID, OperationID: uuid.NewString(), IdempotencyKey: "close", ExpectedVersion: 1}
	if _, err = f.repo.AcceptExperimentalPreviewClose(ctx, closeCmd); err != nil {
		t.Fatal(err)
	}
	state, err = f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
	if err != nil || state.Address != "" || state.State != "closing" {
		t.Fatalf("closing published address %+v %v", state, err)
	}
	var persisted string
	if err = f.runtime.QueryRow(ctx, `SELECT state FROM sandbox.preview_ports WHERE tenant_id=$1 AND preview_id=$2`, c.TenantID, c.PreviewID).Scan(&persisted); err != nil || persisted != "closing" {
		t.Fatalf("persisted state=%q err=%v", persisted, err)
	}
	ports, totalPorts, err := f.repo.ListExperimentalPreviews(ctx, c.TenantID, c.SandboxID, 1, 20)
	if err != nil || totalPorts != 1 || len(ports) != 1 || ports[0].State != "closing" || ports[0].Address != "" {
		t.Fatalf("closing list=%+v total=%d err=%v", ports, totalPorts, err)
	}
	if err = f.repo.RecordExperimentalPreviewAddress(ctx, *work, c.PreviewID, "svc-uid", "http://10.10.1.66:30123"); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("late observation restored address: %v", err)
	}
	items, total, err := f.repo.ListOperations(ctx, c.TenantID, c.SandboxID, 1, 20)
	if err != nil || total < 2 {
		t.Fatalf("preview history total=%d err=%v", total, err)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Action] = true
	}
	if !seen["preview_open"] || !seen["preview_close"] {
		t.Fatalf("preview operations absent: %+v", items)
	}
	replay := closeCmd
	replay.OperationID = uuid.NewString()
	result, err := f.repo.AcceptExperimentalPreviewClose(ctx, replay)
	if err != nil || !result.Replayed || result.OperationID != closeCmd.OperationID || result.Address != "" || result.State != "closing" {
		t.Fatalf("close replay lost operation identity: %+v %v", result, err)
	}
}

func TestExperimentalPreviewNotSentReleasesCleanupFence(t *testing.T) {
	f, ctx, base := codeRunFixture(t)
	c := biz.ExperimentalPreviewOpen{TenantID: base.TenantID, SandboxID: base.SandboxID, PreviewID: uuid.NewString(), OperationID: uuid.NewString(), IdempotencyKey: "not-sent", ExpectedVersion: 1, Port: 8082, Protocol: "http", TTL: time.Minute}
	if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	work, err := f.repo.Claim(ctx, "preview-not-sent", time.Minute)
	if err != nil || work == nil {
		t.Fatalf("claim %v", err)
	}
	m := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, m, true); err != nil {
		t.Fatal(err)
	}
	pause := biz.Command{TenantID: c.TenantID, ID: c.SandboxID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "pause-not-sent", ExpectedVersion: 1, RequestDigest: biz.ActionDigest("", 1, "pause")}
	if _, err = f.repo.Change(ctx, pause); err != nil {
		t.Fatal(err)
	}
	if err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, m, "not_sent", ""); err != nil {
		t.Fatal(err)
	}
	state, err := f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
	if err != nil || state.State != "closed" || state.Pending != nil || state.Reason != "PROVIDER_NOT_SENT" {
		t.Fatalf("state=%+v %v", state, err)
	}
	op, err := f.repo.GetExperimentalPreviewOperation(ctx, c.TenantID, c.SandboxID, c.OperationID)
	if err != nil || op.State != "failed" || op.CompletedAt == nil {
		t.Fatalf("operation=%+v %v", op, err)
	}
	needed, err := f.repo.PreviewCleanupRequired(ctx, c.TenantID, c.SandboxID)
	if err != nil || needed {
		t.Fatalf("cleanup fence remains: %t %v", needed, err)
	}
}

func TestExperimentalPreviewRejectedReceiptAndUnknownFence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unknown bool
	}{
		{name: "rejected_during_pause"},
		{name: "earlier_unknown_stays_fenced", unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ctx, base := codeRunFixture(t)
			c := biz.ExperimentalPreviewOpen{TenantID: base.TenantID, SandboxID: base.SandboxID, PreviewID: uuid.NewString(), OperationID: uuid.NewString(), IdempotencyKey: "rejected", ExpectedVersion: 1, Port: 8082, Protocol: "http", TTL: time.Minute}
			if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, c, true); err != nil {
				t.Fatal(err)
			}
			work, err := f.repo.Claim(ctx, "preview-rejected", time.Minute)
			if err != nil || work == nil {
				t.Fatalf("claim: %v", err)
			}
			m := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
			if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, m, true); err != nil {
				t.Fatal(err)
			}
			pause := biz.Command{TenantID: c.TenantID, ID: c.SandboxID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "pause-rejected", ExpectedVersion: 1, RequestDigest: biz.ActionDigest("", 1, "pause")}
			if _, err = f.repo.Change(ctx, pause); err != nil {
				t.Fatal(err)
			}
			if tc.unknown {
				if err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, m, "unknown", ""); err != nil {
					t.Fatal(err)
				}
			}
			err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, m, "rejected", "")
			if tc.unknown && !errors.Is(err, biz.ErrConflict) || !tc.unknown && err != nil {
				t.Fatalf("receipt: %v", err)
			}
			state, err := f.repo.GetExperimentalPreview(ctx, c.TenantID, c.SandboxID, c.PreviewID)
			if err != nil {
				t.Fatal(err)
			}
			needed, err := f.repo.PreviewCleanupRequired(ctx, c.TenantID, c.SandboxID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.unknown {
				if state.State != "unknown" || state.Pending == nil || !needed {
					t.Fatalf("unknown fence lost: state=%+v needed=%v", state, needed)
				}
				return
			}
			op, err := f.repo.GetExperimentalPreviewOperation(ctx, c.TenantID, c.SandboxID, c.OperationID)
			if err != nil || state.State != "closed" || state.Pending != nil || state.Reason != "PROVIDER_REJECTED" || op.State != "failed" || op.Reason != "PROVIDER_REJECTED" || op.CompletedAt == nil || needed {
				t.Fatalf("state=%+v op=%+v needed=%v err=%v", state, op, needed, err)
			}
		})
	}
}

func TestExperimentalPreviewAutomaticClosePersistsClosing(t *testing.T) {
	f, ctx, base := codeRunFixture(t)
	expired := biz.ExperimentalPreviewOpen{TenantID: base.TenantID, SandboxID: base.SandboxID, PreviewID: uuid.NewString(), OperationID: uuid.NewString(), IdempotencyKey: "expire-ready", ExpectedVersion: 1, Port: 8083, Protocol: "http", TTL: time.Minute}
	paused := expired
	paused.PreviewID = uuid.NewString()
	paused.OperationID = uuid.NewString()
	paused.IdempotencyKey = "pause-ready"
	paused.Port = 8084
	for _, c := range []biz.ExperimentalPreviewOpen{expired, paused} {
		if _, err := f.repo.AcceptExperimentalPreviewOpen(ctx, c, true); err != nil {
			t.Fatal(err)
		}
	}
	work, err := f.repo.Claim(ctx, "preview-auto-close", time.Minute)
	if err != nil || work == nil {
		t.Fatalf("claim %v", err)
	}
	for _, c := range []biz.ExperimentalPreviewOpen{expired, paused} {
		m := biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
		if err = f.repo.PrepareExperimentalPreviewMutation(ctx, *work, c.PreviewID, m, true); err != nil {
			t.Fatal(err)
		}
		if err = f.repo.RecordExperimentalPreviewReceipt(ctx, *work, c.PreviewID, m, "applied", "svc-"+c.PreviewID); err != nil {
			t.Fatal(err)
		}
		if err = f.repo.RecordExperimentalPreviewAddress(ctx, *work, c.PreviewID, "svc-"+c.PreviewID, "http://10.10.1.66:30123"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE sandbox.preview_ports SET expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND preview_id=$2`, expired.TenantID, expired.PreviewID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.NextExperimentalPreviewWork(ctx, *work, true); err != nil {
		t.Fatal(err)
	}
	var state, desired, address string
	read := func(id string) {
		t.Helper()
		if e := f.runtime.QueryRow(ctx, `SELECT state,desired,address FROM sandbox.preview_ports WHERE tenant_id=$1 AND preview_id=$2`, expired.TenantID, id).Scan(&state, &desired, &address); e != nil {
			t.Fatal(e)
		}
		if state != "closing" || desired != "closed" || address != "" {
			t.Fatalf("automatic close state=%s desired=%s address=%q", state, desired, address)
		}
	}
	read(expired.PreviewID)
	pause := biz.Command{TenantID: paused.TenantID, ID: paused.SandboxID, OperationID: uuid.NewString(), Action: "pause", IdempotencyKey: "preview-pause", ExpectedVersion: 1, RequestDigest: biz.ActionDigest("", 1, "pause")}
	if _, err = f.repo.Change(ctx, pause); err != nil {
		t.Fatal(err)
	}
	read(paused.PreviewID)
}
