package data

import (
	"context"
	"slices"

	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network/sqlcgen"
)

func lbWork(row sqlcgen.NetworkLoadBalancer) biz.ResourceWork {
	op := ""
	if row.LastOperationID != nil {
		op = *row.LastOperationID
	}
	return biz.ResourceWork{ID: row.LbID, TenantID: row.TenantID, Kind: "load_balancer", VPCID: row.VpcID, State: biz.ResourceState(row.State), Reason: biz.Reason(row.Reason), Version: row.Version, UpdatedAt: row.UpdatedAt, ObservedAt: row.ObservedAt, LastOperationID: op}
}
func lockLBWork(ctx context.Context, q *sqlcgen.Queries, tenant, id string) (biz.ResourceWork, error) {
	r, err := q.GetLBInternal(ctx, sqlcgen.GetLBInternalParams{TenantID: tenant, LbID: id})
	if err != nil {
		return biz.ResourceWork{}, err
	}
	if _, err = q.LockVPC(ctx, sqlcgen.LockVPCParams{TenantID: tenant, VpcID: r.VpcID}); err != nil {
		return biz.ResourceWork{}, err
	}
	refs, err := q.ListLBSubnetRefs(ctx, sqlcgen.ListLBSubnetRefsParams{TenantID: tenant, LbID: id})
	if err != nil {
		return biz.ResourceWork{}, err
	}
	ids := []string{r.SubnetID}
	for _, ref := range refs {
		ids = append(ids, ref.SubnetID)
	}
	slices.Sort(ids)
	for _, subnet := range slices.Compact(ids) {
		if _, err = q.LockSubnet(ctx, sqlcgen.LockSubnetParams{TenantID: tenant, SubnetID: subnet}); err != nil {
			return biz.ResourceWork{}, err
		}
	}
	if r.PublicEipID != nil {
		if _, err = q.LockEIP(ctx, sqlcgen.LockEIPParams{TenantID: tenant, EipID: *r.PublicEipID}); err != nil {
			return biz.ResourceWork{}, err
		}
	}
	r, err = q.LockLB(ctx, sqlcgen.LockLBParams{TenantID: tenant, LbID: id})
	return lbWork(r), err
}
func hydrateLBWork(ctx context.Context, q *sqlcgen.Queries, work *biz.ResourceWork, b sqlcgen.NetworkProviderBinding) error {
	r, err := q.GetLBInternal(ctx, sqlcgen.GetLBInternalParams{TenantID: work.TenantID, LbID: work.ID})
	if err != nil {
		return err
	}
	v, err := loadLB(ctx, q, r)
	if err != nil {
		return err
	}
	s := &biz.LoadBalancerWork{LoadBalancer: v, VIPOccupiedRevision: r.VipOccupiedRevision, VIPAbsenceRevision: r.VipAbsenceRevision}
	s.Components = append(s.Components, biz.LoadBalancerComponent{ID: b.BindingID, Kind: "gateway", Name: b.ProviderName, Identity: b.ProviderUid, PendingAction: b.PendingAction, CreateDispatched: b.CreateDispatched, TargetVersion: v.DesiredVersion})
	cs, err := q.ListLBComponents(ctx, sqlcgen.ListLBComponentsParams{TenantID: r.TenantID, LbID: r.LbID})
	if err != nil {
		return err
	}
	registry, err := q.ListLBListenerRegistry(ctx, sqlcgen.ListLBListenerRegistryParams{TenantID: r.TenantID, LbID: r.LbID})
	if err != nil {
		return err
	}
	listenerNames := map[string]string{}
	for _, l := range registry {
		listenerNames[l.ListenerID] = l.Name
	}
	for _, c := range cs {
		s.Components = append(s.Components, biz.LoadBalancerComponent{ID: c.ComponentID, Kind: c.Kind, MemberID: textValue(c.MemberID), ListenerID: textValue(c.ListenerID), ListenerName: listenerNames[textValue(c.ListenerID)], Name: c.ProviderName, Identity: c.ProviderUid, PendingAction: c.PendingAction, CreateDispatched: c.CreateDispatched, TargetVersion: c.TargetVersion, AppliedVersion: c.AppliedVersion, Deleted: c.DeletedAt != nil})
	}
	ms, err := q.ListLBMemberIdentities(ctx, sqlcgen.ListLBMemberIdentitiesParams{TenantID: r.TenantID, LbID: r.LbID})
	if err != nil {
		return err
	}
	for _, item := range ms {
		m := item.NetworkLbMember
		s.Members = append(s.Members, biz.LoadBalancerMemberIdentity{LoadBalancerBackend: biz.LoadBalancerBackend{ID: m.MemberID, SubnetID: m.SubnetID, AttachmentID: m.AttachmentID, Address: m.Address, Port: uint32(m.Port)}, PodName: item.PodName, PodUID: m.PodUid, VNicName: m.VnicName, VNicUID: m.VnicUid, VNicIPName: m.VnicipName, VNicIPUID: m.VnicipUid})
	}
	work.LoadBalancer = s
	return nil
}
func (p *Postgres) BeginLoadBalancerMutation(ctx context.Context, w biz.Work, c biz.LoadBalancerComponent, action, identity string) error {
	if w.Resource.LoadBalancer == nil {
		return biz.ErrLeaseLost
	}
	if c.Kind == "gateway" {
		return p.BeginMutation(ctx, w, action, identity)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return databaseFailure(err)
	}
	defer tx.Rollback(ctx)
	q := p.queries.WithTx(tx)
	if err = lockedWork(ctx, q, w); err != nil {
		return err
	}
	if (w.Resource.State == biz.Deleting || w.Resource.State == biz.Deleted) && action != "delete" {
		return biz.ErrLeaseLost
	}
	if err = affected(q.BeginLBComponentMutation(ctx, sqlcgen.BeginLBComponentMutationParams{TenantID: w.Resource.TenantID, LbID: w.Resource.ID, ComponentID: c.ID, Action: action, Identity: identity, TargetVersion: w.Resource.LoadBalancer.LoadBalancer.DesiredVersion})); err != nil {
		return err
	}
	return databaseFailure(tx.Commit(ctx))
}
func (p *Postgres) finishLB(ctx context.Context, q *sqlcgen.Queries, w biz.Work, pv biz.Progress) error {
	if w.Resource.LoadBalancer == nil || pv.LoadBalancer == nil || !pv.Observed {
		return nil
	}
	o := pv.LoadBalancer
	lb := w.Resource.LoadBalancer.LoadBalancer
	for _, c := range o.Components {
		if c.ID == w.BindingID {
			continue
		}
		identity := c.Identity
		if identity == "" {
			for _, known := range w.Resource.LoadBalancer.Components {
				if known.ID == c.ID {
					identity = known.Identity
				}
			}
		}
		if err := affected(q.SaveLBComponentObservation(ctx, sqlcgen.SaveLBComponentObservationParams{TenantID: w.Resource.TenantID, LbID: w.Resource.ID, ComponentID: c.ID, Identity: identity, ClearPending: c.ClearPending, Deleted: c.Deleted, AppliedVersion: c.AppliedVersion})); err != nil {
			return err
		}
	}
	for _, m := range o.Members {
		if err := affected(q.SaveLBMemberObservation(ctx, sqlcgen.SaveLBMemberObservationParams{TenantID: w.Resource.TenantID, LbID: w.Resource.ID, MemberID: m.ID, Eligible: m.Eligible, Reason: string(m.Reason), ObservedAt: &o.Proof.CollectedAt})); err != nil {
			return err
		}
	}
	for _, g := range o.Generated {
		if err := affected(q.SaveLBGenerated(ctx, sqlcgen.SaveLBGeneratedParams{TenantID: w.Resource.TenantID, ClusterID: p.placement.ClusterID, Namespace: g.Namespace, LbID: w.Resource.ID, Kind: g.Kind, ProviderName: g.Name, ProviderUid: g.Identity, GatewayUid: g.GatewayIdentity, ObservedAt: o.Proof.CollectedAt, Released: g.Released})); err != nil {
			return err
		}
	}
	if o.GeneratedReady {
		if err := q.BoundLBClaim(ctx, sqlcgen.BoundLBClaimParams{TenantID: w.Resource.TenantID, LbID: &w.Resource.ID}); err != nil {
			return err
		}
	}
	if pv.State == biz.Deleted {
		if !o.GeneratedReleased || !o.AddressReleased {
			return biz.ErrLeaseLost
		}
		if err := q.ReleaseLBClaim(ctx, sqlcgen.ReleaseLBClaimParams{TenantID: w.Resource.TenantID, LbID: &w.Resource.ID}); err != nil {
			return err
		}
		if err := q.ReleaseLBVIP(ctx, sqlcgen.ReleaseLBVIPParams{TenantID: w.Resource.TenantID, LbID: w.Resource.ID}); err != nil {
			return err
		}
	}
	return q.ReleaseLBSubnetRefs(ctx, sqlcgen.ReleaseLBSubnetRefsParams{TenantID: w.Resource.TenantID, LbID: w.Resource.ID, Deleted: pv.State == biz.Deleted, EntrySubnet: lb.SubnetID, DesiredVersion: lb.DesiredVersion})
}
