package sandbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type sandboxLogsStore struct{ sandbox biz.Sandbox }

func (s *sandboxLogsStore) Get(_ context.Context, tenant, id string) (biz.Sandbox, error) {
	if tenant != s.sandbox.TenantID || id != s.sandbox.ID {
		return biz.Sandbox{}, biz.ErrNotFound
	}
	return s.sandbox, nil
}

type sandboxLogsSource func(context.Context, biz.Sandbox, biz.LogOptions) (io.ReadCloser, error)

func (f sandboxLogsSource) Open(ctx context.Context, s biz.Sandbox, o biz.LogOptions) (io.ReadCloser, error) {
	return f(ctx, s, o)
}

func TestSandboxLogsTimestampedSourceAndResume(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		if r.URL.Path != "/api/v1/namespaces/tenant-x/pods/pod-x/log" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, "2026-11-03T00:00:00Z hello\n")
	}))
	defer server.Close()
	client, err := NewKubernetesSandboxLogs(&rest.Config{Host: server.URL}, &KubernetesProvider{}, &sandboxLogsStore{})
	if err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	body, err := client.source.Open(context.Background(), biz.Sandbox{Snapshot: biz.Snapshot{Namespace: "tenant-x"}, ProviderRefs: biz.ProviderRefs{PodName: "pod-x"}}, biz.LogOptions{TailLines: 20, Follow: true, Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	for _, v := range []string{"container=sandbox", "follow=true", "timestamps=true", "sinceTime=2026-11-03T00%3A00%3A00Z"} {
		if !strings.Contains(query, v) {
			t.Fatalf("query %q misses %q", query, v)
		}
	}
	// Resume must not truncate a disconnected interval to the initial tail.
	if strings.Contains(query, "tailLines=") {
		t.Fatalf("resume would silently truncate missed logs: %s", query)
	}
}

func TestSandboxLogsStreamScopeAndLifecycle(t *testing.T) {
	client, provider, s, ctx := environmentFixture(t)
	store := &sandboxLogsStore{sandbox: s}
	reader := &KubernetesSandboxLogs{provider: provider, reader: store, source: sandboxLogsSource(func(context.Context, biz.Sandbox, biz.LogOptions) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("2026-11-03T00:00:00Z ok\n")), nil
	})}
	var frames []biz.LogFrame
	emit := func(frame biz.LogFrame) error { frames = append(frames, frame); return nil }
	if err := reader.StreamLogs(ctx, s, biz.LogOptions{TailLines: 20}, emit); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || frames[0].Kind != "READY" || frames[0].PodUID != s.ProviderRefs.PodUID || frames[1].Kind != "DATA" || !strings.Contains(string(frames[1].Data), "2026-11-03T00:00:00Z") || frames[2].Reason != "EOF" {
		t.Fatalf("frames=%+v", frames)
	}

	foreign := biz.WithCaller(context.Background(), biz.Caller{TenantID: "33333333-3333-4333-8333-333333333333", Actor: "x"})
	frames = nil
	if err := reader.StreamLogs(foreign, s, biz.LogOptions{TailLines: 20}, emit); !errors.Is(err, biz.ErrDenied) || len(frames) != 0 {
		t.Fatalf("foreign err=%v frames=%+v", err, frames)
	}

	since := time.Now()
	if err := reader.StreamLogs(ctx, s, biz.LogOptions{TailLines: 20, Since: &since, PodUID: "replaced"}, emit); !errors.Is(err, biz.ErrDataPlaneUnavailable) {
		t.Fatalf("resume mismatch: %v", err)
	}
	if err := reader.StreamLogs(ctx, s, biz.LogOptions{TailLines: 20, Since: &since}, emit); !errors.Is(err, biz.ErrInvalid) {
		t.Fatalf("unbound cursor: %v", err)
	}

	reader.source = sandboxLogsSource(func(context.Context, biz.Sandbox, biz.LogOptions) (io.ReadCloser, error) {
		pod, err := client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Get(ctx, s.ProviderRefs.PodName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		pod.SetUID("replacement")
		_, err = client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
		return io.NopCloser(strings.NewReader("foreign-secret")), err
	})
	frames = nil
	if err := reader.StreamLogs(ctx, s, biz.LogOptions{TailLines: 20}, emit); !errors.Is(err, biz.ErrDataPlaneUnavailable) || len(frames) != 0 {
		t.Fatalf("replaced Pod exposed data: err=%v frames=%+v", err, frames)
	}
}

func TestSandboxLogsCancellation(t *testing.T) {
	_, provider, s, parent := environmentFixture(t)
	ctx, cancel := context.WithCancel(parent)
	pipeReader, pipeWriter := io.Pipe()
	defer pipeWriter.Close()
	reader := &KubernetesSandboxLogs{provider: provider, reader: &sandboxLogsStore{sandbox: s}, source: sandboxLogsSource(func(context.Context, biz.Sandbox, biz.LogOptions) (io.ReadCloser, error) { return pipeReader, nil })}
	err := reader.StreamLogs(ctx, s, biz.LogOptions{TailLines: 1, Follow: true}, func(f biz.LogFrame) error {
		if f.Kind == "READY" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestSandboxLogsUnreadyAndLifecycleFences(t *testing.T) {
	client, provider, s, ctx := environmentFixture(t)
	pod, err := client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Get(ctx, s.ProviderRefs.PodName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pod.Object["status"] = map[string]any{"phase": "Pending"}
	if _, err = client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	s.Observed = biz.ObservedProvisioning
	store := &sandboxLogsStore{sandbox: s}
	opened := 0
	reader := &KubernetesSandboxLogs{provider: provider, reader: store, source: sandboxLogsSource(func(context.Context, biz.Sandbox, biz.LogOptions) (io.ReadCloser, error) {
		opened++
		return io.NopCloser(strings.NewReader("startup diagnostics")), nil
	})}
	var data string
	emit := func(frame biz.LogFrame) error { data += string(frame.Data); return nil }
	if err = reader.StreamLogs(ctx, s, biz.LogOptions{TailLines: 1}, emit); err != nil || data != "startup diagnostics" {
		t.Fatalf("unready logs data=%q err=%v", data, err)
	}
	for _, state := range []string{"pause", "pending", "deleted"} {
		changed := s
		switch state {
		case "pause":
			changed.Desired = biz.DesiredPaused
		case "pending":
			changed.Pending = &biz.Mutation{ID: "pending"}
		case "deleted":
			changed.Desired = biz.DesiredDeleted
		}
		before := opened
		if err = reader.StreamLogs(ctx, changed, biz.LogOptions{TailLines: 1}, emit); !errors.Is(err, biz.ErrDataPlaneUnavailable) || opened != before {
			t.Fatalf("%s: opened=%d err=%v", state, opened-before, err)
		}
	}
}

func TestSandboxLogsStopsBeforeNextFrameWhenPaused(t *testing.T) {
	_, provider, s, ctx := environmentFixture(t)
	store := &sandboxLogsStore{sandbox: s}
	pipeReader, pipeWriter := io.Pipe()
	defer pipeWriter.Close()
	reader := &KubernetesSandboxLogs{provider: provider, reader: store, source: sandboxLogsSource(func(context.Context, biz.Sandbox, biz.LogOptions) (io.ReadCloser, error) { return pipeReader, nil })}
	var data string
	var endReason string
	err := reader.StreamLogs(ctx, s, biz.LogOptions{TailLines: 1, Follow: true}, func(frame biz.LogFrame) error {
		switch frame.Kind {
		case "READY":
			go func() { _, _ = io.WriteString(pipeWriter, "first") }()
		case "DATA":
			data += string(frame.Data)
			store.sandbox.Desired = biz.DesiredPaused
			go func() { _, _ = io.WriteString(pipeWriter, "must-not-leak") }()
		case "END":
			endReason = frame.Reason
		}
		return nil
	})
	if err != nil || data != "first" || endReason != "TARGET_UNAVAILABLE" {
		t.Fatalf("data=%q end=%q err=%v", data, endReason, err)
	}
}
