package sandbox

import (
	"testing"

	pb "github.com/zhangzhe-ctrl/ani-resource-service/api/sandbox/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSecurityEventsFailClosedWithoutSecuritySource(t *testing.T) {
	svc, repo, _ := eventService()
	reader := &eventReader{items: []biz.Event{{ID: "failure", Type: "Warning", Reason: "Failed", Message: "Container operation failed"}}}
	svc.config.EventReader = reader
	for _, severity := range []string{"", "info", "warning", "critical"} {
		req := &pb.ListSandboxSecurityEventsRequest{TenantId: testTenant, SandboxId: testSandbox, Severity: severity}
		result, err := svc.ListSandboxSecurityEvents(serviceContext(), req)
		if result != nil || status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "SANDBOX_SECURITY_EVENT_SOURCE_UNAVAILABLE" {
			t.Fatalf("severity %q: result=%+v err=%v", severity, result, err)
		}
	}
	if reader.calls != 0 || repo.calls != 4 {
		t.Fatalf("ordinary event reader calls=%d, sandbox lookup calls=%d", reader.calls, repo.calls)
	}
}

func TestSecurityEventsValidateIdentityBeforeReportingSource(t *testing.T) {
	svc, repo, _ := eventService()
	for _, test := range []struct {
		name string
		edit func(*pb.ListSandboxSecurityEventsRequest)
		code codes.Code
	}{
		{"severity", func(r *pb.ListSandboxSecurityEventsRequest) { r.Severity = "invalid" }, codes.InvalidArgument},
		{"page size", func(r *pb.ListSandboxSecurityEventsRequest) { r.PageSize = 101 }, codes.InvalidArgument},
		{"sandbox id", func(r *pb.ListSandboxSecurityEventsRequest) { r.SandboxId = "bad" }, codes.InvalidArgument},
		{"wrong tenant", func(r *pb.ListSandboxSecurityEventsRequest) { r.TenantId = "66666666-6666-4666-8666-666666666666" }, codes.PermissionDenied},
		{"unknown sandbox", func(r *pb.ListSandboxSecurityEventsRequest) { r.SandboxId = "66666666-6666-4666-8666-666666666666" }, codes.NotFound},
		{"unknown field", func(r *pb.ListSandboxSecurityEventsRequest) { r.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01}) }, codes.InvalidArgument},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := &pb.ListSandboxSecurityEventsRequest{TenantId: testTenant, SandboxId: testSandbox}
			test.edit(req)
			_, err := svc.ListSandboxSecurityEvents(serviceContext(), req)
			if status.Code(err) != test.code {
				t.Fatalf("got %v, want %v", err, test.code)
			}
		})
	}
	if repo.calls != 1 {
		t.Fatalf("invalid and unauthorized requests must not query repository: calls=%d", repo.calls)
	}
}
