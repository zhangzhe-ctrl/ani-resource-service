// The maintenance window pauses new Network admissions before applying this
// command. Default mode is a read-only exact-row plan; -apply requires its hash.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dsnFile := flag.String("dsn-file", "", "restricted runtime database DSN file")
	cluster := flag.String("cluster-id", "", "exact platform cluster UUID")
	kubeconfig := flag.String("kubeconfig", "", "approved cluster kubeconfig path")
	apply := flag.Bool("apply", false, "apply only reviewed exact NULL rows")
	reviewed := flag.String("reviewed-sha256", "", "SHA256 from the read-only plan")
	flag.Parse()
	if *dsnFile == "" || *cluster == "" || *kubeconfig == "" {
		return fmt.Errorf("dsn-file, cluster-id and kubeconfig required")
	}
	raw, err := os.ReadFile(*dsnFile)
	if err != nil {
		return fmt.Errorf("read database DSN file failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	repo, err := data.OpenPostgres(ctx, strings.TrimSpace(string(raw)), data.Placement{ClusterID: *cluster, NamespacePrefix: "tenant-"})
	if err != nil {
		return err
	}
	defer repo.Close()
	_, err = data.OpenKCProvider(repo, *kubeconfig)
	if err != nil {
		return err
	}
	plan, err := repo.PlanNamespaceUIDBackfill(ctx)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	if err = enc.Encode(struct {
		Stage string
		Plan  data.NamespaceUIDBackfillPlan
	}{"before", plan}); err != nil {
		return err
	}
	if !*apply {
		return nil
	}
	after, err := repo.ApplyNamespaceUIDBackfill(ctx, *reviewed)
	if err != nil {
		return err
	}
	if err = enc.Encode(struct {
		Stage string
		Plan  data.NamespaceUIDBackfillPlan
	}{"after", after}); err != nil {
		return err
	}
	if after.NullCount != 0 {
		return fmt.Errorf("namespace UID backfill left NULL rows")
	}
	return nil
}
