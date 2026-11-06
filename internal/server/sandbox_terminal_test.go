package server

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/terminal/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"github.com/zhangzhe-ctrl/ani-resource-service/tests/testenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type sandboxTerminalProbe struct {
	pb.UnimplementedSandboxTerminalServiceServer
}

func (sandboxTerminalProbe) ResolveTerminalSession(ctx context.Context, r *pb.ResolveTerminalSessionRequest) (*pb.SandboxTerminalBinding, error) {
	if err := biz.RequireTenant(ctx, r.TenantId); err != nil {
		return nil, err
	}
	return &pb.SandboxTerminalBinding{TenantId: r.TenantId, SandboxId: r.SandboxId}, nil
}
func (sandboxTerminalProbe) ValidateTerminalSession(context.Context, *pb.SandboxTerminalBinding) (*pb.TerminalSessionValidation, error) {
	return &pb.TerminalSessionValidation{Valid: true}, nil
}
func TestSandboxTerminalSeparateTrustBoundaries(t *testing.T) {
	fixture := testenv.NewInstanceServiceTLS(t)
	governanceTLS, err := GovernanceTLS(fixture.CAFile, fixture.CertFile, fixture.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	govListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	govServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(governanceTLS)), grpc.UnaryInterceptor(GovernanceUnary()))
	pb.RegisterSandboxTerminalServiceServer(govServer, sandboxTerminalProbe{})
	go govServer.Serve(govListener)
	defer govServer.Stop()
	govConn, err := grpc.NewClient(govListener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(fixture.Client.Clone())))
	if err != nil {
		t.Fatal(err)
	}
	defer govConn.Close()
	tenant := "11111111-1111-4111-8111-111111111111"
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:7", "x-ani-request-id", "22222222-2222-4222-8222-222222222222"))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	gov := pb.NewSandboxTerminalServiceClient(govConn)
	if _, err = gov.ResolveTerminalSession(ctx, &pb.ResolveTerminalSessionRequest{TenantId: tenant, SandboxId: "22222222-2222-4222-8222-222222222222"}); err != nil {
		t.Fatal(err)
	}
	if _, err = gov.ResolveTerminalSession(ctx, &pb.ResolveTerminalSessionRequest{TenantId: "33333333-3333-4333-8333-333333333333"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross tenant: %v", err)
	}
	if _, err = gov.ValidateTerminalSession(ctx, &pb.SandboxTerminalBinding{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Gov validate: %v", err)
	}
	reserve, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserve.Addr().String()
	sgServer, err := NewSandboxTerminalServer(address, fixture.CAFile, fixture.CertFile, fixture.KeyFile, sandboxTerminalProbe{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sgServer.server.Serve(reserve) }()
	defer func() { sgServer.Stop(context.Background()); <-done }()
	cert, err := tls.LoadX509KeyPair(fixture.SGClientCertFile, fixture.SGClientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := fixture.Client.Clone()
	cfg.Certificates = []tls.Certificate{cert}
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sg := pb.NewSandboxTerminalServiceClient(conn)
	if _, err = sg.ValidateTerminalSession(ctx, &pb.SandboxTerminalBinding{}); err != nil {
		t.Fatal(err)
	}
	if _, err = sg.ResolveTerminalSession(ctx, &pb.ResolveTerminalSessionRequest{TenantId: tenant}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SG resolve: %v", err)
	}
	wrong, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(fixture.Client.Clone())))
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	if _, err = pb.NewSandboxTerminalServiceClient(wrong).ValidateTerminalSession(ctx, &pb.SandboxTerminalBinding{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Gov cert on SG listener: %v", err)
	}
}
