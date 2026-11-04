package main

import (
	"context"
	"fmt"

	sandboxpb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sandboxCloneGate blocks admission regardless of the authenticated caller.
// It is installed at the common composition boundary, including when Sandbox
// recovers through sandboxRetry. Existing durable transfers remain recoverable.
type sandboxCloneGate struct{ sandboxpb.SandboxServiceServer }

func (*sandboxCloneGate) AcceptSandboxClone(context.Context, *sandboxpb.AcceptSandboxCloneRequest) (*sandboxpb.AcceptSandboxCloneResponse, error) {
	return nil, status.Error(codes.Unavailable, "SANDBOX_CLONE_TEMPORARILY_UNAVAILABLE")
}

func sandboxClonePaused(value string) (bool, error) {
	switch value {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("invalid ANI_SANDBOX_CLONE_WRITES_PAUSED value: expected true or false")
	}
}
