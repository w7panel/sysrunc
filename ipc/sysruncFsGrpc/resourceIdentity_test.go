package sysruncFsGrpc

import (
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	pb "github.com/w7panel/sysrunc/ipc/sysruncFsGrpc/protocol"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestResourceIdentityRoundTrip(t *testing.T) {
	for _, input := range []ContainerData{
		{Id: "legacy", Ctime: time.Unix(123, 0)},
		{Id: "seeded", Ctime: time.Unix(123, 0), CgroupData: "0::/containers/test\n", Pidns: "pid:[123]", InitStartTime: 456},
	} {
		wire, err := containerDataToPbData(&input)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := proto.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		var decoded pb.ContainerData
		if err := proto.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		got, err := pbDatatoContainerData(&decoded)
		if err != nil {
			t.Fatal(err)
		}
		if got.Id != input.Id || got.CgroupData != input.CgroupData || got.Pidns != input.Pidns || got.InitStartTime != input.InitStartTime {
			t.Fatalf("round trip: %#v, want %#v", got, input)
		}
	}
}

func TestLegacyWireServiceName(t *testing.T) {
	for _, name := range []string{"sysruncStateChannel", "sysboxStateChannel"} {
		service := pb.File_sysruncFsProtobuf_proto.Services().ByName(protoreflect.Name(name))
		if service == nil || string(service.FullName()) != "protobuf."+name {
			t.Fatalf("gRPC service %s changed: %v", name, service)
		}
		if service.Methods().ByName("ContainerRegistration") == nil {
			t.Fatalf("%s registration method missing", name)
		}
	}
}
