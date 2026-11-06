package sandbox

import (
	"context"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type environmentRepo struct {
	serviceRepo
	states []biz.Sandbox
	calls  int
}

func (r *environmentRepo) Get(_ context.Context, tenant, id string) (biz.Sandbox, error) {
	r.calls++
	if tenant != testTenant || id != testSandbox || len(r.states) == 0 {
		return biz.Sandbox{}, biz.ErrNotFound
	}
	i := r.calls - 1
	if i >= len(r.states) {
		i = len(r.states) - 1
	}
	return r.states[i], nil
}

type environmentReader struct {
	items []biz.EnvironmentVariable
	calls int
}

func (r *environmentReader) ReadEnvironment(context.Context, biz.Sandbox) ([]biz.EnvironmentVariable, error) {
	r.calls++
	return append([]biz.EnvironmentVariable(nil), r.items...), nil
}
func environmentService() (*Service, *environmentRepo, *environmentReader) {
	state := biz.Sandbox{ID: testSandbox, TenantID: testTenant, Version: 1, Desired: biz.DesiredRunning, Observed: biz.ObservedRunning, Snapshot: biz.Snapshot{NamespaceUID: "namespace-uid"}, ProviderRefs: biz.ProviderRefs{PodName: "sandbox-" + testSandbox, PodUID: "pod-uid"}}
	repo := &environmentRepo{states: []biz.Sandbox{state}}
	reader := &environmentReader{items: []biz.EnvironmentVariable{{Name: "Z", Source: "downward_api"}, {Name: "A", Source: "downward_api"}, {Name: "M", Source: "downward_api"}}}
	return &Service{repo: repo, config: Config{EnvironmentReader: reader}}, repo, reader
}
func TestListEnvironmentVariablesScopesSortsAndPaginates(t *testing.T) {
	svc, repo, reader := environmentService()
	req := &pb.ListEnvironmentVariablesRequest{TenantId: testTenant, SandboxId: testSandbox, Page: 2, PageSize: 1}
	result, err := svc.ListEnvironmentVariables(serviceContext(), req)
	if err != nil || result.Total != 3 || len(result.Items) != 1 || result.Items[0].Name != "M" || repo.calls != 2 || reader.calls != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	req.Page = ^uint32(0)
	result, err = svc.ListEnvironmentVariables(serviceContext(), req)
	if err != nil || result.Total != 3 || len(result.Items) != 0 {
		t.Fatalf("last page=%+v %v", result, err)
	}
	req.PageSize = 101
	if _, err = svc.ListEnvironmentVariables(serviceContext(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("size error %v", err)
	}
	req.PageSize = 0
	req.TenantId = "66666666-6666-4666-8666-666666666666"
	before := reader.calls
	if _, err = svc.ListEnvironmentVariables(serviceContext(), req); status.Code(err) != codes.PermissionDenied || reader.calls != before {
		t.Fatalf("tenant error %v calls %d", err, reader.calls)
	}
}
func TestListEnvironmentVariablesRejectsConcurrentLifecycleChange(t *testing.T) {
	svc, repo, _ := environmentService()
	changed := repo.states[0]
	changed.Version++
	changed.Desired = biz.DesiredPaused
	repo.states = append(repo.states, changed)
	_, err := svc.ListEnvironmentVariables(serviceContext(), &pb.ListEnvironmentVariablesRequest{TenantId: testTenant, SandboxId: testSandbox})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("changed sandbox was not fenced: %v", err)
	}
}
