package data

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image/sqlcgen"
)

func tenantCredential(ctx context.Context, q *sqlcgen.Queries, tenant, space, purpose string) (biz.CredentialInfo, error) {
	var row sqlcgen.ImageCredential
	var err error
	switch purpose {
	case "publisher":
		row, err = q.GetTenantPublisherCredential(ctx, sqlcgen.GetTenantPublisherCredentialParams{TenantID: &tenant, SpaceID: space})
	case "pull":
		row, err = q.GetTenantPullCredential(ctx, sqlcgen.GetTenantPullCredentialParams{TenantID: &tenant, SpaceID: space})
	default:
		return biz.CredentialInfo{}, biz.Fail(biz.InvalidArgument, "invalid credential purpose")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.CredentialInfo{TenantID: tenant, SpaceID: space, Scope: biz.TenantImages, Purpose: purpose, State: "not_issued"}, nil
	}
	if err != nil {
		return biz.CredentialInfo{}, databaseError(err)
	}
	return credentialInfo(row), nil
}
func (p *Postgres) BeginTenantCredentialCommand(ctx context.Context, c biz.Command, expected int64) (biz.Command, error) {
	if _, err := biz.ParseTenant(c.TenantID); err != nil {
		return biz.Command{}, err
	}
	if expected < 0 {
		return biz.Command{}, biz.Fail(biz.InvalidArgument, "invalid expected version")
	}
	tx, err := p.connection(ctx).Begin(ctx)
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	defer tx.Rollback(ctx)
	q := sqlcgen.New(tx)
	if err = q.LockTenantSpace(ctx, sqlcgen.LockTenantSpaceParams{TenantID: c.TenantID}); err != nil {
		return biz.Command{}, databaseError(err)
	}
	s, err := q.GetTenantSpace(ctx, sqlcgen.GetTenantSpaceParams{TenantID: &c.TenantID})
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	if s.SpaceID != c.SpaceID {
		return biz.Command{}, biz.Fail(biz.SpaceNotFound, "image space not found")
	}
	existing, err := q.GetTenantCommand(ctx, sqlcgen.GetTenantCommandParams{TenantID: &c.TenantID, SpaceID: c.SpaceID, IdempotencyKey: c.Key})
	if err == nil {
		if existing.Kind != c.Kind || existing.Actor != c.Actor || existing.Fingerprint != c.Fingerprint {
			return biz.Command{}, biz.Fail(biz.IdempotencyConflict, "idempotency key belongs to another request")
		}
		return fromCommand(existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return biz.Command{}, databaseError(err)
	}
	if s.State != "available" {
		return biz.Command{}, biz.Fail(biz.SpaceNotReady, "image space not ready")
	}
	info, err := tenantCredential(ctx, q, c.TenantID, c.SpaceID, "publisher")
	if err != nil {
		return biz.Command{}, err
	}
	if info.Version != expected {
		return biz.Command{}, biz.Fail(biz.VersionConflict, "credential version changed")
	}
	switch c.Kind {
	case "issue_publisher":
		if info.State == "active" {
			return biz.Command{}, biz.Fail(biz.CredentialAlreadyActive, "publisher already active")
		}
	case "reset_publisher":
		if info.Generation == 0 {
			return biz.Command{}, biz.Fail(biz.InvalidArgument, "publisher has not been issued")
		}
	case "disable_publisher":
		if info.Generation == 0 {
			return biz.Command{}, biz.Fail(biz.CredentialNotIssued, "publisher has not been issued")
		}
	default:
		return biz.Command{}, biz.Fail(biz.InvalidArgument, "invalid credential command")
	}
	stored, err := reserveTenantCommand(ctx, q, c)
	if err != nil {
		return biz.Command{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.Command{}, databaseError(err)
	}
	return stored, nil
}
func checkCandidateCurrent(ctx context.Context, q *sqlcgen.Queries, c biz.Command) (biz.CredentialInfo, error) {
	row, err := q.GetTenantCommand(ctx, sqlcgen.GetTenantCommandParams{TenantID: &c.TenantID, SpaceID: c.SpaceID, IdempotencyKey: c.Key})
	if err != nil {
		return biz.CredentialInfo{}, databaseError(err)
	}
	if row.CommandID != c.ID || row.Version != c.Version || row.State == "succeeded" || row.State == "failed" {
		return biz.CredentialInfo{}, biz.Fail(biz.VersionConflict, "command version changed")
	}
	info, err := tenantCredential(ctx, q, c.TenantID, c.SpaceID, c.Candidate.Purpose)
	if err != nil {
		return info, err
	}
	if info.Version != c.Candidate.CredentialVersion || info.Generation >= c.Candidate.Generation || info.RobotID != c.Candidate.PreviousRobotID {
		return info, biz.Fail(biz.VersionConflict, "credential generation changed")
	}
	return info, nil
}
func (p *Postgres) PrepareTenantCandidate(ctx context.Context, c biz.Command) (biz.Command, error) {
	if _, err := biz.ParseTenant(c.TenantID); err != nil {
		return biz.Command{}, err
	}
	tx, err := p.connection(ctx).Begin(ctx)
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	defer tx.Rollback(ctx)
	q := sqlcgen.New(tx)
	if err = q.LockTenantSpace(ctx, sqlcgen.LockTenantSpaceParams{TenantID: c.TenantID}); err != nil {
		return biz.Command{}, databaseError(err)
	}
	if _, err = checkCandidateCurrent(ctx, q, c); err != nil {
		return biz.Command{}, err
	}
	if c.Candidate.CredentialVersion == 0 {
		row, e := q.PutTenantCandidateState(ctx, sqlcgen.PutTenantCandidateStateParams{TenantID: &c.TenantID, SpaceID: c.SpaceID, Purpose: c.Candidate.Purpose})
		if e != nil {
			return biz.Command{}, databaseError(e)
		}
		c.Candidate.CredentialVersion = row.Version
	}
	stored, err := saveTenantCommandPhase(ctx, q, c)
	if err != nil {
		return biz.Command{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.Command{}, databaseError(err)
	}
	return stored, nil
}
func (p *Postgres) CompleteTenantEnable(ctx context.Context, s biz.Space, c biz.Command) (biz.Space, biz.Command, error) {
	tx, err := p.connection(ctx).Begin(ctx)
	if err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	defer tx.Rollback(ctx)
	q := sqlcgen.New(tx)
	if err = q.LockTenantSpace(ctx, sqlcgen.LockTenantSpaceParams{TenantID: s.TenantID}); err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	s, c, err = completeTenantEnable(ctx, q, s, c)
	if err != nil {
		return biz.Space{}, biz.Command{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	return s, c, nil
}
func completeTenantEnable(ctx context.Context, q *sqlcgen.Queries, s biz.Space, c biz.Command) (biz.Space, biz.Command, error) {
	if c.Kind != "enable_space" || c.TenantID != s.TenantID || c.SpaceID != s.ID {
		return biz.Space{}, biz.Command{}, biz.Fail(biz.InvalidArgument, "invalid enable completion")
	}
	row, err := q.SetTenantSpaceAvailable(ctx, sqlcgen.SetTenantSpaceAvailableParams{TenantID: &s.TenantID, SpaceID: s.ID, ExpectedVersion: s.Version})
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Space{}, biz.Command{}, biz.Fail(biz.VersionConflict, "space or pull credential changed")
	}
	if err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	s = fromSpace(row)
	pull, err := tenantCredential(ctx, q, s.TenantID, s.ID, "pull")
	if err != nil {
		return biz.Space{}, biz.Command{}, err
	}
	s.PullCredentialGeneration = pull.Generation
	c.Result = biz.CommandResult{Space: &s}
	c.ReplayUntil = nil
	c, err = completeTenantCommand(ctx, q, c)
	return s, c, err
}
func (p *Postgres) ActivateTenantCandidate(ctx context.Context, s biz.Space, c biz.Command, pullSecret biz.EncryptedSecret) (biz.Space, biz.Command, error) {
	if _, err := biz.ParseTenant(c.TenantID); err != nil {
		return biz.Space{}, biz.Command{}, err
	}
	if s.TenantID != c.TenantID || s.ID != c.SpaceID {
		return biz.Space{}, biz.Command{}, biz.Fail(biz.InvalidArgument, "invalid credential space")
	}
	tx, err := p.connection(ctx).Begin(ctx)
	if err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	defer tx.Rollback(ctx)
	q := sqlcgen.New(tx)
	if err = q.LockTenantSpace(ctx, sqlcgen.LockTenantSpaceParams{TenantID: c.TenantID}); err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	if _, err = checkCandidateCurrent(ctx, q, c); err != nil {
		return biz.Space{}, biz.Command{}, err
	}
	v := c.Candidate
	var key *string
	var ciphertext []byte
	if v.Purpose == "pull" {
		key = &pullSecret.KeyID
		ciphertext = pullSecret.Ciphertext
		if *key == "" || len(ciphertext) == 0 {
			return biz.Space{}, biz.Command{}, biz.Fail(biz.InternalError, "pull secret missing")
		}
	}
	row, err := q.ActivateTenantCredential(ctx, sqlcgen.ActivateTenantCredentialParams{TenantID: &c.TenantID, SpaceID: c.SpaceID, Purpose: v.Purpose, ExpectedVersion: v.CredentialVersion, Generation: v.Generation, RobotID: pgtype.Int8{Int64: v.RobotID, Valid: true}, RobotName: &v.RobotName, Username: &v.Username, ExpiresAt: v.ExpiresAt, NeverExpires: v.ExpiresAt == nil, SecretKeyID: key, SecretCiphertext: ciphertext})
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Space{}, biz.Command{}, biz.Fail(biz.VersionConflict, "credential version changed")
	}
	if err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	info := credentialInfo(row)
	if c.Kind == "enable_space" {
		s, c, err = completeTenantEnable(ctx, q, s, c)
	} else {
		c.Result = biz.CommandResult{Credential: &info}
		c, err = completeTenantCommand(ctx, q, c)
	}
	if err != nil {
		return biz.Space{}, biz.Command{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	return s, c, nil
}
func (p *Postgres) CompleteTenantDisable(ctx context.Context, c biz.Command) (biz.Command, error) {
	if c.Kind != "disable_publisher" {
		return biz.Command{}, biz.Fail(biz.InvalidArgument, "invalid disable command")
	}
	if _, err := biz.ParseTenant(c.TenantID); err != nil {
		return biz.Command{}, err
	}
	tx, err := p.connection(ctx).Begin(ctx)
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	defer tx.Rollback(ctx)
	q := sqlcgen.New(tx)
	if err = q.LockTenantSpace(ctx, sqlcgen.LockTenantSpaceParams{TenantID: c.TenantID}); err != nil {
		return biz.Command{}, databaseError(err)
	}
	info, err := tenantCredential(ctx, q, c.TenantID, c.SpaceID, "publisher")
	if err != nil {
		return biz.Command{}, err
	}
	if info.Version != c.Candidate.CredentialVersion || info.Generation != c.Candidate.Generation || info.RobotID != c.Candidate.RobotID {
		return biz.Command{}, biz.Fail(biz.VersionConflict, "credential changed")
	}
	if info.Generation == 0 {
		return biz.Command{}, biz.Fail(biz.CredentialNotIssued, "publisher has not been issued")
	}
	if info.State == "active" {
		row, e := q.DisableTenantCredential(ctx, sqlcgen.DisableTenantCredentialParams{TenantID: &c.TenantID, SpaceID: c.SpaceID, Purpose: "publisher", ExpectedVersion: info.Version, Generation: info.Generation})
		if e != nil {
			return biz.Command{}, databaseError(e)
		}
		info = credentialInfo(row)
	}
	c.Result = biz.CommandResult{Credential: &info}
	c.ReplayUntil = nil
	c, err = completeTenantCommand(ctx, q, c)
	if err != nil {
		return biz.Command{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.Command{}, databaseError(err)
	}
	return c, nil
}
func (p *Postgres) BlockTenantSpace(ctx context.Context, s biz.Space, reason biz.Reason) (biz.Space, error) {
	if _, err := biz.ParseTenant(s.TenantID); err != nil {
		return biz.Space{}, err
	}
	row, err := sqlcgen.New(p.connection(ctx)).SetTenantSpaceReason(ctx, sqlcgen.SetTenantSpaceReasonParams{TenantID: &s.TenantID, SpaceID: s.ID, ExpectedVersion: s.Version, State: "blocked", Reason: string(reason)})
	if err != nil {
		return biz.Space{}, databaseError(err)
	}
	return fromSpace(row), nil
}
func (p *Postgres) InspectSpaceForOperator(ctx context.Context, id string) (biz.Space, error) {
	if _, err := biz.ParseTenant(id); err != nil {
		return biz.Space{}, err
	}
	row, err := sqlcgen.New(p.connection(ctx)).InspectSpaceForOperator(ctx, sqlcgen.InspectSpaceForOperatorParams{SpaceID: id})
	if err != nil {
		return biz.Space{}, databaseError(err)
	}
	return fromSpace(row), nil
}
func (p *Postgres) OpenTenantExternalCommand(ctx context.Context, tenant, space string) (biz.Command, error) {
	if _, err := biz.ParseTenant(tenant); err != nil {
		return biz.Command{}, err
	}
	row, err := sqlcgen.New(p.connection(ctx)).GetOpenTenantExternalCommand(ctx, sqlcgen.GetOpenTenantExternalCommandParams{TenantID: &tenant, SpaceID: space})
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	return fromCommand(row)
}
func (p *Postgres) RecoverTenantProject(ctx context.Context, s biz.Space, c biz.Command, id int64, evidence string) (biz.Space, error) {
	if s.Scope != biz.TenantImages || s.TenantID != c.TenantID || s.ID != c.SpaceID || s.ProjectID != 0 || c.Kind != "enable_space" || c.Phase != "project_sent" || id <= 0 || len(evidence) != 64 {
		return biz.Space{}, biz.Fail(biz.InvalidArgument, "invalid project recovery")
	}
	tx, err := p.connection(ctx).Begin(ctx)
	if err != nil {
		return biz.Space{}, databaseError(err)
	}
	defer tx.Rollback(ctx)
	q := sqlcgen.New(tx)
	row, err := q.BindTenantProject(ctx, sqlcgen.BindTenantProjectParams{TenantID: &s.TenantID, SpaceID: s.ID, ExpectedVersion: s.Version, ProjectID: pgtype.Int8{Int64: id, Valid: true}})
	if err != nil {
		return biz.Space{}, databaseError(err)
	}
	c.Candidate.RecoveryEvidence = evidence
	c.Reason = ""
	c.State = "retryable"
	if _, err = saveTenantCommandPhase(ctx, q, c); err != nil {
		return biz.Space{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.Space{}, databaseError(err)
	}
	return fromSpace(row), nil
}
func (p *Postgres) PurgeExpiredDeliverySecrets(ctx context.Context, now time.Time) (int64, error) {
	n, err := sqlcgen.New(p.connection(ctx)).ScrubExpiredDeliverySecrets(ctx, sqlcgen.ScrubExpiredDeliverySecretsParams{NowAt: &now})
	return n, databaseError(err)
}

var _ biz.LifecycleRepository = (*Postgres)(nil)
