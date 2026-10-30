package data_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
)

func TestRemovedDeviceRetirementRemainsAuthorizedAndDoesNotPersist(t *testing.T) {
	p, owner := database(t)
	infra := testEgressInfrastructure{}
	e, err := biz.NewEgress(p, infra, biz.ContextEgressAuthorization{}, []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	intent := biz.DeviceRetirementIntent{DeviceID: "device_" + strings.Repeat("a", 32), ExpectedVersion: 1, IdempotencyKey: "retired-device"}
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
			if _, err := e.RetireNetworkDevice(tc.ctx, intent); biz.ReasonOf(err) != tc.want {
				t.Fatal("retired write entry lost authorization or rejection", err)
			}
		})
	}
	var persisted int
	if err := owner.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM network_legacy_device_retirements)+
 (SELECT count(*) FROM network_device_adoptions)+
 (SELECT count(*) FROM network_platform_operations)`).Scan(&persisted); err != nil || persisted != 0 {
		t.Fatal("rejected retirement persisted work", persisted, err)
	}
}
