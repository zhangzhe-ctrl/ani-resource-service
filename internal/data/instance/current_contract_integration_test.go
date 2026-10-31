//go:build instanceintegration

package instance_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"strings"
	"testing"
	"time"
)

func TestConfigurationPGRejectsIncompleteRowsAndAcceptsCurrent(t *testing.T) {
	f := database(t)
	c := create()
	ctx := context.Background()
	if _, err := f.repo.AcceptCreate(ctx, c); err != nil {
		t.Fatal(err)
	}
	value, err := f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil || value.Spec.WorkspaceMountPath != "/workspace" {
		t.Fatal("configuration create did not persist", err)
	}
	value.Spec.WorkspaceMountPath = ""
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.Exec(ctx, `UPDATE instance.instances SET snapshot=$3 WHERE tenant_id=$1 AND instance_id=$2`, c.TenantID, c.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Get(ctx, c.TenantID, c.ID); !errors.Is(err, biz.ErrInvalid) {
		t.Fatal("incomplete persisted configuration was read", err)
	}
	if _, _, err = f.repo.List(biz.WithCaller(ctx, biz.Caller{TenantID: c.TenantID, Actor: "governance:user:42"}), c.TenantID, biz.ListFilter{}); !errors.Is(err, biz.ErrInvalid) {
		t.Fatal("incomplete persisted configuration appeared in list", err)
	}
	if _, err = f.repo.Claim(ctx, "configuration", time.Second); !errors.Is(err, biz.ErrInvalid) {
		t.Fatal("incomplete persisted configuration entered worker", err)
	}
}

func TestCurrentConfigurationPGDeleteFirstUsesCurrentContract(t *testing.T) {
	for _, kind := range []string{biz.TypeCPU, biz.TypeGPU} {
		t.Run(kind, func(t *testing.T) {
			f := database(t)
			ctx := context.Background()
			c := create()
			c.Spec.ComputeType = kind
			d := biz.Delete{InstanceKind: biz.KindContainer, ComputeType: kind, TenantID: c.TenantID, ID: c.ID, OperationID: uuid.NewString(), CreateOperationID: c.OperationID, RequestHash: strings.Repeat("e", 64)}
			if _, err := f.repo.AcceptDelete(ctx, d); err != nil {
				t.Fatal(err)
			}
			i, err := f.repo.Get(ctx, c.TenantID, c.ID)
			if err != nil || i.Spec.WorkspaceMountPath != "/workspace" || i.CreateRequestHash != "" {
				t.Fatal("delete-first did not persist a current empty-create fence", err)
			}
			if _, err := f.repo.AcceptDelete(ctx, d); err != nil {
				t.Fatal("current delete-first replay failed", err)
			}
		})
	}
}
