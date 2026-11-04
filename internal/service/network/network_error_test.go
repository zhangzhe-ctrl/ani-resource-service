package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestVPCDependencyDeadlineMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want codes.Code
	}{
		{&biz.Error{Reason: biz.DependencyUnavailable, Message: "Network database connection timed out", Cause: context.DeadlineExceeded}, codes.Unavailable},
		{context.DeadlineExceeded, codes.DeadlineExceeded},
		{context.Canceled, codes.Canceled},
		{biz.Fail(biz.ResourceNotFound, "resource not found"), codes.NotFound},
	} {
		if got := status.Code(rpcError(tc.err)); got != tc.want {
			t.Fatalf("%v: %v want %v", tc.err, got, tc.want)
		}
	}
}

func TestPendingObservationMapsToRetryableWithoutLeakingCause(t *testing.T) {
	pending := &biz.ProviderError{Kind: biz.ProviderTemporary, Cause: errors.Join(biz.ErrObservationPending, errors.New("private provider diagnostic"))}
	wire := status.Convert(rpcError(pending))
	if wire.Code() != codes.Unavailable || strings.Contains(wire.Message(), "private provider diagnostic") {
		t.Fatalf("unsafe or non-retryable status: %v", wire)
	}
	details := wire.Details()
	if len(details) != 1 {
		t.Fatalf("missing ErrorInfo: %v", details)
	}
	info, ok := details[0].(*errdetails.ErrorInfo)
	if !ok || info.Reason != string(biz.DependencyUnavailable) {
		t.Fatalf("unexpected detail: %v", details[0])
	}
	for _, tc := range []struct {
		err  error
		want codes.Code
	}{
		{&biz.ProviderError{Kind: biz.ProviderTemporary, Cause: errors.New("other failure")}, codes.Internal},
		{&biz.ProviderError{Kind: biz.ProviderConflict, Cause: biz.ErrObservationPending}, codes.Internal},
		{biz.Fail(biz.PlacementMismatch, "tenant identity mismatch"), codes.FailedPrecondition},
		{biz.Fail(biz.DependencyUnavailable, "tenant namespace unavailable"), codes.Unavailable},
	} {
		if got := status.Code(rpcError(tc.err)); got != tc.want {
			t.Fatalf("unrelated error mapping changed: %v want %v", got, tc.want)
		}
	}
}
