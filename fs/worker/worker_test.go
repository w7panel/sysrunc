package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/w7panel/sysrunc/fs/handler/implementations"
)

func TestFuseRepairCommand(t *testing.T) {
	in, out := io.Pipe()
	var response bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- RunWithRepair(context.Background(), Fuse, in, &response, nil,
			func(_ context.Context, req RepairRequest) ([]string, error) {
				if req.ContainerID != "c" || req.PID != 42 || len(req.Devices) != 1 {
					t.Errorf("wrong repair request: %+v", req)
				}
				return []string{"/proc"}, nil
			})
	}()
	if err := send(&process{input: out}, message{Action: "add", Container: Container{ID: "c", PID: 42}}); err != nil {
		t.Fatal(err)
	}
	if err := send(&process{input: out}, message{Action: "repair", Repair: RepairRequest{
		PID: 42, ContainerID: "c", Devices: []Device{{Major: 0, Minor: 1}},
	}}); err != nil {
		t.Fatal(err)
	}
	out.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var got repairResponse
	if err := json.NewDecoder(&response).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || len(got.Paths) != 1 || got.Paths[0] != "/proc" {
		t.Fatalf("wrong repair response: %+v", got)
	}
}

func TestFuseRepairRequiresLiveContainerTask(t *testing.T) {
	var input, output bytes.Buffer
	for _, command := range []message{
		{Action: "repair", Repair: RepairRequest{ContainerID: "c"}},
		{Action: "add", Container: Container{ID: "c", PID: 42}},
		{Action: "repair", Repair: RepairRequest{ContainerID: "c"}},
		{Action: "remove", Container: Container{ID: "c"}},
		{Action: "repair", Repair: RepairRequest{ContainerID: "c"}},
	} {
		if err := json.NewEncoder(&input).Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	var calls int
	if err := RunWithRepair(context.Background(), Fuse, &input, &output, nil,
		func(_ context.Context, _ RepairRequest) ([]string, error) {
			calls++
			return []string{"/proc"}, nil
		}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 3; i++ {
		var response repairResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if response.Error != "" || len(response.Paths) != 1 {
				t.Fatalf("live repair response = %+v", response)
			}
		} else if response.Error == "" {
			t.Fatalf("inactive repair response %d = %+v", i, response)
		}
	}
	if calls != 1 {
		t.Fatalf("repair calls = %d, want 1", calls)
	}
}

func TestFuseWorkerMountpointLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "container")
	var input, output bytes.Buffer
	if err := json.NewEncoder(&input).Encode(message{Action: "pre_register", Container: Container{ID: "container", NetNS: "net:[123]"}}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"prepare_mountpoint", "remove_mountpoint"} {
		if err := json.NewEncoder(&input).Encode(message{Action: action, Path: path, Container: Container{ID: "container"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Fuse, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 2; i++ {
		var response repairResponse
		if err := decoder.Decode(&response); err != nil || response.Error != "" {
			t.Fatalf("mountpoint operation %d = %+v, %v", i, response, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mountpoint remains after worker removal: %v", err)
	}
}

func TestFuseWorkerRejectsUnregisteredMountpointWithoutExiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "container")
	var input, output bytes.Buffer
	encoder := json.NewEncoder(&input)
	for _, command := range []message{
		{Action: "prepare_mountpoint", Path: path, Container: Container{ID: "container"}},
		{Action: "pre_register", Container: Container{ID: "container"}},
		{Action: "prepare_mountpoint", Path: path, Container: Container{ID: "container"}},
	} {
		if err := encoder.Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Fuse, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var response repairResponse
	if err := decoder.Decode(&response); err != nil || response.Error == "" {
		t.Fatalf("unregistered mountpoint response = %+v, %v", response, err)
	}
	response = repairResponse{}
	if err := decoder.Decode(&response); err != nil || response.Error != "" {
		t.Fatalf("registered mountpoint response = %+v, %v", response, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestPreRegisterDoesNotReplaceRegisteredContainer(t *testing.T) {
	s := NewSupervisor(context.Background(), "")
	if err := s.PreRegister("one", "net:[123]"); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(Container{ID: "one", PID: 42}); err != nil {
		t.Fatal(err)
	}
	if err := s.PreRegister("one", "net:[456]"); err == nil {
		t.Fatal("duplicate pre-registration succeeded")
	}
	s.AbortPreRegister("one")
	if s.containers["one"].PID != 42 || s.preRegs["one"] != "net:[123]" {
		t.Fatalf("registered container changed: %+v, %q", s.containers["one"], s.preRegs["one"])
	}
}

func TestProcUUIDRunsOnlyForRegisteredContainer(t *testing.T) {
	var input, output bytes.Buffer
	encoder := json.NewEncoder(&input)
	for _, command := range []message{
		{Action: "read_uuid", Container: Container{ID: "c"}},
		{Action: "add", Container: Container{ID: "c", PID: int32(os.Getpid()), Ctime: time.Now().Add(-time.Minute)}},
		{Action: "read_uuid", Container: Container{ID: "c"}},
		{Action: "read_diskstats", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "read_swaps", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "read_cpuinfo", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "read_meminfo", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "read_uptime", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "read_stat", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "remove", Container: Container{ID: "c"}},
		{Action: "read_cpuinfo", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "read_meminfo", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "read_uptime", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
		{Action: "read_stat", Container: Container{ID: "c"}, PID: uint32(os.Getpid())},
	} {
		if err := encoder.Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Proc, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 12; i++ {
		var response repairResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if response.Error != "" || len(bytes.TrimSpace(response.Data)) != 36 {
				t.Fatalf("registered UUID response = %+v", response)
			}
		} else if i == 2 {
			if response.Error != "" || len(response.Data) == 0 {
				t.Fatalf("registered diskstats response = %+v", response)
			}
		} else if i == 3 {
			if response.Error != "" || !bytes.HasPrefix(response.Data, []byte("Filename")) {
				t.Fatalf("registered swaps response = %+v", response)
			}
		} else if i == 4 {
			if response.Error != "" || !bytes.Contains(response.Data, []byte("processor")) {
				t.Fatalf("registered cpuinfo response = %+v", response)
			}
		} else if i == 5 {
			if response.Error != "" || !bytes.HasPrefix(response.Data, []byte("MemTotal:")) {
				t.Fatalf("registered meminfo response = %+v", response)
			}
		} else if i == 6 {
			if response.Error != "" || !bytes.Contains(response.Data, []byte(" ")) {
				t.Fatalf("registered uptime response = %+v", response)
			}
		} else if i == 7 {
			if response.Error != "" || !bytes.HasPrefix(response.Data, []byte("cpu ")) {
				t.Fatalf("registered stat response = %+v", response)
			}
		} else if response.Error == "" || len(response.Data) != 0 {
			t.Fatalf("unregistered UUID response %d = %+v", i, response)
		}
	}
}

func TestProcSysinfoMemoryUsesRegisteredTask(t *testing.T) {
	var input, output bytes.Buffer
	encode := json.NewEncoder(&input)
	pid := uint32(os.Getpid())
	for _, command := range []message{
		{Action: "add", Container: Container{ID: "sysinfo", PID: int32(pid)}},
		{Action: "read_sysinfo_memory", Container: Container{ID: "sysinfo"}, PID: pid},
		{Action: "remove", Container: Container{ID: "sysinfo"}},
		{Action: "read_sysinfo_memory", Container: Container{ID: "sysinfo"}, PID: pid},
	} {
		if err := encode.Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Proc, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	decode := json.NewDecoder(&output)
	var registered, removed repairResponse
	if err := decode.Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if err := decode.Decode(&removed); err != nil {
		t.Fatal(err)
	}
	if registered.Error != "" || len(registered.Data) != 0 || removed.Error == "" {
		t.Fatalf("sysinfo responses: registered=%+v removed=%+v", registered, removed)
	}
	if len(registered.Data) != 0 && !json.Valid(registered.Data) {
		t.Fatalf("invalid sysinfo memory response: %q", registered.Data)
	}
}

func TestProcVirtualSysctlStateFollowsContainer(t *testing.T) {
	const path = "/proc/sys/net/core/default_qdisc"
	var input, output bytes.Buffer
	encode := json.NewEncoder(&input)
	for _, command := range []message{
		{Action: "add", Container: Container{ID: "one"}},
		{Action: "add", Container: Container{ID: "two"}},
		{Action: "read_virtual_sysctl", Container: Container{ID: "one"}, Path: path},
		{Action: "set_virtual_sysctl", Container: Container{ID: "one"}, Path: path, Data: []byte("fq\n")},
		{Action: "read_virtual_sysctl", Container: Container{ID: "one"}, Path: path},
		{Action: "read_virtual_sysctl", Container: Container{ID: "two"}, Path: path},
		{Action: "remove", Container: Container{ID: "one"}},
		{Action: "add", Container: Container{ID: "one"}},
		{Action: "read_virtual_sysctl", Container: Container{ID: "one"}, Path: path},
	} {
		if err := encode.Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Proc, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	decode := json.NewDecoder(&output)
	for i, want := range []string{"fq_codel\n", "", "fq\n", "fq_codel\n", "fq_codel\n"} {
		var response repairResponse
		if err := decode.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Error != "" || string(response.Data) != want {
			t.Fatalf("response %d = %+v, want %q", i, response, want)
		}
	}
}

func TestProcHostBackedVirtualSysctlStartsUnseeded(t *testing.T) {
	const path = "/proc/sys/vm/overcommit_memory"
	var input, output bytes.Buffer
	encode := json.NewEncoder(&input)
	for _, command := range []message{
		{Action: "add", Container: Container{ID: "one"}},
		{Action: "add", Container: Container{ID: "two"}},
		{Action: "read_virtual_sysctl", Container: Container{ID: "one"}, Path: path},
		{Action: "set_virtual_sysctl", Container: Container{ID: "one"}, Path: path, Data: []byte("2\n")},
		{Action: "read_virtual_sysctl", Container: Container{ID: "one"}, Path: path},
		{Action: "read_virtual_sysctl", Container: Container{ID: "two"}, Path: path},
		{Action: "remove", Container: Container{ID: "one"}},
		{Action: "add", Container: Container{ID: "one"}},
		{Action: "read_virtual_sysctl", Container: Container{ID: "one"}, Path: path},
	} {
		if err := encode.Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Proc, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	decode := json.NewDecoder(&output)
	for i, want := range []string{"", "", "2\n", "", ""} {
		var response repairResponse
		if err := decode.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Error != "" || string(response.Data) != want {
			t.Fatalf("response %d = %+v, want %q", i, response, want)
		}
	}
}

func TestProcWorkerReadResourceUsesRegisteredContainer(t *testing.T) {
	var input, output bytes.Buffer
	p := uint32(os.Getpid())
	for _, command := range []message{
		{Action: "add", Container: Container{ID: "c", PID: int32(p)}},
		{Action: "read_resource", Container: Container{ID: "c"}, PID: p, Path: "/proc/pressure/cpu"},
		{Action: "read_resource", Container: Container{ID: "c"}, PID: p, Path: "/sys/devices/system/cpu/online"},
		{Action: "read_resource", Container: Container{ID: "c"}, PID: p, Path: "/proc/sys/kernel/cap_last_cap"},
		{Action: "read_resource", Container: Container{ID: "c"}, PID: p, Path: "/proc/sys/kernel/osrelease"},
		{Action: "read_resource", Container: Container{ID: "c"}, PID: p, Path: "/proc/pressure/unknown"},
		{Action: "remove", Container: Container{ID: "c"}},
		{Action: "read_resource", Container: Container{ID: "c"}, PID: p, Path: "/proc/pressure/cpu"},
	} {
		if err := json.NewEncoder(&input).Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Proc, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 6; i++ {
		var response repairResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if i < 4 && (response.Error != "" || len(response.Data) == 0) {
			t.Fatalf("resource response %d = %+v", i, response)
		}
		if i >= 4 && response.Error == "" {
			t.Fatalf("unsupported or unregistered response %d = %+v", i, response)
		}
	}
}

func TestProcWorkerProductUUIDUsesContainerIdentity(t *testing.T) {
	const path = "/sys/devices/virtual/dmi/id/product_uuid"
	var input, output bytes.Buffer
	pid := uint32(os.Getpid())
	for _, command := range []message{
		{Action: "add", Container: Container{ID: "012345678901-first", PID: int32(pid)}},
		{Action: "add", Container: Container{ID: "abcdefabcdef-second", PID: int32(pid)}},
		{Action: "read_resource", Container: Container{ID: "012345678901-first"}, PID: pid, Path: path},
		{Action: "read_resource", Container: Container{ID: "abcdefabcdef-second"}, PID: pid, Path: path},
		{Action: "remove", Container: Container{ID: "012345678901-first"}},
		{Action: "read_resource", Container: Container{ID: "012345678901-first"}, PID: pid, Path: path},
	} {
		if err := json.NewEncoder(&input).Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Proc, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	hostUUID, err := implementations.ReadHostUUID()
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for _, id := range []string{"012345678901-first", "abcdefabcdef-second"} {
		var response repairResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		want := implementations.ContainerProductUUID(hostUUID, id) + "\n"
		if response.Error != "" || string(response.Data) != want {
			t.Fatalf("%s product UUID = %+v; want %q", id, response, want)
		}
	}
	var removed repairResponse
	if err := decoder.Decode(&removed); err != nil || removed.Error == "" {
		t.Fatalf("removed container response = %+v, %v", removed, err)
	}
}

func TestProcWorkerSlabinfoRead(t *testing.T) {
	if _, err := os.ReadFile("/proc/slabinfo"); err != nil {
		t.Skipf("slabinfo backing file unavailable: %v", err)
	}
	var input, output bytes.Buffer
	pid := uint32(os.Getpid())
	for _, command := range []message{
		{Action: "add", Container: Container{ID: "slabinfo-test", PID: int32(pid)}},
		{Action: "read_resource", Container: Container{ID: "slabinfo-test"}, PID: pid, Path: "/proc/slabinfo"},
	} {
		if err := json.NewEncoder(&input).Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Proc, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	var response repairResponse
	if err := json.NewDecoder(&output).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" || !bytes.HasPrefix(response.Data, []byte("slabinfo - version:")) {
		t.Fatalf("worker slabinfo response = %+v", response)
	}
}

func TestProcWorkerCreationTimeUpdatePreservesRegistration(t *testing.T) {
	var input, output bytes.Buffer
	pid := uint32(os.Getpid())
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now().Add(-3 * time.Second)
	for _, command := range []message{
		{Action: "add", Container: Container{ID: "c", PID: int32(pid), Ctime: oldTime}},
		{Action: "update_ctime", Container: Container{ID: "c", Ctime: newTime}},
		{Action: "read_uptime", Container: Container{ID: "c"}, PID: pid},
	} {
		if err := json.NewEncoder(&input).Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Proc, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	var response repairResponse
	if err := json.NewDecoder(&output).Decode(&response); err != nil || response.Error != "" {
		t.Fatalf("updated worker response = %+v, %v", response, err)
	}
	fields := strings.Fields(string(response.Data))
	if len(fields) == 0 {
		t.Fatalf("empty uptime response: %+v", response)
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || uptime < 0 || uptime > 30 {
		t.Fatalf("uptime after update = %v, %v; want recent creation time", uptime, err)
	}
}

func TestTickerWorkerLoadavgRequiresOptInAndRegistration(t *testing.T) {
	var input, output bytes.Buffer
	encoder := json.NewEncoder(&input)
	pid := uint32(os.Getpid())
	for _, command := range []message{
		{Action: "add", Container: Container{ID: "off", PID: int32(pid)}},
		{Action: "read_loadavg", Container: Container{ID: "off"}, PID: pid},
		{Action: "add", Container: Container{ID: "on", PID: int32(pid), Ticker: true}},
		{Action: "read_loadavg", Container: Container{ID: "on"}, PID: pid},
		{Action: "remove", Container: Container{ID: "on"}},
		{Action: "read_loadavg", Container: Container{ID: "on"}, PID: pid},
	} {
		if err := encoder.Encode(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunWithRepair(context.Background(), Ticker, &input, &output, nil, nil); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 3; i++ {
		var response repairResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if response.Error != "" || !bytes.Contains(response.Data, []byte("/")) {
				t.Fatalf("opted-in loadavg response = %+v", response)
			}
		} else if response.Error == "" || len(response.Data) != 0 {
			t.Fatalf("disabled or removed loadavg response %d = %+v", i, response)
		}
	}
}

func TestSupervisorReplaysAfterWorkerExit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands")
	script := filepath.Join(dir, "worker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec cat >> '"+logPath+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(context.Background(), script)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Register(Container{ID: "one", PID: 42}); err != nil {
		t.Fatal(err)
	}
	initialDeadline := time.Now().Add(time.Second)
	for time.Now().Before(initialDeadline) {
		data, _ := os.ReadFile(logPath)
		if countContainerMessages(data, "one") == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := s.children[Proc].cmd.Process.Pid
	if err := s.children[Proc].cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		p := s.children[Proc]
		s.mu.Unlock()
		if p != nil && p.cmd.Process.Pid != before {
			data, _ := os.ReadFile(logPath)
			if count := countContainerMessages(data, "one"); count >= 4 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("proc worker did not restart and replay its registration")
}

func TestSupervisorRequestsDoNotBlockOtherWorkerRoles(t *testing.T) {
	fuseInput, fuseWriter := io.Pipe()
	fuseOutput, fuseResponse := io.Pipe()
	procInput, procWriter := io.Pipe()
	procOutput, procResponse := io.Pipe()
	defer fuseInput.Close()
	defer fuseWriter.Close()
	defer fuseOutput.Close()
	defer fuseResponse.Close()
	defer procInput.Close()
	defer procWriter.Close()
	defer procOutput.Close()
	defer procResponse.Close()

	s := &Supervisor{children: map[Role]*process{
		Fuse: {input: fuseWriter, output: fuseOutput},
		Proc: {input: procWriter, output: procOutput},
	}}
	fuseReceived := make(chan struct{})
	releaseFuse := make(chan struct{})
	go func() {
		var command message
		_ = json.NewDecoder(fuseInput).Decode(&command)
		close(fuseReceived)
		<-releaseFuse
		_ = json.NewEncoder(fuseResponse).Encode(repairResponse{ID: command.ID, Paths: []string{"/proc"}})
	}()
	go func() {
		var command message
		_ = json.NewDecoder(procInput).Decode(&command)
		_ = json.NewEncoder(procResponse).Encode(repairResponse{ID: command.ID, Data: []byte("ready")})
	}()
	fuseDone := make(chan error, 1)
	go func() {
		_, err := s.request(context.Background(), Fuse, message{Action: "repair"})
		fuseDone <- err
	}()
	<-fuseReceived
	procDone := make(chan error, 1)
	go func() {
		response, err := s.request(context.Background(), Proc, message{Action: "read_resource"})
		if err == nil && string(response.Data) != "ready" {
			err = fmt.Errorf("proc response = %q", response.Data)
		}
		procDone <- err
	}()
	select {
	case err := <-procDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(releaseFuse)
		<-fuseDone
		t.Fatal("proc request blocked behind the FUSE worker")
	}
	close(releaseFuse)
	if err := <-fuseDone; err != nil {
		t.Fatal(err)
	}
}

func TestFuseRequestsAcrossContainersDoNotBlock(t *testing.T) {
	workerInput, managerInput := io.Pipe()
	managerOutput, workerOutput := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer workerInput.Close()
	defer managerInput.Close()
	defer managerOutput.Close()
	defer workerOutput.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- RunWithRepair(ctx, Fuse, workerInput, workerOutput, nil,
			func(_ context.Context, req RepairRequest) ([]string, error) {
				if req.ContainerID == "slow" {
					close(started)
					<-release
				}
				return []string{"/proc/" + req.ContainerID}, nil
			})
	}()
	p := &process{input: managerInput, output: managerOutput}
	s := &Supervisor{children: map[Role]*process{Fuse: p}}
	for _, id := range []string{"slow", "fast"} {
		p.mu.Lock()
		err := send(p, message{Action: "add", Container: Container{ID: id}})
		p.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	slowDone := make(chan error, 1)
	go func() {
		_, err := s.Repair(context.Background(), RepairRequest{ContainerID: "slow"})
		slowDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow repair did not start")
	}
	fastDone := make(chan error, 1)
	go func() {
		paths, err := s.Repair(context.Background(), RepairRequest{ContainerID: "fast"})
		if err == nil && (len(paths) != 1 || paths[0] != "/proc/fast") {
			err = fmt.Errorf("fast repair paths = %v", paths)
		}
		fastDone <- err
	}()
	select {
	case err := <-fastDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fast container blocked behind slow repair")
	}
	close(release)
	if err := <-slowDone; err != nil {
		t.Fatal(err)
	}
	managerInput.Close()
	if err := <-workerDone; err != nil {
		t.Fatal(err)
	}
}

func TestFusePreRegistrationReplayedAfterWorkerExit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands")
	script := filepath.Join(dir, "worker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec cat >> '"+logPath+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(context.Background(), script)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PreRegister("one", "net:[123]"); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(Container{ID: "one", PID: 42}); err != nil {
		t.Fatal(err)
	}
	initialDeadline := time.Now().Add(time.Second)
	for time.Now().Before(initialDeadline) {
		data, _ := os.ReadFile(logPath)
		if bytes.Count(data, []byte(`"action":"pre_register"`)) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := s.children[Fuse].cmd.Process.Pid
	if err := s.children[Fuse].cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		p := s.children[Fuse]
		s.mu.Unlock()
		if p != nil && p.cmd.Process.Pid != before {
			data, _ := os.ReadFile(logPath)
			if bytes.Count(data, []byte(`"action":"pre_register"`)) >= 2 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("FUSE worker did not replay pre-registration")
}

func countContainerMessages(data []byte, id string) int {
	count := 0
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if bytes.Contains(line, []byte(`"id":"`+id+`"`)) {
			count++
		}
	}
	return count
}

func TestTickerOptInAndUnregister(t *testing.T) {
	reader, writer := io.Pipe()
	started := make(chan string, 3)
	stopped := make(chan string, 3)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := Run(context.Background(), Ticker, reader, func(ctx context.Context, c Container) {
			started <- c.ID
			<-ctx.Done()
			stopped <- c.ID
		}); err != nil {
			t.Errorf("run worker: %v", err)
		}
	}()
	write := func(m message) {
		t.Helper()
		if err := send(&process{input: writer}, m); err != nil {
			t.Fatal(err)
		}
	}
	write(message{Action: "add", Container: Container{ID: "off"}})
	write(message{Action: "add", Container: Container{ID: "on", Ticker: true}})
	select {
	case id := <-started:
		if id != "on" {
			t.Fatalf("unexpected ticker task %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("ticker task did not start")
	}
	write(message{Action: "remove", Container: Container{ID: "on"}})
	select {
	case id := <-stopped:
		if id != "on" {
			t.Fatalf("unexpected stopped task %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("ticker task did not stop")
	}
	writer.Close()
	wg.Wait()
	select {
	case id := <-started:
		t.Fatalf("unexpected task after close: %s", id)
	default:
	}
}

func TestWorkerCancelsOnInputClose(t *testing.T) {
	reader, writer := io.Pipe()
	started := make(chan struct{})
	stopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Proc, reader, func(ctx context.Context, _ Container) {
			close(started)
			<-ctx.Done()
			close(stopped)
		})
	}()
	if err := send(&process{input: writer}, message{Action: "add", Container: Container{ID: "c"}}); err != nil {
		t.Fatal(err)
	}
	<-started
	writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("task was not cancelled")
	}
}
