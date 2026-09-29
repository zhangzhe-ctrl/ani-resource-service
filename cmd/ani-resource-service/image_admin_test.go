package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageAdminRequestClosedSchema(t *testing.T) {
	good := map[string]string{
		"init-platform":            `{"idempotency_key":"init-platform-1"}`,
		"issue-platform-publisher": `{"idempotency_key":"publisher-1","expected_version":0,"rotate":false}`,
		"register-platform":        `{"idempotency_key":"register-1","image_reference":"registry.invalid/platform/app:v1","display_name":"App","purposes":["container"]}`,
		"update-platform":          `{"idempotency_key":"update-1","image_id":"img_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_version":1,"display_name":"App","purposes":["container"]}`,
		"unregister-platform":      `{"idempotency_key":"remove-1","image_id":"img_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_version":1}`,
		"inspect-space":            `{"space_id":"11111111-1111-4111-8111-111111111111"}`,
		"recover-project":          `{"space_id":"11111111-1111-4111-8111-111111111111","project_id":2,"evidence_sha256":"` + strings.Repeat("a", 64) + `"}`,
		"purge-expired-secrets":    `{}`,
	}
	for action, body := range good {
		if _, err := imageAdminRequest(action, []byte(body)); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		for _, bad := range []string{`{"tenant_id":"forged"}`, `{"scope":"platform"}`, `{"url":"https://arbitrary.invalid"}`, `{"sql":"select 1"}`, `{} {}`, `null`, `[]`, `{"idempotency_key":"first","idempotency_key":"second"}`} {
			if _, err := imageAdminRequest(action, []byte(bad)); err == nil {
				t.Fatalf("%s accepted invalid input", action)
			}
		}
	}
	if _, err := imageAdminRequest("arbitrary-action", []byte(`{}`)); err == nil {
		t.Fatal("unknown action accepted")
	}
	if _, err := imageAdminRequest("init-platform", make([]byte, 65537)); err == nil {
		t.Fatal("oversize input accepted")
	}
}
func TestImageAdminOutputDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "credential.json")
	f, root, err := reserveImageSecretOutput(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("sentinel"); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("output not private")
	}
	f.Close()
	root.Close()
	if f, r, err := reserveImageSecretOutput(dir, path); err == nil {
		f.Close()
		r.Close()
		t.Fatal("existing output overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "sentinel" {
		t.Fatal("existing output changed")
	}
	link := filepath.Join(dir, "link.json")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{link, filepath.Join(t.TempDir(), "outside.json"), dir + "/../outside.json", dir + "/.hidden.json"} {
		if f, r, err := reserveImageSecretOutput(dir, bad); err == nil {
			f.Close()
			r.Close()
			t.Fatal("unsafe output accepted")
		}
	}
	dirLink := dir + "-link"
	if err = os.Symlink(dir, dirLink); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dirLink)
	if f, r, err := reserveImageSecretOutput(dirLink, filepath.Join(dirLink, "new.json")); err == nil {
		f.Close()
		r.Close()
		t.Fatal("symlink directory accepted")
	}
	if err = os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if f, r, err := reserveImageSecretOutput(dir, filepath.Join(dir, "new.json")); err == nil {
		f.Close()
		r.Close()
		t.Fatal("public directory accepted")
	}
}
func TestImageAdminModesAreExclusive(t *testing.T) {
	oldAction, oldInput, oldOperator, oldOutput := imageAdminAction, imageAdminInput, imageAdminOperator, imageAdminSecretOutput
	oldImage, oldNetwork, oldNode, oldBase := flagImageMigrate, flagMigrate, flagNodeFacts, baseConnectivityAction
	defer func() {
		imageAdminAction, imageAdminInput, imageAdminOperator, imageAdminSecretOutput = oldAction, oldInput, oldOperator, oldOutput
		flagImageMigrate, flagMigrate, flagNodeFacts, baseConnectivityAction = oldImage, oldNetwork, oldNode, oldBase
	}()
	imageAdminAction = "init-platform"
	imageAdminInput = ""
	imageAdminOperator = "operator.json"
	imageAdminSecretOutput = ""
	flagImageMigrate = false
	flagMigrate = false
	flagNodeFacts = false
	baseConnectivityAction = ""
	if err := validateImageAdminModes(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*bool{&flagImageMigrate, &flagMigrate, &flagNodeFacts} {
		*p = true
		if validateImageAdminModes() == nil {
			t.Fatal("conflicting mode accepted")
		}
		*p = false
	}
	baseConnectivityAction = "inspect"
	if validateImageAdminModes() == nil {
		t.Fatal("base mode overlap")
	}
	baseConnectivityAction = ""
	imageAdminAction = "issue-platform-publisher"
	if validateImageAdminModes() == nil {
		t.Fatal("publisher output optional")
	}
	imageAdminSecretOutput = "/private/new.json"
	if err := validateImageAdminModes(); err != nil {
		t.Fatal(err)
	}
	imageAdminAction = "init-platform"
	if validateImageAdminModes() == nil {
		t.Fatal("non-secret action output accepted")
	}
	imageAdminSecretOutput = ""
	imageAdminAction = ""
	if validateImageAdminModes() == nil {
		t.Fatal("orphan operator flags accepted")
	}
	imageAdminOperator = ""
	if err := validateImageAdminModes(); err != nil {
		t.Fatal("legacy modes changed", err)
	}
}
