package main

import (
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	sandboxpb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type cloneGateProbe struct {
	sandboxpb.UnimplementedSandboxServiceServer
	clones  atomic.Int32
	creates atomic.Int32
}

func (s *cloneGateProbe) AcceptSandboxClone(context.Context, *sandboxpb.AcceptSandboxCloneRequest) (*sandboxpb.AcceptSandboxCloneResponse, error) {
	s.clones.Add(1)
	return &sandboxpb.AcceptSandboxCloneResponse{}, nil
}
func (s *cloneGateProbe) AcceptCreate(context.Context, *sandboxpb.AcceptCreateRequest) (*sandboxpb.AcceptCreateResponse, error) {
	s.creates.Add(1)
	return &sandboxpb.AcceptCreateResponse{}, nil
}
func TestSandboxCloneGateGRPC(t *testing.T) {
	underlying := &cloneGateProbe{}
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	sandboxpb.RegisterSandboxServiceServer(server, &sandboxCloneGate{SandboxServiceServer: underlying})
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///sandbox", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := sandboxpb.NewSandboxServiceClient(conn)
	for _, identity := range []string{"governance", "another-authorized-client", "no-client-metadata"} {
		ctx := metadata.AppendToOutgoingContext(context.Background(), "caller", identity)
		// Retry the exact same command; a maintenance denial is not an ACK.
		request := &sandboxpb.AcceptSandboxCloneRequest{Context: &sandboxpb.CommandContext{TenantId: "tenant", ResourceId: "sandbox", OperationId: "operation"}}
		for attempt := 0; attempt < 2; attempt++ {
			if _, err = client.AcceptSandboxClone(ctx, request); status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "SANDBOX_CLONE_TEMPORARILY_UNAVAILABLE" {
				t.Fatalf("%s attempt %d: %v", identity, attempt, err)
			}
		}
		if _, err = client.AcceptCreate(ctx, &sandboxpb.AcceptCreateRequest{}); err != nil {
			t.Fatalf("%s: unrelated call: %v", identity, err)
		}
	}
	if underlying.clones.Load() != 0 || underlying.creates.Load() != 3 {
		t.Fatalf("clone calls=%d create calls=%d", underlying.clones.Load(), underlying.creates.Load())
	}
}
func TestSandboxCloneGateConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value           string
		paused, invalid bool
	}{{"", false, false}, {"false", false, false}, {"true", true, false}, {"TRUE", false, true}, {"1", false, true}, {"tru", false, true}} {
		got, err := sandboxClonePaused(tc.value)
		if got != tc.paused || (err != nil) != tc.invalid {
			t.Fatalf("%q = %v, %v", tc.value, got, err)
		}
	}
	t.Setenv("ANI_SANDBOX_CLONE_WRITES_PAUSED", "true")
	result, err := openSandbox(context.Background(), nil, nil, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.service.(*sandboxCloneGate); !ok {
		t.Fatalf("gate missing from composition: %T", result.service)
	}
	t.Setenv("ANI_SANDBOX_CLONE_WRITES_PAUSED", "false")
	result, err = openSandbox(context.Background(), nil, nil, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.service.(*sandboxCloneGate); ok {
		t.Fatal("unpaused configuration unexpectedly installed gate")
	}
	t.Setenv("ANI_SANDBOX_CLONE_WRITES_PAUSED", "invalid")
	if _, err := openSandbox(context.Background(), nil, nil, nil, slog.Default()); err == nil {
		t.Fatal("invalid gate configuration must fail closed")
	}
}

func TestSandboxCloneGateCoversRetryTarget(t *testing.T) {
	probe := &cloneGateProbe{}
	retry := newSandboxRetry(nil, time.Second)
	retry.target = &sandboxRetryTarget{service: probe}
	gate := &sandboxCloneGate{SandboxServiceServer: retry}
	for i := 0; i < 2; i++ {
		if _, err := gate.AcceptSandboxClone(context.Background(), &sandboxpb.AcceptSandboxCloneRequest{}); status.Code(err) != codes.Unavailable {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := gate.AcceptCreate(context.Background(), &sandboxpb.AcceptCreateRequest{}); err != nil {
		t.Fatal(err)
	}
	if probe.clones.Load() != 0 || probe.creates.Load() != 1 {
		t.Fatalf("clone calls=%d create calls=%d", probe.clones.Load(), probe.creates.Load())
	}
}
