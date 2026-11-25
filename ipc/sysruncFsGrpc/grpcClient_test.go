package sysruncFsGrpc

import (
	"context"
	"net"
	"testing"

	pb "github.com/w7panel/sysrunc/ipc/sysruncFsGrpc/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type unimplementedNewService struct{ *Server }

func (*unimplementedNewService) GetMountpoint(context.Context, *pb.MountpointReq) (*pb.MountpointResp, error) {
	return nil, status.Error(codes.Unimplemented, "method unavailable")
}

func TestStateChannelRolloutCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name       string
		newService bool
		legacy     bool
		badMethod  bool
		want       string
		wantCode   codes.Code
	}{
		{name: "new service", newService: true, want: "new"},
		{name: "previous daemon", legacy: true, want: "legacy"},
		{name: "method error is not retried", newService: true, legacy: true, badMethod: true, wantCode: codes.Unimplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener := bufconn.Listen(4096)
			server := grpc.NewServer()
			if tc.newService {
				var handler pb.SysruncStateChannelServer = &Server{FuseMp: "new"}
				if tc.badMethod {
					handler = &unimplementedNewService{Server: &Server{FuseMp: "new"}}
				}
				pb.RegisterSysruncStateChannelServer(server, handler)
			}
			if tc.legacy {
				pb.RegisterSysboxStateChannelServer(server, &Server{FuseMp: "legacy"})
			}
			go server.Serve(listener)
			defer server.Stop()
			defer listener.Close()
			conn, err := grpc.DialContext(context.Background(), "bufnet",
				grpc.WithInsecure(),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			response := &pb.MountpointResp{}
			err = invokeStateChannel(context.Background(), conn, "GetMountpoint", &pb.MountpointReq{}, response)
			if status.Code(err) != tc.wantCode || (err == nil && response.GetMountpoint() != tc.want) {
				t.Fatalf("mountpoint=%q, err=%v; want %q, code %s", response.GetMountpoint(), err, tc.want, tc.wantCode)
			}
		})
	}
}
