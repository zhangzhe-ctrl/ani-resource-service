//go:build instanceintegration

package instance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	history "github.com/zhangzhe-ctrl/ani-resource-service/migrations/instance"
)

// These tests initialize historical upgrade fixtures with SQL, never mutate a
// running business flow into success. Every fixture has the exact embedded
// 001-007 DDL and original checksums, then executes the original immutable 008 migration. These historical SQL tests
// do not claim the current unversioned application reads old instances.
var restartHistoryFiles = []string{
	"001_instance.sql", "002_compute_presets.sql", "003_whole_gpu_quantity.sql",
	"004_gpu_execution.sql", "005_instance_configuration.sql", "006_instance_network_submissions.sql", "007_owner_cleanup_release.sql",
}

type restartMigrationFixture struct {
	owner                      *pgxpool.Pool
	ownerDSN, runtimeDSN, role string
}

func restartHistoricalDatabase(t *testing.T) restartMigrationFixture {
	t.Helper()
	ctx := context.Background()
	raw, err := os.ReadFile(os.Getenv("INSTANCE_TEST_ADMIN_DSN_FILE"))
	if err != nil {
		t.Fatal("exclusive PostgreSQL admin DSN file required")
	}
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	clear(raw)
	if err != nil {
		t.Fatal("invalid private fixture DSN")
	}
	admin, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("fixture PostgreSQL admin unavailable")
	}
	t.Cleanup(admin.Close)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	databaseName, ownerName, runtimeName := "restart_migration_"+suffix, "restart_owner_"+suffix, "restart_runtime_"+suffix
	password, _ := u.User.Password()
	literal := "'" + strings.ReplaceAll(password, "'", "''") + "'"
	for _, role := range []string{ownerName, runtimeName} {
		if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE PASSWORD "+literal); err != nil {
			t.Fatal("exclusive fixture role creation failed")
		}
		role := role
		t.Cleanup(func() {
			if _, err := admin.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("exclusive fixture role cleanup failed")
			}
		})
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" OWNER "+pgx.Identifier{ownerName}.Sanitize()); err != nil {
		t.Fatal("exclusive fixture database creation failed")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("exclusive fixture database cleanup failed")
		}
	})
	u.Path = "/" + databaseName
	u.User = url.UserPassword(ownerName, password)
	ownerDSN := u.String()
	owner, err := pgxpool.New(ctx, ownerDSN)
	if err != nil {
		t.Fatal("fixture owner unavailable")
	}
	t.Cleanup(owner.Close)
	for index, name := range restartHistoryFiles {
		body, err := history.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(ctx, string(body)); err != nil {
			t.Fatalf("historical DDL %s: %v", name, err)
		}
		digest := sha256.Sum256(body)
		if _, err := owner.Exec(ctx, `INSERT INTO instance.schema_version(version,checksum) VALUES($1,$2)`, index+1, hex.EncodeToString(digest[:])); err != nil {
			t.Fatal(err)
		}
	}
	u.User = url.UserPassword(runtimeName, password)
	return restartMigrationFixture{owner: owner, ownerDSN: ownerDSN, runtimeDSN: u.String(), role: runtimeName}
}

func historicalRestartInstance(c biz.Create, desired string, mounts []biz.VolumeMount) biz.Instance {
	c.Spec.VolumeMounts = mounts
	return biz.Instance{TenantID: c.TenantID, ID: c.ID, CreateOperationID: c.OperationID, CreateRequestHash: c.RequestHash, BusinessDigest: c.BusinessDigest,
		Spec: c.Spec, Version: 7, Desired: desired, Observed: desired, LastOperationID: c.OperationID,
		NotebookUID: "historical-notebook-uid", NotebookRV: "19", StatefulSetUID: "historical-sts-uid", PVCUID: "historical-workspace-uid",
		VolumeReferencesReleased: len(mounts) == 0, ExternalComputeStarted: true}
}

func insertHistoricalRestartInstance(t *testing.T, f restartMigrationFixture, i biz.Instance, references []biz.VolumeMount, referenceState string, deleteOperation string, withWork bool) {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	var historical map[string]any
	if err := json.Unmarshal(raw, &historical); err != nil {
		t.Fatal(err)
	}
	historical["Spec"].(map[string]any)["ContractVersion"] = 2
	delete(historical, "TrackedPods") // This field did not exist before 008.
	if i.Pending != nil {
		// Preserve exactly the legacy five fields: no guessed stopped target,
		// phase, mutation marker, or stopped payload is introduced by this fixture.
		historical["Pending"] = map[string]any{"Kind": i.Pending.Kind, "OperationID": i.Pending.OperationID, "Version": i.Pending.Version, "TargetUID": i.Pending.TargetUID, "ResourceVersion": i.Pending.ResourceVersion}
	}
	raw, _ = json.Marshal(historical)
	if _, err := f.owner.Exec(ctx, `INSERT INTO instance.instances(tenant_id,instance_id,create_operation_id,create_request_hash,version,desired,snapshot,instance_contract_version) VALUES($1,$2,$3,$4,$5,$6,$7,2)`, i.TenantID, i.ID, i.CreateOperationID, i.CreateRequestHash, i.Version, i.Desired, raw); err != nil {
		t.Fatal(err)
	}
	insertOperation := func(id, action string, version int64, state string) {
		t.Helper()
		ack, _ := json.Marshal(biz.ACK{OperationID: id, ResourceID: i.ID, Accepted: true})
		if _, err := f.owner.Exec(ctx, `INSERT INTO instance.instance_operations(tenant_id,operation_id,instance_id,action,idempotency_key,request_hash,version,ack,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, i.TenantID, id, i.ID, action, id, strings.Repeat("e", 64), version, ack, state); err != nil {
			t.Fatal(err)
		}
	}
	if i.CreateRequestHash != "" {
		insertOperation(i.CreateOperationID, "create", 1, "completed")
	}
	if i.Pending != nil && i.Pending.OperationID != i.CreateOperationID && i.Pending.OperationID != deleteOperation {
		insertOperation(i.Pending.OperationID, "start", i.Pending.Version, "superseded")
	}
	if deleteOperation != "" {
		state := "accepted"
		if i.ComputeCleaned && i.WorkspaceCleaned && i.VolumeReferencesReleased {
			state = "completed"
		}
		insertOperation(deleteOperation, "delete", i.Version, state)
	}
	if i.Spec.Namespace != "" && i.Spec.PVCName != "" {
		cleanup := "pending"
		if i.WorkspaceCleaned {
			cleanup = "cleaned"
		}
		if _, err := f.owner.Exec(ctx, `INSERT INTO instance.workspace_bindings(tenant_id,instance_id,namespace,pvc_name,pvc_uid,cleanup_state) VALUES($1,$2,$3,$4,$5,$6)`, i.TenantID, i.ID, i.Spec.Namespace, i.Spec.PVCName, i.PVCUID, cleanup); err != nil {
			t.Fatal(err)
		}
	}
	for _, mount := range references {
		if _, err := f.owner.Exec(ctx, `INSERT INTO instance.borrowed_volume_references(tenant_id,instance_id,mount_path,cluster_id,namespace,pvc_name,pvc_uid,read_only,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, i.TenantID, i.ID, mount.MountPath, mount.ClusterID, mount.Namespace, mount.PVCName, mount.PVCUID, mount.ReadOnly, referenceState); err != nil {
			t.Fatal(err)
		}
	}
	if withWork {
		if _, err := f.owner.Exec(ctx, `INSERT INTO instance.instance_work(tenant_id,instance_id) VALUES($1,$2)`, i.TenantID, i.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func migrationRestartFrozenMount(c biz.Create, name, path string, readonly bool) biz.VolumeMount {
	return biz.VolumeMount{ClusterID: c.Spec.ClusterID, Namespace: c.Spec.Namespace, PVCName: name, PVCUID: uuid.NewString(), MountPath: path, ReadOnly: readonly, Ownership: "borrowed"}
}

func TestRestartMigrationPGFrozenMountsAndExplicitEmpty(t *testing.T) {
	f := restartHistoricalDatabase(t)
	ctx := context.Background()
	c := cpuCreate()
	mounts := []biz.VolumeMount{migrationRestartFrozenMount(c, "second-name", "/second", true), migrationRestartFrozenMount(c, "first-name", "/first", false)}
	i := historicalRestartInstance(c, biz.Stopped, mounts)
	insertHistoricalRestartInstance(t, f, i, mounts, "active", "", false)
	var beforeSnapshot string
	if err := f.owner.QueryRow(ctx, `SELECT snapshot::text FROM instance.instances WHERE tenant_id=$1 AND instance_id=$2`, i.TenantID, i.ID).Scan(&beforeSnapshot); err != nil {
		t.Fatal(err)
	}
	empty := []biz.Instance{}
	for _, original := range [][]biz.VolumeMount{nil, {}} {
		row := historicalRestartInstance(cpuCreate(), biz.Stopped, original)
		insertHistoricalRestartInstance(t, f, row, nil, "active", "", false)
		empty = append(empty, row)
	}
	if err := migrateHistoricalRestart(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := migrateHistoricalRestart(ctx, f); err != nil {
		t.Fatal("explicit migration replay", err)
	}
	got, err := readHistoricalRestart(ctx, f, i.TenantID, i.ID)
	if err != nil || !got.MountsInitialized || got.AppliedMountVersion != 0 || got.RuntimeDesired != biz.Stopped || got.Version != i.Version || len(got.CurrentMounts) != len(mounts) {
		t.Fatal("historical current configuration did not migrate unverified", got, err)
	}
	for index, expected := range mounts {
		expected.ReferenceGeneration = 1
		if got.CurrentMounts[index] != expected {
			t.Fatal("frozen UID/order/readonly/placement changed", index, got.CurrentMounts[index], expected)
		}
	}
	var afterSnapshot string
	if err := f.owner.QueryRow(ctx, `SELECT snapshot::text FROM instance.instances WHERE tenant_id=$1 AND instance_id=$2`, i.TenantID, i.ID).Scan(&afterSnapshot); err != nil || afterSnapshot != beforeSnapshot {
		t.Fatal("migration rewrote immutable historical snapshot", err)
	}
	for _, row := range empty {
		value, err := readHistoricalRestart(ctx, f, row.TenantID, row.ID)
		var stored string
		if err != nil || value.CurrentMounts == nil || len(value.CurrentMounts) != 0 {
			t.Fatal("historical null/empty source did not become authoritative empty", value, err)
		}
		if err := f.owner.QueryRow(ctx, `SELECT current_mounts::text FROM instance.instances WHERE tenant_id=$1 AND instance_id=$2`, row.TenantID, row.ID).Scan(&stored); err != nil || stored != "[]" {
			t.Fatal("SQL current mounts was not an explicit array", stored, err)
		}
	}
	for version, name := range restartHistoryFiles {
		body, _ := history.Files.ReadFile(name)
		expected := sha256.Sum256(body)
		var checksum string
		if err := f.owner.QueryRow(ctx, `SELECT checksum FROM instance.schema_version WHERE version=$1`, version+1).Scan(&checksum); err != nil || checksum != hex.EncodeToString(expected[:]) {
			t.Fatal("migration changed historical checksum", name, err)
		}
	}
}

func TestRestartMigrationPGDeletedTombstonesDoNotRestoreReferences(t *testing.T) {
	f := restartHistoricalDatabase(t)
	ctx := context.Background()
	rows := []biz.Instance{}
	for _, deleteFirst := range []bool{false, true} {
		c := cpuCreate()
		mounts := []biz.VolumeMount{migrationRestartFrozenMount(c, "retained-borrowed", "/data", false)}
		i := historicalRestartInstance(c, biz.Deleted, mounts)
		i.VolumeReferencesReleased, i.ComputeCleaned, i.WorkspaceCleaned, i.QuotaReturned = true, true, true, true
		deleteID := uuid.NewString()
		i.LastOperationID = deleteID
		references := mounts
		if deleteFirst {
			i.CreateRequestHash, i.BusinessDigest = "", ""
			i.Spec = biz.Spec{WorkspaceMountPath: "/workspace", InstanceKind: biz.KindContainer, ComputeType: biz.TypeCPU}
			references = nil
		}
		insertHistoricalRestartInstance(t, f, i, references, "released", deleteID, false)
		rows = append(rows, i)
	}
	if err := migrateHistoricalRestart(ctx, f); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		got, err := readHistoricalRestart(ctx, f, row.TenantID, row.ID)
		if err != nil || got.CurrentMounts == nil || len(got.CurrentMounts) != 0 || !got.VolumeReferencesReleased || !got.ComputeCleaned || !got.WorkspaceCleaned {
			t.Fatal("deleted audit row regained current mounts or lost cleanup", got, err)
		}
		var protected, guards int
		if err := f.owner.QueryRow(ctx, `SELECT count(*) FROM instance.borrowed_volume_references WHERE tenant_id=$1 AND instance_id=$2 AND state IN ('pending','active','retiring')`, row.TenantID, row.ID).Scan(&protected); err != nil || protected != 0 {
			t.Fatal("tombstone gained protected references", protected, err)
		}
		if err := f.owner.QueryRow(ctx, `SELECT count(*) FROM instance.borrowed_pvc_guards WHERE tenant_id=$1`, row.TenantID).Scan(&guards); err != nil || guards != 0 {
			t.Fatal("tombstone created a fresh name guard", guards, err)
		}
	}
}

func TestRestartMigrationPGRejectsMissingOrConflictingProtection(t *testing.T) {
	for _, scenario := range []string{"missing-reference", "live-released-flag", "frozen-uid-mismatch", "cross-tenant-physical-name"} {
		t.Run(scenario, func(t *testing.T) {
			f := restartHistoricalDatabase(t)
			c := cpuCreate()
			mount := migrationRestartFrozenMount(c, "shared-physical-name", "/data", false)
			i := historicalRestartInstance(c, biz.Stopped, []biz.VolumeMount{mount})
			references := []biz.VolumeMount{mount}
			if scenario == "missing-reference" || scenario == "live-released-flag" {
				references = nil
			}
			if scenario == "live-released-flag" {
				i.VolumeReferencesReleased = true
			}
			if scenario == "frozen-uid-mismatch" {
				references[0].PVCUID = "different-historical-uid"
			}
			insertHistoricalRestartInstance(t, f, i, references, "active", "", false)
			if scenario == "cross-tenant-physical-name" {
				otherCreate := cpuCreate()
				otherCreate.Spec.ClusterID, otherCreate.Spec.Namespace = mount.ClusterID, mount.Namespace
				other := historicalRestartInstance(otherCreate, biz.Stopped, []biz.VolumeMount{mount})
				insertHistoricalRestartInstance(t, f, other, []biz.VolumeMount{mount}, "active", "", false)
			}
			err := migrateHistoricalRestart(context.Background(), f)
			if err == nil {
				t.Fatal("upgrade manufactured or omitted missing protection", scenario)
			}
			var versions int
			var newColumn bool
			if err := f.owner.QueryRow(context.Background(), `SELECT count(*) FROM instance.schema_version`).Scan(&versions); err != nil || versions != 7 {
				t.Fatal("rejected upgrade committed new checksum", versions, err)
			}
			if err := f.owner.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='instance' AND table_name='instances' AND column_name='current_mounts')`).Scan(&newColumn); err != nil || newColumn {
				t.Fatal("rejected upgrade partially committed new schema", newColumn, err)
			}
		})
	}
}

func TestRestartMigrationPGBaselineProofDoesNotRequirePVCOrReady(t *testing.T) {
	for _, desired := range []string{biz.Running, biz.Stopped} {
		t.Run(desired, func(t *testing.T) {
			f := restartHistoricalDatabase(t)
			ctx := context.Background()
			c := cpuCreate()
			mount := migrationRestartFrozenMount(c, "currently-missing-pvc", "/missing", false)
			i := historicalRestartInstance(c, desired, []biz.VolumeMount{mount})
			insertHistoricalRestartInstance(t, f, i, []biz.VolumeMount{mount}, "active", "", true)
			if err := migrateHistoricalRestart(ctx, f); err != nil {
				t.Fatal(err)
			}
			restored, err := readHistoricalRestart(ctx, f, i.TenantID, i.ID)
			if err != nil || restored.AppliedMountVersion != 0 {
				t.Fatal("SQL migration invented applied evidence", restored.AppliedMountVersion, err)
			}

		})
	}
}

func TestRestartMigrationPGLegacyPendingAndDeleteIdentity(t *testing.T) {
	f := restartHistoricalDatabase(t)
	ctx := context.Background()
	c := cpuCreate()
	mount := migrationRestartFrozenMount(c, "pending-old-data", "/data", false)
	i := historicalRestartInstance(c, biz.Deleted, []biz.VolumeMount{mount})
	deleteID := uuid.NewString()
	i.LastOperationID = deleteID
	i.Pending = &biz.Mutation{Kind: "set_stopped", OperationID: uuid.NewString(), Version: 6, TargetUID: i.NotebookUID, ResourceVersion: "17"}
	insertHistoricalRestartInstance(t, f, i, []biz.VolumeMount{mount}, "active", deleteID, true)
	var ackBefore string
	if err := f.owner.QueryRow(ctx, `SELECT ack::text FROM instance.instance_operations WHERE tenant_id=$1 AND operation_id=$2`, i.TenantID, deleteID).Scan(&ackBefore); err != nil {
		t.Fatal(err)
	}
	if err := migrateHistoricalRestart(ctx, f); err != nil {
		t.Fatal(err)
	}
	got, err := readHistoricalRestart(ctx, f, i.TenantID, i.ID)
	if err != nil || got.Pending == nil || got.Pending.StoppedTarget != nil || got.Pending.ProviderMarkerExpected || got.Pending.Kind != i.Pending.Kind || got.Pending.OperationID != i.Pending.OperationID || got.Pending.Version != 6 || got.Pending.TargetUID != i.Pending.TargetUID || got.Pending.ResourceVersion != "17" {
		t.Fatal("legacy payload was guessed from current DELETE target", got.Pending, err)
	}
	var execution []byte
	if err := f.owner.QueryRow(ctx, `SELECT execution FROM instance.instance_operations WHERE tenant_id=$1 AND operation_id=$2`, i.TenantID, deleteID).Scan(&execution); err != nil || execution != nil {
		t.Fatal("008 guessed legacy execution state", err)
	}
	var ackAfter string
	if err := f.owner.QueryRow(ctx, `SELECT ack::text FROM instance.instance_operations WHERE tenant_id=$1 AND operation_id=$2`, i.TenantID, deleteID).Scan(&ackAfter); err != nil || ackAfter != ackBefore {
		t.Fatal("legacy DELETE initialization changed durable ACK", err)
	}
}

// Historical migration fixture only: read raw SQL output without installing a
// historical decoder or readiness bypass in the current application.
func readHistoricalRestart(ctx context.Context, f restartMigrationFixture, tenant, id string) (biz.Instance, error) {
	var value biz.Instance
	var snapshot, mounts []byte
	err := f.owner.QueryRow(ctx, `SELECT snapshot,current_mounts,mounts_initialized,applied_mount_version,runtime_desired FROM instance.instances WHERE tenant_id=$1 AND instance_id=$2`, tenant, id).Scan(&snapshot, &mounts, &value.MountsInitialized, &value.AppliedMountVersion, &value.RuntimeDesired)
	if err != nil {
		return value, err
	}
	initialized, applied, desired := value.MountsInitialized, value.AppliedMountVersion, value.RuntimeDesired
	if err = json.Unmarshal(snapshot, &value); err != nil {
		return value, err
	}
	value.MountsInitialized, value.AppliedMountVersion, value.RuntimeDesired = initialized, applied, desired
	var stored []biz.StoredMount
	if err = json.Unmarshal(mounts, &stored); err != nil {
		return value, err
	}
	value.CurrentMounts = biz.LoadMounts(stored)
	return value, nil
}
func migrateHistoricalRestart(ctx context.Context, f restartMigrationFixture) error {
	tx, err := f.owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	body, err := history.Files.ReadFile("008_restart_pvc.sql")
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	checksum := hex.EncodeToString(digest[:])
	var existing string
	err = tx.QueryRow(ctx, `SELECT checksum FROM instance.schema_version WHERE version=8`).Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("historical checksum differs")
		}
		return nil
	}
	if err != pgx.ErrNoRows {
		return err
	}
	if _, err = tx.Exec(ctx, string(body)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO instance.schema_version(version,checksum) VALUES(8,$1)`, checksum); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
