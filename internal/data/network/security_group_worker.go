package data

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	db "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network/sqlcgen"
)

func (p *Postgres) GetSecurityGroupOperation(ctx context.Context, tenant, id string) (biz.Operation, error) {
	b, e := p.queries.GetSecurityGroupOperation(ctx, db.GetSecurityGroupOperationParams{TenantID: tenant, OperationID: id})
	if e != nil {
		return biz.Operation{}, databaseFailure(e)
	}
	var v biz.Operation
	e = json.Unmarshal(b, &v)
	return v, databaseFailure(e)
}
func (p *Postgres) GetEIPSecurityGroups(ctx context.Context, tenant, id string) (biz.SecurityGroupBinding, error) {
	eip, e := p.GetEIP(ctx, tenant, id)
	if e != nil {
		return biz.SecurityGroupBinding{}, e
	}
	if eip.Scope != "public" || eip.ManagedBy != "tenant" || eip.State == biz.Deleting || eip.State == biz.Deleted {
		return biz.SecurityGroupBinding{}, biz.Fail(biz.ResourceNotFound, "tenant EIP not found")
	}
	r, e := p.queries.GetSecurityGroupTarget(ctx, db.GetSecurityGroupTargetParams{TenantID: tenant, TargetType: "eip", TargetID: id})
	if errors.Is(e, pgx.ErrNoRows) {
		return biz.SecurityGroupBinding{TenantID: tenant, TargetType: "eip", TargetID: id, Version: 1, State: "unbound", SecurityGroupIDs: []string{}, AppliedSecurityGroupIDs: []string{}}, nil
	}
	if e != nil {
		return biz.SecurityGroupBinding{}, databaseFailure(e)
	}
	return sgTarget(r)
}
func (p *Postgres) SetEIPSecurityGroups(ctx context.Context, i biz.SecurityGroupIntent, a biz.Attribution) (biz.SecurityGroupMutation, error) {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return biz.SecurityGroupMutation{}, databaseFailure(e)
	}
	defer tx.Rollback(ctx)
	q := p.queries.WithTx(tx)
	if e = sgTenantLock(ctx, q, i.TenantID); e != nil {
		return biz.SecurityGroupMutation{}, e
	}
	if v, found, e := sgReplay(ctx, q, i); e != nil || found {
		return v, e
	}
	// The EIP row lock serializes lifecycle acceptance and SG changes.
	row, e := q.LockEIP(ctx, db.LockEIPParams{TenantID: i.TenantID, EipID: i.ID})
	if e != nil {
		return biz.SecurityGroupMutation{}, databaseFailure(e)
	}
	if row.Scope != "public" || row.ManagedBy != "tenant" || row.State == "deleting" || row.State == "deleted" {
		return biz.SecurityGroupMutation{}, biz.Fail(biz.ResourceNotFound, "tenant EIP not found")
	}
	if e = sgNoActiveLease(ctx, q, i.TenantID, "eip", i.ID); e != nil {
		return biz.SecurityGroupMutation{}, e
	}
	target, e := q.LockSecurityGroupTarget(ctx, db.LockSecurityGroupTargetParams{TenantID: i.TenantID, TargetType: "eip", TargetID: i.ID})
	v := biz.SecurityGroupBinding{TenantID: i.TenantID, TargetType: "eip", TargetID: i.ID, Namespace: row.Namespace, Version: 1}
	if e == nil {
		v, e = sgTarget(target)
	} else if errors.Is(e, pgx.ErrNoRows) {
		e = nil
	}
	if e != nil {
		return biz.SecurityGroupMutation{}, databaseFailure(e)
	}
	if v.Version != i.ExpectedVersion {
		return biz.SecurityGroupMutation{}, biz.Fail(biz.VersionConflict, "EIP security group configuration changed")
	}
	if _, e = resolveSecurityGroups(ctx, q, i.TenantID, p.placement.ClusterID, row.Namespace, i.SecurityGroupIDs); e != nil {
		return biz.SecurityGroupMutation{}, e
	}
	now, e := q.DatabaseTime(ctx)
	if e != nil {
		return biz.SecurityGroupMutation{}, databaseFailure(e)
	}
	if e = sgSupersede(ctx, q, i.TenantID, v.LastOperationID, now); e != nil {
		return biz.SecurityGroupMutation{}, e
	}
	op := sgNewOperation(i.TenantID, i.ID, "set_eip_security_groups", "eip", now)
	v.Version++
	v.SecurityGroupIDs = slices.Clone(i.SecurityGroupIDs)
	v.State = "applying"
	v.Reason = ""
	v.LastOperationID = op.ID
	if e = sgSaveTarget(ctx, q, v); e != nil {
		return biz.SecurityGroupMutation{}, databaseFailure(e)
	}
	for _, id := range i.SecurityGroupIDs {
		if e = q.ReserveSecurityGroupReference(ctx, db.ReserveSecurityGroupReferenceParams{TenantID: i.TenantID, TargetType: "eip", TargetID: i.ID, SecurityGroupID: id}); e != nil {
			return biz.SecurityGroupMutation{}, databaseFailure(e)
		}
	}
	result := biz.SecurityGroupMutation{Binding: v, Operation: op}
	if e = sgAccept(ctx, q, i, result, a); e != nil {
		return result, databaseFailure(e)
	}
	if e = q.ScheduleSecurityGroup(ctx, db.ScheduleSecurityGroupParams{TenantID: i.TenantID, TargetType: "eip", TargetID: i.ID, RequestedVersion: v.Version}); e != nil {
		return result, databaseFailure(e)
	}
	return result, databaseFailure(tx.Commit(ctx))
}
func (p *Postgres) ClaimSecurityGroup(ctx context.Context, owner string, duration time.Duration) (biz.SecurityGroupWork, bool, error) {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return biz.SecurityGroupWork{}, false, databaseFailure(e)
	}
	defer tx.Rollback(ctx)
	q := p.queries.WithTx(tx)
	r, e := q.ClaimSecurityGroupTask(ctx)
	if errors.Is(e, pgx.ErrNoRows) {
		return biz.SecurityGroupWork{}, false, nil
	}
	if e != nil {
		return biz.SecurityGroupWork{}, false, databaseFailure(e)
	}
	task, e := q.LeaseSecurityGroupTask(ctx, db.LeaseSecurityGroupTaskParams{TenantID: r.TenantID, TargetType: r.TargetType, TargetID: r.TargetID, Owner: owner, LeaseMicros: duration.Microseconds()})
	if e != nil {
		return biz.SecurityGroupWork{}, false, databaseFailure(e)
	}
	w := biz.SecurityGroupWork{TenantID: r.TenantID, TargetType: r.TargetType, TargetID: r.TargetID, Version: task.RequestedVersion, Epoch: task.LeaseEpoch, Owner: owner}
	var op string
	if r.TargetType == "security_group" {
		row, e := q.GetSecurityGroupRow(ctx, db.GetSecurityGroupRowParams{TenantID: r.TenantID, SecurityGroupID: r.TargetID})
		if e != nil {
			return w, false, databaseFailure(e)
		}
		w.Group, e = sgRead(row)
		if e != nil {
			return w, false, e
		}
		op = w.Group.LastOperationID
	} else {
		row, e := q.GetSecurityGroupTarget(ctx, db.GetSecurityGroupTargetParams{TenantID: r.TenantID, TargetType: "eip", TargetID: r.TargetID})
		if e != nil {
			return w, false, databaseFailure(e)
		}
		w.Binding, e = sgTarget(row)
		if e != nil {
			return w, false, e
		}
		op = w.Binding.LastOperationID
	}
	body, e := q.GetSecurityGroupOperation(ctx, db.GetSecurityGroupOperationParams{TenantID: r.TenantID, OperationID: op})
	if e != nil {
		return w, false, databaseFailure(e)
	}
	if e = json.Unmarshal(body, &w.Operation); e != nil {
		return w, false, databaseFailure(e)
	}
	return w, true, databaseFailure(tx.Commit(ctx))
}
func (p *Postgres) FinishSecurityGroup(ctx context.Context, w biz.SecurityGroupWork, r biz.SecurityGroupResult, applyErr error) error {
	tx, e := p.pool.Begin(ctx)
	if e != nil {
		return databaseFailure(e)
	}
	defer tx.Rollback(ctx)
	q := p.queries.WithTx(tx)
	// Same lock order as admission: resource/target, then reconciliation.
	if w.TargetType == "security_group" {
		row, e := q.LockSecurityGroupRow(ctx, db.LockSecurityGroupRowParams{TenantID: w.TenantID, SecurityGroupID: w.TargetID})
		if e != nil {
			return databaseFailure(e)
		}
		if row.Version != w.Version {
			return biz.ErrLeaseLost
		}
	} else {
		row, e := q.LockSecurityGroupTarget(ctx, db.LockSecurityGroupTargetParams{TenantID: w.TenantID, TargetType: "eip", TargetID: w.TargetID})
		if e != nil {
			return databaseFailure(e)
		}
		if row.Version != w.Version {
			return biz.ErrLeaseLost
		}
	}
	now, e := q.DatabaseTime(ctx)
	if e != nil {
		return databaseFailure(e)
	}
	delay := 10 * time.Second
	task, e := q.LockSecurityGroupTask(ctx, db.LockSecurityGroupTaskParams{TenantID: w.TenantID, TargetType: w.TargetType, TargetID: w.TargetID})
	if e != nil {
		return databaseFailure(e)
	}
	if task.LeaseOwner == nil || *task.LeaseOwner != w.Owner || task.LeaseEpoch != w.Epoch || task.RequestedVersion != w.Version || task.LeaseUntil == nil || !task.LeaseUntil.After(now) {
		return biz.ErrLeaseLost
	}
	op := w.Operation
	op.UpdatedAt = now
	if applyErr != nil {
		r.State = "degraded"
		r.Reason = string(biz.ProviderUnavailable)
		var pe *biz.ProviderError
		if errors.As(applyErr, &pe) {
			switch pe.Kind {
			case biz.ProviderConflict:
				r.Reason = string(biz.ProviderOwnership)
			case biz.ProviderReject:
				r.Reason = string(biz.ProviderRejected)
			case biz.ProviderUncertain:
				r.Reason = string(biz.ProviderUnknown)
			case biz.ProviderInUse:
				r.Reason = string(biz.ResourceInUse)
			}
			if pe.Kind == biz.ProviderConflict || pe.Kind == biz.ProviderReject {
				r.State = "failed"
			}
		}
		op.Reason = biz.Reason(r.Reason)
		if op.CompletedAt == nil {
			op.State = biz.Retrying
		}
	}
	if r.Complete && op.CompletedAt == nil {
		op.State = biz.Succeeded
		op.Reason = ""
		op.CompletedAt = &now
	}
	if !r.Complete && op.CompletedAt == nil && applyErr == nil {
		op.State = biz.Running
	}
	if w.TargetType == "security_group" {
		v := w.Group
		if r.ProviderUID != "" {
			v.ProviderUID = r.ProviderUID
		}
		if r.State != "" {
			v.State = biz.ResourceState(r.State)
		}
		if v.State != biz.Deleted && w.Group.State == biz.Deleting {
			v.State = biz.Deleting
		}
		v.Reason = biz.Reason(r.Reason)
		v.UpdatedAt = now
		if applyErr == nil {
			v.ObservedAt = &now
		}
		if e = affected(q.UpdateSecurityGroupRow(ctx, db.UpdateSecurityGroupRowParams{TenantID: w.TenantID, SecurityGroupID: w.TargetID, ExpectedVersion: w.Version, NewVersion: w.Version, State: string(v.State), Payload: sgJSON(v)})); e != nil {
			return e
		}
	} else {
		v := w.Binding
		if r.State != "" {
			v.State = r.State
		}
		v.Reason = r.Reason
		if applyErr == nil {
			v.ObservedAt = &now
		}
		if r.Complete {
			v.AppliedSecurityGroupIDs = slices.Clone(r.AppliedIDs)
			if e = q.ReleaseSecurityGroupReferences(ctx, db.ReleaseSecurityGroupReferencesParams{TenantID: w.TenantID, TargetType: "eip", TargetID: w.TargetID, KeepIds: r.AppliedIDs}); e != nil {
				return databaseFailure(e)
			}
		}
		if e = sgSaveTarget(ctx, q, v); e != nil {
			return databaseFailure(e)
		}
	}
	if _, e = q.UpdateSecurityGroupOperation(ctx, db.UpdateSecurityGroupOperationParams{TenantID: w.TenantID, OperationID: op.ID, Payload: sgJSON(op)}); e != nil {
		return databaseFailure(e)
	}
	if r.State == "deleted" || r.State == "released" {
		delay = 24 * time.Hour
	}
	return affectedCommit(ctx, tx, q.FinishSecurityGroupTask, db.FinishSecurityGroupTaskParams{TenantID: w.TenantID, TargetType: w.TargetType, TargetID: w.TargetID, Version: w.Version, Owner: w.Owner, Epoch: w.Epoch, DelayMicros: delay.Microseconds()})
}
func affectedCommit(ctx context.Context, tx pgx.Tx, finish func(context.Context, db.FinishSecurityGroupTaskParams) (int64, error), args db.FinishSecurityGroupTaskParams) error {
	if e := affected(finish(ctx, args)); e != nil {
		return e
	}
	return databaseFailure(tx.Commit(ctx))
}
