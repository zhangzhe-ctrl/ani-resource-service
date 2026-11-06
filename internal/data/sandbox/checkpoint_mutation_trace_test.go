package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Exercise the real dynamic client's Retry-After handling, not only a fake
// client reactor that bypasses HTTP writes and cannot detect multiple sends.
func TestCheckpointWritesOnlyClassifySingleDispatchRejection(t *testing.T) {
	for _, kind := range []string{"release", "snapshot-create", "snapshot-delete", "pvc-delete", "transfer-create"} {
		for _, retry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retry=%v", kind, retry), func(t *testing.T) {
				ctx := context.Background()
				c, p, s, transfer := transferProviderFixture(t, "Immediate")
				source := checkpointWorkspaceIdentity{NamespaceUID: s.Snapshot.NamespaceUID, PVCName: s.ProviderRefs.PVCName, PVCUID: s.ProviderRefs.PVCUID, PVName: s.ProviderRefs.PVName, PVUID: s.ProviderRefs.PVUID, Driver: "test.csi", VolumeHandle: "volume-handle"}
				s.Desired = biz.DesiredDeleted
				if kind == "snapshot-create" {
					if err := c.Tracker().Delete(checkpointSnapshotGVR, s.Snapshot.Namespace, "sandbox-checkpoint-"+ckID); err != nil {
						t.Fatal(err)
					}
				}
				pvc, err := c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, source.PVCName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				pvc.SetAnnotations(map[string]string{checkpointPVCProtectionID: ckProtection})
				pvc.SetFinalizers([]string{checkpointPVCFinalizer})
				if _, err = c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
				pv, err := c.Resource(pvGVR).Get(ctx, source.PVName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				pv.SetFinalizers([]string{csiPVReclaimFinalizer})
				if _, err = c.Resource(pvGVR).Update(ctx, pv, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet {
						calls++
						if retry && calls == 1 {
							// The first write commits even though its response is lost.
							var commitErr error
							switch kind {
							case "release":
								obj, e := c.Tracker().Get(pvcGVR, s.Snapshot.Namespace, source.PVCName)
								if e != nil {
									commitErr = e
								} else {
									u := obj.(*unstructured.Unstructured).DeepCopy()
									u.SetFinalizers(nil)
									u.SetAnnotations(nil)
									commitErr = c.Tracker().Update(pvcGVR, u, s.Snapshot.Namespace)
								}
							case "snapshot-delete":
								commitErr = c.Tracker().Delete(checkpointSnapshotGVR, s.Snapshot.Namespace, "sandbox-checkpoint-"+ckID)
							case "pvc-delete":
								commitErr = c.Tracker().Delete(pvcGVR, s.Snapshot.Namespace, source.PVCName)
							case "snapshot-create", "transfer-create":
								u := &unstructured.Unstructured{}
								if e := json.NewDecoder(r.Body).Decode(&u.Object); e != nil {
									commitErr = e
								} else {
									u.SetUID(types.UID("committed-uid"))
									u.SetResourceVersion("1")
									resource := checkpointSnapshotGVR
									if kind == "transfer-create" {
										resource = pvcGVR
									}
									commitErr = c.Tracker().Create(resource, u, s.Snapshot.Namespace)
								}
							}
							if commitErr != nil {
								t.Errorf("commit first write: %v", commitErr)
							}
							w.Header().Set("Retry-After", "1")
							w.WriteHeader(http.StatusInternalServerError)
							_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Reason: metav1.StatusReasonInternalError, Code: 500, Message: "ambiguous first write"})
							return
						}
						w.WriteHeader(http.StatusForbidden)
						_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Reason: metav1.StatusReasonForbidden, Code: 403, Message: "denied"})
						return
					}
					parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
					resource, name := parts[len(parts)-2], parts[len(parts)-1]
					var gvr schema.GroupVersionResource
					ns := ""
					switch resource {
					case "namespaces":
						gvr = namespaceGVR
					case "persistentvolumeclaims":
						gvr = pvcGVR
						ns = s.Snapshot.Namespace
					case "persistentvolumes":
						gvr = pvGVR
					case "pods":
						gvr = podGVR
						ns = s.Snapshot.Namespace
					case "volumesnapshots":
						gvr = checkpointSnapshotGVR
						ns = s.Snapshot.Namespace
					case "volumesnapshotcontents":
						gvr = checkpointContentGVR
					case "volumesnapshotclasses":
						gvr = checkpointSnapshotClassGVR
					case "storageclasses":
						gvr = storageClassGVR
					default:
						t.Errorf("unexpected GET path: %s", r.URL.Path)
						w.WriteHeader(500)
						return
					}
					obj, err := c.Tracker().Get(gvr, ns, name)
					if err != nil {
						if !apierrors.IsNotFound(err) {
							t.Errorf("tracker GET: %v", err)
						}
						w.WriteHeader(404)
						_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Reason: metav1.StatusReasonNotFound, Code: 404})
						return
					}
					_ = json.NewEncoder(w).Encode(obj)
				}))
				defer server.Close()
				client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL, Timeout: 10 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				p.client = client
				var outcome string
				switch kind {
				case "release":
					receipt, _ := p.checkpointProtectPVC(ctx, s, source, ckProtection, false)
					outcome = receipt.Outcome
				case "snapshot-create":
					class := checkpointClassIdentity{Name: "delete-class", UID: "class-uid", ResourceVersion: "1"}
					receipt, _ := p.checkpointCreateSnapshot(ctx, s, ckID, ckMutation, ckProtection, source, class)
					outcome = receipt.Outcome
				case "transfer-create":
					outcome, _, _ = p.CreateCheckpointTransferPVC(ctx, s, transfer, ckMutation)
				case "snapshot-delete":
					now := time.Now()
					cp := biz.Checkpoint{TenantID: s.TenantID, SandboxID: s.ID, ID: ckID, State: "ready", Phase: "done", CompletedAt: &now, SnapshotMutationID: ckMutation, SnapshotName: "sandbox-checkpoint-" + ckID, SnapshotUID: "snapshot-uid", ContentName: "content-name", ContentUID: "content-uid", SnapshotHandle: "underlying-snapshot-id", Source: biz.CheckpointSource{ClusterID: s.Snapshot.ClusterID, Namespace: s.Snapshot.Namespace, NamespaceUID: source.NamespaceUID, PVCName: source.PVCName, PVCUID: source.PVCUID, PVName: source.PVName, PVUID: source.PVUID, CSIDriver: source.Driver, VolumeHandle: source.VolumeHandle, SnapshotClass: "delete-class", SnapshotClassUID: "class-uid", SnapshotClassResourceVersion: "1"}}
					receipt, _ := p.checkpointDeleteSnapshot(ctx, s, cp)
					outcome = receipt.Outcome
				case "pvc-delete":
					g := biz.WorkspaceGenerationCleanup{TenantID: s.TenantID, SandboxID: s.ID, GenerationID: ckID, Refs: s.ProviderRefs, Driver: source.Driver, VolumeHandle: source.VolumeHandle}
					outcome, _ = p.DeleteWorkspaceGenerationPVC(ctx, s, g, ckMutation)
				}
				want, n := biz.MutationRejected, 1
				if retry {
					want, n = biz.MutationUnknown, 2
				}
				if outcome != want || calls != n {
					t.Fatalf("outcome=%s calls=%d; want %s %d", outcome, calls, want, n)
				}
				if retry {
					switch kind {
					case "release":
						obj, err := c.Tracker().Get(pvcGVR, s.Snapshot.Namespace, source.PVCName)
						if err != nil || len(obj.(*unstructured.Unstructured).GetFinalizers()) != 0 {
							t.Fatalf("first release did not commit: %v", err)
						}
					case "snapshot-delete", "pvc-delete":
						gvr, name := checkpointSnapshotGVR, "sandbox-checkpoint-"+ckID
						if kind == "pvc-delete" {
							gvr, name = pvcGVR, source.PVCName
						}
						if _, err := c.Tracker().Get(gvr, s.Snapshot.Namespace, name); !apierrors.IsNotFound(err) {
							t.Fatalf("first delete did not commit: %v", err)
						}
					case "snapshot-create", "transfer-create":
						gvr, name := checkpointSnapshotGVR, "sandbox-checkpoint-"+ckID
						if kind == "transfer-create" {
							gvr, name = pvcGVR, transfer.CandidateName
						}
						if _, err := c.Tracker().Get(gvr, s.Snapshot.Namespace, name); err != nil {
							t.Fatalf("first create did not commit: %v", err)
						}
					}
				}
			})
		}
	}
}
