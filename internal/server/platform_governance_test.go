package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestPlatformGovernanceScope(t *testing.T) {
	cert := &x509.Certificate{DNSNames: []string{GovernanceSAN}}
	base := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}}})
	platform := metadata.Pairs(GovernanceScopeHeader, "platform", "x-ani-actor", "governance:user:7", "x-ani-request-id", "22222222-2222-4222-8222-222222222222")
	methods := []string{}
	for _, desc := range []grpc.ServiceDesc{networkv1.PlatformNetworkService_ServiceDesc, networkv1.PlatformDefaultEgressService_ServiceDesc} {
		for _, method := range desc.Methods {
			methods = append(methods, "/"+desc.ServiceName+"/"+method.MethodName)
		}
	}
	for _, name := range methods {
		t.Run(name, func(t *testing.T) {
			called := false
			_, err := GovernanceUnary()(metadata.NewIncomingContext(base, platform), &networkv1.GetPlatformNetworkCapabilitiesRequest{}, &grpc.UnaryServerInfo{FullMethod: name}, func(ctx context.Context, _ any) (any, error) {
				called = true
				caller, ok := biz.EgressCallerOf(ctx)
				if !ok || !caller.PlatformAdministrator || caller.TenantID != "" || len(caller.DelegatedTenants) != 0 || caller.Attribution.Actor != "governance:user:7" || caller.Attribution.DirectCaller != GovernanceSAN || caller.Attribution.CorrelationID != platform.Get("x-ani-request-id")[0] {
					t.Fatal("platform caller must be bound to the verified delegation without a tenant")
				}
				return nil, nil
			})
			if err != nil || !called {
				t.Fatalf("platform method denied: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name, method string
		md           metadata.MD
		code         codes.Code
		readOnly     bool
	}{
		{"tenant_to_platform", networkv1.PlatformNetworkService_ListPublicAddressPools_FullMethodName, metadata.Pairs("x-ani-tenant-id", "11111111-1111-4111-8111-111111111111", "x-ani-actor", "governance:user:7", "x-ani-request-id", platform.Get("x-ani-request-id")[0]), codes.PermissionDenied, false},
		{"tenant_to_default_vpc", "/network.v1.PlatformDefaultEgressService/CreateDefaultVPCEgress", metadata.Pairs("x-ani-tenant-id", "11111111-1111-4111-8111-111111111111", "x-ani-actor", "governance:user:7", "x-ani-request-id", platform.Get("x-ani-request-id")[0]), codes.PermissionDenied, false},
		{"platform_to_tenant", GetVPCMethod, platform, codes.PermissionDenied, false},
		{"unknown_method", "/network.v1.PlatformNetworkService/Anything", platform, codes.PermissionDenied, false},
		{"read_only", networkv1.PlatformNetworkService_ListPublicAddressPools_FullMethodName, platform, codes.PermissionDenied, true},
		{"mixed_scope", networkv1.PlatformNetworkService_ListPublicAddressPools_FullMethodName, metadata.Join(platform, metadata.Pairs("x-ani-tenant-id", "11111111-1111-4111-8111-111111111111")), codes.Unauthenticated, false},
		{"api_key", networkv1.PlatformNetworkService_ListPublicAddressPools_FullMethodName, metadata.Pairs(GovernanceScopeHeader, "platform", "x-ani-actor", "governance:access-key:7", "x-ani-request-id", platform.Get("x-ani-request-id")[0]), codes.Unauthenticated, false},
		{"duplicate_scope", networkv1.PlatformNetworkService_ListPublicAddressPools_FullMethodName, metadata.Join(platform, metadata.Pairs(GovernanceScopeHeader, "platform")), codes.Unauthenticated, false},
		{"unknown_scope", networkv1.PlatformNetworkService_ListPublicAddressPools_FullMethodName, metadata.Pairs(GovernanceScopeHeader, "tenant", "x-ani-actor", "governance:user:7", "x-ani-request-id", platform.Get("x-ani-request-id")[0]), codes.Unauthenticated, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			_, err := governanceUnary(tc.readOnly)(metadata.NewIncomingContext(base, tc.md), &networkv1.GetVPCRequest{}, &grpc.UnaryServerInfo{FullMethod: tc.method}, func(context.Context, any) (any, error) { called = true; return nil, nil })
			if called || status.Code(err) != tc.code {
				t.Fatalf("rejected delegation reached business: called=%v error=%v", called, err)
			}
		})
	}
}
