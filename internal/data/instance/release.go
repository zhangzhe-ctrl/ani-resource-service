package instance

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type ReleaseClient interface {
	ObserveRelease(context.Context, *pb.ObserveReleaseRequest, ...grpc.CallOption) (*pb.ReleaseObservation, error)
}
type PhysicalReleaseObserver struct{ client ReleaseClient }

func NewPhysicalReleaseObserver(client ReleaseClient) (*PhysicalReleaseObserver, error) {
	if client == nil {
		return nil, biz.ErrInvalid
	}
	return &PhysicalReleaseObserver{client}, nil
}

var releaseDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (o *PhysicalReleaseObserver) ObserveRelease(ctx context.Context, i biz.Instance) (biz.ReleaseEvidence, error) {
	if i.Desired != biz.Deleted || i.Pending != nil || i.ComputeSealAt.IsZero() || !i.ExternalComputeStarted || i.Spec.ComputeType != biz.TypeGPU {
		return biz.ReleaseEvidence{}, biz.ErrInvalid
	}
	ref := &pb.GpuUsageRef{TenantId: i.TenantID, OwnerService: "ani-resource-service", ResourceId: i.ID, CreateOperationId: i.CreateOperationID}
	pods := make([]*pb.PodRef, 0, len(i.GPUPods))
	for _, p := range i.GPUPods {
		pods = append(pods, &pb.PodRef{ClusterId: p.ClusterID, Namespace: p.Namespace, Name: p.Name, Uid: p.UID, ContainerName: p.ContainerName})
	}
	// The direct owner mTLS identity grants only this read. Incoming actor or
	// Governance metadata is never copied onto this outgoing channel.
	call, cancel := context.WithTimeout(metadata.NewOutgoingContext(ctx, metadata.Pairs("x-ani-tenant-id", i.TenantID, "x-ani-action", "ObserveRelease")), 5*time.Second)
	defer cancel()
	r, err := o.client.ObserveRelease(call, &pb.ObserveReleaseRequest{RequestId: uuid.NewString(), Ref: ref, DeleteOperationId: i.LastOperationID, Pods: pods, ClusterId: i.Spec.ClusterID, SealedAt: i.ComputeSealAt.Format(time.RFC3339Nano)})
	if err != nil {
		return biz.ReleaseEvidence{}, err
	}
	if r == nil || r.Ref == nil || r.Ref.TenantId != ref.TenantId || r.Ref.OwnerService != ref.OwnerService || r.Ref.ResourceId != ref.ResourceId || r.Ref.CreateOperationId != ref.CreateOperationId || !releaseDigest.MatchString(r.EvidenceDigest) {
		return biz.ReleaseEvidence{}, fmt.Errorf("invalid physical release receipt")
	}
	confirmed := r.Finding == pb.ReleaseFinding_ALLOCATION_RELEASED && r.Observation != nil && r.Observation.Complete && len(r.Observation.Issues) == 0 && len(r.ActivePodUids) == 0 && len(r.UncertainNodeUids) == 0
	return biz.ReleaseEvidence{Confirmed: confirmed, Digest: r.EvidenceDigest}, nil
}
