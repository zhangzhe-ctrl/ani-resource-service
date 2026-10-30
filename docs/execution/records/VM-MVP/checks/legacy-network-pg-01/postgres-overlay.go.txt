// Package testenv creates only task-owned PostgreSQL fixtures. It is imported by
// tests, never by normal runtime code.
package testenv

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
)

type Database struct {
	Repository                         *data.Postgres
	Owner                              *pgxpool.Pool
	RuntimeDSN, OwnerRole, RuntimeRole string
}

func NewDatabase(t *testing.T, beforeMigrate ...func(*pgxpool.Pool, string)) *Database {
	t.Helper()
	dsn := os.Getenv("NETWORK_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("requires scripts/integration's isolated PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	dbName := "network_test_" + suffix
	ownerRole := "network_owner_" + suffix
	runtimeRole := "network_runtime_" + suffix
	address, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test database address")
	}
	password, _ := address.User.Password()
	passwordLiteral := "'" + strings.ReplaceAll(password, "'", "''") + "'"
	for _, role := range []string{ownerRole, runtimeRole} {
		if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD "+passwordLiteral); err != nil {
			t.Fatal(err)
		}
		var roleOID uint32
		if err := admin.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&roleOID); err != nil {
			t.Fatal(err)
		}
		t.Logf("PG_FIXTURE_ROLE created name=%s oid=%d", role, roleOID)
		t.Cleanup(func() {
			var currentOID uint32
			if err := admin.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&currentOID); err != nil || currentOID != roleOID {
				t.Errorf("fixture role identity changed: %s", role)
				return
			}
			if _, err := admin.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error(err)
				return
			}
			var remaining int
			if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1 OR oid=$2", role, roleOID).Scan(&remaining); err != nil || remaining != 0 {
				t.Errorf("fixture role absence not confirmed: %s", role)
				return
			}
			t.Logf("PG_FIXTURE_ROLE dropped name=%s oid=%d", role, roleOID)
		})
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()+" OWNER "+pgx.Identifier{ownerRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var databaseOID, ownerOID uint32
	if err := admin.QueryRow(ctx, "SELECT d.oid,d.datdba FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=$1 AND r.rolname=$2", dbName, ownerRole).Scan(&databaseOID, &ownerOID); err != nil {
		t.Fatal(err)
	}
	t.Logf("PG_FIXTURE_DATABASE created name=%s oid=%d owner=%s owner_oid=%d", dbName, databaseOID, ownerRole, ownerOID)
	t.Cleanup(func() {
		var currentOID, currentOwnerOID uint32
		if err := admin.QueryRow(ctx, "SELECT oid,datdba FROM pg_database WHERE datname=$1", dbName).Scan(&currentOID, &currentOwnerOID); err != nil || currentOID != databaseOID || currentOwnerOID != ownerOID {
			t.Errorf("fixture database identity changed: %s", dbName)
			return
		}
		var connections int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datid=$1", databaseOID).Scan(&connections); err != nil || connections != 0 {
			t.Errorf("fixture database still has connections: %s", dbName)
			return
		}
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
			t.Error(err)
			return
		}
		var remaining int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname=$1 OR oid=$2", dbName, databaseOID).Scan(&remaining); err != nil || remaining != 0 {
			t.Errorf("fixture database absence not confirmed: %s", dbName)
			return
		}
		t.Logf("PG_FIXTURE_DATABASE dropped name=%s oid=%d", dbName, databaseOID)
	})
	address.Path = "/" + dbName
	address.User = url.UserPassword(ownerRole, password)
	owner, err := pgxpool.New(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	for _, prepare := range beforeMigrate {
		prepare(owner, runtimeRole)
	}
	if err := data.Migrate(ctx, owner, runtimeRole); err != nil {
		t.Fatal(err)
	}
	// Migration replay and grant repair have a dedicated integration test;
	// ordinary fixtures need only the real initial migration.
	address.User = url.UserPassword(runtimeRole, password)
	repository, err := data.OpenPostgres(ctx, address.String(), data.Placement{ClusterID: "test-cluster", NamespacePrefix: "tenant-"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repository.Close)
	return &Database{Repository: repository, Owner: owner, RuntimeDSN: address.String(), OwnerRole: ownerRole, RuntimeRole: runtimeRole}
}
