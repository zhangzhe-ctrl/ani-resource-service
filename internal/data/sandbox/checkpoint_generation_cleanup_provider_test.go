package sandbox

import (
	"context"
	"errors"
	"io"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ktesting "k8s.io/client-go/testing"
)

func TestWorkspaceGenerationDeleteUsesUIDAndUnknownDoesNotClaimReclaim(t *testing.T) {
	c, p, s, source, _ := checkpointProviderFixture(t)
	pv, err := c.Resource(pvGVR).Get(context.Background(), source.PVName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pv.SetFinalizers([]string{csiPVReclaimFinalizer})
	if _, err = c.Resource(pvGVR).Update(context.Background(), pv, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	s.Desired = biz.DesiredDeleted
	g := biz.WorkspaceGenerationCleanup{TenantID: s.TenantID, SandboxID: s.ID, GenerationID: ckID, Refs: s.ProviderRefs, Driver: source.Driver, VolumeHandle: source.VolumeHandle}
	obs, err := p.ObserveWorkspaceGeneration(context.Background(), s, g)
	if err != nil || !obs.PVCExists || !obs.PVExists || obs.Driver != source.Driver {
		t.Fatalf("source: %#v %v", obs, err)
	}
	pinned := false
	c.PrependReactor("delete", "persistentvolumeclaims", func(action ktesting.Action) (bool, runtime.Object, error) {
		opts := action.(ktesting.DeleteAction).GetDeleteOptions()
		pinned = opts.Preconditions != nil && opts.Preconditions.UID != nil && string(*opts.Preconditions.UID) == source.PVCUID
		return true, nil, io.ErrUnexpectedEOF
	})
	outcome, err := p.DeleteWorkspaceGenerationPVC(context.Background(), s, g, ckMutation)
	if outcome != biz.MutationUnknown || !errors.Is(err, io.ErrUnexpectedEOF) || !pinned {
		t.Fatalf("unknown %s %v pinned=%v", outcome, err, pinned)
	}
	obs, err = p.ObserveWorkspaceGeneration(context.Background(), s, g)
	if err != nil || !obs.PVCExists || !obs.PVExists {
		t.Fatalf("lost source evidence: %#v %v", obs, err)
	}
}
func TestWorkspaceGenerationRequiresCSIFinalizer(t *testing.T) {
	_, p, s, _, _ := checkpointProviderFixture(t)
	s.Desired = biz.DesiredDeleted
	g := biz.WorkspaceGenerationCleanup{TenantID: s.TenantID, SandboxID: s.ID, GenerationID: ckID, Refs: s.ProviderRefs}
	if _, err := p.ObserveWorkspaceGeneration(context.Background(), s, g); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("missing CSI reclaim finalizer: %v", err)
	}
}

func TestWorkspaceGenerationReplacementNeverCountsAsReclaimed(t *testing.T) {
	for _, kind := range []string{"pvc", "pv"} {
		t.Run(kind, func(t *testing.T) {
			c, p, s, source, _ := checkpointProviderFixture(t)
			s.Desired = biz.DesiredDeleted
			g := biz.WorkspaceGenerationCleanup{TenantID: s.TenantID, SandboxID: s.ID, GenerationID: ckID, Refs: s.ProviderRefs, Driver: source.Driver, VolumeHandle: source.VolumeHandle}
			ctx := context.Background()
			pv, err := c.Resource(pvGVR).Get(ctx, source.PVName, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			pv.SetFinalizers([]string{csiPVReclaimFinalizer})
			if kind == "pv" {
				pv.SetUID(types.UID("replacement-pv"))
			}
			if _, err = c.Resource(pvGVR).Update(ctx, pv, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			if kind == "pvc" {
				pvc, err := c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, source.PVCName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				pvc.SetUID(types.UID("replacement-pvc"))
				if _, err = c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if obs, err := p.ObserveWorkspaceGeneration(ctx, s, g); !errors.Is(err, biz.ErrOwnership) {
				t.Fatalf("replacement treated as absent: observation=%+v err=%v", obs, err)
			}
			if outcome, err := p.DeleteWorkspaceGenerationPVC(ctx, s, g, ckMutation); !errors.Is(err, biz.ErrOwnership) || outcome != biz.MutationNotSent {
				t.Fatalf("replacement delete attempted: outcome=%s err=%v", outcome, err)
			}
			for _, action := range c.Actions() {
				if action.GetVerb() == "delete" && action.GetResource().Resource == "persistentvolumeclaims" {
					t.Fatal("deleted replacement PVC")
				}
			}
		})
	}
}
