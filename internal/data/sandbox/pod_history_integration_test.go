//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/sandbox"
	migrations "github.com/zhangzhe-ctrl/ani-resource-service/migrations/sandbox"
)

func TestPodHistoryRecordsClearAndAttributedLateReceipt(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c, first := acceptAndClaim(t, f)
	name, firstUID, secondUID := "sandbox-"+c.ID, uuid.NewString(), uuid.NewString()
	progress := first.Sandbox
	progress.ProviderRefs.PodName, progress.ProviderRefs.PodUID = name, firstUID
	if err := f.repo.Finish(ctx, *first, biz.Progress{Sandbox: progress}); err != nil {
		t.Fatal(err)
	}
	forceSandboxWorkDue(t, f, ctx, c, false)
	clearing := claimWork(t, f, ctx, "clear")
	progress = clearing.Sandbox
	progress.ProviderRefs.PodUID = ""
	if err := f.repo.Finish(ctx, *clearing, biz.Progress{Sandbox: progress}); err != nil {
		t.Fatal(err)
	}
	forceSandboxWorkDue(t, f, ctx, c, false)
	late := claimWork(t, f, ctx, "late")
	mutation := biz.Mutation{ID: uuid.NewString(), Kind: biz.MutationCreatePod, OperationID: c.OperationID, Version: late.Sandbox.Version}
	if err := f.repo.PrepareMutation(ctx, *late, mutation); err != nil {
		t.Fatal(err)
	}
	forceSandboxWorkDue(t, f, ctx, c, true)
	_ = claimWork(t, f, ctx, "successor")
	stale := late.Sandbox
	stale.ProviderRefs.PodUID = uuid.NewString()
	wrong := mutation
	wrong.ID = uuid.NewString()
	if err := f.repo.Finish(ctx, *late, biz.Progress{Sandbox: stale, Receipt: &biz.MutationResult{Mutation: wrong, Outcome: biz.MutationApplied, Refs: biz.ProviderRefs{PodName: name, PodUID: uuid.NewString()}}}); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("unattributed late receipt accepted: %v", err)
	}
	before, err := f.repo.ListSandboxPodHistory(ctx, c.TenantID, c.ID)
	if err != nil || len(before) != 1 || before[0].PodUID != firstUID {
		t.Fatalf("rejected receipt wrote history: %+v %v", before, err)
	}
	if err := f.repo.Finish(ctx, *late, biz.Progress{Sandbox: stale, Receipt: &biz.MutationResult{Mutation: mutation, Outcome: biz.MutationApplied, Refs: biz.ProviderRefs{PodName: name, PodUID: secondUID}}}); err != nil {
		t.Fatal(err)
	}
	items, err := f.repo.ListSandboxPodHistory(ctx, c.TenantID, c.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("history: %+v %v", items, err)
	}
	seen := map[string]bool{}
	for _, x := range items {
		if x.PodName != name || x.ClusterID != c.Spec.ClusterID || x.Namespace != c.Spec.Namespace || x.NamespaceUID != c.Spec.NamespaceUID || x.FirstRecordedAt.IsZero() {
			t.Fatalf("bad identity: %+v", x)
		}
		seen[x.PodUID] = true
	}
	if !seen[firstUID] || !seen[secondUID] || seen[stale.ProviderRefs.PodUID] {
		t.Fatalf("stale or lost uid: %+v", seen)
	}
	other := uuid.NewString()
	otherCtx := biz.WithCaller(context.Background(), biz.Caller{TenantID: other, Actor: "governance:user:42"})
	if _, err = f.repo.ListSandboxPodHistory(otherCtx, other, c.ID); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("foreign scope: %v", err)
	}
	if _, err = f.repo.ListSandboxPodHistory(otherCtx, c.TenantID, c.ID); !errors.Is(err, biz.ErrDenied) {
		t.Fatalf("wrong caller: %v", err)
	}
}

func TestPodHistoryMigrationBackfillsAndLimitFailsClosed(t *testing.T) {
	f := sandboxDatabase(t)
	ctx, c := fixtureCreate()
	if _, err := f.repo.AcceptCreate(ctx, c); err != nil {
		t.Fatal(err)
	}
	uid, name := uuid.NewString(), "sandbox-"+c.ID
	if _, err := f.runtime.Exec(ctx, `UPDATE sandbox.sandboxes SET provider_refs=jsonb_build_object('PodUID',$3::text,'PodName',$4::text) WHERE tenant_id=$1 AND sandbox_id=$2`, c.TenantID, c.ID, uid, name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	runtimeRole := u.User.Username()
	owner := strings.Replace(runtimeRole, "sandbox_runtime_", "sandbox_owner_", 1)
	u.User = url.UserPassword(owner, password)
	db, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(ctx, `DROP TABLE sandbox.pod_history`); err != nil {
		t.Fatal(err)
	}
	body, err := migrations.Files.ReadFile("012_pod_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if _, err = db.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	if err = data.Migrate(ctx, u.String(), runtimeRole); err != nil {
		t.Fatal(err)
	}
	items, err := f.repo.ListSandboxPodHistory(ctx, c.TenantID, c.ID)
	if err != nil || len(items) != 1 || items[0].PodUID != uid || items[0].PodName != name || items[0].FirstRecordedAt.Before(before) {
		t.Fatalf("migration backfill: %+v %v", items, err)
	}
	for i := 0; i < biz.PodHistoryLimit; i++ {
		_, err = db.Exec(ctx, `INSERT INTO sandbox.pod_history(tenant_id,sandbox_id,cluster_id,namespace,namespace_uid,pod_name,pod_uid) VALUES($1,$2,$3,$4,$5,$6,$7)`, c.TenantID, c.ID, c.Spec.ClusterID, c.Spec.Namespace, c.Spec.NamespaceUID, name, fmt.Sprintf("overflow-%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.repo.ListSandboxPodHistory(ctx, c.TenantID, c.ID); !errors.Is(err, biz.ErrPodHistoryTooLarge) {
		t.Fatalf("limit: %v", err)
	}
}
