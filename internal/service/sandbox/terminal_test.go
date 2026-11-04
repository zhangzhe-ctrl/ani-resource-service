package sandbox

import (
	"context"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/terminal/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const terminalTenant = "11111111-1111-4111-8111-111111111111"
const terminalID = "22222222-2222-4222-8222-222222222222"

type terminalReader struct {
	s     biz.Sandbox
	reads int
}

func (r *terminalReader) Get(context.Context, string, string) (biz.Sandbox, error) {
	r.reads++
	return r.s, nil
}

type terminalVerifier struct {
	err    error
	during func()
}

func (v *terminalVerifier) VerifyDataPlaneTarget(_ context.Context, _ biz.Sandbox, _ biz.DataPlaneTarget) error {
	if v.during != nil {
		v.during()
	}
	return v.err
}
func terminalFixture() (*TerminalBinding, *terminalReader, *terminalVerifier, context.Context) {
	r := &terminalReader{s: biz.Sandbox{TenantID: terminalTenant, ID: terminalID, Version: 2, Desired: biz.DesiredRunning, Observed: biz.ObservedRunning, Snapshot: biz.Snapshot{ClusterID: "cluster", Namespace: "tenant-test", NamespaceUID: "namespace-uid"}, ProviderRefs: biz.ProviderRefs{PodName: "sandbox-" + terminalID, PodUID: "pod-uid"}}}
	v := &terminalVerifier{}
	return NewTerminalBinding(r, v), r, v, biz.WithCaller(context.Background(), biz.Caller{TenantID: terminalTenant, Actor: "governance:user:1"})
}
func TestTerminalBindingRequiresTenantAndCurrentIdentity(t *testing.T) {
	s, r, v, ctx := terminalFixture()
	req := &pb.ResolveTerminalSessionRequest{TenantId: terminalTenant, SandboxId: terminalID}
	binding, err := s.ResolveTerminalSession(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if binding.SandboxId != terminalID || binding.Shell != "/bin/sh" || binding.NamespaceUid != "namespace-uid" || binding.PodUid != "pod-uid" {
		t.Fatalf("binding: %+v", binding)
	}
	if _, err = s.ValidateTerminalSession(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	cross := biz.WithCaller(context.Background(), biz.Caller{TenantID: "33333333-3333-4333-8333-333333333333", Actor: "governance:user:1"})
	before := r.reads
	if _, err = s.ResolveTerminalSession(cross, req); status.Code(err) != codes.PermissionDenied || r.reads != before {
		t.Fatalf("cross tenant: %v, reads=%d", err, r.reads-before)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*pb.SandboxTerminalBinding)
	}{
		{"version", func(b *pb.SandboxTerminalBinding) { b.Version++ }}, {"namespace", func(b *pb.SandboxTerminalBinding) { b.NamespaceUid = "other" }},
		{"pod", func(b *pb.SandboxTerminalBinding) { b.PodUid = "other" }}, {"container", func(b *pb.SandboxTerminalBinding) { b.ContainerName = "other" }},
		{"shell", func(b *pb.SandboxTerminalBinding) { b.Shell = "/bin/bash" }}, {"cluster", func(b *pb.SandboxTerminalBinding) { b.ClusterId = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := proto.Clone(binding).(*pb.SandboxTerminalBinding)
			tc.mutate(copy)
			if _, err := s.ValidateTerminalSession(ctx, copy); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("stale binding accepted: %v", err)
			}
		})
	}
	v.during = func() { r.s.Version++ }
	if _, err = s.ResolveTerminalSession(ctx, req); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("concurrent version change accepted: %v", err)
	}
}
func TestTerminalBindingRejectsUnavailableRuntime(t *testing.T) {
	s, r, v, ctx := terminalFixture()
	req := &pb.ResolveTerminalSessionRequest{TenantId: terminalTenant, SandboxId: terminalID}
	r.s.Desired = biz.DesiredPaused
	if _, err := s.ResolveTerminalSession(ctx, req); status.Code(err) != codes.FailedPrecondition {
		t.Fatal(err)
	}
	r.s.Desired = biz.DesiredRunning
	v.err = biz.ErrOwnership
	if _, err := s.ResolveTerminalSession(ctx, req); status.Code(err) != codes.FailedPrecondition {
		t.Fatal(err)
	}
	if _, err := NewTerminalBinding(nil, nil).ResolveTerminalSession(ctx, req); status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
}
