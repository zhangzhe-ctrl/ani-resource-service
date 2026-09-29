package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image/sqlcgen"
)

func decodeTyped(body []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return biz.Fail(biz.InternalError, "invalid stored Image snapshot")
	}
	if d.Decode(new(any)) != io.EOF {
		return biz.Fail(biz.InternalError, "invalid stored Image snapshot")
	}
	return nil
}
func fromCommand(v sqlcgen.ImageCommand) (biz.Command, error) {
	c := biz.Command{ID: v.CommandID, SpaceID: v.SpaceID, TenantID: textValue(v.TenantID), Scope: biz.ImageScope(v.OwnerScope), Key: v.IdempotencyKey, Kind: v.Kind, Actor: v.Actor, Fingerprint: v.Fingerprint, State: v.State, Phase: v.Phase, Reason: biz.Reason(v.Reason), Version: v.Version, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, CompletedAt: v.CompletedAt, ReplayUntil: v.SecretReplayUntil, DeliverySecret: biz.EncryptedSecret{KeyID: textValue(v.SecretKeyID), Ciphertext: v.SecretCiphertext}}
	if err := decodeTyped(v.Candidate, &c.Candidate); err != nil {
		return biz.Command{}, err
	}
	if err := decodeTyped(v.Result, &c.Result); err != nil {
		return biz.Command{}, err
	}
	return c, nil
}
func (p *Postgres) FindTenantCommand(ctx context.Context, tenant, space, key string) (biz.Command, error) {
	if _, err := biz.ParseTenant(tenant); err != nil {
		return biz.Command{}, err
	}
	row, err := sqlcgen.New(p.pool).GetTenantCommand(ctx, sqlcgen.GetTenantCommandParams{TenantID: &tenant, SpaceID: space, IdempotencyKey: key})
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	return fromCommand(row)
}
func reserveTenantCommand(ctx context.Context, q *sqlcgen.Queries, c biz.Command) (biz.Command, error) {
	if c.Scope != biz.TenantImages {
		return biz.Command{}, biz.Fail(biz.InvalidArgument, "tenant command required")
	}
	if _, err := biz.ParseTenant(c.TenantID); err != nil {
		return biz.Command{}, err
	}
	if _, err := biz.ParseIdempotencyKey(c.Key); err != nil {
		return biz.Command{}, err
	}
	row, err := q.GetTenantCommand(ctx, sqlcgen.GetTenantCommandParams{TenantID: &c.TenantID, SpaceID: c.SpaceID, IdempotencyKey: c.Key})
	if err == nil {
		if row.Kind != c.Kind || row.Actor != c.Actor || row.Fingerprint != c.Fingerprint {
			return biz.Command{}, biz.Fail(biz.IdempotencyConflict, "idempotency key belongs to another request")
		}
		return fromCommand(row)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return biz.Command{}, databaseError(err)
	}
	row, err = q.InsertTenantCommand(ctx, sqlcgen.InsertTenantCommandParams{CommandID: c.ID, SpaceID: c.SpaceID, TenantID: &c.TenantID, IdempotencyKey: c.Key, Kind: c.Kind, Actor: c.Actor, Fingerprint: c.Fingerprint})
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	return fromCommand(row)
}
func (p *Postgres) SaveTenantCommandPhase(ctx context.Context, c biz.Command) (biz.Command, error) {
	return saveTenantCommandPhase(ctx, sqlcgen.New(p.pool), c)
}
func saveTenantCommandPhase(ctx context.Context, q *sqlcgen.Queries, c biz.Command) (biz.Command, error) {
	if _, err := biz.ParseTenant(c.TenantID); err != nil {
		return biz.Command{}, err
	}
	candidate, err := json.Marshal(c.Candidate)
	if err != nil {
		return biz.Command{}, biz.Fail(biz.InternalError, "candidate encoding failed")
	}
	var key *string
	if c.DeliverySecret.KeyID != "" {
		key = &c.DeliverySecret.KeyID
	}
	row, err := q.UpdateTenantCommandPhase(ctx, sqlcgen.UpdateTenantCommandPhaseParams{TenantID: &c.TenantID, SpaceID: c.SpaceID, CommandID: c.ID, ExpectedVersion: c.Version, Phase: c.Phase, State: c.State, Reason: string(c.Reason), Candidate: candidate, SecretCiphertext: c.DeliverySecret.Ciphertext, SecretKeyID: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Command{}, biz.Fail(biz.VersionConflict, "command version changed")
	}
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	return fromCommand(row)
}
func completeTenantCommand(ctx context.Context, q *sqlcgen.Queries, c biz.Command) (biz.Command, error) {
	result, err := json.Marshal(c.Result)
	if err != nil {
		return biz.Command{}, biz.Fail(biz.InternalError, "command result encoding failed")
	}
	row, err := q.CompleteTenantCommand(ctx, sqlcgen.CompleteTenantCommandParams{TenantID: &c.TenantID, SpaceID: c.SpaceID, CommandID: c.ID, ExpectedVersion: c.Version, Result: result, SecretReplayUntil: c.ReplayUntil})
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.Command{}, biz.Fail(biz.VersionConflict, "command version changed")
	}
	if err != nil {
		return biz.Command{}, databaseError(err)
	}
	return fromCommand(row)
}
func (p *Postgres) CompleteTenantCommand(ctx context.Context, c biz.Command) (biz.Command, error) {
	if _, err := biz.ParseTenant(c.TenantID); err != nil {
		return biz.Command{}, err
	}
	return completeTenantCommand(ctx, sqlcgen.New(p.pool), c)
}

// WithSpaceWriteLock serializes bounded provider writes without holding a SQL
// transaction open across HTTP. Failed unlocks discard the dedicated connection.
func (p *Postgres) WithSpaceWriteLock(ctx context.Context, space string, fn func(context.Context) error) (err error) {
	if _, err = biz.ParseTenant(space); err != nil {
		return err
	} // space IDs use canonical nonzero UUIDs too.
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := p.pool.Acquire(lockCtx)
	if err != nil {
		return databaseError(err)
	}
	defer conn.Release()
	if _, err = conn.Exec(lockCtx, "SELECT pg_advisory_lock(hashtextextended('image.space:' || $1,0))", space); err != nil {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = conn.Hijack().Close(cleanup)
		return biz.Fail(biz.RequestInProgress, "space write lock unavailable")
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		var unlocked bool
		if e := conn.QueryRow(cleanup, "SELECT pg_advisory_unlock(hashtextextended('image.space:' || $1,0))", space).Scan(&unlocked); e != nil || !unlocked {
			_ = conn.Hijack().Close(cleanup)
			if err == nil {
				err = biz.Fail(biz.DependencyUnavailable, "space write lock lost")
			}
		}
	}()
	return fn(ctx)
}
