package data_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
	"k8s.io/client-go/rest"
)

func TestManualVNicPGSecurityGroupVersionFence(t *testing.T) {
	f := newAttachmentFixture(t)
	ctx := context.Background()
	v := manualVNic(t, f, "sg-fence")
	consumer := "vm-policy-owner"
	if _, e := f.n.ReserveVNic(ctx, biz.VNicReservation{TenantID: v.TenantID, VNicID: v.ID, ConsumerKind: "vm", ConsumerID: consumer, Slot: "primary", OperationID: uuid.NewString(), SubnetID: v.SubnetID, Revision: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.p.SyncSecurityGroupBinding(ctx, v.TenantID, "vm", consumer, v.VPCID, v.Namespace, 4, []string{}); e != nil {
		t.Fatal(e)
	}
	var bumped atomic.Bool
	var patches atomic.Int32
	callbackErrors := make(chan error, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/vnics/"+v.ProviderName) && bumped.CompareAndSwap(false, true) {
			// A newer owner choice commits after the old worker's selection read but
			// before its provider send. This also proves no DB lock spans provider I/O.
			if _, e := f.p.SyncSecurityGroupBinding(ctx, v.TenantID, "vm", consumer, v.VPCID, v.Namespace, 5, []string{}); e != nil {
				callbackErrors <- e
				http.Error(w, "selection update failed", 500)
				return
			}
		}
		if r.Method == "PATCH" {
			patches.Add(1)
		}
		f.api.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	if _, e := data.NewKCProvider(f.p, &rest.Config{Host: endpoint.URL, QPS: 1000, Burst: 1000}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.n.SyncVNicSecurityGroups(ctx, v.TenantID, v.ID, "vm", consumer, []string{}); biz.ReasonOf(e) != biz.VersionConflict {
		t.Fatal("old worker wrote after the owner selection version changed", e)
	}
	select {
	case e := <-callbackErrors:
		t.Fatal(e)
	default:
	}
	if !bumped.Load() || patches.Load() != 0 {
		t.Fatal("stale selection reached provider mutation", bumped.Load(), patches.Load())
	}
	if _, e := f.n.SyncVNicSecurityGroups(ctx, v.TenantID, v.ID, "vm", consumer, []string{"sg_11111111111111111111111111111111"}); biz.ReasonOf(e) != biz.VersionConflict {
		t.Fatal("late old nonempty policy overwrote the latest clear selection", e)
	}
	if patches.Load() != 0 {
		t.Fatal("late policy reached provider mutation")
	}
}
