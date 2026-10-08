package data

import (
	"context"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const (
	ociManifest    = "application/vnd.oci.image.manifest.v1+json"
	dockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	ociIndex       = "application/vnd.oci.image.index.v1+json"
	dockerIndex    = "application/vnd.docker.distribution.manifest.list.v2+json"
)

type harborPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}
type harborReference struct {
	Digest      string            `json:"child_digest"`
	Platform    harborPlatform    `json:"platform"`
	Annotations map[string]string `json:"annotations"`
}
type harborArtifact struct {
	Digest            string            `json:"digest"`
	Type              string            `json:"type"`
	ManifestMediaType string            `json:"manifest_media_type"`
	ArtifactType      string            `json:"artifact_type"`
	RepositoryName    string            `json:"repository_name"`
	Extra             harborPlatform    `json:"extra_attrs"`
	References        []harborReference `json:"references"`
	Annotations       map[string]string `json:"annotations"`
}

var imagePlatformToken = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

func supportedPlatform(p harborPlatform) bool {
	return p.OS != "unknown" && p.Architecture != "unknown" && imagePlatformToken.MatchString(p.OS) && imagePlatformToken.MatchString(p.Architecture) && (p.Variant == "" || imagePlatformToken.MatchString(p.Variant))
}
func unsupportedArtifact() error {
	return biz.Fail(biz.UnsupportedArtifact, "artifact has no confirmed runnable image platforms")
}
func (h *Harbor) readArtifact(ctx context.Context, project, repository, ref string) (harborArtifact, error) {
	var a harborArtifact
	if !harborName.MatchString(project) {
		return a, biz.Fail(biz.InvalidReference, "invalid project")
	}
	separator := ":"
	if strings.HasPrefix(ref, "sha256:") {
		separator = "@"
	}
	if _, err := biz.ParseImageReference(h.base.Host, project, h.base.Host+"/"+project+"/"+repository+separator+ref); err != nil {
		return a, err
	}
	path := "/api/v2.0/projects/" + url.PathEscape(project) + "/repositories/" + url.PathEscape(repository) + "/artifacts/" + url.PathEscape(ref) + "?with_tag=false&with_label=false&with_scan_overview=false&with_accessory=false"
	_, err := h.doJSON(ctx, http.MethodGet, path, nil, &a)
	if err != nil {
		return a, err
	}
	if biz.ParseDigest(a.Digest) != nil || a.RepositoryName != project+"/"+repository || (separator == "@" && a.Digest != ref) {
		return a, harborUnavailable()
	}
	return a, nil
}
func (h *Harbor) ResolveArtifact(ctx context.Context, project, repository, ref string) (biz.Artifact, error) {
	if strings.HasPrefix(ref, "sha256:") {
		return h.GetArtifactByDigest(ctx, project, repository, ref)
	}
	a, err := h.readArtifact(ctx, project, repository, ref)
	if err != nil {
		return biz.Artifact{}, err
	}
	// Tags are mutable: all platforms and returned facts are taken from the
	// immutable digest reread, never from the first tag response.
	return h.GetArtifactByDigest(ctx, project, repository, a.Digest)
}
func (h *Harbor) GetArtifactByDigest(ctx context.Context, project, repository, digest string) (biz.Artifact, error) {
	if err := biz.ParseDigest(digest); err != nil {
		return biz.Artifact{}, err
	}
	a, err := h.readArtifact(ctx, project, repository, digest)
	if err != nil {
		return biz.Artifact{}, err
	}
	platforms, err := h.readRunnablePlatforms(ctx, project, repository, a)
	if err != nil {
		return biz.Artifact{}, err
	}
	return biz.Artifact{Digest: a.Digest, MediaType: a.ManifestMediaType, Platforms: platforms}, nil
}
func (h *Harbor) readRunnablePlatforms(ctx context.Context, project, repository string, root harborArtifact) ([]biz.ImagePlatform, error) {
	seen := map[string]bool{}
	platforms := map[string]biz.ImagePlatform{}
	descriptors := 0
	var visit func(harborArtifact, int, harborPlatform) error
	visit = func(a harborArtifact, depth int, expected harborPlatform) error {
		if depth > 2 || seen[a.Digest] || !strings.EqualFold(a.Type, "IMAGE") || a.ArtifactType != "" || a.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
			return unsupportedArtifact()
		}
		if (expected.OS != "" || expected.Architecture != "" || expected.Variant != "") && !supportedPlatform(expected) {
			return unsupportedArtifact()
		}
		seen[a.Digest] = true
		defer delete(seen, a.Digest)
		switch a.ManifestMediaType {
		case ociManifest, dockerManifest:
			if len(a.References) > 0 || !supportedPlatform(a.Extra) {
				return unsupportedArtifact()
			}
			p := a.Extra
			if expected.OS != "" || expected.Architecture != "" {
				if !supportedPlatform(expected) || expected.OS != p.OS || expected.Architecture != p.Architecture || (p.Variant != "" && expected.Variant != "" && p.Variant != expected.Variant) {
					return unsupportedArtifact()
				}
				if p.Variant == "" {
					p.Variant = expected.Variant
				}
			}
			platforms[p.OS+"/"+p.Architecture+"/"+p.Variant] = biz.ImagePlatform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
		case ociIndex, dockerIndex:
			if len(a.References) == 0 {
				return unsupportedArtifact()
			}
			descriptors += len(a.References)
			if descriptors > 32 {
				return unsupportedArtifact()
			}
			for _, r := range a.References {
				if r.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
					continue
				}
				if biz.ParseDigest(r.Digest) != nil {
					return unsupportedArtifact()
				}
				child, err := h.readArtifact(ctx, project, repository, r.Digest)
				if err != nil {
					return err
				}
				next := r.Platform
				if expected.OS != "" {
					if next.OS == "" && next.Architecture == "" {
						next = expected
					} else if next.OS != expected.OS || next.Architecture != expected.Architecture || (next.Variant != "" && expected.Variant != "" && next.Variant != expected.Variant) {
						return unsupportedArtifact()
					}
				}
				if err = visit(child, depth+1, next); err != nil {
					return err
				}
			}
		default:
			return unsupportedArtifact()
		}
		return nil
	}
	if err := visit(root, 0, harborPlatform{}); err != nil {
		return nil, err
	}
	if len(platforms) == 0 {
		return nil, unsupportedArtifact()
	}
	keys := make([]string, 0, len(platforms))
	for k := range platforms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]biz.ImagePlatform, 0, len(keys))
	for _, k := range keys {
		out = append(out, platforms[k])
	}
	return out, nil
}
