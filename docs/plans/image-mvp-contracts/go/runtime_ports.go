// Design-only narrow port, to be placed in internal/biz/image/ports.go after review.
// No generated API, HTTP, pgx or Kubernetes types may enter this package.
package biz

import (
    "context"
    "time"
)

type ImageScope string
const (
    TenantImages ImageScope = "tenant"
    PlatformImages ImageScope = "platform"
)

type ImagePlatform struct { OS, Architecture, Variant string }
type ResolveImage struct {
    TenantID, ImageID string
    Scope ImageScope
    TargetPlatform ImagePlatform
}
type ResolvedImage struct {
    TenantID, ImageID, Reference, Digest string
    Scope ImageScope
    Version int64
    Platform ImagePlatform
}
// Secret must never be marshaled to logs, public responses or telemetry.
type Secret []byte
func (Secret) String() string { return "[REDACTED]" }

type PullMaterial struct {
    TenantID, SpaceID, RegistryAuthority, Username string
    Secret Secret
    Generation int64
    ExpiresAt *time.Time
}
// Same-process consumers use this interface; remote consumers adapt the internal proto.
type RuntimeImages interface {
    ResolveImageForWorkload(context.Context, ResolveImage) (ResolvedImage, error)
    GetTenantPullMaterial(context.Context, string) (PullMaterial, error)
}

type EncryptedSecret struct { KeyID string; Ciphertext []byte }
type SecretAAD struct {
    InstallationID, TenantID, SpaceID, Purpose, CommandID string
    Scope ImageScope
    Generation int64
}
type SecretCipher interface {
    Seal(context.Context, SecretAAD, Secret) (EncryptedSecret, error)
    Open(context.Context, SecretAAD, EncryptedSecret) (Secret, error)
}
