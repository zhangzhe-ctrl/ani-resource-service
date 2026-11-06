package sandbox

import (
	"context"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/terminal/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TerminalBinding is exposed exclusively on the terminal mTLS listener.
// Caller identities are checked against exact RPC names by that listener.
type TerminalBinding struct {
	pb.UnimplementedSandboxTerminalServiceServer
	reader   biz.DataPlaneReader
	verifier biz.CodeRunVerifier
}

func NewTerminalBinding(reader biz.DataPlaneReader, verifier biz.CodeRunVerifier) *TerminalBinding {
	return &TerminalBinding{reader: reader, verifier: verifier}
}
func (s *TerminalBinding) resolve(ctx context.Context, tenant, id string) (*pb.SandboxTerminalBinding, error) {
	if s.reader == nil || s.verifier == nil {
		return nil, status.Error(codes.Unavailable, "SANDBOX_TERMINAL_UNAVAILABLE")
	}
	target, err := biz.ResolveDataPlaneTarget(ctx, s.reader, tenant, id, biz.DataPlaneExec)
	if err != nil {
		return nil, rpcError(err)
	}
	record, err := s.reader.Get(ctx, tenant, id)
	if err != nil {
		return nil, rpcError(err)
	}
	if err = s.verifier.VerifyDataPlaneTarget(ctx, record, target); err != nil {
		return nil, rpcError(err)
	}
	if err = biz.RevalidateDataPlaneTarget(ctx, s.reader, target); err != nil {
		return nil, rpcError(err)
	}
	return &pb.SandboxTerminalBinding{TenantId: target.TenantID, SandboxId: target.SandboxID, Version: target.Version, ClusterId: target.ClusterID, Namespace: target.Namespace, NamespaceUid: target.NamespaceUID, PodName: target.PodName, PodUid: target.PodUID, ContainerName: target.Container, Shell: "/bin/sh"}, nil
}
func (s *TerminalBinding) ResolveTerminalSession(ctx context.Context, req *pb.ResolveTerminalSessionRequest) (*pb.SandboxTerminalBinding, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "INVALID_TERMINAL_REQUEST")
	}
	return s.resolve(ctx, req.TenantId, req.SandboxId)
}
func (s *TerminalBinding) ValidateTerminalSession(ctx context.Context, binding *pb.SandboxTerminalBinding) (*pb.TerminalSessionValidation, error) {
	if binding == nil {
		return nil, status.Error(codes.InvalidArgument, "INVALID_TERMINAL_BINDING")
	}
	ctx = biz.WithCaller(ctx, biz.Caller{TenantID: binding.TenantId, Actor: "trusted-session-gateway"})
	current, err := s.resolve(ctx, binding.TenantId, binding.SandboxId)
	if err != nil {
		return nil, err
	}
	if current.TenantId != binding.TenantId || current.SandboxId != binding.SandboxId || current.Version != binding.Version || current.ClusterId != binding.ClusterId || current.Namespace != binding.Namespace || current.NamespaceUid != binding.NamespaceUid || current.PodName != binding.PodName || current.PodUid != binding.PodUid || current.ContainerName != binding.ContainerName || current.Shell != binding.Shell {
		return nil, status.Error(codes.FailedPrecondition, "SANDBOX_TERMINAL_BINDING_STALE")
	}
	return &pb.TerminalSessionValidation{Valid: true}, nil
}
