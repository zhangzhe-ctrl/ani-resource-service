package server

import (
	"crypto/tls"
	"fmt"

	"github.com/go-kratos/kratos/v3/middleware"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"

	conf "github.com/zhangzhe-ctrl/ani-resource-service/internal/conf/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// NewGRPCServer is the sole internal business listener constructor. Its
// transport and caller policy are shared by full, governance and vpc-read
// compositions; changing composition never enables a plaintext bypass.
func NewGRPCServer(c *conf.Server_GRPC, readiness *Readiness, tlsConfig *tls.Config, readOnly bool, middlewares ...middleware.Middleware) (*kratosgrpc.Server, error) {
	if c == nil || tlsConfig == nil || tlsConfig.MinVersion < tls.VersionTLS13 ||
		tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert || tlsConfig.ClientCAs == nil ||
		len(tlsConfig.Certificates) == 0 || tlsConfig.VerifyConnection == nil {
		return nil, fmt.Errorf("internal business listener requires configured TLS 1.3 mutual authentication")
	}
	server := kratosgrpc.NewServer(
		kratosgrpc.CustomHealth(),
		kratosgrpc.Network(c.Network),
		kratosgrpc.Address(c.Addr),
		kratosgrpc.Timeout(c.Timeout.AsDuration()),
		kratosgrpc.TLSConfig(tlsConfig.Clone()),
		kratosgrpc.UnaryInterceptor(governanceUnary(readOnly)),
		kratosgrpc.StreamInterceptor(GovernanceStreams(readOnly)),
		kratosgrpc.Options(grpc.UnknownServiceHandler(func(any, grpc.ServerStream) error {
			return status.Error(codes.PermissionDenied, "internal RPC is not registered")
		})),
		kratosgrpc.Middleware(middlewares...),
		kratosgrpc.DisableReflection(),
	)
	grpc_health_v1.RegisterHealthServer(server, &networkHealth{readiness: readiness})
	return server, nil
}
