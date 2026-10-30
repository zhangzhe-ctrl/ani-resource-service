package data_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
	"k8s.io/client-go/rest"
)

func TestRemovedDeviceDiscoveryAndRegistrationRemainAuthorized(t *testing.T) {
	p, owner := database(t)
	var requests atomic.Int64
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "retired entry must not reach Kubernetes", http.StatusInternalServerError)
	}))
	defer host.Close()
	provider, err := data.NewKCProvider(p, &rest.Config{Host: host.URL, QPS: 1000, Burst: 1000})
	if err != nil {
		t.Fatal(err)
	}
	p.UseEgressInfrastructure(provider)
	e, err := biz.NewEgress(p, provider, biz.ContextEgressAuthorization{}, []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	intent := biz.PlatformIntent{Kind: "adopt_device", Name: "retired-device", IdempotencyKey: "retired-adopt", Device: &biz.DeviceConfig{DeviceName: "ens35", InventoryFingerprint: strings.Repeat("a", 64)}}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want biz.Reason
	}{
		{"untrusted", context.Background(), biz.PermissionDenied},
		{"tenant", biz.WithEgressCaller(context.Background(), biz.EgressCaller{TenantID: uuid.NewString()}), biz.PermissionDenied},
		{"platform", biz.WithEgressCaller(context.Background(), biz.EgressCaller{PlatformAdministrator: true}), biz.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.ListNodeInterfaces(tc.ctx, ""); biz.ReasonOf(err) != tc.want {
				t.Fatal("retired discovery lost authorization or rejection", err)
			}
			if _, err := e.CreatePlatform(tc.ctx, intent); biz.ReasonOf(err) != tc.want {
				t.Fatal("retired registration lost authorization or rejection", err)
			}
		})
	}
	var persisted int
	if err := owner.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM network_device_adoptions)+
 (SELECT count(*) FROM network_platform_resources WHERE kind='device')+
 (SELECT count(*) FROM network_platform_operations WHERE kind='adopt_device')`).Scan(&persisted); err != nil || persisted != 0 {
		t.Fatal("rejected legacy entry persisted a registration or operation", persisted, err)
	}
	if requests.Load() != 0 {
		t.Fatal("retired entry reached Kubernetes", requests.Load())
	}
}
