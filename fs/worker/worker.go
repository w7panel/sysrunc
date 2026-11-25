package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/w7panel/sysrunc/fs/handler/implementations"
)

// Role identifies an independently supervised sysrunc-fs worker process.
type Role string

const (
	Proc   Role = "proc"
	Fuse   Role = "fuse"
	Ticker Role = "ticker"
)

type Container struct {
	ID     string    `json:"id"`
	PID    int32     `json:"pid"`
	Ticker bool      `json:"ticker,omitempty"`
	Ctime  time.Time `json:"ctime,omitempty"`
}

type message struct {
	Action    string        `json:"action"`
	Container Container     `json:"container"`
	PID       uint32        `json:"pid,omitempty"`
	Repair    RepairRequest `json:"repair,omitempty"`
}

type Device struct{ Major, Minor uint32 }

type RepairRequest struct {
	PID         int      `json:"pid"`
	Root        string   `json:"root"`
	ContainerID string   `json:"container_id"`
	Devices     []Device `json:"devices"`
}

type repairResponse struct {
	Paths []string `json:"paths,omitempty"`
	Data  []byte   `json:"data,omitempty"`
	Error string   `json:"error,omitempty"`
}

type procRequest struct {
	action   string
	pid      uint32
	response chan repairResponse
}

func runProcContainer(ctx context.Context, container Container, requests <-chan procRequest) {
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			var data []byte
			var err error
			switch request.action {
			case "read_uuid":
				data, err = os.ReadFile("/proc/sys/kernel/random/uuid")
			case "read_diskstats":
				data, err = implementations.ReadDiskstatsForPID(request.pid)
			case "read_swaps":
				data, err = implementations.ReadSwapsForPID(request.pid)
			case "read_cpuinfo":
				data, err = implementations.ReadCPUInfoForPID(request.pid)
			case "read_meminfo":
				data, err = implementations.ReadMemInfoForPID(request.pid)
			case "read_uptime":
				data, err = implementations.ReadUptimeForPID(request.pid, container.Ctime)
			case "read_stat":
				data, err = implementations.ReadProcStatForPID(container.ID, request.pid, container.Ctime)
			default:
				err = fmt.Errorf("unknown proc request %q", request.action)
			}
			response := repairResponse{Data: data}
			if err != nil {
				response.Error = err.Error()
			}
			request.response <- response
		}
	}
}

func runTickerContainer(ctx context.Context, container Container, requests <-chan procRequest) {
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			data, err := implementations.ReadLoadavgForWorker(container.ID, request.pid)
			response := repairResponse{Data: data}
			if err != nil {
				response.Error = err.Error()
			}
			request.response <- response
		}
	}
}

// Run reads manager commands and maintains one cancellable task per container.
// The role-specific work is injected so lifecycle behavior can be tested without
// privileged FUSE mounts or a container runtime.
func Run(ctx context.Context, role Role, input io.Reader, work func(context.Context, Container)) error {
	return RunWithRepair(ctx, role, input, io.Discard, work, nil)
}

// RunWithRepair executes mount repairs only in the FUSE worker. The manager
// retains the event stream and state validation, and applies returned paths.
func RunWithRepair(ctx context.Context, role Role, input io.Reader, output io.Writer,
	work func(context.Context, Container), repair func(context.Context, RepairRequest) ([]string, error)) error {
	if role != Proc && role != Fuse && role != Ticker {
		return fmt.Errorf("unknown worker role %q", role)
	}
	procWork := role == Proc && work == nil
	tickerWork := role == Ticker && work == nil
	if work == nil {
		work = defaultWork(role)
	}
	tasks := make(map[string]context.CancelFunc)
	procTasks := make(map[string]chan procRequest)
	tickerTasks := make(map[string]chan procRequest)
	defer func() {
		for id, cancel := range tasks {
			cancel()
			if procWork {
				implementations.ReleaseProcStatForWorker(id)
			}
			if role == Ticker && tickerWork {
				implementations.ReleaseLoadavgForWorker(id)
			}
		}
	}()
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		var command message
		if err := json.Unmarshal(scanner.Bytes(), &command); err != nil {
			return fmt.Errorf("decode worker command: %w", err)
		}
		id := command.Container.ID
		if command.Action == "repair" {
			if role != Fuse || repair == nil {
				return fmt.Errorf("repair unavailable for %s worker", role)
			}
			paths, err := repair(ctx, command.Repair)
			response := repairResponse{Paths: paths}
			if err != nil {
				response.Error = err.Error()
			}
			if err := json.NewEncoder(output).Encode(response); err != nil {
				return err
			}
			continue
		}
		if command.Action == "read_uuid" || command.Action == "read_diskstats" || command.Action == "read_swaps" || command.Action == "read_cpuinfo" || command.Action == "read_meminfo" || command.Action == "read_uptime" || command.Action == "read_stat" {
			if role != Proc {
				return fmt.Errorf("%s unavailable for %s worker", command.Action, role)
			}
			requests := procTasks[id]
			if requests == nil {
				if err := json.NewEncoder(output).Encode(repairResponse{Error: fmt.Sprintf("container %s is not registered", id)}); err != nil {
					return err
				}
				continue
			}
			responseCh := make(chan repairResponse, 1)
			select {
			case requests <- procRequest{action: command.Action, pid: command.PID, response: responseCh}:
			case <-ctx.Done():
				return ctx.Err()
			}
			var response repairResponse
			select {
			case response = <-responseCh:
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := json.NewEncoder(output).Encode(response); err != nil {
				return err
			}
			continue
		}
		if command.Action == "read_loadavg" {
			if role != Ticker {
				return fmt.Errorf("loadavg unavailable for %s worker", role)
			}
			response := repairResponse{}
			if tasks[id] == nil {
				response.Error = fmt.Sprintf("container %s has no ticker task", id)
			} else if requests := tickerTasks[id]; requests != nil {
				responseCh := make(chan repairResponse, 1)
				select {
				case requests <- procRequest{action: command.Action, pid: command.PID, response: responseCh}:
				case <-ctx.Done():
					return ctx.Err()
				}
				select {
				case response = <-responseCh:
				case <-ctx.Done():
					return ctx.Err()
				}
			} else {
				data, err := implementations.ReadLoadavgForWorker(id, command.PID)
				response.Data = data
				if err != nil {
					response.Error = err.Error()
				}
			}
			if err := json.NewEncoder(output).Encode(response); err != nil {
				return err
			}
			continue
		}
		if id == "" {
			return fmt.Errorf("worker command has empty container ID")
		}
		if cancel := tasks[id]; cancel != nil {
			cancel()
			if procWork {
				implementations.ReleaseProcStatForWorker(id)
			}
			if role == Ticker && tickerWork {
				implementations.ReleaseLoadavgForWorker(id)
			}
			delete(tasks, id)
			delete(procTasks, id)
			delete(tickerTasks, id)
		}
		switch command.Action {
		case "add":
			if role == Ticker && !command.Container.Ticker {
				continue
			}
			if tickerWork && command.Container.PID > 0 {
				_ = implementations.StartLoadavgForWorker(id, uint32(command.Container.PID))
			}
			taskCtx, cancel := context.WithCancel(ctx)
			tasks[id] = cancel
			if procWork {
				requests := make(chan procRequest)
				procTasks[id] = requests
				go runProcContainer(taskCtx, command.Container, requests)
			} else if tickerWork {
				requests := make(chan procRequest)
				tickerTasks[id] = requests
				go runTickerContainer(taskCtx, command.Container, requests)
			} else {
				go work(taskCtx, command.Container)
			}
		case "remove":
		default:
			return fmt.Errorf("unknown worker action %q", command.Action)
		}
	}
	return scanner.Err()
}

func defaultWork(_ Role) func(context.Context, Container) {
	return func(ctx context.Context, _ Container) {
		// Proc reads and ticker sampling are driven by worker commands. FUSE
		// descriptors stay in the manager until they can be transferred safely.
		<-ctx.Done()
	}
}

type process struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output io.ReadCloser
}

// Supervisor owns desired registrations. Each child is restarted and receives
// the complete set after an unexpected exit.
type Supervisor struct {
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	executable string
	children   map[Role]*process
	containers map[string]Container
}

func NewSupervisor(parent context.Context, executable string) *Supervisor {
	ctx, cancel := context.WithCancel(parent)
	return &Supervisor{ctx: ctx, cancel: cancel, executable: executable,
		children: make(map[Role]*process), containers: make(map[string]Container)}
}

func (s *Supervisor) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, role := range []Role{Proc, Fuse, Ticker} {
		if err := s.startLocked(role); err != nil {
			s.cancel()
			return err
		}
	}
	return nil
}

func (s *Supervisor) startLocked(role Role) error {
	cmd := exec.CommandContext(s.ctx, s.executable, "--worker", string(role))
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return err
	}
	p := &process{cmd: cmd, input: input, output: output}
	s.children[role] = p
	for _, c := range s.containers {
		if err := send(p, message{Action: "add", Container: c}); err != nil {
			_ = p.input.Close()
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
			delete(s.children, role)
			return err
		}
	}
	go s.watch(role, p)
	return nil
}

func (s *Supervisor) watch(role Role, p *process) {
	_ = p.cmd.Wait()
	s.mu.Lock()
	if s.ctx.Err() != nil || s.children[role] != p {
		s.mu.Unlock()
		return
	}
	delete(s.children, role)
	s.mu.Unlock()
	for {
		timer := time.NewTimer(time.Second)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		s.mu.Lock()
		if s.ctx.Err() != nil || s.children[role] != nil {
			s.mu.Unlock()
			return
		}
		err := s.startLocked(role)
		s.mu.Unlock()
		if err == nil {
			return
		}
	}
}

func send(p *process, command message) error {
	return json.NewEncoder(p.input).Encode(command)
}

func (s *Supervisor) Register(c Container) error {
	if c.ID == "" {
		return fmt.Errorf("empty container ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[c.ID] = c
	for _, p := range s.children {
		if err := send(p, message{Action: "add", Container: c}); err != nil {
			// The watch goroutine will restart this child and replay state.
			continue
		}
	}
	return nil
}

// Repair asks the FUSE worker to replace stale mounts, with the caller's
// context limiting how long a failed worker can hold up fault handling.
func (s *Supervisor) Repair(ctx context.Context, req RepairRequest) ([]string, error) {
	response, err := s.request(ctx, Fuse, message{Action: "repair", Repair: req})
	return response.Paths, err
}

// ReadUUID delegates the virtual proc UUID read to the proc worker.
func (s *Supervisor) ReadUUID(ctx context.Context, id string) ([]byte, error) {
	return s.readProc(ctx, id, "read_uuid", 0)
}

// ReadDiskstats delegates one tracee's cgroup-scoped diskstats view to its
// container's proc worker goroutine.
func (s *Supervisor) ReadDiskstats(ctx context.Context, id string, pid uint32) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("diskstats requires a tracee PID")
	}
	return s.readProc(ctx, id, "read_diskstats", pid)
}

// ReadSwaps delegates one tracee's cgroup-scoped swap view to its proc worker.
func (s *Supervisor) ReadSwaps(ctx context.Context, id string, pid uint32) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("swaps requires a tracee PID")
	}
	return s.readProc(ctx, id, "read_swaps", pid)
}

// ReadCPUInfo delegates one tracee's CPU view to its proc worker goroutine.
func (s *Supervisor) ReadCPUInfo(ctx context.Context, id string, pid uint32) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("cpuinfo requires a tracee PID")
	}
	return s.readProc(ctx, id, "read_cpuinfo", pid)
}

// ReadMemInfo delegates one tracee's memory view to its proc worker goroutine.
func (s *Supervisor) ReadMemInfo(ctx context.Context, id string, pid uint32) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("meminfo requires a tracee PID")
	}
	return s.readProc(ctx, id, "read_meminfo", pid)
}

// ReadUptime delegates one tracee's uptime view to its proc worker goroutine.
func (s *Supervisor) ReadUptime(ctx context.Context, id string, pid uint32) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("uptime requires a tracee PID")
	}
	return s.readProc(ctx, id, "read_uptime", pid)
}

// ReadStat delegates one tracee's cumulative CPU view to its proc worker.
func (s *Supervisor) ReadStat(ctx context.Context, id string, pid uint32) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("stat requires a tracee PID")
	}
	return s.readProc(ctx, id, "read_stat", pid)
}

// TickerEnabled reports the registered opt-in without sending a worker request.
func (s *Supervisor) TickerEnabled(id string) bool {
	s.mu.Lock()
	container, exists := s.containers[id]
	s.mu.Unlock()
	return exists && container.Ticker
}

// ReadLoadavg delegates to the ticker worker only for opted-in containers.
func (s *Supervisor) ReadLoadavg(ctx context.Context, id string, pid uint32) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("loadavg requires a tracee PID")
	}
	s.mu.Lock()
	container, exists := s.containers[id]
	s.mu.Unlock()
	if !exists || !container.Ticker {
		return nil, fmt.Errorf("container %s has no ticker task", id)
	}
	response, err := s.request(ctx, Ticker, message{Action: "read_loadavg", Container: Container{ID: id}, PID: pid})
	return response.Data, err
}

func (s *Supervisor) readProc(ctx context.Context, id, action string, pid uint32) ([]byte, error) {
	s.mu.Lock()
	_, exists := s.containers[id]
	s.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("container %s is not registered", id)
	}
	response, err := s.request(ctx, Proc, message{Action: action, Container: Container{ID: id}, PID: pid})
	return response.Data, err
}

func (s *Supervisor) request(ctx context.Context, role Role, command message) (repairResponse, error) {
	s.mu.Lock()
	p := s.children[role]
	if p == nil {
		s.mu.Unlock()
		return repairResponse{}, fmt.Errorf("%s worker unavailable", role)
	}
	if err := send(p, command); err != nil {
		s.mu.Unlock()
		return repairResponse{}, err
	}
	result := make(chan repairResponse, 1)
	decodeErr := make(chan error, 1)
	go func() {
		var response repairResponse
		if err := json.NewDecoder(p.output).Decode(&response); err != nil {
			decodeErr <- err
			return
		}
		result <- response
	}()
	// Serialize commands and responses; a worker restart replays registrations.
	defer s.mu.Unlock()
	select {
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		return repairResponse{}, ctx.Err()
	case err := <-decodeErr:
		return repairResponse{}, err
	case response := <-result:
		if response.Error != "" {
			return response, fmt.Errorf("%s", response.Error)
		}
		return response, nil
	}
}

func (s *Supervisor) Unregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.containers, id)
	for _, p := range s.children {
		_ = send(p, message{Action: "remove", Container: Container{ID: id}})
	}
}

func (s *Supervisor) Close() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.children {
		_ = p.input.Close()
	}
}
