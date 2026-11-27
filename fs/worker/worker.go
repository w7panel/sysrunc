package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	ID            string    `json:"id"`
	PID           int32     `json:"pid"`
	NetNS         string    `json:"netns,omitempty"`
	UID           uint32    `json:"uid,omitempty"`
	UIDSize       uint32    `json:"uid_size,omitempty"`
	GID           uint32    `json:"gid,omitempty"`
	GIDSize       uint32    `json:"gid_size,omitempty"`
	ProcRO        []string  `json:"proc_ro,omitempty"`
	ProcMask      []string  `json:"proc_mask,omitempty"`
	CgroupData    string    `json:"cgroup_data,omitempty"`
	PidNS         string    `json:"pidns,omitempty"`
	InitStartTime uint64    `json:"init_start_time,omitempty"`
	Ticker        bool      `json:"ticker,omitempty"`
	Ctime         time.Time `json:"ctime,omitempty"`
}

type message struct {
	ID        uint64        `json:"id,omitempty"`
	Action    string        `json:"action"`
	Container Container     `json:"container"`
	PID       uint32        `json:"pid,omitempty"`
	Path      string        `json:"path,omitempty"`
	Data      []byte        `json:"data,omitempty"`
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
	ID    uint64   `json:"id,omitempty"`
	Paths []string `json:"paths,omitempty"`
	Data  []byte   `json:"data,omitempty"`
	Error string   `json:"error,omitempty"`
}

type procRequest struct {
	action   string
	pid      uint32
	path     string
	data     []byte
	ctime    time.Time
	response chan repairResponse
}

var workerHostUUID struct {
	once  sync.Once
	value string
	err   error
}

func workerProductUUID(containerID string) ([]byte, error) {
	workerHostUUID.once.Do(func() {
		workerHostUUID.value, workerHostUUID.err = implementations.ReadHostUUID()
	})
	if workerHostUUID.err != nil {
		return nil, workerHostUUID.err
	}
	return []byte(implementations.ContainerProductUUID(workerHostUUID.value, containerID) + "\n"), nil
}

type fuseRequest struct {
	repair   RepairRequest
	response chan repairResponse
}

func runFuseContainer(ctx context.Context, requests <-chan fuseRequest,
	repair func(context.Context, RepairRequest) ([]string, error)) {
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			paths, err := repair(ctx, request.repair)
			response := repairResponse{Paths: paths}
			if err != nil {
				response.Error = err.Error()
			}
			select {
			case request.response <- response:
			case <-ctx.Done():
			}
		}
	}
}

func runProcContainer(ctx context.Context, container Container, requests <-chan procRequest) {
	virtualSysctls := make(map[string][]byte)
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
			case "read_sysinfo_memory":
				if implementations.ShouldVirtualizeSysinfoForPID(request.pid) {
					if memory, ok := implementations.SysinfoMemoryForPid(request.pid); ok {
						data, err = json.Marshal(memory)
					}
				}
			case "read_uptime":
				data, err = implementations.ReadUptimeForPID(request.pid, container.Ctime)
			case "read_stat":
				data, err = implementations.ReadProcStatForPID(container.ID, request.pid, container.Ctime)
			case "read_resource":
				if request.path == "/sys/devices/virtual/dmi/id/product_uuid" {
					data, err = workerProductUUID(container.ID)
				} else {
					data, err = implementations.ReadWorkerResourceForPID(request.path, request.pid)
				}
			case "update_ctime":
				container.Ctime = request.ctime
			case "read_virtual_sysctl":
				initial, _ := implementations.VirtualSysctlDefault(request.path)
				if !implementations.WorkerVirtualSysctl(request.path) {
					err = fmt.Errorf("unsupported virtual sysctl %q", request.path)
				} else if value, ok := virtualSysctls[request.path]; ok {
					data = append([]byte(nil), value...)
				} else {
					data = initial
				}
			case "set_virtual_sysctl":
				if !implementations.WorkerVirtualSysctl(request.path) {
					err = fmt.Errorf("unsupported virtual sysctl %q", request.path)
				} else {
					virtualSysctls[request.path] = append([]byte(nil), request.data...)
				}
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
	fuseWork := role == Fuse && work == nil
	tickerWork := role == Ticker && work == nil
	if work == nil {
		work = defaultWork(role)
	}
	tasks := make(map[string]context.CancelFunc)
	preRegistered := make(map[string]string)
	procTasks := make(map[string]chan procRequest)
	fuseTasks := make(map[string]chan fuseRequest)
	tickerTasks := make(map[string]chan procRequest)
	taskDone := make(map[string]<-chan struct{})
	var outputMu sync.Mutex
	var pending sync.WaitGroup
	emit := func(id uint64, response repairResponse) error {
		response.ID = id
		outputMu.Lock()
		defer outputMu.Unlock()
		return json.NewEncoder(output).Encode(response)
	}
	dispatch := func(id uint64, work func() repairResponse) error {
		if id == 0 {
			return emit(0, work())
		}
		pending.Add(1)
		go func() {
			defer pending.Done()
			_ = emit(id, work())
		}()
		return nil
	}
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
		pending.Wait()
	}()
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		var command message
		if err := json.Unmarshal(scanner.Bytes(), &command); err != nil {
			return fmt.Errorf("decode worker command: %w", err)
		}
		id := command.Container.ID
		if command.Action == "pre_register" {
			if role != Fuse || id == "" || filepath.Base(id) != id || id == "." || id == ".." {
				return fmt.Errorf("invalid FUSE pre-registration")
			}
			preRegistered[id] = command.Container.NetNS
			continue
		}
		if command.Action == "repair" {
			if role != Fuse || repair == nil {
				return fmt.Errorf("repair unavailable for %s worker", role)
			}
			requests, done := fuseTasks[command.Repair.ContainerID], taskDone[command.Repair.ContainerID]
			if err := dispatch(command.ID, func() repairResponse {
				if requests == nil {
					return repairResponse{Error: fmt.Sprintf("container %s has no FUSE repair task", command.Repair.ContainerID)}
				}
				responseCh := make(chan repairResponse, 1)
				select {
				case requests <- fuseRequest{repair: command.Repair, response: responseCh}:
				case <-done:
					return repairResponse{Error: "FUSE container task stopped"}
				case <-ctx.Done():
					return repairResponse{Error: ctx.Err().Error()}
				}
				select {
				case response := <-responseCh:
					return response
				case <-done:
					return repairResponse{Error: "FUSE container task stopped"}
				case <-ctx.Done():
					return repairResponse{Error: ctx.Err().Error()}
				}
			}); err != nil {
				return err
			}
			continue
		}
		if command.Action == "prepare_mountpoint" || command.Action == "remove_mountpoint" {
			_, registered := preRegistered[id]
			if role != Fuse || !filepath.IsAbs(command.Path) || filepath.Clean(command.Path) != command.Path ||
				id == "" || filepath.Base(command.Path) != id || !registered {
				if err := json.NewEncoder(output).Encode(repairResponse{Error: "invalid FUSE mountpoint command"}); err != nil {
					return err
				}
				continue
			}
			var err error
			if command.Action == "prepare_mountpoint" {
				err = os.MkdirAll(command.Path, 0600)
			} else {
				err = os.Remove(command.Path)
			}
			response := repairResponse{}
			if err != nil {
				response.Error = err.Error()
			}
			if err := emit(command.ID, response); err != nil {
				return err
			}
			continue
		}
		if command.Action == "update_ctime" {
			if role != Proc {
				return fmt.Errorf("creation-time update unavailable for %s worker", role)
			}
			if requests := procTasks[id]; requests != nil {
				responseCh := make(chan repairResponse, 1)
				select {
				case requests <- procRequest{action: command.Action, ctime: command.Container.Ctime, response: responseCh}:
				case <-ctx.Done():
					return ctx.Err()
				}
				select {
				case <-responseCh:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			continue
		}
		if command.Action == "read_uuid" || command.Action == "read_diskstats" || command.Action == "read_swaps" || command.Action == "read_cpuinfo" || command.Action == "read_meminfo" || command.Action == "read_sysinfo_memory" || command.Action == "read_uptime" || command.Action == "read_stat" || command.Action == "read_resource" || command.Action == "read_virtual_sysctl" || command.Action == "set_virtual_sysctl" {
			if role != Proc {
				return fmt.Errorf("%s unavailable for %s worker", command.Action, role)
			}
			requests, done := procTasks[id], taskDone[id]
			if requests == nil {
				if err := emit(command.ID, repairResponse{Error: fmt.Sprintf("container %s is not registered", id)}); err != nil {
					return err
				}
				continue
			}
			if err := dispatch(command.ID, func() repairResponse {
				responseCh := make(chan repairResponse, 1)
				select {
				case requests <- procRequest{action: command.Action, pid: command.PID, path: command.Path, data: command.Data, response: responseCh}:
				case <-done:
					return repairResponse{Error: "proc container task stopped"}
				case <-ctx.Done():
					return repairResponse{Error: ctx.Err().Error()}
				}
				select {
				case response := <-responseCh:
					return response
				case <-done:
					return repairResponse{Error: "proc container task stopped"}
				case <-ctx.Done():
					return repairResponse{Error: ctx.Err().Error()}
				}
			}); err != nil {
				return err
			}
			continue
		}
		if command.Action == "read_loadavg" {
			if role != Ticker {
				return fmt.Errorf("loadavg unavailable for %s worker", role)
			}
			requests, done := tickerTasks[id], taskDone[id]
			active := tasks[id] != nil
			if err := dispatch(command.ID, func() repairResponse {
				response := repairResponse{}
				if !active {
					response.Error = fmt.Sprintf("container %s has no ticker task", id)
				} else if requests != nil {
					responseCh := make(chan repairResponse, 1)
					select {
					case requests <- procRequest{action: command.Action, pid: command.PID, response: responseCh}:
					case <-done:
						return repairResponse{Error: "ticker container task stopped"}
					case <-ctx.Done():
						return repairResponse{Error: ctx.Err().Error()}
					}
					select {
					case response = <-responseCh:
					case <-done:
						return repairResponse{Error: "ticker container task stopped"}
					case <-ctx.Done():
						return repairResponse{Error: ctx.Err().Error()}
					}
				} else {
					var err error
					response.Data, err = implementations.ReadLoadavgForWorker(id, command.PID)
					if err != nil {
						response.Error = err.Error()
					}
				}
				return response
			}); err != nil {
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
			delete(fuseTasks, id)
			delete(tickerTasks, id)
			delete(taskDone, id)
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
			taskDone[id] = taskCtx.Done()
			if procWork {
				requests := make(chan procRequest)
				procTasks[id] = requests
				go runProcContainer(taskCtx, command.Container, requests)
			} else if fuseWork {
				requests := make(chan fuseRequest)
				fuseTasks[id] = requests
				go runFuseContainer(taskCtx, requests, repair)
			} else if tickerWork {
				requests := make(chan procRequest)
				tickerTasks[id] = requests
				go runTickerContainer(taskCtx, command.Container, requests)
			} else {
				go work(taskCtx, command.Container)
			}
		case "remove":
			delete(preRegistered, id)
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
	mu      sync.Mutex
	cmd     *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
	nextID  uint64
	pending map[uint64]chan workerResult
	reader  sync.Once
	readErr error
}

type workerResult struct {
	response repairResponse
	err      error
}

// Supervisor owns desired registrations. Each child is restarted and receives
// the complete set after an unexpected exit.
type Supervisor struct {
	mu             sync.Mutex
	virtualWriteMu sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	executable     string
	children       map[Role]*process
	containers     map[string]Container
	preRegs        map[string]string
	virtual        map[string]map[string][]byte
	dirty          map[string]map[string]bool
}

func NewSupervisor(parent context.Context, executable string) *Supervisor {
	ctx, cancel := context.WithCancel(parent)
	return &Supervisor{ctx: ctx, cancel: cancel, executable: executable,
		children: make(map[Role]*process), containers: make(map[string]Container), preRegs: make(map[string]string),
		virtual: make(map[string]map[string][]byte), dirty: make(map[string]map[string]bool)}
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
	if role == Fuse {
		for id, netns := range s.preRegs {
			if err := send(p, message{Action: "pre_register", Container: Container{ID: id, NetNS: netns}}); err != nil {
				_ = p.input.Close()
				_ = p.cmd.Process.Kill()
				_ = p.cmd.Wait()
				delete(s.children, role)
				return err
			}
		}
	}
	for _, c := range s.containers {
		if err := send(p, message{Action: "add", Container: c}); err != nil {
			_ = p.input.Close()
			_ = p.cmd.Process.Kill()
			_ = p.cmd.Wait()
			delete(s.children, role)
			return err
		}
		if role == Proc {
			for path, data := range s.virtual[c.ID] {
				if err := send(p, message{Action: "set_virtual_sysctl", Container: Container{ID: c.ID}, Path: path, Data: data}); err != nil {
					_ = p.input.Close()
					_ = p.cmd.Process.Kill()
					_ = p.cmd.Wait()
					delete(s.children, role)
					return err
				}
			}
			delete(s.dirty, c.ID)
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
	if netns, ok := s.preRegs[c.ID]; ok {
		c.NetNS = netns
	}
	s.containers[c.ID] = c
	for _, p := range s.children {
		p.mu.Lock()
		if err := send(p, message{Action: "add", Container: c}); err != nil {
			// The watch goroutine will restart this child and replay state.
			p.mu.Unlock()
			continue
		}
		p.mu.Unlock()
	}
	if p := s.children[Proc]; p != nil {
		p.mu.Lock()
		for path, data := range s.virtual[c.ID] {
			_ = send(p, message{Action: "set_virtual_sysctl", Container: Container{ID: c.ID}, Path: path, Data: data})
		}
		p.mu.Unlock()
	}
	return nil
}

// PreRegister records the network namespace relation before the manager asks
// the FUSE worker to create a mountpoint. It is replayed after worker crashes.
func (s *Supervisor) PreRegister(id, netns string) error {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return fmt.Errorf("invalid container ID %q", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.preRegs[id]; exists {
		return fmt.Errorf("container %s already pre-registered", id)
	}
	if _, exists := s.containers[id]; exists {
		return fmt.Errorf("container %s already registered", id)
	}
	s.preRegs[id] = netns
	if p := s.children[Fuse]; p != nil {
		p.mu.Lock()
		_ = send(p, message{Action: "pre_register", Container: Container{ID: id, NetNS: netns}})
		p.mu.Unlock()
	}
	return nil
}

// AbortPreRegister removes only a pending FUSE registration. It never removes
// an already registered container after the state service rejects a duplicate.
func (s *Supervisor) AbortPreRegister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, registered := s.containers[id]; registered {
		return
	}
	delete(s.preRegs, id)
	if p := s.children[Fuse]; p != nil {
		p.mu.Lock()
		_ = send(p, message{Action: "remove", Container: Container{ID: id}})
		p.mu.Unlock()
	}
}

// UpdateCtime keeps the proc worker's per-container clock aligned with a
// post-start runtime update. The updated registration is also replayed after
// an unexpected worker restart.
func (s *Supervisor) UpdateCtime(id string, ctime time.Time) error {
	if id == "" {
		return fmt.Errorf("empty container ID in creation-time update")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	container, ok := s.containers[id]
	if !ok {
		return fmt.Errorf("container %s is not registered", id)
	}
	container.Ctime = ctime
	s.containers[id] = container
	if p := s.children[Proc]; p != nil {
		p.mu.Lock()
		// The worker processes commands in order. A failed pipe is recovered by
		// the supervisor, which replays the updated registration.
		_ = send(p, message{Action: "update_ctime", Container: Container{ID: id, Ctime: ctime}})
		p.mu.Unlock()
	}
	return nil
}

// Repair asks the FUSE worker to replace stale mounts, with the caller's
// context limiting how long a failed worker can hold up fault handling.
func (s *Supervisor) Repair(ctx context.Context, req RepairRequest) ([]string, error) {
	response, err := s.request(ctx, Fuse, message{Action: "repair", Repair: req})
	return response.Paths, err
}

// PrepareMountpoint and RemoveMountpoint move FUSE mount directory lifecycle
// operations into the FUSE worker. The kernel connection remains in the
// manager until request and container state ownership can move together.
func (s *Supervisor) PrepareMountpoint(ctx context.Context, path string) error {
	_, err := s.request(ctx, Fuse, message{Action: "prepare_mountpoint", Path: path, Container: Container{ID: filepath.Base(path)}})
	return err
}

func (s *Supervisor) RemoveMountpoint(ctx context.Context, path string) error {
	_, err := s.request(ctx, Fuse, message{Action: "remove_mountpoint", Path: path, Container: Container{ID: filepath.Base(path)}})
	return err
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

// ReadSysinfoMemory computes the tracee's cgroup memory view in its proc
// worker. A missing limit means the syscall should continue in the kernel.
func (s *Supervisor) ReadSysinfoMemory(ctx context.Context, id string, pid uint32) (implementations.SysinfoMemory, bool, error) {
	if pid == 0 {
		return implementations.SysinfoMemory{}, false, fmt.Errorf("sysinfo requires a tracee PID")
	}
	data, err := s.readProc(ctx, id, "read_sysinfo_memory", pid)
	if err != nil || len(data) == 0 {
		return implementations.SysinfoMemory{}, false, err
	}
	var memory implementations.SysinfoMemory
	if err := json.Unmarshal(data, &memory); err != nil {
		return implementations.SysinfoMemory{}, false, fmt.Errorf("decode sysinfo memory: %w", err)
	}
	return memory, true, nil
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

// ReadResource delegates fixed cgroup-scoped pressure and CPU files to the
// registered container's proc worker goroutine.
func (s *Supervisor) ReadResource(ctx context.Context, id string, pid uint32, path string) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("resource read requires a tracee PID")
	}
	s.mu.Lock()
	_, exists := s.containers[id]
	s.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("container %s is not registered", id)
	}
	response, err := s.request(ctx, Proc, message{Action: "read_resource", Container: Container{ID: id}, PID: pid, Path: path})
	return response.Data, err
}

// ReadVirtualSysctl runs a synthetic, container-local sysctl read in the proc
// worker. A pending write whose worker acknowledgement was lost is returned
// from the supervisor's replay state until the worker has caught up.
func (s *Supervisor) ReadVirtualSysctl(ctx context.Context, id, path string) ([]byte, error) {
	if !implementations.WorkerVirtualSysctl(path) {
		return nil, fmt.Errorf("unsupported virtual sysctl %q", path)
	}
	s.mu.Lock()
	_, registered := s.containers[id]
	value, dirty := s.virtual[id][path], s.dirty[id][path]
	s.mu.Unlock()
	if !registered {
		return nil, fmt.Errorf("container %s is not registered", id)
	}
	if dirty {
		return append([]byte(nil), value...), nil
	}
	response, err := s.request(ctx, Proc, message{Action: "read_virtual_sysctl", Container: Container{ID: id}, Path: path})
	return response.Data, err
}

// SetVirtualSysctl remembers every successful manager-side write so a proc
// worker restart can replay the container-local value before serving reads.
func (s *Supervisor) SetVirtualSysctl(ctx context.Context, id, path string, data []byte) error {
	if !implementations.WorkerVirtualSysctl(path) {
		return fmt.Errorf("unsupported virtual sysctl %q", path)
	}
	// Preserve write order across the replay cache and the worker pipe. Without
	// this lock a later write can reach the worker before an earlier one.
	s.virtualWriteMu.Lock()
	defer s.virtualWriteMu.Unlock()
	s.mu.Lock()
	if _, registered := s.containers[id]; !registered {
		s.mu.Unlock()
		return fmt.Errorf("container %s is not registered", id)
	}
	if s.virtual[id] == nil {
		s.virtual[id] = make(map[string][]byte)
	}
	if s.dirty[id] == nil {
		s.dirty[id] = make(map[string]bool)
	}
	if existing, ok := s.virtual[id][path]; ok && !s.dirty[id][path] && string(existing) == string(data) {
		s.mu.Unlock()
		return nil
	}
	s.virtual[id][path] = append([]byte(nil), data...)
	s.dirty[id][path] = true
	s.mu.Unlock()
	_, err := s.request(ctx, Proc, message{Action: "set_virtual_sysctl", Container: Container{ID: id}, Path: path, Data: data})
	if err == nil {
		s.mu.Lock()
		// Another write may have arrived while the first was in flight.
		if string(s.virtual[id][path]) == string(data) {
			delete(s.dirty[id], path)
		}
		s.mu.Unlock()
	}
	return err
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
	s.mu.Unlock()
	if p == nil {
		return repairResponse{}, fmt.Errorf("%s worker unavailable", role)
	}
	p.reader.Do(func() { go p.readResponses() })
	p.mu.Lock()
	if p.readErr != nil {
		err := p.readErr
		p.mu.Unlock()
		return repairResponse{}, err
	}
	if p.pending == nil {
		p.pending = make(map[uint64]chan workerResult)
	}
	p.nextID++
	command.ID = p.nextID
	result := make(chan workerResult, 1)
	p.pending[command.ID] = result
	err := send(p, command)
	p.mu.Unlock()
	if err != nil {
		p.forget(command.ID)
		return repairResponse{}, err
	}
	select {
	case <-ctx.Done():
		p.forget(command.ID)
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return repairResponse{}, ctx.Err()
	case reply := <-result:
		if reply.err != nil {
			return repairResponse{}, reply.err
		}
		if reply.response.Error != "" {
			return reply.response, fmt.Errorf("%s", reply.response.Error)
		}
		return reply.response, nil
	}
}

func (p *process) forget(id uint64) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func (p *process) readResponses() {
	decoder := json.NewDecoder(p.output)
	for {
		var response repairResponse
		if err := decoder.Decode(&response); err != nil {
			p.mu.Lock()
			p.readErr = err
			for id, ch := range p.pending {
				ch <- workerResult{err: err}
				delete(p.pending, id)
			}
			p.mu.Unlock()
			return
		}
		p.mu.Lock()
		if ch := p.pending[response.ID]; ch != nil {
			ch <- workerResult{response: response}
			delete(p.pending, response.ID)
		}
		p.mu.Unlock()
	}
}

func (s *Supervisor) Unregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.containers, id)
	delete(s.preRegs, id)
	delete(s.virtual, id)
	delete(s.dirty, id)
	for _, p := range s.children {
		p.mu.Lock()
		_ = send(p, message{Action: "remove", Container: Container{ID: id}})
		p.mu.Unlock()
	}
}

func (s *Supervisor) Close() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.children {
		p.mu.Lock()
		_ = p.input.Close()
		p.mu.Unlock()
	}
}
