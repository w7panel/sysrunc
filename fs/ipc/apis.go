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

package ipc

import (
	"github.com/sirupsen/logrus"

	"github.com/w7panel/sysrunc/fs/domain"
	"github.com/w7panel/sysrunc/fs/worker"
	grpc "github.com/w7panel/sysrunc/ipc/sysruncFsGrpc"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

type ipcService struct {
	grpcServer *grpc.Server
	css        domain.ContainerStateServiceIface
	prs        domain.ProcessServiceIface
	ios        domain.IOServiceIface
	workers    *worker.Supervisor
	checkpoint func() error
}

// SetWorkers attaches the process supervisor without changing the public
// IpcServiceIface used by existing service implementations and tests.
func (ips *ipcService) SetWorkers(workers *worker.Supervisor) { ips.workers = workers }

// SetCheckpoint persists completed registrations before their RPC succeeds.
func (ips *ipcService) SetCheckpoint(checkpoint func() error) { ips.checkpoint = checkpoint }

func (ips *ipcService) checkpointState() error {
	if ips.checkpoint == nil {
		return nil
	}
	return ips.checkpoint()
}

func NewIpcService() domain.IpcServiceIface {
	return &ipcService{}
}

func (ips *ipcService) Setup(
	css domain.ContainerStateServiceIface,
	prs domain.ProcessServiceIface,
	ios domain.IOServiceIface,
	fuseMp string) {

	ips.css = css
	ips.prs = prs
	ips.ios = ios

	// Instantiate a grpcServer for inter-process communication.
	ips.grpcServer = grpc.NewServer(
		ips,
		&grpc.CallbacksMap{
			grpc.ContainerPreRegisterMessage: ContainerPreRegister,
			grpc.ContainerRegisterMessage:    ContainerRegister,
			grpc.ContainerUnregisterMessage:  ContainerUnregister,
			grpc.ContainerUpdateMessage:      ContainerUpdate,
		},
		fuseMp,
	)

	logrus.Infof("Listening on %v", ips.grpcServer.GetAddr())
}

func (ips *ipcService) Init() error {
	return ips.grpcServer.Init()
}

func ContainerPreRegister(ctx interface{}, data *grpc.ContainerData) error {

	ipcService := ctx.(*ipcService)
	if ipcService.workers != nil {
		if err := ipcService.workers.PreRegister(data.Id, data.Netns); err != nil {
			return err
		}
	}

	err := ipcService.css.ContainerPreRegister(data.Id, data.Netns)
	if err != nil {
		if ipcService.workers != nil {
			ipcService.workers.AbortPreRegister(data.Id)
		}
		return err
	}

	return nil
}

func ContainerRegister(ctx interface{}, data *grpc.ContainerData) error {

	ipcService := ctx.(*ipcService)

	// Create temporary container struct to be passed as reference to containerDB,
	// where the matching (real) container will be identified and then updated.
	cntr := ipcService.css.ContainerCreate(
		data.Id,
		uint32(data.InitPid),
		data.Ctime,
		uint32(data.UidFirst),
		uint32(data.UidSize),
		uint32(data.GidFirst),
		uint32(data.GidSize),
		data.ProcRoPaths,
		data.ProcMaskPaths,
		ipcService.css,
	)

	if seeded, ok := cntr.(domain.ResourceIdentityContainer); ok {
		seeded.SetResourceIdentity(domain.ResourceIdentity{
			CgroupData: data.CgroupData, Pidns: data.Pidns, InitStartTime: data.InitStartTime,
		})
	}

	err := ipcService.css.ContainerRegister(cntr)
	if err != nil {
		return err
	}
	if ipcService.workers != nil {
		if err := ipcService.workers.Register(worker.Container{
			ID: data.Id, PID: data.InitPid, Ticker: data.Ticker, Ctime: data.Ctime,
			UID: uint32(data.UidFirst), UIDSize: uint32(data.UidSize),
			GID: uint32(data.GidFirst), GIDSize: uint32(data.GidSize),
			ProcRO: append([]string(nil), data.ProcRoPaths...), ProcMask: append([]string(nil), data.ProcMaskPaths...),
			CgroupData: data.CgroupData, PidNS: data.Pidns, InitStartTime: data.InitStartTime,
		}); err != nil {
			return err
		}
	}
	if err := ipcService.checkpointState(); err != nil {
		return grpcStatus.Errorf(grpcCodes.Internal, "save FS recovery checkpoint: %v", err)
	}

	return nil
}

func ContainerUnregister(ctx interface{}, data *grpc.ContainerData) error {

	ipcService := ctx.(*ipcService)

	// Identify the container being unregistered.
	cntr := ipcService.css.ContainerLookupById(data.Id)
	if cntr == nil {
		return grpcStatus.Errorf(
			grpcCodes.NotFound,
			"Container %s not found",
			data.Id,
		)
	}

	err := ipcService.css.ContainerUnregister(cntr)
	if err != nil {
		return err
	}
	if ipcService.workers != nil {
		ipcService.workers.Unregister(data.Id)
	}
	if err := ipcService.checkpointState(); err != nil {
		return grpcStatus.Errorf(grpcCodes.Internal, "save FS recovery checkpoint: %v", err)
	}

	return nil
}

func ContainerUpdate(ctx interface{}, data *grpc.ContainerData) error {

	ipcService := ctx.(*ipcService)

	// Create temporary container struct to be passed as reference to containerDB,
	// where the matching (real) container will be identified and then updated.
	cntr := ipcService.css.ContainerCreate(
		data.Id,
		uint32(data.InitPid),
		data.Ctime,
		uint32(data.UidFirst),
		uint32(data.UidSize),
		uint32(data.GidFirst),
		uint32(data.GidSize),
		nil,
		nil,
		ipcService.css,
	)

	err := ipcService.css.ContainerUpdate(cntr)
	if err != nil {
		return err
	}
	if ipcService.workers != nil {
		if err := ipcService.workers.UpdateCtime(data.Id, data.Ctime); err != nil {
			return err
		}
	}
	if err := ipcService.checkpointState(); err != nil {
		return grpcStatus.Errorf(grpcCodes.Internal, "save FS recovery checkpoint: %v", err)
	}

	return nil
}

func (ips *ipcService) Stop() { ips.grpcServer.Stop() }
