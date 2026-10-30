package data_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	controlled "github.com/zhangzhe-ctrl/ani-resource-service/tests/net05a/provider"
)

func TestKCPlatformShutdownBeforePostPersistsNoWriteOutcome(t *testing.T) {
	var fixture *egressFixture
	var gatewayID, name string
	var stop context.CancelFunc
	var armed, fired atomic.Bool
	var posts atomic.Int32
	faultError := make(chan error, 1)
	f, api, _, _ := newEgressKCFixture(t, func(w http.ResponseWriter, r *http.Request, _ *controlled.Server) bool {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/eipgateways") {
			posts.Add(1)
		}
		if armed.Load() && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/eipgateways/"+name) {
			checkCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			var pending string
			err := fixture.owner.QueryRow(checkCtx, "SELECT pending_action FROM network_platform_resources WHERE resource_id=$1", gatewayID).Scan(&pending)
			cancel()
			if err != nil {
				select {
				case faultError <- err:
				default:
				}
				http.Error(w, "test inspection failed", http.StatusInternalServerError)
				return true
			}
			if pending == "create" && armed.Swap(false) {
				// This is EnsureEgress's GET after BeginMutation committed,
				// before its POST. No simulated write outcome is injected.
				fired.Store(true)
				stop()
				<-r.Context().Done()
				return true
			}
		}
		return false
	})
	fixture = f
	// Fixture bootstrap already created its own unrelated gateway. Count only
	// the accepted resource whose actual KC pre-POST read is interrupted below.
	posts.Store(0)
	gateway, err := f.e.CreatePlatform(f.ctx, biz.PlatformIntent{Kind: "create_egress_gateway", Name: "shutdown-gateway", IdempotencyKey: "shutdown-gateway"})
	if err != nil {
		t.Fatal(err)
	}
	gatewayID, name = gateway.ID, strings.Replace(gateway.ID, "_", "-", 1)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stop = cancel
	armed.Store(true)
	for i := 0; i < 100 && !fired.Load(); i++ {
		_, err = f.w.Step(ctx)
		if err != nil && !fired.Load() {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case e := <-faultError:
		t.Fatal(e)
	default:
	}
	if !fired.Load() {
		t.Fatal("did not interrupt the actual adapter's pre-send GET")
	}
	var pending string
	if e := f.owner.QueryRow(f.ctx, "SELECT pending_action FROM network_platform_resources WHERE resource_id=$1", gateway.ID).Scan(&pending); e != nil {
		t.Fatal(e)
	}
	if err != nil || pending != "" || posts.Load() != 0 || api.Object("eipgateways", "", name) != nil {
		t.Fatalf("pre-send shutdown left uncertain state: pending=%q posts=%d finish_error=%v", pending, posts.Load(), err)
	}
	f.drive(t, func() bool {
		v, e := f.e.GetPlatform(f.ctx, "egress_gateway", gateway.ID)
		return e == nil && v.State == biz.Available
	})
	v, err := f.e.GetPlatform(f.ctx, "egress_gateway", gateway.ID)
	if err != nil || v.LastOperationID != gateway.LastOperationID || posts.Load() != 1 {
		t.Fatal("retry lost operation identity or sent duplicate POST", v, posts.Load(), err)
	}
}

// The dependency reports a definitive pre-send failure while the service is
// shutting down. Acceptance, pending mutation, lease and recovery use real PG.
type cancelEgressCompletion struct {
	*egressProvider
	resourceID string
	cancel     context.CancelFunc
	failure    biz.ProviderFailure
	delay      time.Duration
	fired      bool
}

func (p *cancelEgressCompletion) EnsureEgress(ctx context.Context, target biz.ProviderTarget) (biz.ProviderObservation, error) {
	if target.ResourceID == p.resourceID && !p.fired {
		p.fired = true
		p.cancel()
		if p.delay > 0 {
			time.Sleep(p.delay)
		}
		return biz.ProviderObservation{}, &biz.ProviderError{Kind: p.failure, Cause: context.Canceled}
	}
	return p.egressProvider.EnsureEgress(ctx, target)
}

func TestPlatformCancellationPreservesDefinitiveMutationOutcome(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failure     biz.ProviderFailure
		delay       time.Duration
		wantPending string
	}{
		{"definitive_pre_send_failure", biz.ProviderTemporary, 0, ""},
		{"uncertain_send_remains_pending", biz.ProviderUncertain, 0, "create"},
		{"expired_lease_cannot_clear_pending", biz.ProviderTemporary, 1100 * time.Millisecond, "create"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEgressFixture(t)
			gateway, err := f.e.CreatePlatform(f.ctx, biz.PlatformIntent{Kind: "create_egress_gateway", Name: "cancellation-gateway", IdempotencyKey: "cancellation-gateway"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			provider := &cancelEgressCompletion{egressProvider: f.provider, resourceID: gateway.ID, cancel: cancel, failure: tc.failure, delay: tc.delay}
			policy := biz.DefaultWorkerPolicy()
			policy.Lease, policy.RequestTimeout = time.Second, 200*time.Millisecond
			policy.ObserveEvery, policy.RetryMin, policy.RetryMax = 10*time.Millisecond, time.Millisecond, 5*time.Millisecond
			worker, err := biz.NewWorker(f.p, provider, uuid.NewString(), policy)
			if err != nil {
				t.Fatal(err)
			}
			creates := f.provider.creates
			for i := 0; i < 100 && !provider.fired; i++ {
				_, err = worker.Step(ctx)
				if err != nil && !provider.fired {
					t.Fatal(err)
				}
				time.Sleep(2 * time.Millisecond)
			}
			if !provider.fired {
				t.Fatal("did not reach accepted gateway mutation")
			}
			var pending, uid string
			if readErr := f.owner.QueryRow(f.ctx, "SELECT pending_action,provider_uid FROM network_platform_resources WHERE resource_id=$1", gateway.ID).Scan(&pending, &uid); readErr != nil {
				t.Fatal(readErr)
			}
			if pending != tc.wantPending || uid != "" {
				t.Fatalf("shutdown discarded known mutation outcome: pending=%q want=%q uid=%q finish_error=%v", pending, tc.wantPending, uid, err)
			}
			if f.provider.creates != creates {
				t.Fatal("fault unexpectedly created a Provider object")
			}
			if tc.wantPending == "" {
				if err != nil {
					t.Fatalf("definitive outcome was not persisted: %v", err)
				}
				f.drive(t, func() bool {
					v, e := f.e.GetPlatform(f.ctx, "egress_gateway", gateway.ID)
					if e != nil {
						t.Fatal(e)
					}
					return v.State == biz.Available
				})
				if f.provider.creates != creates+1 {
					t.Fatal("recovery did not create exactly one object")
				}
				v, e := f.e.GetPlatform(f.ctx, "egress_gateway", gateway.ID)
				if e != nil || v.LastOperationID != gateway.LastOperationID {
					t.Fatal("recovery changed original operation", e)
				}
			} else {
				// Allow any abandoned lease to expire before a new worker observes.
				time.Sleep(1100 * time.Millisecond)
				for i := 0; i < 30; i++ {
					if _, e := f.w.Step(f.ctx); e != nil {
						t.Fatal(e)
					}
					time.Sleep(2 * time.Millisecond)
				}
				v, e := f.e.GetPlatform(f.ctx, "egress_gateway", gateway.ID)
				if e != nil || v.Reason != biz.ProviderUnknown || f.provider.creates != creates {
					t.Fatal("uncertain/fenced write was retried", v, e)
				}
			}
		})
	}
}
