package sandbox

import (
	"context"
	"testing"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
)

type previewWorkerTestRepository struct {
	record                       *biz.ExperimentalPreviewRecord
	prepared, received, observed int
	outcome                      string
}

func (r *previewWorkerTestRepository) NextExperimentalPreviewWork(context.Context, biz.Work, bool) (*biz.ExperimentalPreviewRecord, error) {
	return r.record, nil
}
func (r *previewWorkerTestRepository) PrepareExperimentalPreviewMutation(context.Context, biz.Work, string, biz.PreviewExperimentMutation, bool) error {
	r.prepared++
	return nil
}
func (r *previewWorkerTestRepository) RecordExperimentalPreviewReceipt(_ context.Context, _ biz.Work, _ string, _ biz.PreviewExperimentMutation, outcome string, _ string) error {
	r.received++
	r.outcome = outcome
	return nil
}
func (r *previewWorkerTestRepository) ObserveExperimentalPreviewMutation(context.Context, biz.Work, string, biz.PreviewExperimentMutation, bool, string, bool) error {
	r.observed++
	return nil
}
func (r *previewWorkerTestRepository) RecordExperimentalPreviewAddress(context.Context, biz.Work, string, string, string) error {
	return nil
}

func TestExperimentalPreviewWorkerKeepsUnverifiedAndUnknownPending(t *testing.T) {
	id, _, _ := previewFixture()
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}, ProviderRefs: biz.ProviderRefs{PodName: id.podName, PodUID: id.podUID}}
	repo := &previewWorkerTestRepository{record: &biz.ExperimentalPreviewRecord{TenantID: id.tenant, SandboxID: id.sandbox, PreviewID: uuid.NewString(), PodUID: id.podUID, Port: uint16(id.port), Desired: "open"}}
	cli := fake.NewSimpleDynamicClient(runtime.NewScheme())
	p := experimentalNodePortProvider{client: cli, verify: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }, verifyScope: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }}
	w := experimentalPreviewWorker{repo: repo, provider: p, enabled: true}
	if err := w.StepExperimentalPreview(context.Background(), biz.Work{Sandbox: s}); err != nil {
		t.Fatal(err)
	}
	if repo.prepared != 0 || len(cli.Actions()) != 0 {
		t.Fatal("disabled preview dispatched a listener")
	}
	repo.record.Pending = &biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
	if err := w.StepExperimentalPreview(context.Background(), biz.Work{Sandbox: s}); err != nil {
		t.Fatal(err)
	}
	if repo.observed != 0 || repo.prepared != 0 || repo.received != 0 {
		t.Fatal("absent unknown create incorrectly resolved")
	}
}

func TestExperimentalPreviewWorkerRecordsPreflightNotSent(t *testing.T) {
	id, _, _ := previewFixture()
	s := biz.Sandbox{TenantID: id.tenant, ID: id.sandbox, Snapshot: biz.Snapshot{Namespace: id.namespace}, ProviderRefs: biz.ProviderRefs{PodName: id.podName, PodUID: id.podUID}}
	repo := &previewWorkerTestRepository{record: &biz.ExperimentalPreviewRecord{TenantID: id.tenant, SandboxID: id.sandbox, PreviewID: uuid.NewString(), PodUID: id.podUID, Port: uint16(id.port), Desired: "open"}}
	cli := fake.NewSimpleDynamicClient(runtime.NewScheme())
	p := experimentalNodePortProvider{client: cli, enabled: true, verify: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return biz.ErrOwnership }, verifyScope: func(context.Context, biz.Sandbox, previewObjectIdentity) error { return nil }}
	w := experimentalPreviewWorker{repo: repo, provider: p, enabled: true}
	if err := w.StepExperimentalPreview(context.Background(), biz.Work{Sandbox: s}); err != nil {
		t.Fatal(err)
	}
	if repo.prepared != 1 || repo.received != 1 || repo.outcome != "not_sent" || len(cli.Actions()) != 0 {
		t.Fatalf("preflight prepared=%d received=%d outcome=%q actions=%v", repo.prepared, repo.received, repo.outcome, cli.Actions())
	}
}
