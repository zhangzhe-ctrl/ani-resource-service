package server

import (
	"context"
	"net"
	"testing"
	"time"

	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	networkservice "github.com/zhangzhe-ctrl/ani-resource-service/internal/service/network"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRetiredDeviceAPIsHaveNoGRPCDispatch(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	// Exercise the retained generated registration and actual service adapter.
	// Removed methods must fail dispatch before any business use case is called.
	networkv1.RegisterPlatformNetworkServiceServer(srv, networkservice.NewPlatformNetworkService(nil))
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = listener.Close()
	})
	conn, err := grpc.NewClient("passthrough:///platform-api-surface",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	registered := srv.GetServiceInfo()
	if _, ok := registered["network.v1.PlatformDeviceRetirementService"]; ok {
		t.Fatal("retired service remains registered")
	}
	for _, method := range registered["network.v1.PlatformNetworkService"].Methods {
		switch method.Name {
		case "ListNodeInterfaces", "AdoptNetworkDevice", "GetNetworkDevice":
			t.Fatalf("retired method remains in generated registration: %s", method.Name)
		}
	}
	for _, method := range []string{
		"/network.v1.PlatformNetworkService/ListNodeInterfaces",
		"/network.v1.PlatformNetworkService/AdoptNetworkDevice",
		"/network.v1.PlatformNetworkService/GetNetworkDevice",
		"/network.v1.PlatformDeviceRetirementService/RetireNetworkDevice",
		"/network.v1.PlatformDeviceRetirementService/GetNetworkDeviceRetirement",
	} {
		t.Run(method, func(t *testing.T) {
			if _, ok := governancePlatformMethods[method]; ok {
				t.Fatal("retired method remains in the governance allowlist")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := conn.Invoke(ctx, method, &emptypb.Empty{}, &emptypb.Empty{}); status.Code(err) != codes.Unimplemented {
				t.Fatalf("retired method dispatched: code=%s err=%v", status.Code(err), err)
			}
		})
	}
}

func TestRetiredDeviceMessagesAreAbsentAndFieldsReserved(t *testing.T) {
	for _, name := range []string{
		"NodeInterface", "NetworkDevice",
		"ListNodeInterfacesRequest", "ListNodeInterfacesResponse",
		"AdoptNetworkDeviceRequest", "AdoptNetworkDeviceResponse",
		"GetNetworkDeviceRequest", "GetNetworkDeviceResponse",
		"PlatformDeviceRetirementService", "DeviceRetirement",
		"RetireNetworkDeviceRequest", "RetireNetworkDeviceResponse",
		"GetNetworkDeviceRetirementRequest", "GetNetworkDeviceRetirementResponse",
	} {
		if _, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName("network.v1." + name)); err == nil {
			t.Errorf("retired public descriptor remains: %s", name)
		}
	}
	for _, tc := range []struct {
		message protoreflect.MessageDescriptor
		number  protoreflect.FieldNumber
		name    protoreflect.Name
	}{
		{(&networkv1.PlatformResource{}).ProtoReflect().Descriptor(), 14, "device"},
		{(&networkv1.VirtualSwitchNode{}).ProtoReflect().Descriptor(), 3, "mac"},
	} {
		if tc.message.Fields().ByNumber(tc.number) != nil || tc.message.Fields().ByName(tc.name) != nil ||
			!tc.message.ReservedRanges().Has(tc.number) || !tc.message.ReservedNames().Has(tc.name) {
			t.Errorf("removed field is not reserved: %s.%s (%d)", tc.message.FullName(), tc.name, tc.number)
		}
	}
}
