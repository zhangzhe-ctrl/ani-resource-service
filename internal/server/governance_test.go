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
	"github.com/zhangzhe-ctrl/ani-resource-service/tests/testenv"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
	instancev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	imagebiz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	instancebiz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type vpcIdentityProbe struct {
	networkv1.UnimplementedNetworkServiceServer
	calls *atomic.Int32
}

type imageIdentityProbe struct {
	imagev1.UnimplementedTenantImageServiceServer
}
type instanceIdentityProbe struct {
	instancev1.UnimplementedInstanceServiceServer
	calls *atomic.Int32
}

func (p instanceIdentityProbe) AcceptCreate(ctx context.Context, r *instancev1.AcceptCreateRequest) (*instancev1.OperationResponse, error) {
	if e := instancebiz.RequireTenant(ctx, r.GetContext().GetTenantId()); e != nil {
		return nil, e
	}
	p.calls.Add(1)
	return &instancev1.OperationResponse{}, nil
}
func (p instanceIdentityProbe) AcceptDelete(ctx context.Context, r *instancev1.AcceptDeleteRequest) (*instancev1.OperationResponse, error) {
	if e := instancebiz.RequireTenant(ctx, r.GetContext().GetTenantId()); e != nil {
		return nil, e
	}
	p.calls.Add(1)
	return &instancev1.OperationResponse{}, nil
}

func (imageIdentityProbe) GetImageSpace(ctx context.Context, r *imagev1.GetImageSpaceRequest) (*imagev1.GetImageSpaceResponse, error) {
	c, err := imagebiz.RequireTenant(ctx, r.GetTenantId())
	if err != nil {
		return nil, err
	}
	return &imagev1.GetImageSpaceResponse{Space: &imagev1.ImageSpace{TenantId: c.TenantID, SpaceId: c.Actor, State: string(c.Kind), ProjectName: c.Subject}}, nil
}

func (p vpcIdentityProbe) GetVPC(ctx context.Context, req *networkv1.GetVPCRequest) (*networkv1.GetVPCResponse, error) {
	if p.calls != nil {
		p.calls.Add(1)
	}
	return &networkv1.GetVPCResponse{Vpc: &networkv1.VPC{TenantId: req.TenantId}}, nil
}

func TestGovernanceMTLSBoundary(t *testing.T) {
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
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	roots.AddCert(ca)
	serial := int64(1)
	issue := func(name string, usage x509.ExtKeyUsage, mutate ...func(*x509.Certificate)) (tls.Certificate, string, string) {
		t.Helper()
		serial++
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		for _, change := range mutate {
			change(tmpl)
		}
		d, e := x509.CreateCertificate(rand.Reader, tmpl, ca, &k.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		kd, e := x509.MarshalECPrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		cp, kp := filepath.Join(dir, name+".pem"), filepath.Join(dir, name+".key")
		if e = os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := tls.LoadX509KeyPair(cp, kp)
		if e != nil {
			t.Fatal(e)
		}
		return c, cp, kp
	}
	_, cp, kp := issue("ani-network-service", x509.ExtKeyUsageServerAuth)
	good, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth)
	wrong, _, _ := issue("wrong-service", x509.ExtKeyUsageClientAuth)
	wrongUsage, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageServerAuth)
	extraDNS, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth, func(c *x509.Certificate) { c.DNSNames = append(c.DNSNames, "other-service") })
	mixedURI, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth, func(c *x509.Certificate) { c.URIs = []*url.URL{{Scheme: "spiffe", Host: "ani", Path: "/governance"}} })
	mixedIP, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth, func(c *x509.Certificate) { c.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")} })
	mixedEmail, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth, func(c *x509.Certificate) { c.EmailAddresses = []string{"governance@example.test"} })
	cnOnly, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth, func(c *x509.Certificate) { c.DNSNames = nil; c.Subject.CommonName = GovernanceSAN })
	expired, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth, func(c *x509.Certificate) {
		c.NotBefore = time.Now().Add(-2 * time.Hour)
		c.NotAfter = time.Now().Add(-time.Hour)
	})
	wildcard, _, _ := issue(GovernanceSAN, x509.ExtKeyUsageClientAuth, func(c *x509.Certificate) { c.DNSNames = []string{"*.ani-governance"} })
	wrongCA := testenv.NewServiceTLS(t).Client.Certificates[0]
	serverTLS, err := GovernanceTLS(caPath, cp, kp)
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)), grpc.UnaryInterceptor(GovernanceUnary()))
	var calls atomic.Int32
	networkv1.RegisterNetworkServiceServer(srv, vpcIdentityProbe{calls: &calls})
	imagev1.RegisterTenantImageServiceServer(srv, imageIdentityProbe{})
	var instanceCalls atomic.Int32
	instancev1.RegisterInstanceServiceServer(srv, instanceIdentityProbe{calls: &instanceCalls})
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Log(err)
		}
	}()
	defer srv.Stop()
	tenant := "11111111-1111-4111-8111-111111111111"
	base := metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:7", "x-ani-request-id", "22222222-2222-4222-8222-222222222222")
	for _, tc := range []struct {
		name   string
		certs  []tls.Certificate
		mutate func(metadata.MD)
		tenant string
		method string
		want   codes.Code
	}{
		{"valid", []tls.Certificate{good}, nil, tenant, "", codes.OK},
		{"valid-access-key", []tls.Certificate{good}, func(m metadata.MD) { m.Set("x-ani-actor", "governance:access-key:42") }, tenant, "", codes.OK},
		{"no-cert", nil, nil, tenant, "", codes.Unavailable},
		{"wrong-san", []tls.Certificate{wrong}, nil, tenant, "", codes.Unavailable},
		{"multiple-client-dns", []tls.Certificate{extraDNS}, nil, tenant, "", codes.Unavailable},
		{"mixed-uri-san", []tls.Certificate{mixedURI}, nil, tenant, "", codes.Unavailable},
		{"mixed-ip-san", []tls.Certificate{mixedIP}, nil, tenant, "", codes.Unavailable},
		{"mixed-email-san", []tls.Certificate{mixedEmail}, nil, tenant, "", codes.Unavailable},
		{"cn-only", []tls.Certificate{cnOnly}, nil, tenant, "", codes.Unavailable},
		{"expired", []tls.Certificate{expired}, nil, tenant, "", codes.Unavailable},
		{"wrong-ca", []tls.Certificate{wrongCA}, nil, tenant, "", codes.Unavailable},
		{"wildcard-client", []tls.Certificate{wildcard}, nil, tenant, "", codes.Unavailable},
		{"wrong-usage", []tls.Certificate{wrongUsage}, nil, tenant, "", codes.Unavailable},
		{"duplicate", []tls.Certificate{good}, func(m metadata.MD) { m.Append("x-ani-tenant-id", tenant) }, tenant, "", codes.Unauthenticated},
		{"duplicate-actor", []tls.Certificate{good}, func(m metadata.MD) { m.Append("x-ani-actor", "governance:access-key:42") }, tenant, "", codes.Unauthenticated},
		{"duplicate-request", []tls.Certificate{good}, func(m metadata.MD) { m.Append("x-ani-request-id", "22222222-2222-4222-8222-222222222222") }, tenant, "", codes.Unauthenticated},
		{"missing", []tls.Certificate{good}, func(m metadata.MD) { m.Delete("x-ani-actor") }, tenant, "", codes.Unauthenticated},
		{"actor-format", []tls.Certificate{good}, func(m metadata.MD) { m.Set("x-ani-actor", "governance:user:07") }, tenant, "", codes.Unauthenticated},
		{"key-zero", []tls.Certificate{good}, func(m metadata.MD) { m.Set("x-ani-actor", "governance:access-key:0") }, tenant, "", codes.Unauthenticated},
		{"key-leading-zero", []tls.Certificate{good}, func(m metadata.MD) { m.Set("x-ani-actor", "governance:access-key:042") }, tenant, "", codes.Unauthenticated},
		{"key-overflow", []tls.Certificate{good}, func(m metadata.MD) { m.Set("x-ani-actor", "governance:access-key:4294967296") }, tenant, "", codes.Unauthenticated},
		{"unknown-actor", []tls.Certificate{good}, func(m metadata.MD) { m.Set("x-ani-actor", "governance:machine:42") }, tenant, "", codes.Unauthenticated},
		{"tenant-mismatch", []tls.Certificate{good}, nil, "33333333-3333-4333-8333-333333333333", "", codes.PermissionDenied},
		{"wrong-method", []tls.Certificate{good}, nil, tenant, "/network.v1.NetworkService/PrepareAttachment", codes.PermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			conn, e := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "ani-network-service", Certificates: tc.certs})))
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			md := base.Copy()
			if tc.mutate != nil {
				tc.mutate(md)
			}
			ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), md), 2*time.Second)
			defer cancel()
			method := tc.method
			if method == "" {
				method = GetVPCMethod
			}
			out := new(networkv1.GetVPCResponse)
			e = conn.Invoke(ctx, method, &networkv1.GetVPCRequest{TenantId: tc.tenant}, out)
			if status.Code(e) != tc.want {
				t.Fatalf("code=%s want=%s err=%v", status.Code(e), tc.want, e)
			}
			if tc.want != codes.OK && calls.Load() != before {
				t.Fatal("rejected request reached handler")
			}
			if e == nil && (out.Vpc == nil || out.Vpc.TenantId != tenant) {
				t.Fatalf("principal lost: %v", out)
			}
			if tc.method == "" {
				imageOut := new(imagev1.GetImageSpaceResponse)
				e = conn.Invoke(ctx, imagev1.TenantImageService_GetImageSpace_FullMethodName, &imagev1.GetImageSpaceRequest{TenantId: tc.tenant}, imageOut)
				if status.Code(e) != tc.want {
					t.Fatalf("Image code=%s want=%s", status.Code(e), tc.want)
				}
				if e == nil && (imageOut.GetSpace().GetTenantId() != tenant || imageOut.GetSpace().GetSpaceId() != md.Get("x-ani-actor")[0] || imageOut.GetSpace().GetState() != "governance" || imageOut.GetSpace().GetProjectName() != GovernanceSAN) {
					t.Fatal("Image trusted caller lost")
				}
			}
		})
	}
	t.Run("instance-command-context", func(t *testing.T) {
		conn, e := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: NetworkSAN, Certificates: []tls.Certificate{good}})))
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		for _, commandTenant := range []string{tenant, "33333333-3333-4333-8333-333333333333", ""} {
			for _, method := range []string{instancev1.InstanceService_AcceptCreate_FullMethodName, instancev1.InstanceService_AcceptDelete_FullMethodName} {
				var command *instancev1.CommandContext
				if commandTenant != "" {
					command = &instancev1.CommandContext{TenantId: commandTenant}
				}
				var request proto.Message = &instancev1.AcceptCreateRequest{Context: command}
				if method == instancev1.InstanceService_AcceptDelete_FullMethodName {
					request = &instancev1.AcceptDeleteRequest{Context: command}
				}
				before := instanceCalls.Load()
				call, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), base.Copy()), 2*time.Second)
				e := conn.Invoke(call, method, request, &instancev1.OperationResponse{})
				cancel()
				want := codes.PermissionDenied
				if commandTenant == tenant {
					want = codes.OK
				}
				if status.Code(e) != want || (instanceCalls.Load() != before) != (want == codes.OK) {
					t.Fatal("new command context authority", method, commandTenant, status.Code(e), e)
				}
			}
		}
	})
	t.Run("tls12", func(t *testing.T) {
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, RootCAs: roots, ServerName: NetworkSAN, Certificates: []tls.Certificate{good}})))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), base), time.Second)
		defer cancel()
		if err := conn.Invoke(ctx, GetVPCMethod, &networkv1.GetVPCRequest{TenantId: tenant}, &networkv1.GetVPCResponse{}); status.Code(err) != codes.Unavailable {
			t.Fatalf("TLS12 accepted: %v", err)
		}
	})
	if _, e := GovernanceTLS("", cp, kp); e == nil {
		t.Fatal("missing CA accepted")
	}
}

func TestGovernanceActorNamespaces(t *testing.T) {
	for _, prefix := range []string{"governance:user:", "governance:access-key:"} {
		for _, id := range []string{"1", "42", "4294967295"} {
			if !validGovernanceActor(prefix + id) {
				t.Errorf("valid actor rejected: %q", prefix+id)
			}
		}
		for _, id := range []string{"", "0", "01", "-1", "+1", "1 ", "1,2", "4294967296"} {
			if validGovernanceActor(prefix + id) {
				t.Errorf("invalid actor accepted: %q", prefix+id)
			}
		}
	}
}
