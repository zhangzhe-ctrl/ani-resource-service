package data

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	imagebiz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image/sqlcgen"
)

type Postgres struct{ pool *pgxpool.Pool }

// A space write owns one session, not a transaction. Its sequential repository
// calls borrow that session for SQL and short transactions, so eight admitted
// writers never wait for a ninth connection from the same eight-slot pool.
type spaceWriteConnectionKey struct{}
type spaceWriteConnection struct {
	owner *Postgres
	conn  *pgxpool.Conn
}
type imageConnection interface {
	sqlcgen.DBTX
	Begin(context.Context) (pgx.Tx, error)
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func (p *Postgres) connection(ctx context.Context) imageConnection {
	if held, ok := ctx.Value(spaceWriteConnectionKey{}).(spaceWriteConnection); ok && held.owner == p {
		return held.conn
	}
	return p.pool
}

func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid Image runtime database configuration")
	}
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("Image database unavailable")
	}
	p := &Postgres{pool: pool}
	if err = p.CheckReady(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}
func (p *Postgres) Close() { p.pool.Close() }

// CheckReady uses infrastructure catalogs, not product queries. It fails on
// excessive inherited privileges; it never repairs shared deployment grants.
func (p *Postgres) CheckReady(ctx context.Context) error {
	var unsafe bool
	err := p.pool.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole
 OR has_database_privilege(current_user,current_database(),'CREATE')
 OR has_database_privilege(current_user,current_database(),'TEMP')
 OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND has_schema_privilege(current_user,n.oid,'CREATE'))
 OR EXISTS(SELECT 1 FROM pg_roles r WHERE (r.rolsuper OR r.rolbypassrls OR r.rolcreatedb OR r.rolcreaterole) AND pg_has_role(current_user,r.oid,'SET'))
 FROM pg_roles WHERE rolname=current_user`).Scan(&unsafe)
	if err != nil {
		return fmt.Errorf("Image runtime role inspection failed")
	}
	if unsafe {
		return fmt.Errorf("Image runtime role has administrative privileges")
	}
	var count, bad int
	err = p.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER(WHERE c.relrowsecurity OR c.relforcerowsecurity
 OR pg_has_role(current_user,c.relowner,'SET') OR NOT has_table_privilege(current_user,c.oid,'SELECT')
 OR has_table_privilege(current_user,c.oid,'DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR (c.relname='schema_version' AND has_table_privilege(current_user,c.oid,'INSERT,UPDATE'))
 OR (c.relname<>'schema_version' AND (NOT has_table_privilege(current_user,c.oid,'INSERT') OR NOT has_table_privilege(current_user,c.oid,'UPDATE'))))
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='image' AND c.relkind='r' AND c.relname=ANY($1::text[])`, imageTables).Scan(&count, &bad)
	if err != nil {
		return fmt.Errorf("Image table privilege inspection failed")
	}
	if count != len(imageTables) || bad != 0 {
		return fmt.Errorf("Image schema missing, RLS enabled, or unsafe table privileges")
	}
	err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname NOT IN ('image','pg_catalog','information_schema') AND n.nspname !~ '^pg_'
 AND c.relkind IN ('r','p','v','m','f') AND has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE'))`).Scan(&unsafe)
	if err != nil {
		return fmt.Errorf("Image foreign table privilege inspection failed")
	}
	if unsafe {
		return fmt.Errorf("Image runtime can access another domain's tables")
	}
	return VerifyImageMigrationChecksums(ctx, p.pool)
}

func databaseError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return imagebiz.Fail(imagebiz.ImageNotFound, "image not found")
	}
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch e.Code {
		case "23505":
			switch e.ConstraintName {
			case "image_registration_active_digest":
				return imagebiz.Fail(imagebiz.ImageAlreadyRegistered, "image already registered")
			case "image_command_single_external_write":
				return imagebiz.Fail(imagebiz.RequestInProgress, "space write in progress")
			case "image_space_one_per_tenant", "spaces_registry_authority_project_name_key", "image_space_harbor_id":
				return imagebiz.Fail(imagebiz.SpaceNameConflict, "image space conflict")
			default:
				return imagebiz.Fail(imagebiz.IdempotencyConflict, "operation conflicts with existing state")
			}
		case "23503", "23514", "22023", "22P02":
			return imagebiz.Fail(imagebiz.InvalidArgument, "invalid Image data relation or value")
		case "40001", "40P01":
			return imagebiz.Fail(imagebiz.RequestInProgress, "concurrent operation; retry same request")
		}
	}
	return imagebiz.Fail(imagebiz.DependencyUnavailable, "Image database unavailable")
}
