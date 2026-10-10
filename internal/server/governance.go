package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const GovernanceSAN = "ani-governance"
const OperatorHeader = "x-ani-operator"

// governanceReadMethods is the authoritative per-domain allowlist for the
// governance mode. Domain = one allowlist group; adding a domain adds a group,
// never a new transport or trust contract. Keep in sync with
// docs/plans/governance-integration.md (the plan table is authoritative).
var governanceReadMethods = map[string]struct{}{
	// Network domain: tenant VPC/Subnet read+write and operation lookup.
	"/network.v1.NetworkService/GetVPC":             {},
	"/network.v1.NetworkService/ListVPCs":           {},
	"/network.v1.NetworkService/ListVPCCIDRPresets": {},
	"/network.v1.NetworkService/CreateVPC":          {},
	"/network.v1.NetworkService/DeleteVPC":          {},
	"/network.v1.NetworkService/GetSubnet":          {},
	"/network.v1.NetworkService/ListSubnets":        {},
	"/network.v1.NetworkService/CreateSubnet":       {},
	"/network.v1.NetworkService/DeleteSubnet":       {},
	"/network.v1.NetworkService/GetOperation":       {},
	// Egress domain: tenant EIP and SNAT binding read+write.
	"/network.v1.TenantEgressService/CreateEIP":            {},
	"/network.v1.TenantEgressService/GetEIP":               {},
	"/network.v1.TenantEgressService/ListEIPs":             {},
	"/network.v1.TenantEgressService/DeleteEIP":            {},
	"/network.v1.TenantEgressService/BindVPCSnat":          {},
	"/network.v1.TenantEgressService/GetVPCSnat":           {},
	"/network.v1.TenantEgressService/GetVPCSnatBinding":    {},
	"/network.v1.TenantEgressService/SetVPCSnatEnabled":    {},
	"/network.v1.TenantEgressService/DeleteVPCSnatBinding": {},
	// Load balancer domain: tenant LB read+write.
	"/network.v1.TenantLoadBalancerService/CreateLoadBalancer":       {},
	"/network.v1.TenantLoadBalancerService/GetLoadBalancer":          {},
	"/network.v1.TenantLoadBalancerService/ListLoadBalancers":        {},
	"/network.v1.TenantLoadBalancerService/UpdateLoadBalancer":       {},
	"/network.v1.TenantLoadBalancerService/DeleteLoadBalancer":       {},
	"/network.v1.TenantLoadBalancerService/GetLoadBalancerOperation": {},
}

// GetVPCMethod is kept for the vpc-read mode and existing deployment checks.
const GetVPCMethod = "/network.v1.NetworkService/GetVPC"

// GovernanceTLS requires a private CA, a server certificate and the fixed client SAN.
func GovernanceTLS(caFile, certFile, keyFile string) (*tls.Config, error) {
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read governance CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("governance CA contains no certificates")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load network server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	if !exactDNS(leaf, "ani-network-service") {
		return nil, fmt.Errorf("network server certificate requires exact DNS SAN ani-network-service")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.VerifiedChains) == 0 || len(cs.PeerCertificates) == 0 || !exactDNS(cs.PeerCertificates[0], GovernanceSAN) {
				return fmt.Errorf("untrusted governance service identity")
			}
			return nil
		}}, nil
}

func exactDNS(cert *x509.Certificate, name string) bool {
	for _, dns := range cert.DNSNames {
		if dns == name {
			return true
		}
	}
	return false
}

// This identity is asserted by the authenticated Governance workload, not IAM.
type governancePrincipal struct{ TenantID, Actor, RequestID, Operator, Workload string }

type GovernanceResolver struct{}

func (GovernanceResolver) Resolve(ctx context.Context) (governancePrincipal, error) {
	denied := status.Error(codes.Unauthenticated, "trusted governance identity required")
	p, ok := peer.FromContext(ctx)
	if !ok {
		return governancePrincipal{}, denied
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.PeerCertificates) == 0 || !exactDNS(ti.State.PeerCertificates[0], GovernanceSAN) {
		return governancePrincipal{}, denied
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return governancePrincipal{}, denied
	}
	value := func(key string) string {
		vals := md.Get(key)
		if len(vals) != 1 {
			return ""
		}
		return vals[0]
	}
	tenant, actor, request, operator := value("x-ani-tenant-id"), value("x-ani-actor"), value("x-ani-request-id"), value(OperatorHeader)
	validUUID := func(v string) bool { id, err := uuid.Parse(v); return err == nil && id != uuid.Nil && id.String() == v }
	if !validUUID(tenant) || !validUUID(request) || !validGovernanceActor(actor) {
		return governancePrincipal{}, denied
	}
	// The operator header is optional for deployed vpc-read clients, but when
	// present it must be a single non-empty value (duplicates are rejected by
	// value() above returning "").
	if len(md.Get(OperatorHeader)) > 1 || (len(md.Get(OperatorHeader)) == 1 && strings.TrimSpace(operator) == "") {
		return governancePrincipal{}, denied
	}
	return governancePrincipal{TenantID: tenant, Actor: actor, RequestID: request, Operator: strings.TrimSpace(operator), Workload: GovernanceSAN}, nil
}

// Actor namespaces keep human and API Key identities distinct. Both are
// delegated by Governance; Network does not re-authorize their roles.
func validGovernanceActor(actor string) bool {
	for _, prefix := range []string{"governance:user:", "governance:access-key:"} {
		if strings.HasPrefix(actor, prefix) {
			id, err := strconv.ParseUint(strings.TrimPrefix(actor, prefix), 10, 32)
			return err == nil && id != 0 && actor == prefix+strconv.FormatUint(id, 10)
		}
	}
	return false
}

// GovernanceUnary admits the allowlisted tenant methods before any business
// handler or SQL runs. Read+write tenant surfaces of Network, Egress and LB are
// admitted; PlatformNetworkService and every streaming RPC stay denied. For
// Egress/LB methods it installs the trusted EgressCaller the biz layer requires
// (same pattern as the controlled lb-api socket adapter), bound to the
// authenticated principal — never to request fields.
func GovernanceUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		p, err := (GovernanceResolver{}).Resolve(ctx)
		if err != nil {
			return nil, err
		}
		_, imageMethod := imageGovernanceMethods[info.FullMethod]
		if _, ok := governanceReadMethods[info.FullMethod]; !ok && !imageMethod {
			return nil, status.Error(codes.PermissionDenied, "governance credential does not permit this method")
		}
		r, ok := req.(proto.Message)
		if !ok {
			return nil, status.Error(codes.PermissionDenied, "governance requests must be proto messages")
		}
		if imageMethod {
			ctx, err = withImageGovernanceCaller(ctx, p, r)
			if err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
		value := r.ProtoReflect()
		// Bind the request's tenant field to the authenticated governance
		// scope. Empty tenant is only accepted for NetworkService reads where
		// the legacy contract sends it explicitly; Egress/LB always carry
		// target_tenant_id.
		for _, field := range []string{"tenant_id", "target_tenant_id"} {
			fd := value.Descriptor().Fields().ByName(protoreflect.Name(field))
			if fd == nil {
				continue
			}
			if got := value.Get(fd).String(); got != "" && got != p.TenantID {
				return nil, status.Error(codes.PermissionDenied, "request tenant differs from authenticated governance scope")
			}
		}
		if strings.HasPrefix(info.FullMethod, "/network.v1.NetworkService/") {
			// Keep the legacy strict behavior: NetworkService messages must
			// state the tenant explicitly and it must match the principal.
			fd := value.Descriptor().Fields().ByName("tenant_id")
			if fd != nil && value.Get(fd).String() != p.TenantID {
				return nil, status.Error(codes.PermissionDenied, "request tenant differs from authenticated governance scope")
			}
		}
		// GetOperation resolves egress/lb operations, whose authorization also
		// requires the trusted caller context.
		needsCaller := strings.HasPrefix(info.FullMethod, "/network.v1.TenantEgressService/") ||
			strings.HasPrefix(info.FullMethod, "/network.v1.TenantLoadBalancerService/") ||
			info.FullMethod == "/network.v1.NetworkService/GetOperation"
		if needsCaller {
			caller := biz.EgressCaller{
				TenantID: p.TenantID,
				Attribution: biz.Attribution{
					Actor:         p.Actor,
					DirectCaller:  p.Workload,
					CorrelationID: p.RequestID,
				},
			}
			if p.Operator != "" {
				caller.Attribution.CorrelationID = p.Operator
			}
			ctx = biz.WithEgressCaller(ctx, caller)
		}
		return next(ctx, req)
	}
}

// DenyGovernanceStreams keeps the governance channel unary-only. The old name
// is retained for one release as an alias for deployed composition roots.
func DenyGovernanceStreams(_ any, _ grpc.ServerStream, _ *grpc.StreamServerInfo, _ grpc.StreamHandler) error {
	return status.Error(codes.PermissionDenied, "governance mode does not permit streaming RPCs")
}

// DenyVPCStreams is the historical name of DenyGovernanceStreams.
//
// Deprecated: use DenyGovernanceStreams.
var DenyVPCStreams = DenyGovernanceStreams
