//go:build sandboxintegration

package sandbox_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

func TestSandboxEgressIntentPersistsAndReplaysWithTenantIsolation(t *testing.T) {
	f := sandboxDatabase(t)
	tenant, id, operation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: tenant, Actor: "governance:user:42"})
	raw, err := pb.EncodeEgressIntent(&pb.SandboxEgressIntent{NetworkEgressPolicy: "allowlist", EgressAllowlist: []string{"api.example"}})
	if err != nil {
		t.Fatal(err)
	}
	c := biz.Create{TenantID: tenant, ID: id, OperationID: operation, RequestDigest: strings.Repeat("a", 64), IntentDigest: strings.Repeat("b", 64), Spec: biz.CreateSpec{Name: "demo", ImageDigest: "sha256:" + strings.Repeat("d", 64), EgressIntentJSON: raw}}
	if _, err = f.repo.AcceptCreate(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.Get(ctx, tenant, id)
	if err != nil || got.Snapshot.EgressIntentJSON != raw {
		t.Fatalf("snapshot %+v err=%v", got.Snapshot, err)
	}
	ack, err := f.repo.AcceptCreate(ctx, c)
	if err != nil || !ack.Replayed {
		t.Fatalf("replay %+v %v", ack, err)
	}
	c.Spec.EgressIntentJSON, err = pb.EncodeEgressIntent(&pb.SandboxEgressIntent{NetworkEgressPolicy: "allowlist"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.AcceptCreate(ctx, c); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("different intent accepted %v", err)
	}
	other := uuid.NewString()
	otherCtx := biz.WithCaller(context.Background(), biz.Caller{TenantID: other, Actor: "governance:user:43"})
	if _, err = f.repo.Get(otherCtx, other, id); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("cross-tenant leak: %v", err)
	}
	if _, err = f.repo.Get(otherCtx, tenant, id); !errors.Is(err, biz.ErrDenied) {
		t.Fatalf("tenant spoof accepted: %v", err)
	}
}
