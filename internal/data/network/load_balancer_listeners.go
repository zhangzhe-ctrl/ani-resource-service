package data

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network/sqlcgen"
	"slices"
	"strings"
)

func loadLBListeners(ctx context.Context, q *sqlcgen.Queries, row sqlcgen.NetworkLoadBalancer, v *biz.LoadBalancer) error {
	rows, err := q.ListLBConfigurationListeners(ctx, sqlcgen.ListLBConfigurationListenersParams{TenantID: row.TenantID, LbID: row.LbID, ConfigVersion: row.DesiredVersion})
	if err != nil {
		return databaseFailure(err)
	}
	if len(rows) == 0 {
		return biz.Fail(biz.DependencyUnavailable, "load balancer has no accepted listeners")
	}
	for _, item := range rows {
		l, c := item.NetworkLbListener, item.NetworkLbConfigurationListener
		listener := biz.LoadBalancerListener{ID: l.ListenerID, Name: l.Name, Protocol: c.Protocol, Port: uint32(c.Port), Health: biz.LoadBalancerHealth{Port: uint32(c.HealthCheckPort), IntervalSeconds: uint32(c.IntervalSeconds), TimeoutSeconds: uint32(c.TimeoutSeconds), UnhealthyThreshold: uint32(c.UnhealthyThreshold), HealthyThreshold: uint32(c.HealthyThreshold)}}
		members, err := q.ListLBListenerMembers(ctx, sqlcgen.ListLBListenerMembersParams{TenantID: row.TenantID, LbID: row.LbID, ConfigVersion: row.DesiredVersion, ListenerID: l.ListenerID})
		if err != nil {
			return databaseFailure(err)
		}
		for _, member := range members {
			m := member.NetworkLbMember
			listener.Backends = append(listener.Backends, biz.LoadBalancerBackend{ID: m.MemberID, SubnetID: m.SubnetID, Address: m.Address, Port: uint32(m.Port), Weight: uint32(member.Weight), AttachmentID: m.AttachmentID, State: m.State, Reason: biz.Reason(m.Reason), ObservedAt: m.ObservedAt})
		}
		v.Listeners = append(v.Listeners, listener)
	}
	first := v.Listeners[0]
	v.Listener = biz.LoadBalancerListener{ID: first.ID, Port: first.Port}
	v.Health = first.Health
	return nil
}

// Parent VPC locking serializes this preparation with updates and Attachment
// writes. The caller captures the original fingerprint before deriving values.
func prepareLBListeners(ctx context.Context, q *sqlcgen.Queries, i *biz.LoadBalancerIntent, prior *sqlcgen.NetworkLoadBalancer) error {
	var current biz.LoadBalancer
	if prior != nil {
		var err error
		current, err = loadLB(ctx, q, *prior)
		if err != nil {
			return err
		}
		if len(i.UpdateMask) > 0 {
			if !slices.Contains(i.UpdateMask, "name") {
				i.Name = current.Name
			}
			if !slices.Contains(i.UpdateMask, "description") {
				i.Description = current.Description
			}
		}
	}
	if i.Listeners == nil {
		if len(i.UpdateMask) > 0 {
			i.Listeners = biz.EffectiveLoadBalancerListeners(current)
		} else {
			listener := biz.LoadBalancerListener{Name: "http", Protocol: "HTTP", Port: i.ListenerPort, Backends: i.Backends, Health: i.Health}
			if prior != nil {
				if len(current.Listeners) != 1 {
					return biz.Fail(biz.InvalidArgument, "legacy updates require exactly one listener")
				}
				listener.ID, listener.Name, listener.Port = current.Listeners[0].ID, current.Listeners[0].Name, current.Listeners[0].Port
			}
			i.Listeners = []biz.LoadBalancerListener{listener}
		}
	}
	registry := map[string]sqlcgen.NetworkLbListener{}
	if prior != nil {
		rows, err := q.ListLBListenerRegistry(ctx, sqlcgen.ListLBListenerRegistryParams{TenantID: prior.TenantID, LbID: prior.LbID})
		if err != nil {
			return databaseFailure(err)
		}
		for _, l := range rows {
			registry[l.Name] = l
		}
	}
	union := map[string]biz.LoadBalancerBackend{}
	for j := range i.Listeners {
		l := &i.Listeners[j]
		known, exists := registry[l.Name]
		if l.ID != "" && (!exists || known.ListenerID != l.ID || !slices.ContainsFunc(current.Listeners, func(c biz.LoadBalancerListener) bool { return c.ID == l.ID })) {
			return biz.Fail(biz.InvalidArgument, "retained listener identity and name must match the current LB")
		}
		if exists {
			l.ID = known.ListenerID
		}
		for _, m := range l.Backends {
			key := lbBackendKey(m)
			if old, exists := union[key]; exists {
				if old.ID != "" && m.ID != "" && old.ID != m.ID {
					return biz.Fail(biz.InvalidArgument, "shared backend identities must agree")
				}
				if old.ID != "" && m.ID == "" {
					continue
				}
			}
			union[key] = m
		}
	}
	i.Backends = nil
	for _, m := range union {
		i.Backends = append(i.Backends, m)
	}
	slices.SortFunc(i.Backends, func(a, b biz.LoadBalancerBackend) int {
		if lbBackendKey(a) < lbBackendKey(b) {
			return -1
		}
		if lbBackendKey(a) > lbBackendKey(b) {
			return 1
		}
		return 0
	})
	i.Health = i.Listeners[0].Health
	return nil
}

func lbBackendKey(m biz.LoadBalancerBackend) string {
	return fmt.Sprintf("%s/%s/%d", m.SubnetID, m.Address, m.Port)
}

func saveLBListeners(ctx context.Context, q *sqlcgen.Queries, row sqlcgen.NetworkLoadBalancer, listeners []biz.LoadBalancerListener, members map[string]string) error {
	registry, err := q.ListLBListenerRegistry(ctx, sqlcgen.ListLBListenerRegistryParams{TenantID: row.TenantID, LbID: row.LbID})
	if err != nil {
		return databaseFailure(err)
	}
	components, err := q.ListLBComponents(ctx, sqlcgen.ListLBComponentsParams{TenantID: row.TenantID, LbID: row.LbID})
	if err != nil {
		return databaseFailure(err)
	}
	for _, l := range listeners {
		known := slices.ContainsFunc(registry, func(r sqlcgen.NetworkLbListener) bool { return r.Name == l.Name })
		if !known {
			l.ID = uuid.NewString()
			if err = q.InsertLBListener(ctx, sqlcgen.InsertLBListenerParams{TenantID: row.TenantID, ClusterID: row.ClusterID, Namespace: row.Namespace, LbID: row.LbID, ListenerID: l.ID, Name: l.Name, Port: int32(l.Port)}); err != nil {
				return databaseFailure(err)
			}
		}
		h := l.Health
		if err = q.InsertLBConfigurationListener(ctx, sqlcgen.InsertLBConfigurationListenerParams{TenantID: row.TenantID, ClusterID: row.ClusterID, Namespace: row.Namespace, LbID: row.LbID, ConfigVersion: row.DesiredVersion, ListenerID: l.ID, Protocol: l.Protocol, Port: int32(l.Port), IntervalSeconds: int64(h.IntervalSeconds), TimeoutSeconds: int64(h.TimeoutSeconds), UnhealthyThreshold: int64(h.UnhealthyThreshold), HealthyThreshold: int64(h.HealthyThreshold), HealthCheckPort: int32(h.Port)}); err != nil {
			return databaseFailure(err)
		}
		for _, kind := range []string{"route", "policy"} {
			if !slices.ContainsFunc(components, func(c sqlcgen.NetworkLbComponent) bool {
				return c.Kind == kind && textValue(c.ListenerID) == l.ID && c.DeletedAt == nil
			}) {
				if err = insertLBListenerComponent(ctx, q, row, kind, nil, &l.ID); err != nil {
					return err
				}
			}
		}
		for _, m := range l.Backends {
			if err = q.InsertLBListenerMember(ctx, sqlcgen.InsertLBListenerMemberParams{TenantID: row.TenantID, LbID: row.LbID, ConfigVersion: row.DesiredVersion, ListenerID: l.ID, MemberID: members[lbBackendKey(m)], Weight: int32(m.Weight)}); err != nil {
				return databaseFailure(err)
			}
		}
	}
	return nil
}

func insertLBListenerComponent(ctx context.Context, q *sqlcgen.Queries, row sqlcgen.NetworkLoadBalancer, kind string, member, listener *string) error {
	id := uuid.NewString()
	return databaseFailure(q.InsertLBComponent(ctx, sqlcgen.InsertLBComponentParams{TenantID: row.TenantID, ClusterID: row.ClusterID, Namespace: row.Namespace, LbID: row.LbID, ComponentID: id, Kind: kind, MemberID: member, ListenerID: listener, ProviderName: "lb-" + kind + "-" + strings.ReplaceAll(id, "-", "")}))
}
