package data

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image/sqlcgen"
)

func credentialInfo(v sqlcgen.ImageCredential) biz.CredentialInfo {
	return biz.CredentialInfo{SpaceID: v.SpaceID, TenantID: textValue(v.TenantID), Scope: biz.ImageScope(v.OwnerScope), Purpose: v.Purpose, State: v.State, Generation: v.Generation, Version: v.Version, RobotID: v.RobotID.Int64, RobotName: textValue(v.RobotName), Username: textValue(v.Username), ExpiresAt: v.ExpiresAt, UpdatedAt: v.UpdatedAt}
}
func (p *Postgres) GetTenantPublisher(ctx context.Context, tenant, space string) (biz.CredentialInfo, error) {
	if _, err := biz.ParseTenant(tenant); err != nil {
		return biz.CredentialInfo{}, err
	}
	s, err := p.FindTenantSpace(ctx, tenant)
	if err != nil {
		return biz.CredentialInfo{}, err
	}
	if s.ID != space {
		return biz.CredentialInfo{}, biz.Fail(biz.SpaceNotFound, "image space not found")
	}
	row, err := sqlcgen.New(p.connection(ctx)).GetTenantPublisherCredential(ctx, sqlcgen.GetTenantPublisherCredentialParams{TenantID: &tenant, SpaceID: space})
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.CredentialInfo{TenantID: tenant, SpaceID: space, Scope: biz.TenantImages, Purpose: "publisher", State: "not_issued"}, nil
	}
	if err != nil {
		return biz.CredentialInfo{}, databaseError(err)
	}
	return credentialInfo(row), nil
}
func (p *Postgres) GetTenantPull(ctx context.Context, tenant, space string) (biz.StoredCredential, error) {
	if _, err := biz.ParseTenant(tenant); err != nil {
		return biz.StoredCredential{}, err
	}
	s, err := p.FindTenantSpace(ctx, tenant)
	if err != nil {
		return biz.StoredCredential{}, err
	}
	if s.ID != space {
		return biz.StoredCredential{}, biz.Fail(biz.SpaceNotFound, "image space not found")
	}
	row, err := sqlcgen.New(p.connection(ctx)).GetTenantPullCredential(ctx, sqlcgen.GetTenantPullCredentialParams{TenantID: &tenant, SpaceID: space})
	if err != nil {
		return biz.StoredCredential{}, databaseError(err)
	}
	return biz.StoredCredential{Info: credentialInfo(row), Secret: biz.EncryptedSecret{KeyID: textValue(row.SecretKeyID), Ciphertext: row.SecretCiphertext}, AAD: biz.SecretAAD{InstallationID: s.InstallationID, TenantID: tenant, SpaceID: space, Purpose: "pull", Scope: biz.TenantImages, Generation: row.Generation}}, nil
}

var _ biz.SpaceRepository = (*Postgres)(nil)
var _ biz.CatalogRepository = (*Postgres)(nil)
var _ biz.CredentialRepository = (*Postgres)(nil)
var _ biz.CommandRepository = (*Postgres)(nil)
