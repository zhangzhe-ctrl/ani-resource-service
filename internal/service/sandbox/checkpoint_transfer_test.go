package sandbox

import (
	"context"
	"strings"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type cloneReplayRepo struct {
	serviceRepo
	lookups int
}

func (r *cloneReplayRepo) LookupCheckpointClone(_ context.Context, c biz.CheckpointCloneCommand) (biz.CheckpointTransfer, error) {
	r.lookups++
	return biz.CheckpointTransfer{OperationID: c.Transfer.OperationID, TargetSandboxID: c.Transfer.TargetSandboxID}, nil
}
func (r *cloneReplayRepo) AcceptCheckpointClone(context.Context, biz.CheckpointCloneCommand) (biz.CheckpointTransfer, error) {
	panic("accepted replay must not create")
}

func TestCloneRejectsNoncanonicalEgressBeforeReplay(t *testing.T) {
	s := newTestService(t, validSelection())
	resolved, err := s.ResolveCreateSpec(serviceContext(), validResolveRequest())
	if err != nil {
		t.Fatal(err)
	}
	repo := &cloneReplayRepo{}
	s.repo = repo
	for _, tc := range []struct {
		name   string
		intent *pb.SandboxEgressIntent
		want   codes.Code
		lookup bool
	}{
		{"canonical", &pb.SandboxEgressIntent{NetworkEgressPolicy: "allowlist", EgressAllowlist: []string{"a.example", "z.example"}}, codes.OK, true},
		{"noncanonical", &pb.SandboxEgressIntent{NetworkEgressPolicy: "allowlist", EgressAllowlist: []string{"Z.example", "a.example"}}, codes.InvalidArgument, false},
		{"invalid", &pb.SandboxEgressIntent{NetworkEgressPolicy: "deny_all", EgressAllowlist: []string{"a.example"}}, codes.InvalidArgument, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := proto.Clone(resolved).(*pb.ResolveCreateSpecResponse)
			spec.EgressIntent = tc.intent
			if d, e := frozenSpecDigest(spec); e == nil {
				spec.SpecDigest = d
			}
			req := &pb.AcceptSandboxCloneRequest{Context: &pb.CommandContext{TenantId: testTenant, ResourceId: testSandbox, OperationId: testOp}, Spec: spec, SourceSandboxId: testCluster, CheckpointId: testPreset, ExpectedVersion: 1, CreateIntentDigest: strings.Repeat("a", 64)}
			req.Context.RequestDigest, err = pb.CloneCommandDigest(req.Context, req.SourceSandboxId, req.CheckpointId, req.ExpectedVersion, req.Spec, req.CreateIntentDigest)
			if err != nil {
				t.Fatal(err)
			}
			before := repo.lookups
			_, err = s.AcceptSandboxClone(serviceContext(), req)
			if status.Code(err) != tc.want {
				t.Fatalf("got %v", err)
			}
			if (repo.lookups > before) != tc.lookup {
				t.Fatalf("lookup calls=%d before=%d", repo.lookups, before)
			}
		})
	}
}

func TestCloneRestrictedEgressRejectsNewButReplaysAccepted(t *testing.T) {
	s := newTestService(t, validSelection())
	resolved, err := s.ResolveCreateSpec(serviceContext(), validResolveRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"allowlist", "deny_all"} {
		t.Run(policy, func(t *testing.T) {
			spec := proto.Clone(resolved).(*pb.ResolveCreateSpecResponse)
			spec.EgressIntent = &pb.SandboxEgressIntent{NetworkEgressPolicy: policy}
			spec.SpecDigest, err = frozenSpecDigest(spec)
			if err != nil {
				t.Fatal(err)
			}
			req := &pb.AcceptSandboxCloneRequest{Context: &pb.CommandContext{TenantId: testTenant, ResourceId: testSandbox, OperationId: testOp}, Spec: spec, SourceSandboxId: testCluster, CheckpointId: testPreset, ExpectedVersion: 1, CreateIntentDigest: strings.Repeat("a", 64)}
			req.Context.RequestDigest, err = pb.CloneCommandDigest(req.Context, req.SourceSandboxId, req.CheckpointId, req.ExpectedVersion, req.Spec, req.CreateIntentDigest)
			if err != nil {
				t.Fatal(err)
			}
			repo := &transferAcceptanceRepo{}
			s.repo = repo
			if _, err = s.AcceptSandboxClone(serviceContext(), req); status.Code(err) != codes.Unimplemented || status.Convert(err).Message() != "SANDBOX_EGRESS_ENFORCEMENT_UNSUPPORTED" || repo.cloneAccepts != 0 {
				t.Fatalf("new clone: accepts=%d err=%v", repo.cloneAccepts, err)
			}
			repo.cloneReplay = true
			out, err := s.AcceptSandboxClone(serviceContext(), req)
			if err != nil || !out.GetAck().GetReplayed() {
				t.Fatalf("accepted replay: %v %v", out, err)
			}
		})
	}
}

type transferPreflightStub struct {
	err   error
	calls int
}

func (p *transferPreflightStub) ValidateCheckpointTransferStorage(context.Context, string) error {
	p.calls++
	return p.err
}

type transferAcceptanceRepo struct {
	serviceRepo
	biz.CheckpointTransferRepository
	cloneReplay, restoreReplay   bool
	cloneAccepts, restoreAccepts int
	target                       biz.Sandbox
}

func (r *transferAcceptanceRepo) Get(context.Context, string, string) (biz.Sandbox, error) {
	return r.target, nil
}
func (r *transferAcceptanceRepo) LookupCheckpointClone(_ context.Context, c biz.CheckpointCloneCommand) (biz.CheckpointTransfer, error) {
	if !r.cloneReplay {
		return biz.CheckpointTransfer{}, biz.ErrNotFound
	}
	return biz.CheckpointTransfer{OperationID: c.Transfer.OperationID, TargetSandboxID: c.Transfer.TargetSandboxID, Replayed: true}, nil
}
func (r *transferAcceptanceRepo) RejectCheckpointCloneWFFC(context.Context, biz.CheckpointCloneCommand) (biz.CheckpointTransfer, error) {
	return biz.CheckpointTransfer{}, biz.ErrCheckpointWFFCUnsupported
}
func (r *transferAcceptanceRepo) RejectCheckpointTransferWFFC(context.Context, biz.CheckpointTransferCommand) (biz.CheckpointTransfer, error) {
	return biz.CheckpointTransfer{}, biz.ErrCheckpointWFFCUnsupported
}
func (r *transferAcceptanceRepo) AcceptCheckpointClone(context.Context, biz.CheckpointCloneCommand) (biz.CheckpointTransfer, error) {
	r.cloneAccepts++
	panic("unsupported class accepted")
}
func (r *transferAcceptanceRepo) LookupCheckpointTransfer(_ context.Context, c biz.CheckpointTransferCommand) (biz.CheckpointTransfer, error) {
	if !r.restoreReplay {
		return biz.CheckpointTransfer{}, biz.ErrNotFound
	}
	return biz.CheckpointTransfer{OperationID: testOp, TargetSandboxID: c.TargetSandboxID, Replayed: true}, nil
}
func (r *transferAcceptanceRepo) AcceptCheckpointTransfer(context.Context, biz.CheckpointTransferCommand) (biz.CheckpointTransfer, error) {
	r.restoreAccepts++
	panic("unsupported class accepted")
}

func TestCheckpointTransferPreflightBeforeFirstAcceptButNotReplay(t *testing.T) {
	s := newTestService(t, validSelection())
	spec, err := s.ResolveCreateSpec(serviceContext(), validResolveRequest())
	if err != nil {
		t.Fatal(err)
	}
	repo := &transferAcceptanceRepo{target: biz.Sandbox{Snapshot: biz.Snapshot{Workspace: biz.WorkspaceSpec{StorageClass: "wffc"}}}}
	s.repo = repo
	preflight := &transferPreflightStub{err: biz.ErrCheckpointWFFCUnsupported}
	s.config.CheckpointTransferPreflight = preflight
	clone := &pb.AcceptSandboxCloneRequest{Context: &pb.CommandContext{TenantId: testTenant, ResourceId: testSandbox, OperationId: testOp}, Spec: spec, SourceSandboxId: testCluster, CheckpointId: testPreset, ExpectedVersion: 1, CreateIntentDigest: strings.Repeat("a", 64)}
	clone.Context.RequestDigest, err = pb.CloneCommandDigest(clone.Context, clone.SourceSandboxId, clone.CheckpointId, clone.ExpectedVersion, clone.Spec, clone.CreateIntentDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptSandboxClone(serviceContext(), clone); status.Code(err) != codes.Unimplemented || status.Convert(err).Message() != "SANDBOX_TRANSFER_WFFC_UNSUPPORTED" {
		t.Fatalf("clone preflight: %v", err)
	}
	if repo.cloneAccepts != 0 || preflight.calls != 1 {
		t.Fatalf("clone accepted=%d checks=%d", repo.cloneAccepts, preflight.calls)
	}
	repo.cloneReplay = true
	if out, err := s.AcceptSandboxClone(serviceContext(), clone); err != nil || !out.Ack.Replayed || preflight.calls != 1 {
		t.Fatalf("clone replay %+v %v checks=%d", out, err, preflight.calls)
	}
	restore := &pb.RestoreSandboxCheckpointRequest{TenantId: testTenant, SandboxId: testSandbox, CheckpointId: testPreset, ExpectedVersion: 1, IdempotencyKey: "restore"}
	if _, err = s.RestoreSandboxCheckpoint(serviceContext(), restore); status.Code(err) != codes.Unimplemented || status.Convert(err).Message() != "SANDBOX_TRANSFER_WFFC_UNSUPPORTED" {
		t.Fatalf("restore preflight: %v", err)
	}
	if repo.restoreAccepts != 0 || preflight.calls != 2 {
		t.Fatalf("restore accepted=%d checks=%d", repo.restoreAccepts, preflight.calls)
	}
	repo.restoreReplay = true
	if out, err := s.RestoreSandboxCheckpoint(serviceContext(), restore); err != nil || !out.Ack.Replayed || preflight.calls != 2 {
		t.Fatalf("restore replay %+v %v checks=%d", out, err, preflight.calls)
	}
}
