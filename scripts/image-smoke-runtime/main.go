// image-smoke-runtime is an operator-only technical acceptance helper. It opens
// no listener and is never the ordinary container owner or a public credential API.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	conf "github.com/zhangzhe-ctrl/ani-resource-service/internal/conf/v1"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/image"
	"google.golang.org/protobuf/encoding/protojson"
)

type request struct {
	TenantID string            `json:"tenant_id"`
	ImageID  string            `json:"image_id"`
	Scope    biz.ImageScope    `json:"scope"`
	Platform biz.ImagePlatform `json:"platform"`
}
type profile struct {
	Format   string `json:"format"`
	Approved bool   `json:"approved"`
	Approval string `json:"approval_reference"`
	RunID    string `json:"run_id"`
	Tenants  []struct {
		ID string `json:"tenant_id"`
	} `json:"test_tenants"`
	Smoke struct {
		RuntimeConfig string `json:"runtime_config"`
		Technical     bool   `json:"allow_technical_runtime_read"`
	} `json:"smoke"`
}

func privateFile(name string) ([]byte, error) {
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() > 65536 {
		return nil, errors.New("private input unavailable")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, errors.New("private input unavailable")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || after.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private input changed")
	}
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(b) > 65536 || len(b) == 0 {
		return nil, errors.New("private input size invalid")
	}
	return b, nil
}
func decodeRequest(raw []byte) (request, error) {
	var r request
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > 65536 || d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF {
		return r, errors.New("invalid request")
	}
	if _, err := biz.ParseTenant(r.TenantID); err != nil {
		return r, err
	}
	if _, err := biz.ParseImageID(r.ImageID); err != nil {
		return r, errors.New("invalid image ID")
	}
	if r.Scope != biz.TenantImages && r.Scope != biz.PlatformImages {
		return r, errors.New("invalid scope")
	}
	return r, biz.ValidateImagePlatform(r.Platform)
}
func validateProfile(p profile, tenant string) error {
	if p.Format != "image-mvp-live-profile/v1" || !p.Approved || p.Approval == "" || p.RunID == "" || !p.Smoke.Technical || !filepath.IsAbs(p.Smoke.RuntimeConfig) {
		return errors.New("approved technical profile required")
	}
	for _, v := range p.Tenants {
		if v.ID == tenant {
			return nil
		}
	}
	return errors.New("tenant outside approved profile")
}
func reserveOutput(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("absolute private output required")
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private output directory required")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}
func execute(ctx context.Context, profilePath, output string, in io.Reader, out io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(in, 65537))
	if err != nil {
		return err
	}
	req, err := decodeRequest(raw)
	if err != nil {
		return err
	}
	b, err := privateFile(profilePath)
	if err != nil {
		return err
	}
	defer clear(b)
	var p profile
	if json.Unmarshal(b, &p) != nil {
		return errors.New("invalid profile")
	}
	if err = validateProfile(p, req.TenantID); err != nil {
		return err
	}
	config, err := privateFile(p.Smoke.RuntimeConfig)
	if err != nil {
		return err
	}
	defer clear(config)
	var bootstrap conf.Bootstrap
	if protojson.Unmarshal(config, &bootstrap) != nil || bootstrap.Image == nil || !bootstrap.Image.Enabled {
		return errors.New("enabled JSON Image config required")
	}
	c := bootstrap.Image
	if err = c.Validate(); err != nil {
		return err
	}
	dsn, err := privateFile(c.DatabaseDsnFile)
	if err != nil {
		return err
	}
	defer clear(dsn)
	password, err := privateFile(c.HarborPasswordFile)
	if err != nil {
		return err
	}
	defer clear(password)
	keyBytes, err := privateFile(c.EncryptionKeysFile)
	if err != nil {
		return err
	}
	defer clear(keyBytes)
	var keys struct {
		Active string            `json:"active_key_id"`
		Keys   map[string][]byte `json:"keys"`
	}
	d := json.NewDecoder(bytes.NewReader(keyBytes))
	d.DisallowUnknownFields()
	if d.Decode(&keys) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid keyring")
	}
	defer func() {
		for _, key := range keys.Keys {
			clear(key)
		}
	}()
	cipher, err := data.NewAESGCMKeyring(keys.Active, keys.Keys)
	if err != nil {
		return err
	}
	ca, err := os.ReadFile(c.HarborCaFile)
	if err != nil || len(ca) > 65536 {
		return errors.New("registry CA unavailable")
	}
	registry, err := data.NewHarbor(data.HarborConfig{URL: c.HarborUrl, Username: c.HarborUsername, Password: password, CAPEM: ca, RobotNamePrefix: c.RobotNamePrefix, Timeout: c.RequestTimeout.AsDuration()})
	if err != nil {
		return err
	}
	defer registry.Close()
	repo, err := data.OpenPostgres(ctx, strings.TrimSpace(string(dsn)))
	if err != nil {
		return err
	}
	defer repo.Close()
	runtime, err := biz.NewRuntime(repo, repo, repo, registry, cipher, time.Now)
	if err != nil {
		return err
	}
	// This identity proves only this explicitly approved local technical read. A
	// production owner must authenticate its own call and enforce its own tenancy.
	ctx = biz.WithCaller(ctx, biz.Caller{Kind: biz.RuntimeCaller, Subject: "image-smoke/" + p.RunID, TenantID: req.TenantID})
	resolved, err := runtime.ResolveImageForWorkload(ctx, biz.ResolveImage{TenantID: req.TenantID, ImageID: req.ImageID, Scope: req.Scope, TargetPlatform: req.Platform})
	if err != nil {
		return err
	}
	pull, err := runtime.GetTenantPullMaterial(ctx, req.TenantID)
	if err != nil {
		return err
	}
	defer clear(pull.Secret)
	f, err := reserveOutput(output)
	if err != nil {
		return err
	}
	defer f.Close()
	auth := base64.StdEncoding.EncodeToString(append(append([]byte(pull.Username), ':'), pull.Secret...))
	err = json.NewEncoder(f).Encode(map[string]any{"auths": map[string]any{pull.RegistryAuthority: map[string]string{"auth": auth}}})
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(resolved)
}
func main() {
	profilePath := flag.String("profile", "", "approved private profile")
	output := flag.String("auth-output", "", "new 0600 registry auth file in private directory")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if flag.NArg() != 0 || *profilePath == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "profile and private auth-output required")
		os.Exit(2)
	}
	if err := execute(ctx, *profilePath, *output, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Image technical runtime probe failed; no secret or dependency error emitted")
		os.Exit(1)
	}
}
