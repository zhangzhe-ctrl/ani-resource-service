package data_test

import (
	"github.com/google/uuid"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
)

// Reuses the actual admission, PostgreSQL, worker and KC adapter. Only the
// instance owner and Kubernetes/controller protocol boundary are controlled.
func TestLBMultipleListenersMainFlow(t *testing.T) {
	c := &lbControllerFixture{}
	f := newLBAdmissionFixture(t, c.http)
	second := addLBBackend(t, f, f.backendSubnet, "lb-second", "10.42.2.3")
	second.Port = 9090 // Independent backend and health port on this listener.
	port := func(v uint32) *uint32 { return &v }
	listener := func(name string, p uint32, backend biz.LoadBalancerBackendInput) biz.LoadBalancerListenerInput {
		return biz.LoadBalancerListenerInput{Name: name, Protocol: "HTTP", Port: port(p), Backends: []biz.LoadBalancerBackendInput{backend}, Health: biz.LoadBalancerHealthInput{Port: port(backend.Port)}}
	}
	r := f.request
	r.LoadBalancerMutableInput = biz.LoadBalancerMutableInput{Name: "two-listeners", Listeners: []biz.LoadBalancerListenerInput{listener("first", 80, f.request.Backends[0]), listener("second", 8081, second)}}
	created, err := f.lbs.Create(f.f.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	lb := lbState(t, f, created.LoadBalancer.ID, biz.Available)
	if lb.AppliedVersion != 1 || len(lb.Listeners) != 2 || lb.Listeners[0].Backends[0].Address == lb.Listeners[1].Backends[0].Address {
		t.Fatal("independent listeners did not configure", lb)
	}
	var namespace, gateway, gatewayUID string
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT namespace,provider_name,provider_uid FROM network_provider_bindings WHERE tenant_id=$1 AND lb_id=$2 AND resource_kind='load_balancer'`, f.f.tenant, lb.ID).Scan(&namespace, &gateway, &gatewayUID); err != nil {
		t.Fatal(err)
	}
	firstID, secondID := lb.Listeners[0].ID, lb.Listeners[1].ID
	checkRoutes := func(want int) {
		t.Helper()
		rows, err := f.f.owner.Query(f.f.ctx, `SELECT listener_id,provider_name FROM network_lb_components WHERE tenant_id=$1 AND lb_id=$2 AND kind='route' AND deleted_at IS NULL`, f.f.tenant, lb.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var id, name string
			if err = rows.Scan(&id, &name); err != nil {
				t.Fatal(err)
			}
			obj := f.api.Object("httproutes", namespace, name)
			if obj == nil {
				t.Fatal("missing listener route", id)
			}
			spec := obj["spec"].(map[string]any)
			section := spec["parentRefs"].([]any)[0].(map[string]any)["sectionName"]
			found := false
			for _, l := range lb.Listeners {
				if l.ID == id {
					found = section == l.Name
				}
			}
			if !found {
				t.Fatal("route lost listener binding", id, section)
			}
			count++
		}
		if rows.Err() != nil || count != want {
			t.Fatal("incomplete route set", count, want, rows.Err())
		}
	}
	checkRoutes(2)
	// A missing EndpointSlice port cannot stand for the whole configured LB.
	slice := f.api.Object("endpointslices", namespace, gateway+"-slice")
	allPorts := slice["ports"]
	slice["ports"] = slice["ports"].([]any)[:1]
	f.api.Change("endpointslices", slice, false)
	f.f.drive(t, func() bool {
		lb, err = f.lbs.Get(f.f.ctx, "", lb.ID)
		return err == nil && lb.ConfigurationState == "degraded"
	})
	if lb.AppliedVersion != 1 {
		t.Fatal("incomplete port observation advanced the applied version")
	}
	slice["ports"] = allPorts
	f.api.Change("endpointslices", slice, false)
	lb = lbState(t, f, lb.ID, biz.Available)
	// Losing one backend's Provider identity withdraws only its listener route.
	var firstRoute, secondRoute string
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT provider_name FROM network_lb_components WHERE tenant_id=$1 AND lb_id=$2 AND kind='route' AND listener_id=$3`, f.f.tenant, lb.ID, firstID).Scan(&firstRoute); err != nil {
		t.Fatal(err)
	}
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT provider_name FROM network_lb_components WHERE tenant_id=$1 AND lb_id=$2 AND kind='route' AND listener_id=$3`, f.f.tenant, lb.ID, secondID).Scan(&secondRoute); err != nil {
		t.Fatal(err)
	}
	ip := f.api.Object("vnicips", namespace, "lb-backend-ip")
	originalUID := ip["metadata"].(map[string]any)["uid"]
	ip["metadata"].(map[string]any)["uid"] = uuid.NewString()
	f.api.Change("vnicips", ip, false)
	f.f.drive(t, func() bool {
		lb, err = f.lbs.Get(f.f.ctx, "", lb.ID)
		firstObj, secondObj := f.api.Object("httproutes", namespace, firstRoute), f.api.Object("httproutes", namespace, secondRoute)
		if err != nil || firstObj == nil || secondObj == nil {
			return false
		}
		firstRule := firstObj["spec"].(map[string]any)["rules"].([]any)[0].(map[string]any)
		secondRule := secondObj["spec"].(map[string]any)["rules"].([]any)[0].(map[string]any)
		return lb.State == biz.Degraded && firstRule["backendRefs"] == nil && secondRule["backendRefs"] != nil
	})
	if lb.AppliedVersion != 1 {
		t.Fatal("backend identity loss changed applied version")
	}
	ip["metadata"].(map[string]any)["uid"] = originalUID
	f.api.Change("vnicips", ip, false)
	lb = lbState(t, f, lb.ID, biz.Available)
	// A masked metadata update omits the listener set and preserves its identity.
	metadata, err := f.lbs.Update(f.f.ctx, biz.UpdateLoadBalancer{ID: lb.ID, ExpectedVersion: lb.Version, IdempotencyKey: "rename-only", LoadBalancerMutableInput: biz.LoadBalancerMutableInput{Name: "renamed", UpdateMask: []string{"name"}}})
	if err != nil {
		t.Fatal(err)
	}
	f.f.drive(t, func() bool {
		lb, err = f.lbs.Get(f.f.ctx, "", lb.ID)
		return err == nil && lb.AppliedVersion == metadata.LoadBalancer.DesiredVersion && lb.ConfigurationState == "configured"
	})
	if len(lb.Listeners) != 2 || lb.Listeners[0].ID != firstID || lb.Listeners[1].ID != secondID || lb.Listeners[0].Port != 80 || lb.Listeners[1].Port != 8081 {
		t.Fatal("omitted listener set changed", lb.Listeners)
	}
	update := func(key string, listeners []biz.LoadBalancerListenerInput) {
		t.Helper()
		priorApplied := lb.AppliedVersion
		if key == "add-and-change" {
			c.mu.Lock()
			c.holdGenerated = true
			c.mu.Unlock()
		}
		accepted, err := f.lbs.Update(f.f.ctx, biz.UpdateLoadBalancer{ID: lb.ID, ExpectedVersion: lb.Version, IdempotencyKey: key, LoadBalancerMutableInput: biz.LoadBalancerMutableInput{Listeners: listeners, UpdateMask: []string{"listeners"}}})
		if err != nil {
			t.Fatal(err)
		}
		if key == "add-and-change" {
			f.f.drive(t, func() bool {
				obj := f.api.Object("gateways", namespace, gateway)
				return obj != nil && len(obj["spec"].(map[string]any)["listeners"].([]any)) == 3
			})
			lb, err = f.lbs.Get(f.f.ctx, "", lb.ID)
			if err != nil || lb.AppliedVersion != priorApplied {
				t.Fatal("missing generated Service ports advanced applied_version", lb, err)
			}
			c.mu.Lock()
			c.holdGenerated = false
			c.generated(f.api, f.api.Object("gateways", namespace, gateway), false)
			c.mu.Unlock()
		}
		f.f.drive(t, func() bool {
			lb, err = f.lbs.Get(f.f.ctx, "", lb.ID)
			return err == nil && lb.AppliedVersion == accepted.LoadBalancer.DesiredVersion && lb.ConfigurationState == "configured"
		})
		obj := f.api.Object("gateways", namespace, gateway)
		if obj == nil || obj["metadata"].(map[string]any)["uid"] != gatewayUID {
			t.Fatal("listener update replaced the Gateway")
		}
	}
	first := listener("first", 80, f.request.Backends[0])
	first.ID = firstID
	first.Backends[0].ID = lb.Listeners[0].Backends[0].ID
	changed := listener("second", 8082, second)
	changed.ID = secondID
	changed.Health.IntervalSeconds = port(7)
	third := listener("third", 8083, f.request.Backends[0])
	update("add-and-change", []biz.LoadBalancerListenerInput{first, changed, third})
	if len(lb.Listeners) != 3 || lb.Listeners[1].ID != secondID || lb.Listeners[1].Port != 8082 || lb.Listeners[1].Health.IntervalSeconds != 7 {
		t.Fatal("listener update lost identity or health", lb.Listeners)
	}
	checkRoutes(3)
	third.ID = lb.Listeners[2].ID
	update("remove-second", []biz.LoadBalancerListenerInput{first, third})
	if len(lb.Listeners) != 2 || lb.Listeners[0].ID != firstID {
		t.Fatal("removing one listener changed another", lb.Listeners)
	}
	checkRoutes(2)
	if _, err = f.lbs.Delete(f.f.ctx, "", lb.ID); err != nil {
		t.Fatal(err)
	}
	lbState(t, f, lb.ID, biz.Deleted)
	var active int
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT (SELECT count(*) FROM network_lb_subnet_refs WHERE tenant_id=$1 AND lb_id=$2 AND released_at IS NULL)+(SELECT count(*) FROM network_lb_vip_intents WHERE tenant_id=$1 AND lb_id=$2 AND released_at IS NULL)+(SELECT count(*) FROM network_lb_components WHERE tenant_id=$1 AND lb_id=$2 AND deleted_at IS NULL)`, f.f.tenant, lb.ID).Scan(&active); err != nil || active != 0 {
		t.Fatal("LB cleanup leaked occupancy or components", active, err)
	}
}
