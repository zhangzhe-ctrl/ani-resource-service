package main

import (
	"context"
	"encoding/base64"
	"log/slog"
	"os"
	"time"

	kratos "github.com/go-kratos/kratos/v3"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"github.com/google/uuid"
	imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	conf "github.com/zhangzhe-ctrl/ani-resource-service/internal/conf/v1"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/server"
	networkService "github.com/zhangzhe-ctrl/ani-resource-service/internal/service/network"
)

// Explicit governance composition: full worker so tenant writes are executed,
// but only tenant-surface services are registered and the governance allowlist
// gates every method. PlatformNetworkService is never registered here. It never
// falls back to the legacy listener.
func runGovernance(bc *conf.Bootstrap, logger *slog.Logger) error {
	if err := bc.Validate(); err != nil {
		return err
	}
	tlsConfig, err := server.GovernanceTLS(os.Getenv("ANI_NETWORK_CLIENT_CA"), os.Getenv("ANI_NETWORK_TLS_CERT"), os.Getenv("ANI_NETWORK_TLS_KEY"))
	if err != nil {
		return err
	}
	images, err := openImage(context.Background(), bc.Image)
	if err != nil {
		return err
	}
	defer images.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	repository, err := data.OpenPostgres(ctx, bc.Network.DatabaseDsn, data.Placement{ClusterID: bc.Network.ClusterId, NamespacePrefix: bc.Network.NamespacePrefix})
	cancel()
	if err != nil {
		return err
	}
	configured := false
	defer func() {
		if !configured {
			repository.Close()
		}
	}()
	// Writes need the same provider access as full mode; the governance
	// boundary is enforced by service registration and the allowlist, not by
	// removing the worker.
	provider, err := data.OpenKCProvider(repository, bc.Network.Kubeconfig, data.KCClientPolicy{QPS: 5, Burst: 10})
	if err != nil {
		return err
	}
	if err = repository.ConfigureVPCCIDRPresets(bc.Network.VpcCidrPresets, provider); err != nil {
		return err
	}
	w := bc.Network.Worker
	policy := biz.WorkerPolicy{Lease: w.Lease.AsDuration(), RequestTimeout: w.RequestTimeout.AsDuration(), ObserveEvery: w.ObserveEvery.AsDuration(), StaleAfter: w.StaleAfter.AsDuration(), RetryMin: w.RetryMin.AsDuration(), RetryMax: w.RetryMax.AsDuration()}
	repository.UseBaseConnectivityFreshness(policy.StaleAfter)
	repository.UseEgressInfrastructure(provider)
	key, _ := base64.StdEncoding.DecodeString(bc.Network.CursorSigningKey)
	networkUC, err := biz.NewNetwork(repository, key, policy.StaleAfter, time.Now)
	if err != nil {
		return err
	}
	worker, err := biz.NewWorker(repository, provider, uuid.NewString(), policy, func(ctx context.Context, work biz.Work, progress biz.Progress, err error) {})
	if err != nil {
		return err
	}
	egress, err := biz.NewEgress(repository, provider, biz.ContextEgressAuthorization{}, key, policy.StaleAfter, time.Now)
	if err != nil {
		return err
	}
	lbs, err := biz.NewLoadBalancers(repository, biz.ContextEgressAuthorization{}, key, policy.StaleAfter, time.Now)
	if err != nil {
		return err
	}
	execution := server.NewWorkerServer(worker, repository, logger, w.PollInterval.AsDuration())
	readiness := server.NewReadiness(func() bool { return execution.Ready() })
	observability, err := server.NewObservability(Name, Version, readiness)
	if err != nil {
		return err
	}
	configured = true
	defer func() {
		if err := observability.Shutdown(context.Background()); err != nil {
			logger.Error("observability shutdown", "error", err)
		}
	}()
	middlewares := observability.ServerMiddleware(logger)
	gs := kratosgrpc.NewServer(kratosgrpc.Network(bc.Server.Grpc.Network), kratosgrpc.Address(bc.Server.Grpc.Addr),
		kratosgrpc.Timeout(bc.Server.Grpc.Timeout.AsDuration()), kratosgrpc.TLSConfig(tlsConfig),
		kratosgrpc.UnaryInterceptor(server.GovernanceUnary()), kratosgrpc.StreamInterceptor(server.DenyGovernanceStreams),
		kratosgrpc.DisableReflection(), kratosgrpc.Middleware(middlewares...))
	networkv1.RegisterNetworkServiceServer(gs, networkService.NewNetworkService(networkUC))
	networkv1.RegisterTenantEgressServiceServer(gs, networkService.NewTenantEgressService(egress))
	networkv1.RegisterTenantLoadBalancerServiceServer(gs, networkService.NewTenantLoadBalancerService(lbs))
	if images != nil {
		imagev1.RegisterTenantImageServiceServer(gs, images.tenant)
	}
	admin := server.NewAdminServer(bc.Server.Admin, readiness, observability.Gatherer(), middlewares...)
	return kratos.New(kratos.ID(id), kratos.Name(Name), kratos.Version(Version), kratos.Logger(logger),
		kratos.Server(gs, admin, execution),
		kratos.AfterStart(func(context.Context) error { readiness.Set(true); return nil }),
		kratos.BeforeStop(func(context.Context) error { readiness.Set(false); return nil }),
		kratos.AfterStop(func(ctx context.Context) error {
			repository.Close()
			return nil
		}),
		kratos.StopTimeout(bc.Server.ShutdownTimeout.AsDuration())).Run()
}
