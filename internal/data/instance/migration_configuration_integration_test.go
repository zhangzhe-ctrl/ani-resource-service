//go:build instanceintegration

package instance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/instance"
	history "github.com/zhangzhe-ctrl/ani-resource-service/migrations/instance"
)

func TestConfigurationCutoverPGRequiresEmptyLedger(t *testing.T) {
	for _, desired := range []string{biz.Running, biz.Stopped, biz.Deleted} {
		t.Run(desired, func(t *testing.T) {
			ctx := context.Background()
			f := restartHistoricalDatabase(t)
			i := historicalRestartInstance(cpuCreate(), desired, nil)
			insertHistoricalRestartInstance(t, f, i, nil, "released", "", false)
			if err := migrateHistoricalRestart(ctx, f); err != nil {
				t.Fatal(err)
			}
			if err := data.Migrate(ctx, f.ownerDSN, f.role); err == nil || !strings.Contains(err.Error(), "empty instance ledger") {
				t.Fatal("cutover accepted a remaining instance or tombstone", err)
			}
			var version int
			var retained bool
			if err := f.owner.QueryRow(ctx, `SELECT max(version) FROM instance.schema_version`).Scan(&version); err != nil || version != 8 {
				t.Fatal("rejected cutover changed migration history", version, err)
			}
			if err := f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='instance' AND table_name='instances' AND column_name='instance_contract_version')`).Scan(&retained); err != nil || !retained {
				t.Fatal("rejected cutover partially dropped the old column", err)
			}
			// Only this exclusive fixture's already inventoried records are removed.
			for _, table := range []string{"instance_operations", "instance_work", "workspace_bindings", "instances"} {
				if _, err := f.owner.Exec(ctx, "DELETE FROM instance."+table+" WHERE tenant_id=$1 AND instance_id=$2", i.TenantID, i.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := data.Migrate(ctx, f.ownerDSN, f.role); err != nil {
				t.Fatal("empty-ledger cutover failed", err)
			}
			if err := data.Migrate(ctx, f.ownerDSN, f.role); err != nil {
				t.Fatal("cutover replay failed", err)
			}
			repo, err := data.OpenPostgres(ctx, f.runtimeDSN)
			if err != nil {
				t.Fatal("unversioned application did not open the migrated schema", err)
			}
			defer repo.Close()
			if _, err := repo.AcceptCreate(ctx, cpuCreate()); err != nil {
				t.Fatal("new configuration failed after cutover", err)
			}
			for index, name := range append(append([]string{}, restartHistoryFiles...), "008_restart_pvc.sql", "009_unversioned_configuration.sql") {
				body, _ := history.Files.ReadFile(name)
				digest := sha256.Sum256(body)
				var checksum string
				if err := f.owner.QueryRow(ctx, `SELECT checksum FROM instance.schema_version WHERE version=$1`, index+1).Scan(&checksum); err != nil || checksum != hex.EncodeToString(digest[:]) {
					t.Fatal("cutover changed historical checksum", name, err)
				}
			}
		})
	}
}

func TestConfigurationCutoverPGFreshDatabase(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	c := cpuCreate()
	if _, err := f.repo.AcceptCreate(ctx, c); err != nil {
		t.Fatal(err)
	}
	i, err := f.repo.Get(ctx, c.TenantID, c.ID)
	if err != nil || i.Spec.WorkspaceMountPath != "/workspace" {
		t.Fatal("new configuration did not persist", err)
	}
	var field bool
	if err := f.runtime.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='instance' AND table_name='instances' AND column_name='instance_contract_version') OR EXISTS(SELECT 1 FROM instance.instances WHERE snapshot->'Spec' ? 'ContractVersion')`).Scan(&field); err != nil || field {
		t.Fatal("new configuration retains version state", err)
	}
}
