package sandboxv1

import "errors"

// CloneIntentDigest binds the unresolved public request. It is stable across
// catalogue and platform changes and is used to find accepted commands before
// resolving current defaults again.
func CloneIntentDigest(sourceSandboxID, checkpointID string, expectedVersion int64, data *CreateSandboxData) (string, error) {
	if sourceSandboxID == "" || checkpointID == "" || expectedVersion < 1 || data == nil {
		return "", errors.New("INVALID_SANDBOX_REQUEST")
	}
	createIntent, err := CreateIntentDigest("", data)
	if err != nil {
		return "", err
	}
	return digest("sandbox-clone-intent-v1\n", struct {
		SourceSandboxID    string `json:"source_sandbox_id"`
		CheckpointID       string `json:"checkpoint_id"`
		ExpectedVersion    int64  `json:"expected_version"`
		CreateIntentDigest string `json:"create_intent_digest"`
	}{sourceSandboxID, checkpointID, expectedVersion, createIntent})
}

// CloneCommandDigest binds an accepted immutable command, including both
// target identity and source snapshot ownership. RequestDigest is excluded.
func CloneCommandDigest(ctx *CommandContext, sourceSandboxID, checkpointID string, expectedVersion int64, spec *ResolveCreateSpecResponse, intentDigest string) (string, error) {
	if ctx == nil || spec == nil || sourceSandboxID == "" || checkpointID == "" || expectedVersion < 1 || intentDigest == "" {
		return "", errors.New("INVALID_SANDBOX_REQUEST")
	}
	return digest("sandbox-clone-command-v1\n", struct {
		TenantID        string                     `json:"tenant_id"`
		ResourceID      string                     `json:"resource_id"`
		OperationID     string                     `json:"operation_id"`
		SourceSandboxID string                     `json:"source_sandbox_id"`
		CheckpointID    string                     `json:"checkpoint_id"`
		ExpectedVersion int64                      `json:"expected_version"`
		Spec            *ResolveCreateSpecResponse `json:"spec"`
		IntentDigest    string                     `json:"create_intent_digest"`
	}{ctx.TenantId, ctx.ResourceId, ctx.OperationId, sourceSandboxID, checkpointID, expectedVersion, spec, intentDigest})
}
