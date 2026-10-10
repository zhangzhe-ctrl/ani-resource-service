package data_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
	controlled "github.com/zhangzhe-ctrl/ani-resource-service/tests/net05a/provider"
	"github.com/zhangzhe-ctrl/ani-resource-service/tests/testenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"sigs.k8s.io/yaml"
)

type lbProcessOwner struct {
	networkv1.UnimplementedInstanceNetworkConsumerServiceServer
	a biz.Attachment
}

func (o *lbProcessOwner) GetSubmission(_ context.Context, r *networkv1.GetSubmissionRequest) (*networkv1.GetSubmissionResponse, error) {
	a := o.a
	if r.ProtocolVersion != 1 || r.TenantId != a.TenantID || r.InstanceId != a.InstanceID || r.AttachmentId != a.ID || r.SubmissionId != a.SubmissionID || r.Generation != a.Generation {
		return nil, status.Error(codes.NotFound, "owner submission not found")
	}
	return &networkv1.GetSubmissionResponse{ProtocolVersion: 1, TenantId: a.TenantID, InstanceId: a.InstanceID, AttachmentId: a.ID, SubmissionId: a.SubmissionID, Generation: a.Generation, ClusterId: a.ClusterID, Namespace: a.Namespace, State: networkv1.SubmissionState_SUBMISSION_STATE_OPEN, PodUids: []string{a.PodUID}}, nil
}
func startLBProcessEndpoints(t *testing.T, f *lbAdmissionFixture) (networkv1.TenantLoadBalancerServiceClient, string) {
	t.Helper()
	var id string
	if err := f.f.owner.QueryRow(f.f.ctx, `SELECT attachment_id FROM network_attachments WHERE tenant_id=$1 AND instance_id='lb-backend'`, f.f.tenant).Scan(&id); err != nil {
		t.Fatal(err)
	}
	a, err := biz.NewAttachments(f.f.p, time.Minute).Get(f.f.ctx, f.f.tenant, id)
	if err != nil {
		t.Fatal(err)
	}
	// The isolated socket fixes one explicit test caller context at startup.
	// No header/body value can grant administrator status or change this tenant.
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		return next(biz.WithEgressCaller(ctx, biz.EgressCaller{TenantID: f.f.tenant, Attribution: biz.Attribution{Actor: "isolated-lb-process-fixture", DirectCaller: "fixture-socket"}}), request)
	}))
	networkv1.RegisterTenantLoadBalancerServiceServer(server, f.rpc)
	networkv1.RegisterInstanceNetworkConsumerServiceServer(server, &lbProcessOwner{a: a})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return networkv1.NewTenantLoadBalancerServiceClient(conn), listener.Addr().String()
}
func lbProcessConfig(t *testing.T, root, owner string, expected data.LoadBalancerInstallation) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "configs/config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = yaml.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	network := config["network"].(map[string]any)
	network["instance_consumer_endpoint"] = owner
	network["load_balancer"] = map[string]any{"enable_isolated_api": true, "installation_fingerprint": expected.Fingerprint, "controller_image_id": expected.ControllerImageID, "envoy_image_id": expected.EnvoyImageID, "shutdown_image_id": expected.ShutdownImageID, "kc_image_id": expected.KCImageID}
	body, err = yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lb-process.yaml")
	if err = os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func lbProcessIntercept(fault *baseProcessFault, controller *lbControllerFixture) func(http.ResponseWriter, *http.Request, *controlled.Server) bool {
	return func(w http.ResponseWriter, r *http.Request, api *controlled.Server) bool {
		if strings.HasSuffix(r.URL.Path, "/subjectaccessreviews") && r.Method == "POST" {
			var obj map[string]any
			_ = json.NewDecoder(r.Body).Decode(&obj)
			obj["status"] = map[string]any{"allowed": true}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(obj)
			return true
		}
		match := r.Method == fault.method && ((r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/"+fault.kind)) || (r.Method != "POST" && strings.Contains(r.URL.Path, "/"+fault.kind+"/")))
		if !match || !fault.armed.Load() || !fault.captured.CompareAndSwap(false, true) {
			return controller.http(w, r, api)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "body unavailable", 400)
			return true
		}
		detached := r.Clone(context.WithoutCancel(r.Context()))
		detached.Body = io.NopCloser(bytes.NewReader(body))
		recorder := httptest.NewRecorder()
		if fault.delayed {
			close(fault.reached)
			<-fault.release
		}
		if !controller.http(recorder, detached, api) {
			http.Error(w, "unexpected LB resource", 500)
			return true
		}
		fault.status.Store(int32(recorder.Code))
		close(fault.executed)
		if !fault.delayed {
			close(fault.reached)
			<-fault.release
		}
		for k, values := range recorder.Header() {
			w.Header()[k] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
		return true
	}
}
func waitLBProcesses(t *testing.T, f *lbAdmissionFixture, id string, condition func(biz.LoadBalancer) bool, processes ...*networkProcess) biz.LoadBalancer {
	t.Helper()
	var lb biz.LoadBalancer
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		lb, err = f.lbs.Get(f.f.ctx, "", id)
		if err == nil && condition(lb) {
			return lb
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, p := range processes {
		body, _ := os.ReadFile(p.logPath)
		t.Logf("process log: %s", body)
	}
	t.Fatalf("LB process recovery did not converge: %+v", lb)
	return lb
}
func TestLBServiceProcessesRecoverUnknownMutationsWithTwoWorkers(t *testing.T) {
	if os.Getenv("NETWORK_TEST_ADMIN_DSN") == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "network-lb-recovery")
	build := exec.Command("go", "build", "-race", "-trimpath", "-o", binary, "./cmd/ani-resource-service")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build LB process: %v\n%s", err, output)
	}
	for _, tc := range []struct {
		name, method, kind string
		late               bool
		terminate          bool
	}{{"backend_POST_response_lost", "POST", "backends", false, false}, {"gateway_POST_late_success", "POST", "gateways", true, false}, {"policy_POST_response_lost", "POST", "backendtrafficpolicies", false, false}, {"route_UPDATE_response_lost", "PATCH", "httproutes", false, false}, {"gateway_UPDATE_response_lost", "PATCH", "gateways", false, false}, {"gateway_DELETE_response_lost", "DELETE", "gateways", false, false}, {"delete_during_unknown_gateway_POST", "POST", "gateways", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			fault := newBaseProcessFault(tc.method, tc.kind, tc.late)
			controller := &lbControllerFixture{}
			f := newLBAdmissionFixture(t, lbProcessIntercept(fault, controller))
			t.Cleanup(fault.unblock)
			expected := seedLBInstallation(f.api)
			client, owner := startLBProcessEndpoints(t, f)
			config := lbProcessConfig(t, root, owner, expected)
			start := func() *networkProcess {
				return startNetworkProcess(t, root, binary, f.runtimeDSN, f.kubeconfig, testenv.SigningKey(), "load_balancer", "config="+config)
			}
			first := start()
			awaitNET05A(t, 8*time.Second, func() bool {
				var ready bool
				err := f.f.owner.QueryRow(f.f.ctx, `SELECT ready FROM network_lb_capabilities WHERE cluster_id='test-cluster' AND fingerprint=$1`, expected.Fingerprint).Scan(&ready)
				return err == nil && ready
			})
			// The production service registers the isolated RPC, but an arbitrary
			// caller still cannot manufacture the authorization context.
			conn, err := grpc.NewClient(first.grpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			_, err = networkv1.NewTenantLoadBalancerServiceClient(conn).ListLoadBalancers(context.Background(), &networkv1.ListLoadBalancersRequest{})
			_ = conn.Close()
			if status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.Unauthenticated {
				t.Fatal("standard LB service did not deny missing authorization", err)
			}
			if tc.method == "POST" {
				fault.armed.Store(true)
			}
			health := &networkv1.LoadBalancerHealthCheck{Port: f.request.Health.Port}
			request := &networkv1.CreateLoadBalancerRequest{HealthCheck: health, Name: "process-lb", VpcId: f.vpc.ID, SubnetId: f.subnet.ID, Exposure: networkv1.LoadBalancerExposure_LOAD_BALANCER_EXPOSURE_PRIVATE, PrivateIp: "10.42.1.100", IdempotencyKey: "process-lb", Backends: []*networkv1.LoadBalancerBackendInput{{SubnetId: f.backendSubnet.ID, Address: "10.42.2.2", Port: 8080}}}
			var created *networkv1.CreateLoadBalancerResponse
			awaitNET05A(t, 8*time.Second, func() bool {
				created, err = client.CreateLoadBalancer(context.Background(), request)
				if err != nil && status.Code(err) != codes.Unavailable {
					t.Fatal(err)
				}
				return err == nil
			})
			id := created.LoadBalancer.Id
			if tc.method != "POST" {
				lb := waitLBProcesses(t, f, id, func(l biz.LoadBalancer) bool { return l.State == biz.Available }, first)
				fault.armed.Store(true)
				if tc.method == "PATCH" {
					zero := uint32(0)
					update := &networkv1.UpdateLoadBalancerRequest{HealthCheck: health, LoadBalancerId: id, ExpectedVersion: lb.Version, Name: "updated", IdempotencyKey: "update", Backends: []*networkv1.LoadBalancerBackendInput{{Id: lb.Backends[0].ID, SubnetId: lb.Backends[0].SubnetID, Address: lb.Backends[0].Address, Port: lb.Backends[0].Port, Weight: &zero}}}
					if tc.kind == "gateways" {
						changedPort := uint32(8081)
						b := lb.Backends[0]
						retained := &networkv1.LoadBalancerBackendInput{Id: b.ID, SubnetId: b.SubnetID, Address: b.Address, Port: b.Port, Weight: &b.Weight}
						update = &networkv1.UpdateLoadBalancerRequest{LoadBalancerId: id, ExpectedVersion: lb.Version, IdempotencyKey: "update", UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"listeners"}}, Data: &networkv1.LoadBalancerMutableData{Listeners: &networkv1.LoadBalancerListenerSet{Items: []*networkv1.LoadBalancerListenerInput{{Id: lb.Listeners[0].ID, Name: lb.Listeners[0].Name, Port: &changedPort, Backends: []*networkv1.LoadBalancerBackendInput{retained}, HealthCheck: health}}}}}
					}
					_, err = client.UpdateLoadBalancer(context.Background(), update)
				} else {
					_, err = client.DeleteLoadBalancer(context.Background(), &networkv1.DeleteLoadBalancerRequest{LoadBalancerId: id})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			waitBaseFault(t, fault, first)
			first.kill(t)
			var reserved int
			if err = f.f.owner.QueryRow(f.f.ctx, `SELECT count(*) FROM network_lb_vip_intents WHERE tenant_id=$1 AND lb_id=$2 AND released_at IS NULL`, f.f.tenant, id).Scan(&reserved); err != nil || reserved != 1 {
				t.Fatal("unknown mutation released VIP", reserved, err)
			}
			second, third := start(), start()
			if tc.terminate {
				deleted, err := client.DeleteLoadBalancer(context.Background(), &networkv1.DeleteLoadBalancerRequest{LoadBalancerId: id})
				if err != nil {
					t.Fatal(err)
				}
				waitLBProcesses(t, f, id, func(l biz.LoadBalancer) bool { return l.State == biz.Deleting && l.Reason == biz.ProviderUnknown }, second, third)
				op, err := f.lbs.GetOperation(f.f.ctx, "", deleted.Operation.Id)
				if err != nil || op.State != biz.Blocked {
					t.Fatal("unknown POST deletion must remain blocked", op, err)
				}
				if err = f.f.owner.QueryRow(f.f.ctx, `SELECT count(*) FROM network_lb_vip_intents WHERE tenant_id=$1 AND lb_id=$2 AND released_at IS NULL`, f.f.tenant, id).Scan(&reserved); err != nil || reserved != 1 {
					t.Fatal("termination released an uncertain write's VIP", reserved, err)
				}
				if _, err = client.UpdateLoadBalancer(context.Background(), &networkv1.UpdateLoadBalancerRequest{HealthCheck: health, LoadBalancerId: id, ExpectedVersion: deleted.LoadBalancer.Version, Name: "closed", IdempotencyKey: "closed", Backends: request.Backends}); status.Code(err) != codes.FailedPrecondition {
					t.Fatal("delete did not close updates", err)
				}
			}
			if tc.late {
				var pending string
				if err = f.f.owner.QueryRow(f.f.ctx, `SELECT pending_action FROM network_provider_bindings WHERE tenant_id=$1 AND lb_id=$2`, f.f.tenant, id).Scan(&pending); err != nil || pending != "create" {
					t.Fatal("pending create was not retained", pending, err)
				}
			}
			fault.unblock()
			select {
			case <-fault.executed:
			case <-time.After(4 * time.Second):
				t.Fatal("detached mutation never completed")
			}
			if fault.status.Load() < 200 || fault.status.Load() >= 300 {
				t.Fatal("controlled mutation rejected", fault.status.Load())
			}
			want := biz.Available
			if tc.method == "DELETE" || tc.terminate {
				want = biz.Deleted
			}
			lb := waitLBProcesses(t, f, id, func(l biz.LoadBalancer) bool {
				return l.State == want && (tc.method != "PATCH" || l.AppliedVersion == 2)
			}, second, third)
			if tc.method != "DELETE" && !tc.terminate {
				if _, err = client.DeleteLoadBalancer(context.Background(), &networkv1.DeleteLoadBalancerRequest{LoadBalancerId: id}); err != nil {
					t.Fatal(err)
				}
				lb = waitLBProcesses(t, f, id, func(l biz.LoadBalancer) bool { return l.State == biz.Deleted }, second, third)
			}
			var occupied int
			if err = f.f.owner.QueryRow(f.f.ctx, `SELECT count(*) FROM network_lb_subnet_refs WHERE tenant_id=$1 AND lb_id=$2 AND released_at IS NULL`, f.f.tenant, id).Scan(&occupied); err != nil || occupied != 0 {
				t.Fatal("recovery failed to release parents", occupied, err)
			}
			if lb.DataPlaneState != "unknown" || lb.DataPlaneObservedAt != nil {
				t.Fatal("process success asserted traffic health")
			}
			controller.mu.Lock()
			posts := map[string]int{}
			for _, request := range controller.requests {
				if strings.HasPrefix(request, "POST/") {
					posts[strings.Split(request, "/")[1]]++
				}
			}
			controller.mu.Unlock()
			for _, kind := range []string{"backends", "gateways", "httproutes", "backendtrafficpolicies"} {
				want := 1
				if tc.terminate && (kind == "httproutes" || kind == "backendtrafficpolicies") {
					want = 0
				}
				if posts[kind] != want {
					t.Fatal("duplicate Provider create after recovery", kind, posts)
				}
			}
			second.stop(t)
			third.stop(t)
			t.Logf("%s: same LB=%s recovered using two service processes; product cleanup verified", tc.name, id)
		})
	}
}
