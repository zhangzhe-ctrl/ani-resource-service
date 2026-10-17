package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	instancebiz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type logStreamProbe struct {
	grpc.ServerStream
	ctx     context.Context
	request *pb.StreamInstanceLogsRequest
}

func (s *logStreamProbe) Context() context.Context { return s.ctx }
func (s *logStreamProbe) RecvMsg(m any) error      { proto.Merge(m.(proto.Message), s.request); return nil }
func TestInstanceLogStreamExclusiveAuthority(t *testing.T) {
	const tenant = "11111111-1111-4111-8111-111111111111"
	const request = "22222222-2222-4222-8222-222222222222"
	cert := &x509.Certificate{DNSNames: []string{GovernanceSAN}}
	base := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}}})
	metadataFor := func() metadata.MD {
		return metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:42", "x-ani-request-id", request)
	}
	for _, test := range []struct {
		name       string
		md         metadata.MD
		ctx        context.Context
		method     string
		readonly   bool
		bodyTenant string
		want       codes.Code
	}{
		{"authorized", metadataFor(), base, InstanceLogsMethod, false, tenant, codes.OK},
		{"different body tenant", metadataFor(), base, InstanceLogsMethod, false, request, codes.PermissionDenied},
		{"read-only composition", metadataFor(), base, InstanceLogsMethod, true, tenant, codes.PermissionDenied},
		{"other stream", metadataFor(), base, "/instance.v1.InstanceService/OtherStream", false, tenant, codes.PermissionDenied},
		{"missing TLS", metadataFor(), context.Background(), InstanceLogsMethod, false, tenant, codes.Unauthenticated},
		{"platform identity", metadata.Pairs("x-ani-scope", "platform", "x-ani-actor", "governance:user:42", "x-ani-request-id", request), base, InstanceLogsMethod, false, tenant, codes.PermissionDenied},
		{"duplicate tenant", func() metadata.MD { m := metadataFor(); m.Append("x-ani-tenant-id", tenant); return m }(), base, InstanceLogsMethod, false, tenant, codes.Unauthenticated},
		{"missing actor", metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-request-id", request), base, InstanceLogsMethod, false, tenant, codes.Unauthenticated},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			stream := &logStreamProbe{ctx: metadata.NewIncomingContext(test.ctx, test.md), request: &pb.StreamInstanceLogsRequest{TenantId: test.bodyTenant}}
			e := GovernanceStreams(test.readonly)(nil, stream, &grpc.StreamServerInfo{FullMethod: test.method, IsServerStream: true}, func(_ any, s grpc.ServerStream) error {
				r := new(pb.StreamInstanceLogsRequest)
				if e := s.RecvMsg(r); e != nil {
					return e
				}
				c, e := instancebiz.CallerFromContext(s.Context())
				if e != nil || c.TenantID != tenant || c.Actor != "governance:user:42" {
					t.Fatal("caller context lost", c, e)
				}
				called = true
				return nil
			})
			if status.Code(e) != test.want || called != (test.want == codes.OK) {
				t.Fatal("stream boundary", status.Code(e), test.want, called)
			}
		})
	}
}
func TestInstancePresetScopeUsesOwnPlatformCaller(t *testing.T) {
	cert := &x509.Certificate{DNSNames: []string{GovernanceSAN}}
	base := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}}})
	platform := metadata.NewIncomingContext(base, metadata.Pairs("x-ani-scope", "platform", "x-ani-actor", "governance:user:42", "x-ani-request-id", "22222222-2222-4222-8222-222222222222"))
	for method := range instancePlatformMethods {
		_, e := GovernanceUnary()(platform, &pb.AdminGetPresetRequest{Id: "11111111-1111-4111-8111-111111111111"}, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ any) (any, error) {
			c, e := instancebiz.RequirePlatform(ctx)
			if e != nil || !c.Platform || c.TenantID != "" {
				t.Fatal("instance platform caller missing", c, e)
			}
			return nil, nil
		})
		if e != nil {
			t.Fatal(method, e)
		}
	}
}
