package sandbox

import "context"

// EnvironmentVariable describes a Pod spec entry without its value. This is
// deliberately not a list of image ENV or of all process environment values.
type EnvironmentVariable struct {
	Name, Source, SecretRef, FieldRef string
}

// EnvironmentReader observes metadata for the verified current Sandbox Pod.
type EnvironmentReader interface {
	ReadEnvironment(context.Context, Sandbox) ([]EnvironmentVariable, error)
}
