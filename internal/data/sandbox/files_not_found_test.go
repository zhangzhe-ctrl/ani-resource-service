package sandbox

import (
	"context"
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

func TestKubernetesFilesMapsMissingDirectoryToNotFound(t *testing.T) {
	const cluster = "44444444-4444-4444-8444-444444444444"
	const tenant = "11111111-1111-4111-8111-111111111111"
	const id = "22222222-2222-4222-8222-222222222222"
	executor, err := NewKubernetesCodeExecutor(&rest.Config{Host: "https://kubernetes.invalid", Timeout: time.Second}, cluster)
	if err != nil {
		t.Fatal(err)
	}
	executor.newStream = func(*rest.Config, string, *url.URL) (remotecommand.Executor, error) {
		return fakeCodeRunStream(func(_ context.Context, opts remotecommand.StreamOptions) error {
			_, _ = io.WriteString(opts.Stderr, `{"error":"not_found"}`)
			return codeRunExit(1)
		}), nil
	}
	ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: tenant, Actor: "user"})
	target := biz.DataPlaneTarget{TenantID: tenant, SandboxID: id, Action: biz.DataPlaneFiles, Version: 1, ClusterID: cluster, Namespace: "tenant-test", NamespaceUID: "ns-uid", PodName: "sandbox-" + id, PodUID: "pod-uid", Container: "sandbox"}
	_, err = executor.ExecuteFiles(ctx, target, biz.FileRequest{Action: "list", Path: "missing", Page: 1, PageSize: 20})
	if !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("missing directory: got %v, want not found", err)
	}
}
