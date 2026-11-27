//
// Copyright 2019-2020 Nestybox, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package sysruncFsGrpc

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/golang/protobuf/ptypes"
	pb "github.com/w7panel/sysrunc/ipc/sysruncFsGrpc/protocol"
	"github.com/w7panel/sysrunc/libs/formatter"
	"google.golang.org/grpc"

	"github.com/sirupsen/logrus"
)

var grpcTimeout time.Duration = 20 * time.Second

// Container info passed by the client to the server across the grpc channel
type ContainerData struct {
	CgroupData    string
	Pidns         string
	InitStartTime uint64
	Id            string
	Netns         string
	InitPid       int32
	Hostname      string
	Ctime         time.Time
	UidFirst      int32
	UidSize       int32
	GidFirst      int32
	GidSize       int32
	ProcRoPaths   []string
	ProcMaskPaths []string
	Ticker        bool
}

func unixConnect(addr string, t time.Duration) (net.Conn, error) {
	unixAddr, err := net.ResolveUnixAddr("unix", sysFsGrpcSockAddr)
	conn, err := net.DialUnix("unix", nil, unixAddr)
	return conn, err
}

// Establishes a gRPC connection to sysrunc-fs.
func connect() (*grpc.ClientConn, error) {
	// Set up a connection to the server.
	// TODO: Secure me through TLS.
	conn, err := grpc.Dial(
		sysFsGrpcSockAddr,
		grpc.WithInsecure(),
		grpc.WithDialer(unixConnect),
	)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func invokeStateChannel(ctx context.Context, conn *grpc.ClientConn, method string, request, response interface{}) error {
	return conn.Invoke(ctx, "/protobuf.sysruncStateChannel/"+method, request, response)
}

func containerDataToPbData(data *ContainerData) (*pb.ContainerData, error) {
	pbTime, err := ptypes.TimestampProto(data.Ctime)
	if err != nil {
		return nil, fmt.Errorf("time conversion error: %v", err)
	}

	return &pb.ContainerData{
		Id:            data.Id,
		CgroupData:    data.CgroupData,
		Pidns:         data.Pidns,
		InitStartTime: data.InitStartTime,
		Netns:         data.Netns,
		InitPid:       data.InitPid,
		Ctime:         pbTime,
		UidFirst:      data.UidFirst,
		UidSize:       data.UidSize,
		GidFirst:      data.GidFirst,
		GidSize:       data.GidSize,
		ProcRoPaths:   data.ProcRoPaths,
		ProcMaskPaths: data.ProcMaskPaths,
		Ticker:        data.Ticker,
	}, nil
}

func pbDatatoContainerData(pbdata *pb.ContainerData) (*ContainerData, error) {
	cTime, err := ptypes.Timestamp(pbdata.Ctime)
	if err != nil {
		return nil, fmt.Errorf("time conversion error: %v", err)
	}

	return &ContainerData{
		Id:            pbdata.Id,
		CgroupData:    pbdata.CgroupData,
		Pidns:         pbdata.Pidns,
		InitStartTime: pbdata.InitStartTime,
		Netns:         pbdata.Netns,
		InitPid:       pbdata.InitPid,
		Ctime:         cTime,
		UidFirst:      pbdata.UidFirst,
		UidSize:       pbdata.UidSize,
		GidFirst:      pbdata.GidFirst,
		GidSize:       pbdata.GidSize,
		ProcRoPaths:   pbdata.ProcRoPaths,
		ProcMaskPaths: pbdata.ProcMaskPaths,
		Ticker:        pbdata.Ticker,
	}, nil
}

// Pre-registers container creation in sysrunc-fs. Notice that this
// is a blocking call that can potentially have a minor impact
// on container's boot-up speed.
func SendContainerPreRegistration(data *ContainerData) (err error) {
	// Set up the sysrunc-fs pipeline.
	conn, err := connect()
	if err != nil {
		return fmt.Errorf("failed to connect with sysrunc-fs: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), grpcTimeout)
	defer cancel()

	pbData, err := containerDataToPbData(data)
	if err != nil {
		return fmt.Errorf("convertion to protobuf data failed: %v", err)
	}

	err = invokeStateChannel(ctx, conn, "ContainerPreRegistration", pbData, &pb.Response{})
	if err != nil {
		logrus.Warningf("Container %s pre-registration error: %s",
			formatter.ContainerID{data.Id}, err)
		return fmt.Errorf("failed to pre-register container with sysrunc-fs: %v", err)
	}

	return nil
}

// Registers container creation in sysrunc-fs.
func SendContainerRegistration(data *ContainerData) (err error) {
	// Set up the sysrunc-fs pipeline.
	conn, err := connect()
	if err != nil {
		return fmt.Errorf("failed to connect with sysrunc-fs: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), grpcTimeout)
	defer cancel()

	pbData, err := containerDataToPbData(data)
	if err != nil {
		return fmt.Errorf("convertion to protobuf data failed: %v", err)
	}

	err = invokeStateChannel(ctx, conn, "ContainerRegistration", pbData, &pb.Response{})
	if err != nil {
		logrus.Warningf("Container %s registration error: %s",
			formatter.ContainerID{data.Id}, err)
		return fmt.Errorf("failed to register container with sysrunc-fs: %v", err)
	}

	return nil
}

// Unregisters container from sysrunc-fs.
func SendContainerUnregistration(data *ContainerData) (err error) {
	// Set up the sysrunc-fs pipeline.
	conn, err := connect()
	if err != nil {
		return fmt.Errorf("failed to connect with sysrunc-fs: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), grpcTimeout)
	defer cancel()

	pbData, err := containerDataToPbData(data)
	if err != nil {
		return fmt.Errorf("convertion to protobuf data failed: %v", err)
	}

	err = invokeStateChannel(ctx, conn, "ContainerUnregistration", pbData, &pb.Response{})
	if err != nil {
		return fmt.Errorf("failed to unregister container with sysrunc-fs: %v", err)
	}

	return nil
}

// Sends a container-update message to sysrunc-fs. At this point, we are
// only utilizing this message for a particular case, update the container
// creation-time attribute, but this function can serve more general purposes
// in the future.
func SendContainerUpdate(data *ContainerData) (err error) {
	// Set up the sysrunc-fs pipeline.
	conn, err := connect()
	if err != nil {
		return fmt.Errorf("failed to connect with sysrunc-fs: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), grpcTimeout)
	defer cancel()

	pbData, err := containerDataToPbData(data)
	if err != nil {
		return fmt.Errorf("convertion to protobuf data failed: %v", err)
	}

	err = invokeStateChannel(ctx, conn, "ContainerUpdate", pbData, &pb.Response{})
	if err != nil {
		logrus.Warningf("Container %s update error: %s",
			formatter.ContainerID{data.Id}, err)
		return fmt.Errorf("failed to send container-update message to sysrunc-fs: %w", err)
	}

	return nil
}

func GetMountpoint() (string, error) {
	var mpResp *pb.MountpointResp

	// Set up the sysrunc-fs pipeline.
	conn, err := connect()
	if err != nil {
		return "", fmt.Errorf("failed to connect with sysrunc-fs: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), grpcTimeout)
	defer cancel()

	mpResp = &pb.MountpointResp{}
	err = invokeStateChannel(ctx, conn, "GetMountpoint", &pb.MountpointReq{}, mpResp)
	if err != nil {
		return "", fmt.Errorf("failed to get sysrunc-fs mountpoint: %v", err)
	}

	return mpResp.GetMountpoint(), nil
}
