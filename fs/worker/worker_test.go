package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
