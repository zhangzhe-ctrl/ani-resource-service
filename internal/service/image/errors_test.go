package service

import (
	"context"
	"errors"
	imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/image"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"testing"
)

func TestImageErrorsAreBoundedAndDetailed(t *testing.T) {
	for reason, code := range map[biz.Reason]codes.Code{biz.InvalidArgument: codes.InvalidArgument, biz.InvalidReference: codes.InvalidArgument, biz.InvalidCursor: codes.InvalidArgument, biz.TrustedCallerRequired: codes.Unauthenticated, biz.PermissionDenied: codes.PermissionDenied, biz.ImageProjectDenied: codes.PermissionDenied, biz.ImageNotFound: codes.NotFound, biz.SpaceNotFound: codes.NotFound, biz.ImageAlreadyRegistered: codes.AlreadyExists, biz.SpaceNameConflict: codes.AlreadyExists, biz.IdempotencyConflict: codes.AlreadyExists, biz.VersionConflict: codes.Aborted, biz.SpaceNameImmutable: codes.FailedPrecondition, biz.SpaceNotReady: codes.FailedPrecondition, biz.CredentialAlreadyActive: codes.FailedPrecondition, biz.CredentialDeliveryExpired: codes.FailedPrecondition, biz.UnsupportedArtifact: codes.FailedPrecondition, biz.PlatformMismatch: codes.FailedPrecondition, biz.SpaceOwnershipUnconfirmed: codes.FailedPrecondition, biz.DependencyUnavailable: codes.Unavailable, biz.RequestInProgress: codes.Unavailable, biz.DeadlineExceeded: codes.DeadlineExceeded, biz.InternalError: codes.Internal} {
		s := status.Convert(rpcError(biz.Fail(reason, "raw-password-sentinel")))
		if s.Code() != code || strings.Contains(s.Message(), "sentinel") {
			t.Fatal(reason, s)
		}
		if len(s.Details()) != 1 {
			t.Fatal("ErrorInfo missing")
		}
		d, ok := s.Details()[0].(*errdetails.ErrorInfo)
		if !ok || d.Domain != "image.ani.io" || d.Reason != string(reason) {
			t.Fatal("incorrect Image reason")
		}
	}
	if e := rpcError(errors.New("postgres://admin:sentinel@private")); status.Code(e) != codes.Internal || strings.Contains(e.Error(), "sentinel") {
		t.Fatal("unexpected error disclosure")
	}
	if status.Code(rpcError(context.DeadlineExceeded)) != codes.DeadlineExceeded {
		t.Fatal("deadline lost")
	}
}
func TestUnknownImageEnumsRejectedBeforeUseCase(t *testing.T) {
	s := NewTenantService(nil, nil)
	for _, scope := range []imagev1.ImageScope{0, 99, -1} {
		if _, err := s.GetImage(context.Background(), &imagev1.GetImageRequest{Scope: scope}); status.Code(err) != codes.InvalidArgument {
			t.Fatal("unknown scope accepted")
		}
	}
	for _, r := range []*imagev1.RegisterImageRequest{{Purposes: []imagev1.ImagePurpose{0}}, {Purposes: []imagev1.ImagePurpose{99}}, {Accelerator: 99}} {
		if _, err := s.RegisterImage(context.Background(), r); status.Code(err) != codes.InvalidArgument {
			t.Fatal("unknown declaration accepted")
		}
	}
	if wireCredential(biz.CredentialInfo{}).ProtoReflect().Descriptor().Fields().ByName("secret") != nil {
		t.Fatal("metadata exposes secret")
	}
}
