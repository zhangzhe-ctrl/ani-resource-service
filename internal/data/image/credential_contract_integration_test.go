//go:build imageintegration

package data_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/server"
	imageservice "github.com/zhangzhe-ctrl/ani-resource-service/internal/service/image"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Only the registry is a fixture. The lifecycle, migrations, restricted PG role,
// transport adapter, workload TLS policy and identity interceptor are production.
type imageContractEndpoint struct{ Address, CAFile, CertFile, KeyFile string }

func startImageContractServer(t *testing.T) (*fixture, *registryFixture, imageContractEndpoint) {
	t.Helper()
	f, registry, cfg, ring, _ := lifecycleFixture(t)
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.pem")
	if err = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0600); err != nil {
		t.Fatal(err)
	}
	issue := func(name string, usage x509.ExtKeyUsage, serial int64) (string, string) {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, &k.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		pk, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		cp, kp := filepath.Join(dir, name+".pem"), filepath.Join(dir, name+".key")
		if err = os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: pk}), 0600); err != nil {
			t.Fatal(err)
		}
		return cp, kp
	}
	sc, sk := issue("ani-network-service", x509.ExtKeyUsageServerAuth, 2)
	cp, kp := issue("ani-governance", x509.ExtKeyUsageClientAuth, 3)
	tlsConfig, err := server.GovernanceTLS(caPath, sc, sk)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)), grpc.UnaryInterceptor(server.GovernanceUnary()))
	imagev1.RegisterTenantImageServiceServer(srv, imageservice.NewTenantService(lifecycle(t, f.Repo, registry, ring, cfg, nil), nil))
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	return f, registry, imageContractEndpoint{listener.Addr().String(), caPath, cp, kp}
}

func imageContractClient(t *testing.T, endpoint imageContractEndpoint) imagev1.TenantImageServiceClient {
	t.Helper()
	ca, err := os.ReadFile(endpoint.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid fixture CA")
	}
	cert, err := tls.LoadX509KeyPair(endpoint.CertFile, endpoint.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient(endpoint.Address, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: "ani-network-service"})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return imagev1.NewTenantImageServiceClient(conn)
}

func requireImageRPCError(t *testing.T, err error, code codes.Code, reason string) {
	t.Helper()
	s := status.Convert(err)
	if s.Code() != code || len(s.Details()) != 1 {
		t.Fatalf("RPC code=%s details=%d; want %s/%s", s.Code(), len(s.Details()), code, reason)
	}
	d, ok := s.Details()[0].(*errdetails.ErrorInfo)
	if !ok || d.Domain != "image.ani.io" || d.Reason != reason {
		t.Fatal("incorrect stable Image reason")
	}
}

func TestDisablePublisherContract(t *testing.T) {
	f, registry, endpoint := startImageContractServer(t)
	client := imageContractClient(t, endpoint)
	tenant := uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:1", "x-ani-request-id", uuid.NewString()))
	space, err := client.EnsureImageSpace(ctx, &imagev1.EnsureImageSpaceRequest{TenantId: tenant, Slug: "contract", IdempotencyKey: "contract-enable"})
	if err != nil {
		t.Fatal(err)
	}
	disable := &imagev1.DisablePublisherCredentialRequest{TenantId: tenant, IdempotencyKey: "disable-empty", ExpectedVersion: 0}
	_, err = client.DisablePublisherCredential(ctx, disable)
	// Keep testing persistence after an unexpected success to expose both defects.
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("not-issued disable code=%s, want FailedPrecondition", status.Code(err))
	} else {
		requireImageRPCError(t, err, codes.FailedPrecondition, "CREDENTIAL_NOT_ISSUED")
	}
	if _, err = f.Repo.FindTenantCommand(ctx, tenant, space.Space.SpaceId, disable.IdempotencyKey); biz.ReasonOf(err) != biz.ImageNotFound {
		t.Error("not-issued disable persisted a command")
	}
	issued, err := client.IssuePublisherCredential(ctx, &imagev1.IssuePublisherCredentialRequest{TenantId: tenant, IdempotencyKey: "contract-issue", ExpectedVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	disable.ExpectedVersion = issued.Credential.Version
	disable.IdempotencyKey = "disable-active"
	disabled, err := client.DisablePublisherCredential(ctx, disable)
	if err != nil || disabled.GetCredential().GetState() != "disabled" {
		t.Fatal("active disable did not complete", err)
	}
	again, err := client.DisablePublisherCredential(ctx, disable)
	if err != nil || again.Credential.Version != disabled.Credential.Version {
		t.Fatal("same-key disable replay changed", err)
	}
	conflict := &imagev1.DisablePublisherCredentialRequest{TenantId: tenant, IdempotencyKey: disable.IdempotencyKey, ExpectedVersion: disable.ExpectedVersion + 1}
	_, err = client.DisablePublisherCredential(ctx, conflict)
	requireImageRPCError(t, err, codes.AlreadyExists, "IDEMPOTENCY_CONFLICT")
	otherActor := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-ani-tenant-id", tenant, "x-ani-actor", "governance:user:2", "x-ani-request-id", uuid.NewString()))
	_, err = client.DisablePublisherCredential(otherActor, disable)
	requireImageRPCError(t, err, codes.AlreadyExists, "IDEMPOTENCY_CONFLICT")
	_, err = client.DisablePublisherCredential(ctx, &imagev1.DisablePublisherCredentialRequest{TenantId: tenant, IdempotencyKey: "contract-stale", ExpectedVersion: issued.Credential.Version})
	requireImageRPCError(t, err, codes.Aborted, "VERSION_CONFLICT")
	again, err = client.DisablePublisherCredential(ctx, &imagev1.DisablePublisherCredentialRequest{TenantId: tenant, IdempotencyKey: "already-disabled", ExpectedVersion: disabled.Credential.Version})
	if err != nil || again.Credential.Version != disabled.Credential.Version {
		t.Fatal("already-disabled changed the credential", err)
	}
	_, err = client.IssuePublisherCredential(ctx, &imagev1.IssuePublisherCredentialRequest{TenantId: tenant, IdempotencyKey: "contract-reissue", ExpectedVersion: disabled.Credential.Version})
	if err != nil {
		t.Fatal(err)
	}
	again, err = client.DisablePublisherCredential(ctx, disable)
	if err != nil || again.Credential.State != "disabled" || again.Credential.Version != disabled.Credential.Version {
		t.Fatal("new current state masked completed command replay", err)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.creates != 1 || registry.robotCreates != 3 || registry.secretSets != 3 {
		t.Fatal("credential replay duplicated external identities")
	}
}

// Compiled from an exact Resource SHA and launched by Governance's isolated
// HTTP test. No cross-repository internal imports, replace directive or product
// deployment is involved. EOF is the graceful shutdown and PG cleanup signal.
func TestImageResourceContractHelper(t *testing.T) {
	if os.Getenv("IMAGE_RESOURCE_CONTRACT_HELPER") != "1" {
		return
	}
	_, _, endpoint := startImageContractServer(t)
	body, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ready := os.Getenv("IMAGE_RESOURCE_CONTRACT_READY")
	if ready == "" {
		t.Fatal("missing private ready path")
	}
	if err = os.WriteFile(ready+".tmp", body, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(ready+".tmp", ready); err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal("helper shutdown channel failed")
	}
}
