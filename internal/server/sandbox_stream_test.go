package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type sandboxLogStreamProbe struct {
	grpc.ServerStream
	ctx    context.Context
	tenant string
}

func (s *sandboxLogStreamProbe) Context() context.Context { return s.ctx }
func (s *sandboxLogStreamProbe) RecvMsg(m any) error {
	proto.Merge(m.(proto.Message), &pb.StreamSandboxLogsRequest{TenantId: s.tenant})
	return nil
}

func TestSandboxLogStreamExclusiveAuthority(t *testing.T) {
	const tenant = "11111111-1111-4111-8111-111111111111"
	const other = "22222222-2222-4222-8222-222222222222"
	cert := &x509.Certificate{DNSNames: []string{GovernanceSAN}}
	base := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}}})
	for _, tc := range []struct {
		name     string
		body     string
		readonly bool
		base     context.Context
		want     codes.Code
	}{
		{"authorized", tenant, false, base, codes.OK}, {"other tenant", other, false, base, codes.PermissionDenied}, {"readonly", tenant, true, base, codes.PermissionDenied}, {"missing tls", tenant, false, context.Background(), codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(tc.base, metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:42", "x-ani-request-id", other))
			probe := &sandboxLogStreamProbe{ctx: ctx, tenant: tc.body}
			err := GovernanceStreams(tc.readonly)(nil, probe, &grpc.StreamServerInfo{FullMethod: SandboxLogsMethod, IsServerStream: true}, func(_ any, s grpc.ServerStream) error {
				if e := biz.RequireTenant(s.Context(), tenant); e != nil {
					return e
				}
				return s.RecvMsg(new(pb.StreamSandboxLogsRequest))
			})
			if status.Code(err) != tc.want {
				t.Fatalf("want=%v got=%v err=%v", tc.want, status.Code(err), err)
			}
		})
	}
	tokenCtx := metadata.NewIncomingContext(base, metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:sandbox-token:33333333-3333-4333-8333-333333333333", "x-ani-request-id", other))
	tokenStream := &sandboxLogStreamProbe{ctx: tokenCtx, tenant: tenant}
	err := GovernanceStreams(false)(nil, tokenStream, &grpc.StreamServerInfo{FullMethod: SandboxLogsMethod, IsServerStream: true}, func(_ any, _ grpc.ServerStream) error {
		t.Fatal("sandbox token must not reach log handler")
		return nil
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("sandbox token: %v", err)
	}
}
