package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	systemd "github.com/coreos/go-systemd/daemon"
	"github.com/moby/sys/mountinfo"
	"github.com/sirupsen/logrus"
	"github.com/w7panel/sysrunc/fs/domain"
	"github.com/w7panel/sysrunc/fs/fuse"
	"github.com/w7panel/sysrunc/fs/recovery"
	"github.com/w7panel/sysrunc/fs/state"
	"github.com/w7panel/sysrunc/fs/worker"
	libutils "github.com/w7panel/sysrunc/libs/utils"
	"golang.org/x/sys/unix"
)

// The CKM Server rootfs can be fuse-overlayfs. Replacing a checkpoint file
// inherited from its lower layer by rename returns ENOTDIR there. Keep the
// live checkpoint on the container's tmpfs, which also survives an FS process
// crash, and read the old location once when upgrading a live session.
const fsRestartFile = "/dev/shm/sysrunc/fs-restart.json"
const legacyFSRestartFile = "/run/sysrunc/fs-restart.json"
const recoveryMetricsSocket = "/run/sysrunc/recovery-metrics.sock"
const recoveryMetricsTextfile = "/run/sysrunc-metrics/sysrunc-fs.prom"

func readFSRestart(primary, legacy string, brokerRecovery bool) (*state.RestartState, error) {
	saved, err := state.ReadRestart(primary, brokerRecovery)
	if err != nil || saved != nil {
		return saved, err
	}
	return state.ReadRestart(legacy, brokerRecovery)
}

type recoveryRunner struct {
	workers *worker.Supervisor
	events  interface {
		Read() (recovery.Fault, error)
		Close() error
	}
	css                                             domain.ContainerStateServiceIface
	root                                            string
	recordsMu                                       sync.RWMutex
	records                                         map[uint32][]state.RestartContainer
	restored                                        []state.RestartContainer
	repairMu                                        sync.Mutex
	statsMu                                         sync.Mutex
	repaired                                        sync.Map
	fallbackDone                                    sync.Map
	silentDone                                      map[string]map[uint64]bool
	pendingMounts                                   map[string][]string
	attempts, succeeded, failed, stale, skipped     uint64
	faultEvents, frequentFaults, droppedFaultEvents uint64
	duration                                        time.Duration
	durationCount                                   uint64
	durationBuckets                                 [len(recoveryDurationBounds)]uint64
	fallbackScanDuration                            time.Duration
	fallbackScanCount                               uint64
	fallbackScanCandidates                          int
	faultCounts                                     map[faultProcess]int
}

type faultProcess struct {
	pid       uint32
	namespace uint64
	device    uint32
}

type faultMount struct {
	namespace uint64
	device    uint32
}

type idleRecoveryEvents struct {
	done chan struct{}
	once sync.Once
}

func (e *idleRecoveryEvents) Read() (recovery.Fault, error) {
	<-e.done
	return recovery.Fault{}, io.EOF
}

func (e *idleRecoveryEvents) Close() error {
	e.once.Do(func() { close(e.done) })
	return nil
}

const frequentFaultThreshold = 5

var recoveryDurationBounds = [...]time.Duration{
	10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond,
	250 * time.Millisecond, 500 * time.Millisecond, time.Second, 5 * time.Second,
}

func (r *recoveryRunner) observeDuration(elapsed time.Duration) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	r.duration += elapsed
	r.durationCount++
	for i, bound := range recoveryDurationBounds {
		if elapsed <= bound {
			r.durationBuckets[i]++
		}
	}
}

type repairResult int

const (
	repairFailed repairResult = iota
	repairUnchanged
	repairReplaced
	repairUnsupported
)

func prepareRecovery(css domain.ContainerStateServiceIface, fss *fuse.FuseServerService, root string, listenerCount int) (*recoveryRunner, error) {
	brokerRecovery := os.Getenv("SYSRUNC_FD_BROKER_SOCKET") != ""
	saved, err := readFSRestart(fsRestartFile, legacyFSRestartFile, brokerRecovery)
	if err != nil {
		return nil, err
	}
	if saved != nil && !saved.Clean {
		live, err := hasLiveCheckpointContainers(saved)
		if err != nil {
			return nil, err
		}
		if !live {
			// A CKM Server recreation gives the new K3s process a fresh PID
			// and mount namespace. Its old /run checkpoint may persist, but
			// none of its container sessions can be adopted here.
			logrus.Info("discarding FS checkpoint from a previous K3s PID namespace")
			saved = nil
		} else if listenerCount == 0 {
			return nil, fmt.Errorf("FS crash checkpoint has containers but the FD broker retained no listeners")
		}
		if saved != nil {
			if err := detachCrashedHostMounts(root, saved); err != nil {
				return nil, err
			}
		}
	}
	if saved == nil {
		entries, err := os.ReadDir(root)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, entry := range entries {
			// A host reboot leaves empty mountpoint directories but no /run
			// checkpoint. Remove only empty directories; mounts fail EBUSY
			// and populated paths are preserved for manual recovery.
			if !entry.IsDir() {
				return nil, fmt.Errorf("unexpected FUSE root entry %s", entry.Name())
			}
			if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
				return nil, fmt.Errorf("cannot adopt existing FUSE mounts without a checkpoint: %w", err)
			}
		}
	}
	r := &recoveryRunner{css: css, root: root, records: make(map[uint32][]state.RestartContainer), faultCounts: make(map[faultProcess]int)}
	var devices []uint32
	if saved != nil {
		r.restored = append([]state.RestartContainer(nil), saved.Containers...)
		for _, c := range saved.Containers {
			for _, dev := range c.Devices {
				if len(r.records[dev]) == 0 {
					devices = append(devices, dev)
				}
				r.records[dev] = append(r.records[dev], c)
			}
		}
	}
	r.events, err = recovery.NewEvents(devices)
	if err != nil {
		if !brokerRecovery {
			return nil, err
		}
		// L1 user namespaces may not permit BPF attachment. The bounded
		// namespace scan still repairs disconnected mounts without events.
		logrus.WithError(err).Warn("FUSE fault events unavailable; using namespace scan")
		r.events = &idleRecoveryEvents{done: make(chan struct{})}
	}
	if saved != nil {
		saved.Clean = false
		if err = state.WriteRestart(fsRestartFile, saved); err == nil {
			err = state.RestoreRestart(css, saved)
		}
	} else {
		err = state.SaveRestart(css, fss.StateContainer, fsRestartFile, root, false)
	}
	if err != nil {
		r.events.Close()
		return nil, err
	}
	return r, nil
}

func hasLiveCheckpointContainers(saved *state.RestartState) (bool, error) {
	for _, container := range saved.Containers {
		start, err := state.ProcessStartTime(container.PID)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if start == container.StartTime {
			return true, nil
		}
	}
	return false, nil
}

// An FS SIGKILL disconnects the manager-owned host FUSE connections. Remove
// only mounts whose exact path and device are recorded in the same-boot
// checkpoint; the container namespace copies are repaired after restoration.
func detachCrashedHostMounts(root string, saved *state.RestartState) error {
	mounts, err := mountinfo.GetMounts(nil)
	if err != nil {
		return err
	}
	byPath := make(map[string][]*mountinfo.Info)
	for _, mount := range mounts {
		byPath[mount.Mountpoint] = append(byPath[mount.Mountpoint], mount)
	}
	knownPaths := make(map[string]bool, len(saved.Containers))
	for _, container := range saved.Containers {
		path := filepath.Join(root, container.ID)
		knownPaths[path] = true
		for _, mount := range byPath[path] {
			device := uint32(mount.Major)<<20 | uint32(mount.Minor)
			known := false
			for _, expected := range container.Devices {
				known = known || expected == device
			}
			if mount.FSType != "fuse" || mount.Source != "sysruncfs" || !known {
				return fmt.Errorf("refusing to detach unrecognized mount at %s", path)
			}
			if err := unix.Unmount(path, unix.MNT_DETACH); err != nil {
				return fmt.Errorf("detach crashed FUSE host mount %s: %w", path, err)
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove crashed FUSE mountpoint %s: %w", path, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if knownPaths[path] {
			continue
		}
		if len(byPath[path]) > 0 {
			return fmt.Errorf("uncheckpointed FUSE mount at %s", path)
		}
		if !entry.IsDir() || os.Remove(path) != nil {
			return fmt.Errorf("uncheckpointed nonempty FUSE path at %s", path)
		}
	}
	return nil
}

func (r *recoveryRunner) repair(ctx context.Context, pid uint32, c state.RestartContainer, deviceNums []uint32) repairResult {
	r.repairMu.Lock()
	defer r.repairMu.Unlock()
	current := r.css.ContainerLookupById(c.ID)
	if ctx.Err() != nil || current == nil || current.InitPid() != c.PID {
		return repairFailed
	}
	start, err := state.ProcessStartTime(c.PID)
	if err != nil || start != c.StartTime {
		return repairFailed
	}
	devices := make([]recovery.Device, 0, len(deviceNums))
	for _, dev := range deviceNums {
		devices = append(devices, recovery.Device{Major: dev >> 20, Minor: dev & ((1 << 20) - 1)})
	}
	r.statsMu.Lock()
	r.attempts++
	r.statsMu.Unlock()
	started := time.Now()
	defer func() { r.observeDuration(time.Since(started)) }()
	targetNS, targetErr := os.Stat(fmt.Sprintf("/proc/%d/ns/mnt", pid))
	outerNS, outerErr := os.Stat(fmt.Sprintf("/proc/%d/ns/mnt", c.PID))
	sameNamespace := targetErr == nil && outerErr == nil && os.SameFile(targetNS, outerNS)
	var paths []string
	if r.workers != nil {
		request := worker.RepairRequest{PID: int(pid), Root: r.root, ContainerID: c.ID}
		for _, d := range devices {
			request.Devices = append(request.Devices, worker.Device{Major: d.Major, Minor: d.Minor})
		}
		paths, err = r.workers.Repair(ctx, request)
	} else {
		paths, err = recovery.RepairMounts(ctx, int(pid), r.root, c.ID, devices)
	}
	if sameNamespace {
		if refreshErr := r.refreshMountPolicy(c.ID, paths, func(id string, paths []string) error {
			return state.RefreshRecoveredMounts(r.css, id, paths)
		}); refreshErr != nil {
			err = fmt.Errorf("repair error: %v; refresh mount policy: %w", err, refreshErr)
		}
	}
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			r.statsMu.Lock()
			r.stale++
			r.statsMu.Unlock()
			return repairFailed
		}
		if errors.Is(err, recovery.ErrUnsupportedPropagation) {
			r.statsMu.Lock()
			r.skipped++
			r.statsMu.Unlock()
			logrus.WithError(err).WithField("container", c.ID).WithField("pid", pid).Warn("FUSE recovery skipped unsupported namespace")
			return repairUnsupported
		}
		r.statsMu.Lock()
		r.failed++
		r.statsMu.Unlock()
		logrus.WithError(err).WithField("container", c.ID).WithField("pid", pid).WithField("duration_ms", time.Since(started).Milliseconds()).Error("FUSE recovery incomplete")
		return repairFailed
	} else if len(paths) > 0 {
		r.statsMu.Lock()
		r.succeeded++
		r.statsMu.Unlock()
		logrus.WithField("container", c.ID).WithField("pid", pid).WithField("duration_ms", time.Since(started).Milliseconds()).Info("FUSE mounts recovered; reopen stale descriptors")
		return repairReplaced
	} else {
		r.statsMu.Lock()
		r.stale++
		r.statsMu.Unlock()
		return repairUnchanged
	}
}

// Keep replaced paths until the policy parser accepts them: a later mount
// scan cannot rediscover a path whose disconnected mount was already replaced.
func (r *recoveryRunner) refreshMountPolicy(id string, paths []string, refresh func(string, []string) error) error {
	pending := append(r.pendingMounts[id], paths...)
	if len(pending) == 0 {
		return nil
	}
	if err := refresh(id, pending); err != nil {
		if r.pendingMounts == nil {
			r.pendingMounts = make(map[string][]string)
		}
		r.pendingMounts[id] = pending
		return err
	}
	delete(r.pendingMounts, id)
	return nil
}

func (r *recoveryRunner) checkpointContainers() []state.RestartContainer {
	r.recordsMu.RLock()
	defer r.recordsMu.RUnlock()
	seen := make(map[string]bool)
	var containers []state.RestartContainer
	for _, owners := range r.records {
		for _, c := range owners {
			if !seen[c.ID] {
				seen[c.ID] = true
				containers = append(containers, c)
			}
		}
	}
	return containers
}

func (r *recoveryRunner) forgetRecords(dead map[string]bool) {
	if len(dead) == 0 {
		return
	}
	r.recordsMu.Lock()
	for dev, owners := range r.records {
		kept := owners[:0]
		for _, c := range owners {
			if !dead[c.ID] {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			delete(r.records, dev)
		} else {
			r.records[dev] = kept
		}
	}
	r.recordsMu.Unlock()
	for id := range dead {
		r.fallbackDone.Delete(id)
		delete(r.silentDone, id)
	}
}

func (r *recoveryRunner) pruneSilentNamespaces(id string, namespaces map[uint64]uint32) {
	for namespace := range r.silentDone[id] {
		if _, alive := namespaces[namespace]; !alive {
			delete(r.silentDone[id], namespace)
		}
	}
	if len(r.silentDone[id]) == 0 {
		delete(r.silentDone, id)
	}
}

func (r *recoveryRunner) pruneRepaired(active map[faultMount]bool, complete bool) {
	if !complete {
		return
	}
	r.repaired.Range(func(key, _ interface{}) bool {
		if !active[key.(faultMount)] {
			r.repaired.Delete(key)
		}
		return true
	})
}

func (r *recoveryRunner) recordOwners(device uint32) []state.RestartContainer {
	r.recordsMu.RLock()
	defer r.recordsMu.RUnlock()
	return append([]state.RestartContainer(nil), r.records[device]...)
}

func (r *recoveryRunner) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The minute fallback inspects only registered containers and their cgroups.
	fallbackDone := make(chan struct{})
	go func() {
		defer close(fallbackDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		first := time.NewTimer(time.Second)
		defer first.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-first.C:
			case <-ticker.C:
			}
			scanStarted := time.Now()
			r.statsMu.Lock()
			r.faultCounts = make(map[faultProcess]int)
			r.statsMu.Unlock()
			dead := make(map[string]bool)
			active := make(map[faultMount]bool)
			complete := true
			candidates := 0
			for _, c := range r.checkpointContainers() {
				start, err := state.ProcessStartTime(c.PID)
				if r.css.ContainerLookupById(c.ID) == nil || err != nil || start != c.StartTime {
					dead[c.ID] = true
					continue
				}
				candidates++
				namespaces, err := r.repairSilentNamespaces(ctx, c)
				if err != nil {
					complete = false
				} else {
					for namespace := range namespaces {
						for _, device := range c.Devices {
							active[faultMount{namespace, device}] = true
						}
					}
				}
				if _, done := r.fallbackDone.Load(c.ID); done {
					continue
				}
				// Only checkpointed, disconnected devices are eligible. Inspect
				// mount metadata directly rather than block on a FUSE stat.
				if result := r.repair(ctx, c.PID, c, c.Devices); result != repairFailed && result != repairUnsupported {
					r.fallbackDone.Store(c.ID, struct{}{})
				}
			}
			r.forgetRecords(dead)
			r.pruneRepaired(active, complete)
			r.statsMu.Lock()
			r.fallbackScanDuration += time.Since(scanStarted)
			r.fallbackScanCount++
			r.fallbackScanCandidates = candidates
			logrus.WithFields(logrus.Fields{"attempts": r.attempts, "successes": r.succeeded, "failures": r.failed, "old_fd_events": r.stale, "unsupported": r.skipped, "scan_candidates": candidates, "scan_duration_ms": time.Since(scanStarted).Milliseconds()}).Info("FUSE recovery counters")
			r.statsMu.Unlock()
		}
	}()
	err := r.runEvents(ctx, r.handleFault)
	cancel()
	<-fallbackDone
	return err
}

// runEvents keeps reading kernel faults while one worker performs mount repairs.
// Duplicate faults share one pending repair; a full queue leaves recovery to the
// minute fallback without blocking the ring buffer reader.
func (r *recoveryRunner) runEvents(ctx context.Context, handle func(context.Context, recovery.Fault)) error {
	ctx, cancel := context.WithCancel(ctx)
	eventsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		r.events.Close()
		close(eventsClosed)
	}()
	queue := make(chan recovery.Fault, 256)
	pending := make(map[faultMount]bool)
	var pendingMu sync.Mutex
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case fault := <-queue:
				if ctx.Err() != nil {
					return
				}
				handle(ctx, fault)
				pendingMu.Lock()
				delete(pending, faultMount{fault.Namespace, fault.Device})
				pendingMu.Unlock()
			}
		}
	}()
	defer func() { cancel(); <-workerDone; <-eventsClosed }()
	for {
		fault, err := r.events.Read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("FUSE fault event stream stopped: %w", err)
		}
		r.statsMu.Lock()
		r.faultEvents++
		process := faultProcess{fault.PID, fault.Namespace, fault.Device}
		r.faultCounts[process]++
		frequent := r.faultCounts[process] == frequentFaultThreshold
		if frequent {
			r.frequentFaults++
		}
		r.statsMu.Unlock()
		if frequent {
			logrus.WithFields(logrus.Fields{"pid": fault.PID, "namespace": fault.Namespace, "device": fault.Device}).Warn("frequent stale FUSE reads; process must reopen old descriptors")
		}
		key := faultMount{fault.Namespace, fault.Device}
		if _, done := r.repaired.Load(key); done {
			continue
		}
		pendingMu.Lock()
		if !pending[key] {
			pending[key] = true
			select {
			case queue <- fault:
			default:
				delete(pending, key)
				r.statsMu.Lock()
				r.droppedFaultEvents++
				r.statsMu.Unlock()
			}
		}
		pendingMu.Unlock()
	}
}

func (r *recoveryRunner) handleFault(ctx context.Context, fault recovery.Fault) {
	key := faultMount{fault.Namespace, fault.Device}
	if _, done := r.repaired.Load(key); done {
		return
	}
	owners := r.recordOwners(fault.Device)
	for _, c := range owners {
		pid, err := recoveryPID(ctx, fault, c)
		if err != nil {
			continue
		}
		// The helper pins and revalidates the namespace and mounts before mutation.
		result := r.repair(ctx, pid, c, []uint32{fault.Device})
		if result == repairReplaced || result == repairUnchanged && len(owners) == 1 {
			r.repaired.Store(key, struct{}{})
			break
		}
	}
}

func (r *recoveryRunner) repairSilentNamespaces(ctx context.Context, c state.RestartContainer) (map[uint64]uint32, error) {
	root, err := containerCgroupRoot(c.PID)
	if err != nil {
		return nil, err
	}
	namespaces, err := findContainerNamespaces(ctx, root)
	if err != nil {
		logrus.WithError(err).WithField("container", c.ID).Warn("FUSE namespace fallback scan failed")
		return nil, err
	}
	var outer unix.Stat_t
	if err := unix.Stat(fmt.Sprintf("/proc/%d/ns/mnt", c.PID), &outer); err != nil {
		return nil, err
	}
	namespaces[outer.Ino] = c.PID
	r.pruneSilentNamespaces(c.ID, namespaces)
	for namespace, pid := range namespaces {
		if namespace != outer.Ino {
			if r.silentDone[c.ID][namespace] {
				continue
			}
			if r.repair(ctx, pid, c, c.Devices) != repairFailed {
				if r.silentDone == nil {
					r.silentDone = make(map[string]map[uint64]bool)
				}
				if r.silentDone[c.ID] == nil {
					r.silentDone[c.ID] = make(map[uint64]bool)
				}
				r.silentDone[c.ID][namespace] = true
			}
		}
	}
	return namespaces, nil
}

func (r *recoveryRunner) metrics(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/metrics" {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	r.writeMetrics(w)
}

func (r *recoveryRunner) writeMetrics(w io.Writer) {
	r.statsMu.Lock()
	values := [8]uint64{r.attempts, r.succeeded, r.failed, r.stale, r.skipped, r.faultEvents, r.frequentFaults, r.droppedFaultEvents}
	duration := r.duration
	durationCount := r.durationCount
	durationBuckets := r.durationBuckets
	fallbackScanDuration := r.fallbackScanDuration
	fallbackScanCount := r.fallbackScanCount
	fallbackScanCandidates := r.fallbackScanCandidates
	r.statsMu.Unlock()
	for i, name := range [...]string{
		"recovery_attempts", "recovery_successes", "recovery_failures", "recovery_old_fd_events",
		"recovery_unsupported", "recovery_fault_events", "recovery_frequent_fault_processes", "recovery_dropped_fault_events",
	} {
		fmt.Fprintf(w, "sysrunc_fs_%s_total %d\n", name, values[i])
	}
	fmt.Fprintln(w, "# TYPE sysrunc_fs_recovery_duration_seconds histogram")
	for i, bound := range recoveryDurationBounds {
		fmt.Fprintf(w, "sysrunc_fs_recovery_duration_seconds_bucket{le=\"%g\"} %d\n", bound.Seconds(), durationBuckets[i])
	}
	fmt.Fprintf(w, "sysrunc_fs_recovery_duration_seconds_bucket{le=\"+Inf\"} %d\n", durationCount)
	fmt.Fprintf(w, "sysrunc_fs_recovery_duration_seconds_sum %.9f\n", duration.Seconds())
	fmt.Fprintf(w, "sysrunc_fs_recovery_duration_seconds_count %d\n", durationCount)
	fmt.Fprintln(w, "# TYPE sysrunc_fs_fallback_scan_duration_seconds summary")
	fmt.Fprintf(w, "sysrunc_fs_fallback_scan_duration_seconds_sum %.9f\n", fallbackScanDuration.Seconds())
	fmt.Fprintf(w, "sysrunc_fs_fallback_scan_duration_seconds_count %d\n", fallbackScanCount)
	fmt.Fprintln(w, "# TYPE sysrunc_fs_fallback_scan_candidates gauge")
	fmt.Fprintf(w, "sysrunc_fs_fallback_scan_candidates %d\n", fallbackScanCandidates)
}

func (r *recoveryRunner) writeMetricsTextfile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var data bytes.Buffer
	r.writeMetrics(&data)
	f, err := os.CreateTemp(filepath.Dir(path), ".sysrunc-fs-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0644); err != nil {
		return err
	}
	if _, err := f.Write(data.Bytes()); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (r *recoveryRunner) checkpointCrash(fss *fuse.FuseServerService) error {
	r.repairMu.Lock()
	defer r.repairMu.Unlock()
	var ticker func(string) bool
	if r.workers != nil {
		ticker = r.workers.TickerEnabled
	}
	return state.SaveRestart(r.css, fss.StateContainer, fsRestartFile, r.root, false, ticker)
}

func (r *recoveryRunner) exportMetrics(ctx context.Context, path string) {
	defer os.Remove(path)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.writeMetricsTextfile(path); err != nil {
			logrus.WithError(err).Warn("FUSE recovery metrics textfile update failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *recoveryRunner) serveMetrics(path string) (func(), error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	server := &http.Server{Handler: http.HandlerFunc(r.metrics)}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Error("FUSE recovery metrics stopped")
		}
	}()
	return func() {
		server.Close()
		os.Remove(path)
	}, nil
}

func runRecoverable(r *recoveryRunner, ipc domain.IpcServiceIface, fss *fuse.FuseServerService, quiesce func() error, profile interface{ Stop() }) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checkpointDone := make(chan struct{})
	if os.Getenv("SYSRUNC_FD_BROKER_SOCKET") != "" {
		go func() {
			defer close(checkpointDone)
			interval := time.NewTicker(time.Second)
			defer interval.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-interval.C:
					if err := r.checkpointCrash(fss); err != nil {
						logrus.WithError(err).Warn("FS crash checkpoint update failed")
					}
				}
			}
		}()
	} else {
		close(checkpointDone)
	}
	eventResult := make(chan error, 1)
	go func() { eventResult <- r.run(ctx) }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(signals)
	if err := libutils.CreatePidFile("sysrunc-fs", sysruncFsPidFile); err != nil {
		return err
	}
	defer libutils.DestroyPidFile(sysruncFsPidFile)
	stopMetrics, err := r.serveMetrics(recoveryMetricsSocket)
	if err != nil {
		return err
	}
	defer stopMetrics()
	metricsDone := make(chan struct{})
	go func() {
		r.exportMetrics(ctx, recoveryMetricsTextfile)
		close(metricsDone)
	}()
	defer func() { cancel(); <-metricsDone }()
	result := make(chan error, 1)
	go func() { result <- ipc.Init() }()
	systemd.SdNotify(false, systemd.SdNotifyReady)
	select {
	case err := <-result:
		return err
	case err := <-eventResult:
		return err
	case <-signals:
	}
	systemd.SdNotify(false, systemd.SdNotifyStopping)
	ipc.(interface{ Stop() }).Stop()
	if err := quiesce(); err != nil {
		return err
	}
	cancel()
	<-checkpointDone
	if err := <-eventResult; err != nil {
		return err
	}
	r.repairMu.Lock()
	defer r.repairMu.Unlock()
	fss.Quiesce()
	var ticker func(string) bool
	if r.workers != nil {
		ticker = r.workers.TickerEnabled
	}
	if err := state.SaveRestart(r.css, fss.StateContainer, fsRestartFile, r.root, true, ticker); err != nil {
		return err
	}
	if err := fss.DetachForRestart(); err != nil {
		// A partially detached host tree must not be treated as recoverable.
		saved, readErr := state.ReadRestart(fsRestartFile)
		if readErr == nil && saved != nil {
			saved.Clean = false
			if writeErr := state.WriteRestart(fsRestartFile, saved); writeErr != nil {
				return fmt.Errorf("detach: %v; invalidate checkpoint: %w", err, writeErr)
			}
		}
		return err
	}
	if profile != nil {
		profile.Stop()
	}
	return nil
}
