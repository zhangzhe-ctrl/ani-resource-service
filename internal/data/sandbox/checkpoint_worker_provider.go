package sandbox

import (
	"context"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ biz.CheckpointProvider = (*KubernetesProvider)(nil)

func checkpointProviderSource(s biz.CheckpointSource) checkpointWorkspaceIdentity {
	return checkpointWorkspaceIdentity{NamespaceUID: s.NamespaceUID, PVCName: s.PVCName, PVCUID: s.PVCUID, PVName: s.PVName, PVUID: s.PVUID, Driver: s.CSIDriver, VolumeHandle: s.VolumeHandle}
}
func checkpointProviderClass(s biz.CheckpointSource) checkpointClassIdentity {
	return checkpointClassIdentity{Name: s.SnapshotClass, UID: s.SnapshotClassUID, ResourceVersion: s.SnapshotClassResourceVersion}
}
func checkpointDiagnosticError(err error) error {
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return biz.ErrDenied
	}
	return err
}
func (p *KubernetesProvider) PreflightCheckpoint(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (out biz.CheckpointSource, result error) {
	defer func() { result = checkpointDiagnosticError(result) }()
	if c.TenantID != s.TenantID || c.SandboxID != s.ID {
		return out, biz.ErrOwnership
	}
	src, err := p.checkpointWorkspace(ctx, s)
	if err != nil {
		return out, err
	}
	if src.NamespaceUID != c.Source.NamespaceUID || src.PVCName != c.Source.PVCName || src.PVCUID != c.Source.PVCUID || src.PVName != c.Source.PVName || src.PVUID != c.Source.PVUID {
		return out, biz.ErrOwnership
	}
	class, err := p.checkpointSelectClass(ctx, src.Driver)
	if err != nil {
		return out, err
	}
	out = c.Source
	out.CSIDriver, out.VolumeHandle, out.SnapshotClass, out.SnapshotClassUID, out.SnapshotClassResourceVersion = src.Driver, src.VolumeHandle, class.Name, class.UID, class.ResourceVersion
	return out, nil
}
func (p *KubernetesProvider) ApplyCheckpointMutation(ctx context.Context, s biz.Sandbox, c biz.Checkpoint, m biz.CheckpointMutation) (biz.CheckpointMutationReceipt, error) {
	out := biz.CheckpointMutationReceipt{Mutation: m, Outcome: biz.MutationNotSent}
	if c.TenantID != s.TenantID || c.SandboxID != s.ID {
		return out, biz.ErrOwnership
	}
	src, class := checkpointProviderSource(c.Source), checkpointProviderClass(c.Source)
	var receipt checkpointProviderReceipt
	var err error
	switch m.Kind {
	case "protect":
		receipt, err = p.checkpointProtectPVC(ctx, s, src, c.ID, true)
	case "create":
		receipt, err = p.checkpointCreateSnapshot(ctx, s, c.ID, m.ID, c.ID, src, class)
	case "release":
		receipt, err = p.checkpointProtectPVC(ctx, s, src, c.ID, false)
	default:
		return out, biz.ErrInvalid
	}
	out.Outcome, out.UID = receipt.Outcome, receipt.UID
	return out, err
}
func (p *KubernetesProvider) ObserveCheckpointMutation(ctx context.Context, s biz.Sandbox, c biz.Checkpoint, m biz.CheckpointMutation) (out biz.CheckpointMutationReceipt, result error) {
	defer func() { result = checkpointDiagnosticError(result) }()
	out = biz.CheckpointMutationReceipt{Mutation: m, Outcome: biz.MutationUnknown}
	if c.TenantID != s.TenantID || c.SandboxID != s.ID || m.ID != c.MutationID || m.Kind != c.MutationKind {
		return out, biz.ErrOwnership
	}
	src, class := checkpointProviderSource(c.Source), checkpointProviderClass(c.Source)
	if m.Kind == "create" {
		obs, err := p.checkpointObserve(ctx, s, c.ID, m.ID, "", "", src, class)
		if err != nil {
			return out, err
		}
		if obs.SnapshotUID != "" && (obs.State == "creating" || obs.State == "ready") {
			out.Outcome, out.UID = biz.MutationApplied, obs.SnapshotUID
		}
		return out, nil
	}
	if m.Kind != "protect" && m.Kind != "release" {
		return out, biz.ErrInvalid
	}
	if err := p.requireCluster(s); err != nil {
		return out, err
	}
	if _, err := p.verifyTenantNamespace(ctx, s, src.NamespaceUID); err != nil {
		return out, err
	}
	obj, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, src.PVCName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if !owned(obj, s) || string(obj.GetUID()) != src.PVCUID || obj.GetResourceVersion() == "" {
		return out, biz.ErrOwnership
	}
	count := 0
	for _, f := range obj.GetFinalizers() {
		if f == checkpointPVCFinalizer {
			count++
		}
	}
	marker := obj.GetAnnotations()[checkpointPVCProtectionID]
	if count > 1 || (marker != "" && marker != c.ID) {
		return out, biz.ErrOwnership
	}
	if (m.Kind == "protect" && count == 1 && marker == c.ID) || (m.Kind == "release" && count == 0 && marker == "") {
		if _, err = p.verifyTenantNamespace(ctx, s, src.NamespaceUID); err != nil {
			return out, err
		}
		out.Outcome, out.UID = biz.MutationApplied, src.PVCUID
	}
	return out, nil
}
func (p *KubernetesProvider) ObserveCheckpointReady(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (out biz.CheckpointReadyObservation, result error) {
	defer func() { result = checkpointDiagnosticError(result) }()
	if c.TenantID != s.TenantID || c.SandboxID != s.ID {
		return out, biz.ErrOwnership
	}
	obs, err := p.checkpointObserve(ctx, s, c.ID, c.SnapshotMutationID, c.SnapshotUID, c.ContentUID, checkpointProviderSource(c.Source), checkpointProviderClass(c.Source))
	if err != nil {
		return out, err
	}
	out.Reason = obs.Reason
	if obs.State == "ready" {
		out = biz.CheckpointReadyObservation{Ready: true, SnapshotUID: obs.SnapshotUID, ContentName: obs.ContentName, ContentUID: obs.ContentUID, SnapshotHandle: obs.SnapshotHandle}
	}
	return out, nil
}
