//go:build imageintegration

package data_test

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
			wantReason := biz.PermissionDenied
			if refusal == http.StatusBadRequest {
				wantReason = biz.InvalidArgument
			}
			if biz.ReasonOf(first) != wantReason || command.Reason != wantReason {
				t.Fatal("rejection reason lost", first, command.Reason)
			}
			other := biz.WithCaller(ctx, biz.Caller{Kind: biz.GovernanceCaller, Subject: "governance", Actor: "user:other", TenantID: in.TenantID})
			if _, err = l.EnsureImageSpace(other, in); biz.ReasonOf(err) != biz.IdempotencyConflict {
				t.Fatal("same key accepted a different actor", err)
			}
			changed := in
			changed.Slug = "another-name"
			if _, err = l.EnsureImageSpace(ctx, changed); biz.ReasonOf(err) != biz.SpaceNameImmutable {
				t.Fatal("same key changed immutable parameters", err)
			}
			changed = in
			changed.IdempotencyKey = "different-key"
			if _, err = l.EnsureImageSpace(ctx, changed); biz.ReasonOf(err) != biz.RequestInProgress {
				t.Fatal("new key bypassed the pending operation", err)
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

type loseProjectBinding struct{ biz.LifecycleRepository }

func (r loseProjectBinding) BindTenantProject(context.Context, biz.Space, int64) (biz.Space, error) {
	return biz.Space{}, biz.Fail(biz.DependencyUnavailable, "injected failure before binding commit")
}

func TestProjectUncertainCreateNeverResent(t *testing.T) {
	for _, fault := range []string{"500", "409", "truncated_403", "timeout_after_create", "lost_response", "missing_location", "bind_not_committed"} {
		t.Run(fault, func(t *testing.T) {
			f, registry, cfg, ring, ctx := lifecycleFixture(t)
			upstream := registryHTTP(t, registry)
			var posts atomic.Int32
			var hidden atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hidden.Load() && r.Method == http.MethodGet && r.URL.Path == "/api/v2.0/projects/t-uncertain" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Method == http.MethodPost && r.URL.Path == "/api/v2.0/projects" {
					posts.Add(1)
					switch fault {
					case "500":
						w.WriteHeader(http.StatusInternalServerError)
						return
					case "409":
						w.WriteHeader(http.StatusConflict)
						return
					case "truncated_403":
						w.Header().Set("Content-Length", "10")
						w.WriteHeader(http.StatusForbidden)
						return // The complete refusal response was not received.
					case "lost_response", "missing_location", "timeout_after_create":
						// Commit the external object, then lose only the receipt.
						response := httptest.NewRecorder()
						upstream.Config.Handler.ServeHTTP(response, r)
						if response.Code != http.StatusCreated {
							t.Error("fixture did not create project")
						}
						if fault == "timeout_after_create" {
							<-r.Context().Done()
							return
						}
						if fault == "lost_response" {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
							return
						}
						w.WriteHeader(http.StatusCreated)
						return
					}
				}
				upstream.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			h, err := imagedata.NewHarbor(imagedata.HarborConfig{URL: server.URL, Username: "fixture-admin", Password: biz.Secret("fixture-password"), RobotNamePrefix: "fixture$", Timeout: time.Second, CAPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(h.Close)
			var repo biz.LifecycleRepository = f.Repo
			if fault == "bind_not_committed" {
				repo = loseProjectBinding{repo}
			}
			l := lifecycle(t, repo, h, ring, cfg, nil)
			in := biz.EnableSpace{TenantID: tenantFrom(t, ctx), Slug: "uncertain", IdempotencyKey: "uncertain-key"}
			if _, err = l.EnsureImageSpace(ctx, in); err == nil {
				t.Fatal("uncertain create succeeded")
			}
			hidden.Store(true)
			l = lifecycle(t, f.Repo, h, ring, cfg, nil)
			if _, err = l.EnsureImageSpace(ctx, in); biz.ReasonOf(err) != biz.SpaceOwnershipUnconfirmed {
				t.Fatal("uncertain write was not blocked", err)
			}
			if posts.Load() != 1 || registry.robotCreates != 0 {
				t.Fatalf("404 allowed a duplicate POST: posts=%d robots=%d", posts.Load(), registry.robotCreates)
			}
			space, err := f.Repo.FindTenantSpace(ctx, in.TenantID)
			if err != nil || space.ProjectID != 0 {
				t.Fatal("uncertain project adopted", space, err)
			}
			command, err := f.Repo.FindTenantCommand(ctx, in.TenantID, space.ID, in.IdempotencyKey)
			if err != nil || command.State != "blocked" || command.Phase != "project_sent" {
				t.Fatal("uncertainty not durably recorded", command.State, command.Phase, err)
			}
			if fault == "500" || fault == "409" || fault == "truncated_403" {
				if registry.creates != 0 {
					t.Fatal("unexpected external object")
				}
				return
			}
			project, err := registry.FindProjectByName(ctx, space.ProjectName)
			if err != nil {
				t.Fatal(err)
			}
			operator := biz.WithCaller(context.Background(), biz.Caller{Kind: biz.PlatformCaller, Subject: "local-cli", Actor: "operator:test"})
			if _, err = l.RecoverProjectBinding(operator, space.ID, project.ID, strings.Repeat("a", 64)); err != nil {
				t.Fatal("controlled ownership recovery failed", err)
			}
			ready, err := l.EnsureImageSpace(ctx, in)
			if err != nil || ready.State != "available" || ready.ProjectID != project.ID || registry.creates != 1 || posts.Load() != 1 {
				t.Fatal("recovery duplicated or lost project", ready, err)
			}
		})
	}
}
