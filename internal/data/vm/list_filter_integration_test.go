//go:build instanceintegration

package vm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	"github.com/zhangzhe-ctrl/ani-resource-service/tests/testenv"
)

// VM list keeps deleted tombstones out by default, filters on the closed
// display-state set, matches name/ID as a case-insensitive substring and keeps
// the total consistent with the same filter.
func TestVMListFilterIntegration(t *testing.T) {
	ctx := context.Background()
	db := testenv.NewDatabase(t)
	if e := Migrate(ctx, db.Owner.Config().ConnString(), db.RuntimeRole); e != nil {
		t.Fatal(e)
	}
	repository, e := OpenPostgres(ctx, db.RuntimeDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer repository.Close()
	tenant, other := uuid.NewString(), uuid.NewString()
	base := time.Now().UTC().Add(-time.Hour)
	rows := []struct {
		name              string
		desired, observed string
		closing           bool
		createdAt         time.Time
	}{
		{"List-Run-1", "Running", "Running", false, base.Add(time.Minute)},
		{"List-Run-2", "Running", "Running", false, base.Add(2 * time.Minute)},
		{"List-Stop", "Stopped", "Stopped", false, base.Add(3 * time.Minute)},
		{"List-Fail", "Stopped", "Failed", false, base.Add(4 * time.Minute)},
		{"List-Gone", "Deleted", "Deleted", true, base.Add(5 * time.Minute)},
		{"List-Deleting", "Deleted", "Running", true, base.Add(6 * time.Minute)},
	}
	ids := map[string]string{}
	insert := func(tenantID, name, desired, observed string, closing bool, created time.Time) {
		t.Helper()
		id := uuid.NewString()
		ids[name] = id
		v := biz.VM{TenantID: tenantID, ID: id, Name: name, CreateOperationID: uuid.NewString(), Version: 1, Desired: desired, Observed: observed, Closing: closing}
		if _, e := repository.pool.Exec(ctx, `INSERT INTO vm.virtual_machines(tenant_id,id,create_operation_id,request_digest,spec_digest,initial_spec,current_configuration,version,closing,created_at) VALUES($1,$2,$3,$4,$5,'{}'::jsonb,$6,1,$7,$8)`, tenantID, id, v.CreateOperationID, strings.Repeat("a", 64), strings.Repeat("b", 64), encode(v), closing, created); e != nil {
			t.Fatal(e)
		}
	}
	for _, r := range rows {
		insert(tenant, r.name, r.desired, r.observed, r.closing, r.createdAt)
	}
	insert(other, "List-Other", "Running", "Running", false, base)

	list := func(filter biz.ListFilter) ([]biz.VM, int64) {
		t.Helper()
		items, total, e := repository.List(ctx, tenant, filter)
		if e != nil {
			t.Fatal(e)
		}
		return items, total
	}

	items, total := list(biz.ListFilter{})
	if total != 5 || len(items) != 5 {
		t.Fatalf("default list must hide only the deleted tombstone: total=%d items=%d", total, len(items))
	}
	if items[0].Name != "List-Deleting" || items[0].CreatedAt.IsZero() {
		t.Fatalf("default list order or createdAt drifted: %+v", items[0])
	}
	for _, item := range items {
		if item.Observed == "Deleted" {
			t.Fatal("default list returned a deleted tombstone")
		}
	}

	for _, state := range []string{"running", "stopped", "failed", "deleted", "deleting"} {
		items, total = list(biz.ListFilter{PageSize: 100, State: state})
		if state == "running" {
			if total != 2 || len(items) != 2 {
				t.Fatalf("running: total=%d items=%d", total, len(items))
			}
			continue
		}
		if total != 1 || len(items) != 1 {
			t.Fatalf("%s: total=%d items=%d", state, total, len(items))
		}
	}

	items, total = list(biz.ListFilter{Keyword: "list-run"})
	if total != 2 || len(items) != 2 {
		t.Fatalf("keyword must be a case-insensitive name substring: total=%d", total)
	}
	items, total = list(biz.ListFilter{Keyword: strings.ToUpper(ids["List-Stop"][:8])})
	if total != 1 || len(items) != 1 || items[0].Name != "List-Stop" {
		t.Fatalf("keyword must match the id fragment: total=%d", total)
	}
	items, total = list(biz.ListFilter{Keyword: "list-run", State: "stopped"})
	if total != 0 || len(items) != 0 {
		t.Fatalf("filters must intersect: total=%d", total)
	}
	items, total = list(biz.ListFilter{Keyword: "list-"})
	if total != 5 || len(items) != 5 {
		t.Fatalf("tenant scope leaked: total=%d", total)
	}
	items, total = list(biz.ListFilter{Page: 2, PageSize: 2})
	if total != 5 || len(items) != 2 {
		t.Fatalf("pagination must keep the filtered total: total=%d items=%d", total, len(items))
	}
	if _, _, e = repository.List(ctx, tenant, biz.ListFilter{PageSize: 101}); !errors.Is(e, biz.ErrInvalid) {
		t.Fatalf("oversized page must be rejected: %v", e)
	}
}
