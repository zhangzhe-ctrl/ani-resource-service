package biz

import "context"

// RuntimeImages is consumed by the actual workload owner; Image owns no Pods.
type RuntimeImages interface {
	ResolveImageForWorkload(context.Context, ResolveImage) (ResolvedImage, error)
	GetTenantPullMaterial(context.Context, string) (PullMaterial, error)
}
type SecretCipher interface {
	Seal(context.Context, SecretAAD, Secret) (EncryptedSecret, error)
	Open(context.Context, SecretAAD, EncryptedSecret) (Secret, error)
}
type SpaceRepository interface {
	FindTenantSpace(context.Context, string) (Space, error)
	FindPlatformSpace(context.Context) (Space, error)
	ReserveTenantSpace(context.Context, Space, Command) (Space, Command, error)
}
type CatalogRepository interface {
	FindTenantRegistration(context.Context, string, string) (Registration, error)
	FindPlatformRegistration(context.Context, string) (Registration, error)
	PageTenantRegistrations(context.Context, string, Filter, *PageKey) ([]Registration, error)
	PagePlatformRegistrations(context.Context, Filter, *PageKey) ([]Registration, error)
	ApplyTenantRegistration(context.Context, Command, Registration) (Registration, error)
	ApplyTenantMetadata(context.Context, Command, UpdateImage) (Registration, error)
	ApplyTenantUnregister(context.Context, Command, UnregisterImage) (Registration, error)
}
type CommandRepository interface {
	FindTenantCommand(context.Context, string, string, string) (Command, error)
	WithSpaceWriteLock(context.Context, string, func(context.Context) error) error
}
type StoredCredential struct {
	Info   CredentialInfo
	Secret EncryptedSecret
	AAD    SecretAAD
}
type CredentialRepository interface {
	GetTenantPublisher(context.Context, string, string) (CredentialInfo, error)
	GetTenantPull(context.Context, string, string) (StoredCredential, error)
}
type Project struct {
	ID      int64
	Name    string
	Private bool
}
type RobotPermission struct{ Project, Resource, Action string }
type Robot struct {
	ID                          int64
	Name, Description, Username string
	Disabled                    bool
	Permissions                 []RobotPermission
	ExpiresAt                   int64
}
type RobotRequest struct {
	Name, Description string
	DurationDays      int64
	Permissions       []RobotPermission
}
type Artifact struct {
	Digest, MediaType string
	Platforms         []ImagePlatform
}
type Registry interface {
	GetProjectByID(context.Context, int64) (Project, error)
	FindProjectByName(context.Context, string) (Project, error)
	CreatePrivateProject(context.Context, string) (Project, error)
	CreateRobot(context.Context, RobotRequest) (Robot, error)
	FindOwnedRobot(context.Context, RobotRequest) (Robot, error)
	GetRobot(context.Context, int64) (Robot, error)
	SetRobotSecret(context.Context, Robot, Secret) error
	SetRobotDisabled(context.Context, Robot, bool) error
	ResolveArtifact(context.Context, string, string, string) (Artifact, error)
	GetArtifactByDigest(context.Context, string, string, string) (Artifact, error)
}
