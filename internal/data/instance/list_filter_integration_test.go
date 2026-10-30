//go:build instanceintegration

package instance_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
)

// List defaults to hiding deleted tombstones and matches the instance id as
// a case-insensitive substring within the trusted tenant.
func TestListFiltersExcludeDeletedAndMatchInstanceID(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	active := create()
	active.Spec.Name = "list-active"
	if _, e := f.repo.AcceptCreate(ctx, active); e != nil {
		t.Fatal(e)
	}
	gone := create()
	gone.TenantID = active.TenantID
	gone.Spec.Name = "list-gone"
	if _, e := f.repo.AcceptCreate(ctx, gone); e != nil {
		t.Fatal(e)
	}
	if _, e := f.runtime.Exec(ctx, `UPDATE instance.instances SET snapshot=jsonb_set(snapshot,'{Observed}','"deleted"') WHERE tenant_id=$1 AND instance_id=$2`, gone.TenantID, gone.ID); e != nil {
		t.Fatal(e)
	}
	caller := biz.WithCaller(ctx, biz.Caller{TenantID: active.TenantID, Actor: "governance:user:42"})

	items, total, e := f.repo.List(caller, active.TenantID, biz.ListFilter{})
	if e != nil || total != 1 || len(items) != 1 || items[0].ID != active.ID {
		t.Fatal("default list kept a deleted tombstone", total, items, e)
	}

	items, total, e = f.repo.List(caller, active.TenantID, biz.ListFilter{ObservedState: biz.Deleted})
	if e != nil || total != 1 || len(items) != 1 || items[0].ID != gone.ID {
		t.Fatal("explicit deleted filter did not select the tombstone", total, items, e)
	}

	fragment := strings.ToUpper(active.ID[9:22])
	items, total, e = f.repo.List(caller, active.TenantID, biz.ListFilter{ID: fragment})
	if e != nil || total != 1 || len(items) != 1 || items[0].ID != active.ID {
		t.Fatal("id fragment search did not match the active instance", total, items, e)
	}

	items, total, e = f.repo.List(caller, active.TenantID, biz.ListFilter{ID: "zzz"})
	if e != nil || total != 0 || len(items) != 0 {
		t.Fatal("unknown id fragment returned matches", total, items, e)
	}

	other := create()
	if _, e = f.repo.AcceptCreate(ctx, other); e != nil {
		t.Fatal(e)
	}
	if items, total, e = f.repo.List(caller, active.TenantID, biz.ListFilter{ID: other.ID}); e != nil || total != 0 || len(items) != 0 {
		t.Fatal("id filter crossed the tenant boundary", total, items, e)
	}

	if _, _, e = f.repo.List(caller, active.TenantID, biz.ListFilter{ID: strings.Repeat("a", 37)}); !errors.Is(e, biz.ErrInvalid) {
		t.Fatal("over-long id fragment was accepted", e)
	}
}
