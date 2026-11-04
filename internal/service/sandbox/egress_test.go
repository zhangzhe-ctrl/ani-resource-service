package sandbox

import (
	"context"
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type egressReader struct{ calls int }

func (r *egressReader) ReadEgressDeclaration(context.Context, biz.Sandbox) (biz.EgressDeclaration, error) {
	r.calls++
	return biz.EgressDeclaration{Declaration: "restricted", Isolated: true, Rules: []biz.EgressRule{{Peers: []biz.EgressPeer{{IPBlock: &biz.EgressIPBlock{CIDR: "192.0.2.0/24", Except: []string{"192.0.2.2/32"}}}}, Ports: []biz.EgressPort{{Protocol: "TCP", PortType: "number", Port: "443"}}}}}, nil
}

func TestGetSandboxEgressChecksTenantAndSnapshot(t *testing.T) {
	svc, repo, _ := environmentService()
	reader := &egressReader{}
	svc.config.EgressReader = reader
	req := &pb.GetSandboxEgressRequest{TenantId: testTenant, SandboxId: testSandbox}
	got, err := svc.GetSandboxEgress(serviceContext(), req)
	if err != nil || got.Source != "kubernetes_network_policy" || got.Enforcement != "unknown" || got.HostAllowlistStatus != "unknown" || got.HostAllowlistKnown || got.Declaration != "restricted" || !got.Isolated || len(got.Rules) != 1 || got.Rules[0].Peers[0].IpBlock.Cidr != "192.0.2.0/24" || got.Rules[0].Ports[0].Port != "443" || repo.calls != 2 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	req.TenantId = "66666666-6666-4666-8666-666666666666"
	before := reader.calls
	if _, err = svc.GetSandboxEgress(serviceContext(), req); status.Code(err) != codes.PermissionDenied || reader.calls != before {
		t.Fatalf("tenant check: %v", err)
	}
	req.TenantId = testTenant
	changed := repo.states[0]
	changed.ProviderRefs.PodUID = "new-pod"
	repo.states = append(repo.states, changed)
	repo.calls = 0
	if _, err = svc.GetSandboxEgress(serviceContext(), req); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("changed target: %v", err)
	}
}

func TestGetSandboxEgressIntentDistinctFromUnknownAndPaused(t *testing.T) {
	for _, tc := range []struct {
		name, policy string
		hosts        []string
		paused       bool
		known        bool
		status       string
	}{
		{"legacy", "", nil, false, false, "unknown"}, {"empty_allowlist", "allowlist", nil, false, true, "known"},
		{"declared_hosts_paused", "allowlist", []string{"api.example"}, true, true, "known"}, {"internet", "internet", nil, false, false, "not_applicable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, _ := environmentService()
			reader := &egressReader{}
			svc.config.EgressReader = reader
			if tc.policy != "" {
				raw, err := pb.EncodeEgressIntent(&pb.SandboxEgressIntent{NetworkEgressPolicy: tc.policy, EgressAllowlist: tc.hosts})
				if err != nil {
					t.Fatal(err)
				}
				repo.states[0].Snapshot.EgressIntentJSON = raw
			}
			if tc.paused {
				repo.states[0].Desired = biz.DesiredPaused
				repo.states[0].Observed = biz.ObservedPaused
				repo.states[0].ProviderRefs.PodUID = ""
			}
			got, err := svc.GetSandboxEgress(serviceContext(), &pb.GetSandboxEgressRequest{TenantId: testTenant, SandboxId: testSandbox})
			if err != nil || got.HostAllowlistKnown != tc.known || got.HostAllowlistStatus != tc.status || (tc.policy != "" && (got.Intent == nil || got.IntentEnforcement != "not_applied_by_sandbox")) {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if tc.paused && (got.Source != "unavailable" || got.Declaration != "unknown" || reader.calls != 0) {
				t.Fatalf("paused %+v %d", got, reader.calls)
			}
		})
	}
}
