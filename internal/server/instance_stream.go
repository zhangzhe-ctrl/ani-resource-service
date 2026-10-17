package server

import (
	"context"

	instancebiz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const InstanceLogsMethod = "/instance.v1.InstanceService/StreamInstanceLogs"

type instanceLogServerStream struct {
	grpc.ServerStream
	ctx    context.Context
	tenant string
}

func (s *instanceLogServerStream) Context() context.Context { return s.ctx }
func (s *instanceLogServerStream) RecvMsg(message any) error {
	if e := s.ServerStream.RecvMsg(message); e != nil {
		return e
	}
	r, ok := message.(proto.Message)
	if !ok || !r.ProtoReflect().IsValid() {
		return status.Error(codes.PermissionDenied, "instance log request must be a proto message")
	}
	field := r.ProtoReflect().Descriptor().Fields().ByName("tenant_id")
	if field == nil || r.ProtoReflect().Get(field).String() != s.tenant {
		return status.Error(codes.PermissionDenied, "instance tenant differs from authenticated scope")
	}
	return nil
}

// GovernanceStreams grants one protected server stream. Read-only composition
// and every unregistered stream remain closed under the same Gov mTLS policy.
func GovernanceStreams(readOnly bool) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		if readOnly || stream == nil || info == nil || info.FullMethod != InstanceLogsMethod || !info.IsServerStream || info.IsClientStream || next == nil {
			return status.Error(codes.PermissionDenied, "internal stream is not permitted")
		}
		p, e := (GovernanceResolver{}).Resolve(stream.Context())
		if e != nil {
			return e
		}
		if p.Platform {
			return status.Error(codes.PermissionDenied, "instance logs require tenant delegation")
		}
		ctx := instancebiz.WithCaller(context.WithValue(stream.Context(), governancePrincipalKey{}, p), instancebiz.Caller{TenantID: p.TenantID, Actor: p.Actor})
		return next(server, &instanceLogServerStream{ServerStream: stream, ctx: ctx, tenant: p.TenantID})
	}
}
