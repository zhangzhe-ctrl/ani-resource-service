package instance

import (
	"context"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Service) diagnosticInstance(ctx context.Context, r *pb.GetInstanceRequest) (biz.Instance, error) {
	if r == nil || !biz.UUID(r.InstanceId) {
		return biz.Instance{}, biz.ErrInvalid
	}
	if e := biz.RequireTenant(ctx, r.TenantId); e != nil {
		return biz.Instance{}, e
	}
	i, e := s.repo.Get(ctx, r.TenantId, r.InstanceId)
	if e != nil {
		return i, e
	}
	if i.TenantID != r.TenantId || i.ID != r.InstanceId {
		return i, biz.ErrDenied
	}
	if i.Spec.ClusterID != s.config.ClusterID || s.diagnostics == nil {
		return i, biz.ErrDiagnosticTarget
	}
	return i, nil
}
func (s *Service) GetInstanceRuntime(ctx context.Context, r *pb.GetInstanceRequest) (*pb.InstanceRuntime, error) {
	i, e := s.diagnosticInstance(ctx, r)
	if e != nil {
		return nil, rpcError(e)
	}
	v, e := s.diagnostics.ReadRuntime(ctx, i)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &pb.InstanceRuntime{InstanceId: i.ID, Version: i.Version, ObservedAt: timestamppb.New(v.ObservedAt)}
	if v.StatefulSet != nil {
		out.StatefulSet = &pb.RuntimeStatefulSet{DesiredReplicas: v.StatefulSet.DesiredReplicas, ReadyReplicas: v.StatefulSet.ReadyReplicas}
	}
	if p := v.Pod; p != nil {
		out.Pod = &pb.RuntimePod{Name: p.Name, Uid: p.UID, NodeName: p.NodeName, PodIp: p.PodIP, Phase: p.Phase, Ready: p.Ready, Reason: p.Reason, Message: p.Message, EnvironmentSources: []*pb.EnvironmentSource{}, Mounts: []*pb.RuntimeMount{}}
		for _, v := range p.EnvironmentSources {
			out.Pod.EnvironmentSources = append(out.Pod.EnvironmentSources, &pb.EnvironmentSource{Name: v.Name, SourceType: v.SourceType})
		}
		for _, v := range p.Mounts {
			out.Pod.Mounts = append(out.Pod.Mounts, &pb.RuntimeMount{Name: v.Name, VolumeType: v.VolumeType, MountPath: v.MountPath, ReadOnly: v.ReadOnly})
		}
	}
	return out, nil
}
func (s *Service) ListInstanceEvents(ctx context.Context, r *pb.GetInstanceRequest) (*pb.InstanceEvents, error) {
	i, e := s.diagnosticInstance(ctx, r)
	if e != nil {
		return nil, rpcError(e)
	}
	v, e := s.diagnostics.ReadEvents(ctx, i)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &pb.InstanceEvents{PodUid: v.PodUID, ObservedAt: timestamppb.New(v.ObservedAt), Events: []*pb.InstanceEvent{}, Truncated: v.Truncated}
	for _, v := range v.Items {
		item := &pb.InstanceEvent{Type: v.Type, Reason: v.Reason, Message: v.Message, Count: v.Count}
		if v.LastOccurredAt != nil {
			item.LastOccurredAt = timestamppb.New(*v.LastOccurredAt)
		}
		out.Events = append(out.Events, item)
	}
	return out, nil
}
func (s *Service) acquireLog(i biz.Instance) (func(), error) {
	limits := s.config.LogLimits.Defaults()
	key := i.TenantID + ":" + i.ID
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.logTotal >= limits.ProcessConnections || s.logTenants[i.TenantID] >= limits.TenantConnections || s.logInstances[key] >= limits.InstanceConnections {
		return nil, biz.ErrLogLimit
	}
	s.logTotal++
	s.logTenants[i.TenantID]++
	s.logInstances[key]++
	return func() {
		s.logMu.Lock()
		defer s.logMu.Unlock()
		s.logTotal--
		s.logTenants[i.TenantID]--
		s.logInstances[key]--
		if s.logTenants[i.TenantID] == 0 {
			delete(s.logTenants, i.TenantID)
		}
		if s.logInstances[key] == 0 {
			delete(s.logInstances, key)
		}
	}, nil
}
func (s *Service) StreamInstanceLogs(r *pb.StreamInstanceLogsRequest, stream grpc.ServerStreamingServer[pb.InstanceLogFrame]) error {
	if r == nil || r.TailLines < 1 || r.TailLines > 1000 {
		return rpcError(biz.ErrInvalid)
	}
	i, e := s.diagnosticInstance(stream.Context(), &pb.GetInstanceRequest{TenantId: r.TenantId, InstanceId: r.InstanceId})
	if e != nil {
		return rpcError(e)
	}
	release, e := s.acquireLog(i)
	if e != nil {
		return rpcError(e)
	}
	defer release()
	limits := s.config.LogLimits.Defaults()
	ctx, cancel := context.WithTimeout(stream.Context(), time.Duration(limits.LifetimeSeconds)*time.Second)
	defer cancel()
	e = s.diagnostics.StreamLogs(ctx, i, r.TailLines, r.Follow, func(frame biz.LogFrame) error {
		if len(frame.Data) > limits.FrameBytes {
			return biz.ErrInvalid
		}
		out := &pb.InstanceLogFrame{Kind: pb.InstanceLogFrame_Kind(pb.InstanceLogFrame_Kind_value[frame.Kind]), Data: frame.Data, Reason: frame.Reason}
		done := make(chan error, 1)
		go func() { done <- stream.Send(out) }()
		timer := time.NewTimer(time.Duration(limits.WriteTimeoutSeconds) * time.Second)
		defer timer.Stop()
		select {
		case e := <-done:
			return e
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-timer.C:
			return biz.ErrLogSlow
		}
	})
	return rpcError(e)
}
