//go:build imageintegration

package data_test

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	imagedata "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image"
)

func TestProjectRejectedCreateSameKeyRetry(t *testing.T) {
	for _, refusal := range []int{400, 401, 403} {
		t.Run(http.StatusText(refusal), func(t *testing.T) {
			f, registry, cfg, ring, ctx := lifecycleFixture(t)
			upstream := registryHTTP(t, registry)
			var reject, posts atomic.Int32
			reject.Store(int32(refusal))
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v2.0/projects" {
					posts.Add(1)
					if code := reject.Load(); code != 0 {
						w.WriteHeader(int(code))
						return
					}
				}
				upstream.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			h, err := imagedata.NewHarbor(imagedata.HarborConfig{URL: server.URL, Username: "fixture-admin", Password: biz.Secret("fixture-password"), RobotNamePrefix: "fixture$", CAPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(h.Close)
			l := lifecycle(t, f.Repo, h, ring, cfg, nil)
			in := biz.EnableSpace{TenantID: tenantFrom(t, ctx), Slug: "retry-refused", IdempotencyKey: "retry-refused-key"}
			_, first := l.EnsureImageSpace(ctx, in)
			space, err := f.Repo.FindTenantSpace(ctx, in.TenantID)
			if err != nil || space.ProjectID != 0 || registry.creates != 0 || posts.Load() != 1 {
				t.Fatalf("refusal side effects: project=%d creates=%d posts=%d err=%v", space.ProjectID, registry.creates, posts.Load(), err)
			}
			command, err := f.Repo.FindTenantCommand(ctx, in.TenantID, space.ID, in.IdempotencyKey)
			if err != nil {
				t.Fatal(err)
			}
			if first == nil || command.State != "retryable" || command.Phase != "project_rejected" {
				t.Fatalf("definite refusal must remain safely retryable: state=%s phase=%s reason=%s error=%v", command.State, command.Phase, command.Reason, first)
			}
			// Correct the provider permission/input cause, then recreate only the
			// use case: PostgreSQL must retain the same request and its safe retry.
			reject.Store(0)
			l = lifecycle(t, f.Repo, h, ring, cfg, nil)
			ready, err := l.EnsureImageSpace(ctx, in)
			if err != nil || ready.State != "available" || ready.ID != space.ID || ready.ProjectID == 0 {
				t.Fatalf("same-key retry did not recover: %+v %v", ready, err)
			}
			completed, err := f.Repo.FindTenantCommand(ctx, in.TenantID, space.ID, in.IdempotencyKey)
			if err != nil || completed.ID != command.ID || completed.State != "succeeded" {
				t.Fatalf("command replaced or not completed: %+v %v", completed, err)
			}
			if _, err = l.EnsureImageSpace(ctx, in); err != nil {
				t.Fatal("completed replay", err)
			}
			if registry.creates != 1 || registry.robotCreates != 1 || posts.Load() != 2 {
				t.Fatalf("duplicate writes: creates=%d robots=%d posts=%d", registry.creates, registry.robotCreates, posts.Load())
			}
		})
	}
}
