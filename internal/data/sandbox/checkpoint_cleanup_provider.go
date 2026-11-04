package sandbox

// Candidate-only checkpoint cleanup adapter. It must be driven by a durable
// delete journal with exclusive source-checkpoint references. It is not wired.
import (
	"context"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// checkpointDeleteSnapshot verifies the recorded Snapshot/Content identities
// before a UID-preconditioned delete. A caller must journal the intent before
// sending it; retries must retain that same journal and original identities.
func (p *KubernetesProvider) checkpointDeleteSnapshot(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (checkpointProviderReceipt, error) {
	out := checkpointProviderReceipt{Outcome: biz.MutationNotSent}
	if c.TenantID != s.TenantID || c.SandboxID != s.ID || c.SnapshotName != "sandbox-checkpoint-"+c.ID || c.SnapshotUID == "" || c.ContentUID == "" || c.ContentName == "" || c.SnapshotHandle == "" {
		return out, biz.ErrOwnership
	}
	obs, err := p.observeRecordedCheckpoint(ctx, s, c, true)
	if err != nil {
		return out, err
	}
	if obs.State != "deletable" {
		return out, biz.ErrOwnership
	}
	uid := types.UID(c.SnapshotUID)
	var trace checkpointWriteTrace
	err = p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Delete(trace.context(ctx), c.SnapshotName, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	out.Outcome = trace.outcome(err)
	if err != nil {
		return out, err
	}
	out.UID = c.SnapshotUID
	return out, nil
}

// checkpointCleanupObserved is valid only after a durable delete intent has
// been dispatched or has an unknown outcome. Under the platform's trusted CSI
// controller/administrator boundary, Content disappearance after an exact-UID
// Delete-class snapshot deletion is the controller's cleanup receipt.
func (p *KubernetesProvider) checkpointCleanupObserved(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (bool, error) {
	if c.TenantID != s.TenantID || c.SandboxID != s.ID || c.SnapshotUID == "" || c.ContentUID == "" || c.ContentName == "" || c.SnapshotHandle == "" || c.SnapshotName != "sandbox-checkpoint-"+c.ID {
		return false, biz.ErrOwnership
	}
	if err := p.requireCluster(s); err != nil {
		return false, err
	}
	if _, err := p.verifyTenantNamespace(ctx, s, c.Source.NamespaceUID); err != nil {
		return false, err
	}
	snap, err := p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(ctx, c.SnapshotName, metav1.GetOptions{})
	if err == nil {
		if string(snap.GetUID()) != c.SnapshotUID {
			return false, biz.ErrOwnership
		}
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}
	content, err := p.client.Resource(checkpointContentGVR).Get(ctx, c.ContentName, metav1.GetOptions{})
	if err == nil {
		if string(content.GetUID()) != c.ContentUID {
			return false, biz.ErrOwnership
		}
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}
	if _, err = p.verifyTenantNamespace(ctx, s, c.Source.NamespaceUID); err != nil {
		return false, err
	}
	return true, nil
}

func (p *KubernetesProvider) DeleteCheckpointSnapshot(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (string, error) {
	receipt, err := p.checkpointDeleteSnapshot(ctx, s, c)
	return receipt.Outcome, err
}

func (p *KubernetesProvider) ObserveCheckpointCleanup(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (bool, error) {
	return p.checkpointCleanupObserved(ctx, s, c)
}

var _ biz.CheckpointCleanupProvider = (*KubernetesProvider)(nil)
