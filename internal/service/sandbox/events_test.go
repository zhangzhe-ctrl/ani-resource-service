package sandbox

import (
	"context"
	"testing"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type eventReader struct {
	items []biz.Event
	calls int
}

func (r *eventReader) ReadEvents(context.Context, biz.Sandbox, []biz.PodHistory) ([]biz.Event, error) {
	r.calls++
	return append([]biz.Event(nil), r.items...), nil
}

type eventHistoryRepo struct {
	*environmentRepo
	history [][]biz.PodHistory
	calls   int
}

func (r *eventHistoryRepo) ListSandboxPodHistory(_ context.Context, tenant, sandbox string) ([]biz.PodHistory, error) {
	if tenant != testTenant || sandbox != testSandbox {
		return nil, biz.ErrNotFound
	}
	i := r.calls
	r.calls++
	if i >= len(r.history) {
		i = len(r.history) - 1
	}
	return append([]biz.PodHistory(nil), r.history[i]...), nil
}
func eventService() (*Service, *environmentRepo, *eventHistoryRepo) {
	svc, repo, _ := environmentService()
	history := &eventHistoryRepo{environmentRepo: repo, history: [][]biz.PodHistory{{{PodUID: "pod-uid"}}}}
	svc.repo = history
	return svc, repo, history
}
func TestListSandboxEventsPaginationAndScope(t *testing.T) {
	svc, repo, _ := eventService()
	at := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	reader := &eventReader{items: []biz.Event{{ID: "a", OccurredAt: at, Type: "Normal", Reason: "Started", Message: "safe"}, {ID: "b", OccurredAt: at, Type: "Normal", Reason: "Started", Message: "safe"}, {ID: "c", OccurredAt: at.Add(time.Second), Type: "Warning", Reason: "Failed", Message: "safe"}}}
	svc.config.EventReader = reader
	r := &pb.ListSandboxEventsRequest{TenantId: testTenant, SandboxId: testSandbox, Page: 2, PageSize: 1}
	got, err := svc.ListSandboxEvents(serviceContext(), r)
	if err != nil || got.Total != 3 || got.Scope != "retained_events_for_recorded_pods" || got.HistoryCompleteBeforeMigration || len(got.Items) != 1 || got.Items[0].Id != "b" || got.Items[0].OccurredAt != "2026-01-01T01:00:00Z" || repo.calls != 2 {
		t.Fatalf("got %+v err %v", got, err)
	}
	r.Page = ^uint32(0)
	got, err = svc.ListSandboxEvents(serviceContext(), r)
	if err != nil || got.Total != 3 || len(got.Items) != 0 {
		t.Fatalf("overflow %+v %v", got, err)
	}
	r.PageSize = 101
	if _, err = svc.ListSandboxEvents(serviceContext(), r); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid page: %v", err)
	}
	r.PageSize = 1
	r.TenantId = "66666666-6666-4666-8666-666666666666"
	before := reader.calls
	if _, err = svc.ListSandboxEvents(serviceContext(), r); status.Code(err) != codes.PermissionDenied || reader.calls != before {
		t.Fatalf("tenant: %v", err)
	}
}
func TestListSandboxEventsRejectsConcurrentLifecycle(t *testing.T) {
	svc, repo, _ := eventService()
	svc.config.EventReader = &eventReader{}
	changed := repo.states[0]
	changed.ProviderRefs.PodUID = "new"
	repo.states = append(repo.states, changed)
	_, err := svc.ListSandboxEvents(serviceContext(), &pb.ListSandboxEventsRequest{TenantId: testTenant, SandboxId: testSandbox})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("%v", err)
	}
}

func TestListSandboxEventsRetainsKnownHistoryAfterPauseAndDelete(t *testing.T) {
	for _, state := range []string{biz.DesiredPaused, biz.DesiredDeleted} {
		t.Run(state, func(t *testing.T) {
			svc, repo, _ := eventService()
			repo.states[0].Desired = state
			repo.states[0].ProviderRefs.PodUID = ""
			svc.config.EventReader = &eventReader{items: []biz.Event{{ID: "event", Type: "Warning", Reason: "Failed", Message: "safe", OccurredAt: time.Now()}}}
			result, err := svc.ListSandboxEvents(serviceContext(), &pb.ListSandboxEventsRequest{TenantId: testTenant, SandboxId: testSandbox})
			if err != nil || result.Total != 1 {
				t.Fatalf("result %+v err %v", result, err)
			}
		})
	}
}

func TestListSandboxEventsRejectsMissingOrChangingHistory(t *testing.T) {
	for _, missing := range []bool{true, false} {
		svc, _, repo := eventService()
		reader := &eventReader{}
		svc.config.EventReader = reader
		if missing {
			repo.history = [][]biz.PodHistory{nil}
		} else {
			repo.history = append(repo.history, []biz.PodHistory{{PodUID: "old"}, {PodUID: "new"}})
		}
		_, err := svc.ListSandboxEvents(serviceContext(), &pb.ListSandboxEventsRequest{TenantId: testTenant, SandboxId: testSandbox})
		if status.Code(err) != codes.FailedPrecondition || (missing && reader.calls != 0) {
			t.Fatalf("missing=%v calls=%d err=%v", missing, reader.calls, err)
		}
	}
}
