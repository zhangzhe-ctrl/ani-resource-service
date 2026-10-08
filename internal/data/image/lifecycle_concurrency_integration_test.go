//go:build imageintegration

package data_test

import (
	"bytes"
	"context"
	"encoding/pem"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestLifecycleTwoProcessesSameCommands(t *testing.T) {
	f, r, cfg, _, ctx := lifecycleFixture(t)
	server := registryHTTP(t, r)
	input := crashInput{DSN: f.RuntimeDSN, URL: server.URL, CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), Config: cfg, Tenant: tenantFrom(t, ctx)}
	runCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, 2)
	for i := range commands {
		cmd := exec.CommandContext(runCtx, os.Args[0], "-test.run=^TestLifecycleCrashHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "IMAGE_LIFECYCLE_CHILD_CONFIG="+childFile(t, input))
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		commands[i] = cmd
		if err := cmd.Start(); err != nil {
			cancel()
			for j := 0; j < i; j++ {
				_ = commands[j].Wait()
			}
			t.Fatal("could not start owned child")
		}
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Errorf("owned child pid=%d failed: %s", cmd.Process.Pid, outputs[i].String())
		} else {
			t.Logf("owned concurrent child pid=%d exit=0", cmd.Process.Pid)
		}
	}
	r.mu.Lock()
	projects, robots, secretSets := r.creates, r.robotCreates, r.secretSets
	r.mu.Unlock()
	if projects != 1 || robots != 2 || secretSets != 2 {
		t.Fatalf("duplicate writes across two processes: projects=%d robots=%d sets=%d", projects, robots, secretSets)
	}
}
