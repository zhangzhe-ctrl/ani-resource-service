package sandbox

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
)

// experimentalPreviewWorker is intentionally not wired in the production
// composition root. NodePort is an explicitly enabled unauthenticated mode.
type experimentalPreviewWorker struct {
	repo     previewWorkerRepository
	provider experimentalNodePortProvider
	enabled  bool
}

type previewWorkerRepository interface {
	NextExperimentalPreviewWork(context.Context, biz.Work, bool) (*biz.ExperimentalPreviewRecord, error)
	PrepareExperimentalPreviewMutation(context.Context, biz.Work, string, biz.PreviewExperimentMutation, bool) error
	RecordExperimentalPreviewReceipt(context.Context, biz.Work, string, biz.PreviewExperimentMutation, string, string) error
	ObserveExperimentalPreviewMutation(context.Context, biz.Work, string, biz.PreviewExperimentMutation, bool, string, bool) error
	RecordExperimentalPreviewAddress(context.Context, biz.Work, string, string, string) error
}

func (w experimentalPreviewWorker) StepExperimentalPreview(ctx context.Context, work biz.Work) error {
	if w.repo == nil {
		return biz.ErrInvalid
	}
	r, err := w.repo.NextExperimentalPreviewWork(ctx, work, w.enabled)
	if err != nil || r == nil {
		return err
	}
	s := work.Sandbox
	id := previewObjectIdentity{tenant: r.TenantID, sandbox: r.SandboxID, namespace: s.Snapshot.Namespace, podName: "sandbox-" + r.SandboxID, podUID: r.PodUID, podIP: r.PodIP, serviceName: "sbp-" + r.PreviewID, serviceUID: r.ServiceUID, sliceName: "sbe-" + r.PreviewID, sliceUID: r.SliceUID, port: int32(r.Port)}
	serviceCreate := biz.PreviewExperimentMutation{ID: r.CreateMutationID, Kind: "create"}
	sliceCreate := biz.PreviewExperimentMutation{ID: r.SliceCreateMutationID, Kind: "create_slice", TargetUID: r.ServiceUID, PodIP: r.PodIP}
	if m := r.Pending; m != nil {
		switch m.Kind {
		case "create":
			uid, e := w.provider.observeSelectorService(ctx, s, id, *m)
			if e != nil || uid == "" {
				return e
			}
			return w.repo.ObserveExperimentalPreviewMutation(ctx, work, r.PreviewID, *m, true, uid, false)
		case "create_slice":
			uid, e := w.provider.observeSlice(ctx, s, id, *m)
			if e != nil || uid == "" {
				return e
			}
			return w.repo.ObserveExperimentalPreviewMutation(ctx, work, r.PreviewID, *m, true, uid, false)
		case "delete_slice":
			uid, e := w.provider.observeSlice(ctx, s, id, sliceCreate)
			if e != nil {
				return e
			}
			if uid != "" {
				return w.provider.deleteSlice(ctx, s, id, sliceCreate, *m)
			}
			empty, e := w.provider.noPreviewSlices(ctx, s, id)
			if e != nil || !empty {
				return e
			}
			return w.repo.ObserveExperimentalPreviewMutation(ctx, work, r.PreviewID, *m, false, "", true)
		case "delete":
			uid, e := w.provider.observeSelectorService(ctx, s, id, serviceCreate)
			if e != nil {
				return e
			}
			if uid != "" {
				return w.provider.deleteService(ctx, s, id, *m)
			}
			empty, e := w.provider.noPreviewSlices(ctx, s, id)
			if e != nil || !empty {
				return e
			}
			return w.repo.ObserveExperimentalPreviewMutation(ctx, work, r.PreviewID, *m, false, "", true)
		default:
			return biz.ErrInvalid
		}
	}
	var m biz.PreviewExperimentMutation
	if r.Desired == "closed" {
		if r.SliceUID != "" {
			m = biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "delete_slice", TargetUID: r.SliceUID}
		} else if r.ServiceUID != "" {
			m = biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "delete", TargetUID: r.ServiceUID}
		} else {
			return nil
		}
	} else {
		if !w.enabled || !w.provider.enabled || w.provider.verify == nil || w.provider.verifyScope == nil || w.provider.client == nil {
			return nil
		}
		if r.ServiceUID == "" {
			m = biz.PreviewExperimentMutation{ID: uuid.NewString(), Kind: "create"}
		} else {
			address, verifyErr := w.provider.selectorAddress(ctx, s, id, serviceCreate, r.Protocol)
			if verifyErr != nil {
				address = ""
			}
			return w.repo.RecordExperimentalPreviewAddress(ctx, work, r.PreviewID, r.ServiceUID, address)
		}
	}
	if err = w.repo.PrepareExperimentalPreviewMutation(ctx, work, r.PreviewID, m, w.enabled); err != nil {
		return err
	}
	var uid string
	switch m.Kind {
	case "create":
		uid, err = w.provider.createSelectorService(ctx, s, id, m)
	case "create_slice":
		uid, err = w.provider.createSlice(ctx, s, id, serviceCreate, m)
	case "delete_slice":
		err = w.provider.deleteSlice(ctx, s, id, sliceCreate, m)
	case "delete":
		err = w.provider.deleteService(ctx, s, id, m)
	}
	outcome := "applied"
	if err != nil {
		outcome = "unknown"
		if m.Kind == "create" && errors.Is(err, errPreviewNotSent) {
			outcome = "not_sent"
		} else if m.Kind == "create" && errors.Is(err, errPreviewRejected) {
			outcome = "rejected"
		}
		uid = ""
	}
	// A deadline may have canceled the provider context; reserve the worker's
	// final lease budget to persist the known not-sent or uncertain outcome.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	recordErr := w.repo.RecordExperimentalPreviewReceipt(persistCtx, work, r.PreviewID, m, outcome, uid)
	if recordErr != nil {
		return recordErr
	}
	if outcome == "not_sent" || outcome == "rejected" {
		return nil
	}
	return err
}

// NewExperimentalPreviewWorker always reconciles existing cleanup, even if
// enabled is false. The flag enables only creation of new NodePort listeners.
func NewExperimentalPreviewWorker(repo *Postgres, provider *KubernetesProvider, enabled bool) (biz.PreviewWorkRunner, error) {
	if repo == nil || provider == nil || provider.client == nil {
		return nil, biz.ErrInvalid
	}
	scope := func(ctx context.Context, s biz.Sandbox, _ previewObjectIdentity) error {
		if err := provider.requireCluster(s); err != nil {
			return err
		}
		_, err := provider.verifyTenantNamespace(ctx, s, s.Snapshot.NamespaceUID)
		return err
	}
	verify := func(ctx context.Context, s biz.Sandbox, id previewObjectIdentity) error {
		if err := scope(ctx, s, id); err != nil {
			return err
		}
		obj, err := provider.getOwned(ctx, podGVR, id.namespace, id.podName, s, id.podUID)
		if err != nil {
			return err
		}
		if obj == nil || obj.GetDeletionTimestamp() != nil || !provider.matchesPod(obj, s) {
			return biz.ErrOwnership
		}
		return scope(ctx, s, id)
	}
	return experimentalPreviewWorker{repo: repo, provider: experimentalNodePortProvider{client: provider.client, enabled: enabled, verify: verify, verifyScope: scope}, enabled: enabled}, nil
}
