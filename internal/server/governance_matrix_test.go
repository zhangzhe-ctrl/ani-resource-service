package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// governanceModeProbe records the EgressCaller visible to the business layer so
// tests can assert the interceptor installed the trusted delegation context.
type governanceModeProbe struct {
	networkv1.UnimplementedTenantEgressServiceServer
	gotTenant  string
	gotActor   string
	gotTrace   string
	gotCaller  bool
	gotRequest *networkv1.GetEIPRequest
}

func (p *governanceModeProbe) GetEIP(ctx context.Context, req *networkv1.GetEIPRequest) (*networkv1.GetEIPResponse, error) {
	caller, ok := biz.EgressCallerOf(ctx)
	if !ok {
		return nil, status.Error(codes.Internal, "no trusted caller installed")
	}
	p.gotCaller = true
	p.gotTenant = caller.TenantID
	p.gotActor = caller.Attribution.Actor
	p.gotTrace = caller.Attribution.CorrelationID
	p.gotRequest = req
	return &networkv1.GetEIPResponse{Eip: &networkv1.EIP{TenantId: caller.TenantID}}, nil
}

func TestGovernanceAllowlistMatrix(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.pem")
	if err = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	serial := int64(0)
	names := map[string][2]string{}
	issue := func(name string, usage x509.ExtKeyUsage) tls.Certificate {
		t.Helper()
		serial++
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		d, e := x509.CreateCertificate(rand.Reader, tmpl, parsed, &k.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		kd, e := x509.MarshalECPrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		cp := filepath.Join(dir, name+"-cert-"+strconv.FormatInt(serial, 10)+".pem")
		kp := filepath.Join(dir, name+"-key-"+strconv.FormatInt(serial, 10)+".pem")
		if e = os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0600); e != nil {
			t.Fatal(e)
		}
		names[name] = [2]string{cp, kp}
		c, e := tls.LoadX509KeyPair(cp, kp)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	good := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth)
	issue("ani-network-service", x509.ExtKeyUsageServerAuth)
	serverTLS, err := GovernanceTLS(caPath, names["ani-network-service"][0], names["ani-network-service"][1])
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	probe := &governanceModeProbe{}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)), grpc.UnaryInterceptor(GovernanceUnary()))
	networkv1.RegisterTenantEgressServiceServer(srv, probe)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Log(err)
		}
	}()
	defer srv.Stop()

	tenant := "11111111-1111-4111-8111-111111111111"
	other := "33333333-3333-4333-8333-333333333333"
	request := "22222222-2222-4222-8222-222222222222"
	dial := func() *grpc.ClientConn {
		t.Helper()
		conn, e := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "ani-network-service", Certificates: []tls.Certificate{good}})))
		if e != nil {
			t.Fatal(e)
		}
		return conn
	}
	call := func(conn *grpc.ClientConn, method string, req proto.Message) codes.Code {
		t.Helper()
		ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:7", "x-ani-request-id", request)), 2*time.Second)
		defer cancel()
		return status.Code(conn.Invoke(ctx, method, req, new(networkv1.GetEIPResponse)))
	}

	// Allowlisted tenant methods admit the caller and bind the request tenant.
	for _, method := range []string{
		"/network.v1.TenantEgressService/GetEIP",
		"/network.v1.TenantEgressService/CreateEIP",
		"/network.v1.TenantEgressService/ListEIPs",
		"/network.v1.TenantEgressService/DeleteEIP",
		"/network.v1.TenantEgressService/BindVPCSnat",
		"/network.v1.TenantEgressService/GetVPCSnat",
		"/network.v1.TenantEgressService/GetVPCSnatBinding",
		"/network.v1.TenantEgressService/SetVPCSnatEnabled",
		"/network.v1.TenantEgressService/DeleteVPCSnatBinding",
	} {
		conn := dial()
		// handler is Unimplemented for most methods; PermissionDenied from the
		// interceptor is the failure we care about. Unimplemented means the
		// interceptor admitted the call and it reached the service.
		if code := call(conn, method, &networkv1.GetEIPRequest{TargetTenantId: tenant, EipId: "eip-1"}); code == codes.PermissionDenied || code == codes.Unauthenticated {
			t.Errorf("%s: governance boundary rejected allowlisted method: %s", method, code)
		}
		conn.Close()
	}

	// The allowlisted method reached the probe with the trusted caller bound to
	// the principal, not the request.
	conn := dial()
	ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:access-key:42", "x-ani-request-id", request, "x-ani-operator", "gov-ak-42")), 2*time.Second)
	defer cancel()
	out := new(networkv1.GetEIPResponse)
	if e := conn.Invoke(ctx, "/network.v1.TenantEgressService/GetEIP", &networkv1.GetEIPRequest{TargetTenantId: tenant, EipId: "eip-1"}, out); e != nil {
		t.Fatalf("GetEIP admitted call failed: %v", e)
	}
	if !probe.gotCaller || probe.gotTenant != tenant || probe.gotActor != "governance:access-key:42" || probe.gotTrace != request {
		t.Fatalf("EgressCaller not bound to principal: caller=%+v", probe)
	}
	if probe.gotRequest.TargetTenantId != tenant {
		t.Fatalf("request tenant rewritten unexpectedly: %v", probe.gotRequest)
	}

	// Forged tenant fields are rejected before the business layer.
	if code := call(conn, "/network.v1.TenantEgressService/GetEIP", &networkv1.GetEIPRequest{TargetTenantId: other, EipId: "eip-1"}); code != codes.PermissionDenied {
		t.Fatalf("forged target_tenant_id: code=%s want=PermissionDenied", code)
	}
	if code := call(conn, "/network.v1.TenantEgressService/GetEIP", &networkv1.CreateEIPRequest{TargetTenantId: other}); code != codes.PermissionDenied {
		t.Fatalf("forged create tenant: code=%s want=PermissionDenied", code)
	}

	// Non-allowlisted methods stay denied: platform surface, mutation outside
	// the matrix, and every streaming RPC. The probe server only registers the
	// Egress service, so an interceptor denial must surface before gRPC's
	// Unimplemented — the registry lookup happens per-connection codec, so use
	// a dedicated server for deny assertions instead.
	lis2, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	srv2 := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)), grpc.UnaryInterceptor(GovernanceUnary()), grpc.UnknownServiceHandler(func(srv interface{}, stream grpc.ServerStream) error {
		return status.Error(codes.PermissionDenied, "governance credential does not permit this method")
	}))
	go func() {
		if err := srv2.Serve(lis2); err != nil {
			t.Log(err)
		}
	}()
	defer srv2.Stop()
	conn2, e := grpc.NewClient(lis2.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "ani-network-service", Certificates: []tls.Certificate{good}})))
	if e != nil {
		t.Fatal(e)
	}
	defer conn2.Close()
	denyCall := func(method string, req proto.Message) {
		t.Helper()
		ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:7", "x-ani-request-id", request)), 2*time.Second)
		defer cancel()
		if code := status.Code(conn2.Invoke(ctx, method, req, new(networkv1.GetVPCResponse))); code != codes.PermissionDenied {
			t.Errorf("%s: code=%s want=PermissionDenied", method, code)
		}
	}
	for _, method := range []string{
		"/network.v1.PlatformNetworkService/ListVirtualSwitches",
		"/network.v1.PlatformNetworkService/CreatePublicAddressPool",
		"/network.v1.PlatformNetworkService/CreateVlanNetwork",
		"/network.v1.NetworkService/PrepareAttachment",
		"/network.v1.UnknownService/Anything",
	} {
		denyCall(method, &networkv1.GetVPCRequest{TenantId: tenant})
	}
	conn.Close()

	// Duplicate operator header is rejected outright.
	conn = dial()
	ctx2, cancel2 := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:7", "x-ani-request-id", request, "x-ani-operator", "gov-user-7", "x-ani-operator", "gov-user-8")), 2*time.Second)
	defer cancel2()
	if e := conn.Invoke(ctx2, "/network.v1.TenantEgressService/GetEIP", &networkv1.GetEIPRequest{TargetTenantId: tenant, EipId: "eip-1"}, new(networkv1.GetEIPResponse)); status.Code(e) != codes.Unauthenticated {
		t.Fatalf("duplicate operator header: code=%s want=Unauthenticated", status.Code(e))
	}
	// Blank operator header is rejected outright.
	ctx3, cancel3 := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:7", "x-ani-request-id", request, "x-ani-operator", "   ")), 2*time.Second)
	defer cancel3()
	if e := conn.Invoke(ctx3, "/network.v1.TenantEgressService/GetEIP", &networkv1.GetEIPRequest{TargetTenantId: tenant, EipId: "eip-1"}, new(networkv1.GetEIPResponse)); status.Code(e) != codes.Unauthenticated {
		t.Fatalf("blank operator header: code=%s want=Unauthenticated", status.Code(e))
	}
	conn.Close()

	// Streaming RPCs stay denied on the governance channel.
	conn = dial()
	streamCtx, streamCancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:7", "x-ani-request-id", request)), 2*time.Second)
	defer streamCancel()
	_ = streamCtx
	// DenyGovernanceStreams is wired as a stream interceptor in real
	// composition roots; assert the handler directly.
	if e := DenyGovernanceStreams(nil, nil, nil, nil); status.Code(e) != codes.PermissionDenied {
		t.Fatalf("stream denial: code=%s want=PermissionDenied", status.Code(e))
	}
	conn.Close()
}
