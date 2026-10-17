package instance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type releaseClientFake struct {
	req   *pb.ObserveReleaseRequest
	md    metadata.MD
	reply *pb.ReleaseObservation
	err   error
}

func (f *releaseClientFake) ObserveRelease(ctx context.Context, r *pb.ObserveReleaseRequest, _ ...grpc.CallOption) (*pb.ReleaseObservation, error) {
	f.req = r
	f.md, _ = metadata.FromOutgoingContext(ctx)
	if f.err != nil {
		return nil, f.err
	}
	reply := proto.Clone(f.reply).(*pb.ReleaseObservation)
	if reply.Ref == nil {
		reply.Ref = proto.Clone(r.Ref).(*pb.GpuUsageRef)
	}
	return reply, nil
}
func TestPhysicalReleaseObserverScopeAndFailClosed(t *testing.T) {
	i := biz.Instance{TenantID: uuid.NewString(), ID: uuid.NewString(), CreateOperationID: uuid.NewString(), LastOperationID: uuid.NewString(), Desired: biz.Deleted, ExternalComputeStarted: true, ComputeSealAt: time.Now().UTC(), Spec: biz.Spec{ComputeType: biz.TypeGPU, ClusterID: uuid.NewString()}}
	i.GPUPods = []biz.PodRef{{ClusterID: i.Spec.ClusterID, Namespace: "tenant", Name: "retired-0", UID: "retired-uid", ContainerName: biz.Container}}
	good := &pb.ReleaseObservation{Finding: pb.ReleaseFinding_ALLOCATION_RELEASED, Observation: &pb.Observation{Complete: true}, EvidenceDigest: strings.Repeat("a", 64)}
	f := &releaseClientFake{reply: good}
	o, _ := NewPhysicalReleaseObserver(f)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-actor-id", "must-not-forward", "x-ani-tenant-id", "wrong"))
	evidence, e := o.ObserveRelease(ctx, i)
	if e != nil || !evidence.Confirmed || f.req.ClusterId != i.Spec.ClusterID || f.req.SealedAt != i.ComputeSealAt.Format(time.RFC3339Nano) || len(f.req.Pods) != 1 || f.req.Pods[0].Uid != "retired-uid" || len(f.md) != 2 || f.md.Get("x-ani-tenant-id")[0] != i.TenantID || f.md.Get("x-ani-action")[0] != "ObserveRelease" {
		t.Fatal("lost sealed scope or inherited caller metadata", e)
	}
	for _, mutate := range []func(*pb.ReleaseObservation){
		func(r *pb.ReleaseObservation) { r.Finding = pb.ReleaseFinding_RELEASE_UNKNOWN },
		func(r *pb.ReleaseObservation) { r.Observation.Complete = false },
		func(r *pb.ReleaseObservation) { r.Observation.Issues = []string{"SOURCE_UNKNOWN"} },
		func(r *pb.ReleaseObservation) { r.ActivePodUids = []string{"retired-uid"} },
		func(r *pb.ReleaseObservation) { r.UncertainNodeUids = []string{"offline-node"} },
		func(r *pb.ReleaseObservation) { r.Ref = &pb.GpuUsageRef{TenantId: uuid.NewString()} },
		func(r *pb.ReleaseObservation) { r.EvidenceDigest = "untrusted" },
	} {
		f.reply = proto.Clone(good).(*pb.ReleaseObservation)
		mutate(f.reply)
		v, _ := o.ObserveRelease(ctx, i)
		if v.Confirmed {
			t.Fatal("untrusted physical observation confirmed")
		}
	}
	f.reply = good
	f.err = errors.New("ACC unavailable")
	if v, e := o.ObserveRelease(ctx, i); e == nil || v.Confirmed {
		t.Fatal("unavailable source confirmed")
	}
	f.err = nil
	i.GPUPods = nil
	if v, e := o.ObserveRelease(ctx, i); e != nil || !v.Confirmed || len(f.req.Pods) != 0 {
		t.Fatal("owner-wide empty scope fence unavailable", e)
	}
	i.ComputeSealAt = time.Time{}
	if _, e := o.ObserveRelease(ctx, i); e == nil {
		t.Fatal("unsealed scope sent")
	}
}
