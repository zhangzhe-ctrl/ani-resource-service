package data

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image/sqlcgen"
)

func textValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func fromSpace(v sqlcgen.ImageSpace) biz.Space {
	return biz.Space{ID: v.SpaceID, TenantID: textValue(v.TenantID), Scope: biz.ImageScope(v.OwnerScope), InstallationID: v.InstallationID, RegistryAuthority: v.RegistryAuthority, ProjectName: v.ProjectName, ProjectID: v.HarborProjectID.Int64, State: v.State, Reason: biz.Reason(v.Reason), Version: v.Version, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func (p *Postgres) FindTenantSpace(ctx context.Context, tenant string) (biz.Space, error) {
	if _, err := biz.ParseTenant(tenant); err != nil {
		return biz.Space{}, err
	}
	v, err := sqlcgen.New(p.pool).GetTenantSpace(ctx, sqlcgen.GetTenantSpaceParams{TenantID: &tenant})
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Space{}, biz.Fail(biz.SpaceNotFound, "image space not found")
	}
	if err != nil {
		return biz.Space{}, databaseError(err)
	}
	return fromSpace(v), nil
}
func (p *Postgres) FindPlatformSpace(ctx context.Context) (biz.Space, error) {
	v, err := sqlcgen.New(p.pool).GetPlatformSpace(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Space{}, biz.Fail(biz.SpaceNotFound, "platform image space not found")
	}
	if err != nil {
		return biz.Space{}, databaseError(err)
	}
	return fromSpace(v), nil
}

func (p *Postgres) ReserveTenantSpace(ctx context.Context, s biz.Space, c biz.Command) (biz.Space, biz.Command, error) {
	if _, err := biz.ParseTenant(s.TenantID); err != nil {
		return biz.Space{}, biz.Command{}, err
	}
	if s.Scope != biz.TenantImages || c.Scope != biz.TenantImages || s.TenantID != c.TenantID || c.Kind != "enable_space" {
		return biz.Space{}, biz.Command{}, biz.Fail(biz.InvalidArgument, "invalid space reservation")
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	defer tx.Rollback(ctx)
	q := sqlcgen.New(tx)
	if err = q.LockTenantSpace(ctx, sqlcgen.LockTenantSpaceParams{TenantID: s.TenantID}); err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	row, err := q.GetTenantSpace(ctx, sqlcgen.GetTenantSpaceParams{TenantID: &s.TenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = q.ReserveTenantSpace(ctx, sqlcgen.ReserveTenantSpaceParams{SpaceID: s.ID, TenantID: &s.TenantID, InstallationID: s.InstallationID, RegistryAuthority: s.RegistryAuthority, ProjectName: s.ProjectName})
	}
	if err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	if row.ProjectName != s.ProjectName || row.RegistryAuthority != s.RegistryAuthority || row.InstallationID != s.InstallationID {
		return biz.Space{}, biz.Command{}, biz.Fail(biz.SpaceNameImmutable, "image space binding is immutable")
	}
	s = fromSpace(row)
	c.SpaceID = s.ID
	stored, err := reserveTenantCommand(ctx, q, c)
	if err != nil {
		return biz.Space{}, biz.Command{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return biz.Space{}, biz.Command{}, databaseError(err)
	}
	return s, stored, nil
}

func (p *Postgres) BindTenantProject(ctx context.Context, s biz.Space, projectID int64) (biz.Space, error) {
	if _, err := biz.ParseTenant(s.TenantID); err != nil {
		return biz.Space{}, err
	}
	if projectID <= 0 {
		return biz.Space{}, biz.Fail(biz.InvalidArgument, "invalid project ID")
	}
	row, err := sqlcgen.New(p.pool).BindTenantProject(ctx, sqlcgen.BindTenantProjectParams{ProjectID: pgtype.Int8{Int64: projectID, Valid: true}, TenantID: &s.TenantID, SpaceID: s.ID, ExpectedVersion: s.Version})
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Space{}, biz.Fail(biz.VersionConflict, "image space version changed")
	}
	if err != nil {
		return biz.Space{}, databaseError(err)
	}
	return fromSpace(row), nil
}
