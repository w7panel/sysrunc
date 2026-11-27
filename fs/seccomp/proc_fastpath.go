package seccomp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	libseccomp "github.com/seccomp/libseccomp-golang"
	"github.com/w7panel/sysrunc/fs/domain"
	"golang.org/x/sys/unix"
)

const procUUIDPath = "/proc/sys/kernel/random/uuid"
const procRandomDir = "/proc/sys/kernel/random"

// These exact mounts have read-only handlers which already construct a
// container-specific snapshot. Reuse those handlers before entering FUSE.
var procVirtualFiles = map[string]bool{
	"/proc/cpuinfo": true, "/proc/diskstats": true,
	"/proc/meminfo": true, "/proc/stat": true,
	"/proc/slabinfo": true, "/proc/swaps": true,
	"/proc/uptime": true, "/proc/loadavg": true,
	"/proc/pressure/cpu": true, "/proc/pressure/io": true,
	"/proc/pressure/memory":           true,
	"/sys/devices/system/cpu/online":  true,
	"/sys/devices/system/cpu/present": true,
}

// These files are virtual entries of the /proc/sys/kernel directory handler.
// Its per-container cache must supply the snapshot, rather than a host read.
var procKernelReadOnlyFiles = map[string]bool{
	"/proc/sys/kernel/cap_last_cap": true,
	"/proc/sys/kernel/ngroups_max":  true,
}

// These world-readable sysctls have container-backed FUSE handlers. Snapshot
// reads can bypass FUSE; write opens still use the FUSE handler. Owner-only
// files cannot use ADDFD without reproducing FUSE's access check.
var virtualSnapshotFiles = map[string]string{
	"/proc/sys/kernel/hostname":                                    "/proc/sys/kernel",
	"/proc/sys/kernel/domainname":                                  "/proc/sys/kernel",
	"/proc/sys/kernel/kptr_restrict":                               "/proc/sys/kernel",
	"/proc/sys/kernel/dmesg_restrict":                              "/proc/sys/kernel",
	"/proc/sys/kernel/panic":                                       "/proc/sys/kernel",
	"/proc/sys/kernel/panic_on_oops":                               "/proc/sys/kernel",
	"/proc/sys/kernel/printk":                                      "/proc/sys/kernel",
	"/proc/sys/kernel/sysrq":                                       "/proc/sys/kernel",
	"/proc/sys/kernel/pid_max":                                     "/proc/sys/kernel",
	"/proc/sys/kernel/yama/ptrace_scope":                           "/proc/sys/kernel/yama",
	"/proc/sys/fs/file-max":                                        "/proc/sys/fs",
	"/proc/sys/fs/nr_open":                                         "/proc/sys/fs",
	"/proc/sys/net/core/default_qdisc":                             "/proc/sys/net/core",
	"/proc/sys/net/core/somaxconn":                                 "/proc/sys/net/core",
	"/proc/sys/net/ipv4/ip_unprivileged_port_start":                "/proc/sys/net/ipv4",
	"/proc/sys/net/ipv4/ping_group_range":                          "/proc/sys/net/ipv4",
	"/proc/sys/net/ipv4/neigh/default/gc_thresh1":                  "/proc/sys/net/ipv4/neigh",
	"/proc/sys/net/ipv4/neigh/default/gc_thresh2":                  "/proc/sys/net/ipv4/neigh",
	"/proc/sys/net/ipv4/neigh/default/gc_thresh3":                  "/proc/sys/net/ipv4/neigh",
	"/proc/sys/net/ipv4/vs/conntrack":                              "/proc/sys/net/ipv4/vs",
	"/proc/sys/net/ipv4/vs/conn_reuse_mode":                        "/proc/sys/net/ipv4/vs",
	"/proc/sys/net/ipv4/vs/expire_nodest_conn":                     "/proc/sys/net/ipv4/vs",
	"/proc/sys/net/ipv4/vs/expire_quiescent_template":              "/proc/sys/net/ipv4/vs",
	"/proc/sys/net/netfilter/nf_conntrack_max":                     "/proc/sys/net/netfilter",
	"/proc/sys/net/netfilter/nf_conntrack_generic_timeout":         "/proc/sys/net/netfilter",
	"/proc/sys/net/netfilter/nf_conntrack_tcp_be_liberal":          "/proc/sys/net/netfilter",
	"/proc/sys/net/netfilter/nf_conntrack_tcp_timeout_established": "/proc/sys/net/netfilter",
	"/proc/sys/net/netfilter/nf_conntrack_tcp_timeout_close_wait":  "/proc/sys/net/netfilter",
	"/proc/sys/net/unix/max_dgram_qlen":                            "/proc/sys/net/unix",
	"/proc/sys/vm/overcommit_memory":                               "/proc/sys/vm",
	"/proc/sys/vm/mmap_min_addr":                                   "/proc/sys/vm",
}

// FUSE exposes these entries with the container's mapped root as owner.
// A plain read can use a snapshot only after checking the tracee's fsuid.
// Other users and capability-based access retain the kernel/FUSE path.
var virtualOwnerFiles = map[string]struct {
	parent string
	mode   os.FileMode
}{
	"/proc/sys/fs/protected_hardlinks":             {"/proc/sys/fs", 0600},
	"/proc/sys/fs/protected_symlinks":              {"/proc/sys/fs", 0600},
	"/sys/devices/virtual/dmi/id/product_uuid":     {"/sys/devices/virtual/dmi/id", 0400},
	"/sys/module/nf_conntrack/parameters/hashsize": {"/sys/module/nf_conntrack/parameters", 0600},
}

// These UTS files are read-only and have the same contents in every namespace.
var procHostUTSFiles = map[string]bool{
	"/proc/sys/kernel/ostype": true, "/proc/sys/kernel/osrelease": true,
	"/proc/sys/kernel/version": true,
}

func procPlainReadFlags(flags, resolve uint64) bool {
	return resolve == 0 && flags&^uint64(unix.O_CLOEXEC|unix.O_LARGEFILE) == 0
}

func virtualSnapshotAvailable(path string) bool {
	if virtualSnapshotFiles[path] != "/proc/sys/net/ipv4/vs" {
		return true
	}
	info, err := os.Stat("/proc/sys/net/ipv4/vs")
	return err == nil && info.IsDir()
}

// UUID needs a fresh value for every open. Other worker-backed resources use
// tryProcVirtualOpen, while unsupported opens retain the kernel/FUSE path.
func procUUIDFastPath(path string, flags, resolve uint64) bool {
	if path != procUUIDPath {
		return false
	}
	// The injected memfd only reproduces a plain read-only open. Pass all
	// other flags to the kernel so it retains their validation and semantics.
	return procPlainReadFlags(flags, resolve)
}

func procUUIDMasked(paths []string) bool {
	return procPathMasked(procUUIDPath, paths)
}

func procPathMasked(path string, paths []string) bool {
	for _, masked := range paths {
		if masked == "" {
			continue
		}
		if masked == "/" || masked == path || strings.HasPrefix(path, strings.TrimRight(masked, "/")+"/") {
			return true
		}
	}
	return false
}

// Directory metadata can use the host path only if no masked descendant is
// exposed. Opening the host directory itself must still go through FUSE:
// relative operations on an injected directory fd can escape to other host
// proc paths (including with "../"), even if this directory has no masks.
func procRandomDirMasked(paths []string) bool {
	return procDirMasked(procRandomDir, paths)
}

func procDirMasked(dir string, paths []string) bool {
	for _, masked := range paths {
		if masked == "" {
			continue
		}
		masked = strings.TrimRight(masked, "/")
		if masked == "/" || masked == dir ||
			strings.HasPrefix(dir, masked+"/") ||
			strings.HasPrefix(masked, dir+"/") {
			return true
		}
	}
	return false
}

func virtualRelativeFile(name string) bool {
	if name == "uuid" || procVirtualFiles["/proc/"+name] ||
		procVirtualFiles["/proc/pressure/"+name] ||
		procVirtualFiles["/sys/devices/system/cpu/"+name] ||
		procKernelReadOnlyFiles["/proc/sys/kernel/"+name] ||
		procHostUTSFiles["/proc/sys/kernel/"+name] {
		return true
	}
	for path := range virtualSnapshotFiles {
		if filepath.Base(path) == name {
			return true
		}
	}
	for path := range virtualOwnerFiles {
		if filepath.Base(path) == name {
			return true
		}
	}
	return false
}

// A leading "./" does not change a single-component lookup through the
// tracee's FUSE directory fd. Keep all other path traversal on the kernel
// path, where mount and openat2 resolution rules remain authoritative.
func virtualRelativeComponent(path string) (string, bool) {
	name := strings.TrimPrefix(path, "./")
	return name, name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
}

// Directory injection must preserve the virtual UUID file on a subsequent
// openat(dirfd, "uuid"). Resolve only this single relative component; any
// traversal or other relative pathname is left to the kernel.
func (t *syscallTracer) procFastPath(req *sysRequest, path string) string {
	name, ok := virtualRelativeComponent(path)
	if !ok {
		return path
	}
	if !virtualRelativeFile(name) {
		return path
	}
	process := t.service.prs.ProcessCreate(req.Pid, 0, 0)
	var parent string
	if int32(req.Data.Args[0]) == unix.AT_FDCWD {
		parent = process.Cwd()
	} else {
		var err error
		parent, err = process.GetFd(int32(req.Data.Args[0]))
		if err != nil {
			return path
		}
	}
	if parent == procRandomDir {
		if name == "uuid" {
			return procUUIDPath
		}
	}
	if parent == "/proc" && procVirtualFiles["/proc/"+name] {
		return "/proc/" + name
	}
	if parent == "/proc/pressure" && procVirtualFiles[parent+"/"+name] {
		return parent + "/" + name
	}
	if parent == "/sys/devices/system/cpu" && procVirtualFiles[parent+"/"+name] {
		return parent + "/" + name
	}
	if parent == "/proc/sys/kernel" && procHostUTSFiles[parent+"/"+name] {
		return parent + "/" + name
	}
	if parent == "/proc/sys/kernel" && procKernelReadOnlyFiles[parent+"/"+name] {
		return parent + "/" + name
	}
	if _, ok := virtualSnapshotFiles[parent+"/"+name]; ok {
		return parent + "/" + name
	}
	if owner, ok := virtualOwnerFiles[parent+"/"+name]; ok && owner.parent == parent {
		return parent + "/" + name
	}
	return path
}

func procFSUID(status []byte) (uint32, error) {
	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			break
		}
		value, err := strconv.ParseUint(fields[4], 10, 32)
		return uint32(value), err
	}
	return 0, fmt.Errorf("tracee fsuid unavailable")
}

func (t *syscallTracer) procVirtualOwnerIDs(pid uint32) (uint32, uint32, bool) {
	if pid == 0 || t.service.prs == nil {
		return 0, 0, false
	}
	selfNS, selfErr := os.Stat("/proc/self/ns/user")
	traceeNS, traceeErr := os.Stat(fmt.Sprintf("/proc/%d/ns/user", pid))
	if selfErr != nil || traceeErr != nil {
		return 0, 0, false
	}
	if os.SameFile(selfNS, traceeNS) {
		// FUSE reports uid 0 when both processes share a user namespace.
		return 0, 0, true
	}
	rootUID, rootGID, err := t.service.prs.ProcessCreate(pid, 0, 0).UsernsRootUidGid()
	return rootUID, rootGID, err == nil
}

func (t *syscallTracer) procOwnerReadable(pid uint32) bool {
	rootUID, _, ok := t.procVirtualOwnerIDs(pid)
	if !ok {
		return false
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	fsuid, err := procFSUID(status)
	return err == nil && fsuid == rootUID
}

func (t *syscallTracer) tryProcHostUTSOpen(req *sysRequest, notifyFD int32, cntr domain.ContainerIface, path string, flags, resolve uint64) (*sysResponse, bool) {
	if !procHostUTSFiles[path] || !procPlainReadFlags(flags, resolve) || procPathMasked(path, cntr.ProcMaskPaths()) {
		return nil, false
	}
	var data []byte
	var err error
	if t.service.resourceReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.resourceReader.ReadResource(ctx, cntr.ID(), req.Pid, path)
		cancel()
	}
	if data == nil || err != nil {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, false
	}
	memfd, err := makeReadOnlyProcFD("sysrunc-"+filepath.Base(path), data)
	if err != nil {
		return nil, false
	}
	defer unix.Close(memfd)
	opener := openat2SyscallInfo{flags: flags, notifyFd: notifyFD, syscallCtx: syscallCtx{reqId: req.ID}}
	newFD, err := opener.injectFd(memfd)
	if err != nil {
		return t.createContinueResponse(req.ID), true
	}
	return t.createSuccessResponseWithRetValue(req.ID, uint64(newFD)), true
}

func (t *syscallTracer) procVirtualHandler(path string) (domain.HandlerIface, bool) {
	service := t.service.procHandlers
	_, ownerOnly := virtualOwnerFiles[path]
	if service == nil || (!procVirtualFiles[path] && !procKernelReadOnlyFiles[path] && virtualSnapshotFiles[path] == "" && !ownerOnly) {
		return nil, false
	}
	var handler domain.HandlerIface
	var ok bool
	if procKernelReadOnlyFiles[path] || virtualSnapshotFiles[path] != "" || ownerOnly {
		node := service.IOService().NewIOnode(filepath.Base(path), path, os.FileMode(0444))
		handler, ok = service.LookupHandler(node)
		parent := "/proc/sys/kernel"
		if virtualSnapshotFiles[path] != "" {
			parent = virtualSnapshotFiles[path]
		} else if ownerOnly {
			parent = virtualOwnerFiles[path].parent
		}
		ok = ok && handler.GetPath() == parent
	} else {
		handler, ok = service.FindHandler(path)
	}
	return handler, ok && handler.GetEnabled()
}

// Containers in the same network namespace share one FUSE emulation state.
// Resolve the serving container to that state before a seccomp fast path reads
// cached virtual data; otherwise ADDFD can return a different value from FUSE.
func (t *syscallTracer) procStateContainer(cntr domain.ContainerIface) domain.ContainerIface {
	if cntr == nil || t.service == nil || t.service.css == nil {
		return cntr
	}
	fss := t.service.css.FuseServerService()
	lookup, ok := fss.(interface {
		StateContainer(string) domain.ContainerIface
	})
	if !ok {
		return cntr
	}
	if state := lookup.StateContainer(cntr.ID()); state != nil {
		return state
	}
	return cntr
}

// Read the existing virtual handler directly. Its ResourceOwner is shared
// with FUSE so cgroup views and snapshot invalidation retain their semantics.
func (t *syscallTracer) readVirtualProc(cntr domain.ContainerIface, pid uint32, path string) ([]byte, error) {
	handler, ok := t.procVirtualHandler(path)
	if !ok {
		return nil, fmt.Errorf("no direct proc handler for %s", path)
	}
	service := t.service.procHandlers
	owner := service.AcquireResourceOwner(cntr)
	defer service.ReleaseResourceOwner(owner)
	node := service.IOService().NewIOnode(filepath.Base(path), path, os.FileMode(0444))
	node.SetOpenFlags(unix.O_RDONLY)
	req := &domain.HandlerRequest{Pid: pid, Container: cntr, ResourceOwner: owner, SkipWorker: true}
	if _, err := handler.Open(node, req); err != nil {
		return nil, err
	}
	return readProcHandlerSnapshot(handler, node, req)
}

func readProcHandlerSnapshot(handler domain.HandlerIface, node domain.IOnodeIface, req *domain.HandlerRequest) ([]byte, error) {
	const chunkSize = 64 << 10
	const maxSize = 16 << 20
	data := make([]byte, 0, 4096)
	buffer := make([]byte, chunkSize)
	for len(data) < maxSize {
		req.Offset = int64(len(data))
		req.Data = buffer
		n, err := handler.Read(node, req)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n < 0 || n > len(req.Data) {
			return nil, fmt.Errorf("invalid proc read size %d", n)
		}
		// Container-cached handlers may replace req.Data with the cached
		// slice instead of copying into the supplied buffer.
		data = append(data, req.Data[:n]...)
		if n == 0 || err == io.EOF {
			return data, nil
		}
	}
	return nil, fmt.Errorf("virtual proc snapshot exceeds %d bytes", maxSize)
}

func (t *syscallTracer) tryProcVirtualOpen(req *sysRequest, notifyFD int32, cntr domain.ContainerIface, path string, flags, resolve uint64) (*sysResponse, bool) {
	if !procPlainReadFlags(flags, resolve) || procPathMasked(path, cntr.ProcMaskPaths()) || !virtualSnapshotAvailable(path) {
		return nil, false
	}
	if _, ok := t.procVirtualHandler(path); !ok {
		return nil, false
	}
	if _, ownerOnly := virtualOwnerFiles[path]; ownerOnly && !t.procOwnerReadable(req.Pid) {
		return nil, false
	}
	stateCntr := t.procStateContainer(cntr)
	var data []byte
	var err error
	if path == "/proc/diskstats" && t.service.diskstatsReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.diskstatsReader.ReadDiskstats(ctx, stateCntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/swaps" && t.service.swapsReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.swapsReader.ReadSwaps(ctx, stateCntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/cpuinfo" && t.service.cpuinfoReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.cpuinfoReader.ReadCPUInfo(ctx, stateCntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/meminfo" && t.service.meminfoReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.meminfoReader.ReadMemInfo(ctx, stateCntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/uptime" && t.service.uptimeReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.uptimeReader.ReadUptime(ctx, stateCntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/stat" && t.service.statReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.statReader.ReadStat(ctx, stateCntr.ID(), req.Pid)
		cancel()
	} else if t.service.resourceReader != nil &&
		(procKernelReadOnlyFiles[path] || path == "/proc/slabinfo" || strings.HasPrefix(path, "/proc/pressure/") || strings.HasPrefix(path, "/sys/devices/system/cpu/") ||
			path == "/sys/devices/virtual/dmi/id/product_uuid") {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.resourceReader.ReadResource(ctx, stateCntr.ID(), req.Pid, path)
		cancel()
	}
	if data == nil || err != nil {
		data, err = t.readVirtualProc(stateCntr, req.Pid, path)
	}
	if err != nil {
		return nil, false
	}
	mode := os.FileMode(0444)
	if owner, ok := virtualOwnerFiles[path]; ok {
		mode = owner.mode
	}
	memfd, err := makeReadOnlyProcFDWithMode("sysrunc-"+filepath.Base(path), data, mode)
	if err != nil {
		return nil, false
	}
	defer unix.Close(memfd)
	opener := openat2SyscallInfo{flags: flags, notifyFd: notifyFD, syscallCtx: syscallCtx{reqId: req.ID}}
	newFD, err := opener.injectFd(memfd)
	if err != nil {
		return t.createContinueResponse(req.ID), true
	}
	return t.createSuccessResponseWithRetValue(req.ID, uint64(newFD)), true
}

func writeProcMemfd(fd int, data []byte) error {
	for len(data) != 0 {
		n, err := unix.Write(fd, data)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func makeReadOnlyProcFD(name string, data []byte) (int, error) {
	return makeReadOnlyProcFDWithMode(name, data, 0444)
}

func makeReadOnlyProcFDWithMode(name string, data []byte, mode os.FileMode) (int, error) {
	fd, err := unix.MemfdCreate(name, unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return -1, err
	}
	defer unix.Close(fd)
	if err := writeProcMemfd(fd, data); err != nil {
		return -1, err
	}
	if err := unix.Fchmod(fd, uint32(mode.Perm())); err != nil {
		return -1, err
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return -1, err
	}
	// ADDFD duplicates the source fd's access mode. Reopen the sealed memfd
	// read-only so the tracee cannot write even through its original fd.
	readonly, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", fd), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	return readonly, nil
}

// statx returns struct statx in caller memory; ADDFD cannot complete it.
// Read the metadata from the tracee's view, where the FUSE mount supplies the
// virtual inode, ownership, device, and size. Host proc metadata differs even
// for paths that happen to exist on the host. The FUSE attribute cache makes
// this lookup cheap after the first access; errors retain the kernel path.
func (t *syscallTracer) processStatx(req *sysRequest, notifyFD int32, cntr domain.ContainerIface) (*sysResponse, error) {
	path, err := t.memParser.ReadSyscallStringArgs(req.Pid, []memParserDataElem{{req.Data.Args[1], unix.PathMax, nil}})
	if err != nil || len(path) == 0 {
		return t.createContinueResponse(req.ID), nil
	}
	target := t.procFastPath(req, path[0])
	if req.Data.Args[2] != 0 || req.Data.Args[4] == 0 {
		return t.createContinueResponse(req.ID), nil
	}
	switch target {
	case procUUIDPath:
		if procUUIDMasked(cntr.ProcMaskPaths()) {
			return t.createContinueResponse(req.ID), nil
		}
	case procRandomDir:
		if procRandomDirMasked(cntr.ProcMaskPaths()) {
			return t.createContinueResponse(req.ID), nil
		}
	default:
		if _, ok := t.procVirtualHandler(target); (!ok && !procHostUTSFiles[target]) || procPathMasked(target, cntr.ProcMaskPaths()) || !virtualSnapshotAvailable(target) {
			return t.createContinueResponse(req.ID), nil
		}
	}
	var stat unix.Statx_t
	traceePath := fmt.Sprintf("/proc/%d/root%s", req.Pid, target)
	if err := unix.Statx(unix.AT_FDCWD, traceePath, 0, int(req.Data.Args[3]), &stat); err != nil {
		return t.createContinueResponse(req.ID), nil
	}
	if err := libseccomp.NotifIDValid(libseccomp.ScmpFd(notifyFD), req.ID); err != nil {
		return nil, err
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(&stat)), int(unsafe.Sizeof(stat)))
	if err := t.memParser.WriteSyscallBytesArgs(req.Pid, []memParserDataElem{{req.Data.Args[4], len(data), data}}); err != nil {
		return t.createErrorResponse(req.ID, syscall.EFAULT), nil
	}
	return t.createSuccessResponse(req.ID), nil
}

func (t *syscallTracer) tryProcUUIDOpen(id uint64, notifyFD int32, cntr domain.ContainerIface, path string, dirfd, flags, resolve uint64) (*sysResponse, bool) {
	_ = dirfd // Absolute proc path: openat ignores dirfd when resolve is zero.
	reader := t.service.uuidReader
	if reader == nil || !procUUIDFastPath(path, flags, resolve) {
		return nil, false
	}
	if procUUIDMasked(cntr.ProcMaskPaths()) {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	data, err := reader.ReadUUID(ctx, t.procStateContainer(cntr).ID())
	cancel()
	if err != nil {
		return nil, false
	}
	memfd, err := makeReadOnlyProcFD("sysrunc-proc-uuid", data)
	if err != nil {
		return nil, false
	}
	defer unix.Close(memfd)
	opener := openat2SyscallInfo{flags: flags, notifyFd: notifyFD, syscallCtx: syscallCtx{reqId: id}}
	newFD, err := opener.injectFd(memfd)
	if err != nil {
		return t.createContinueResponse(id), true
	}
	return t.createSuccessResponseWithRetValue(id, uint64(newFD)), true
}

func (t *syscallTracer) processOpenat(req *sysRequest, fd int32, cntr domain.ContainerIface) (*sysResponse, error) {
	path, err := t.memParser.ReadSyscallStringArgs(req.Pid, []memParserDataElem{{req.Data.Args[1], unix.PathMax, nil}})
	if err != nil || len(path) == 0 {
		return t.createErrorResponse(req.ID, syscall.EFAULT), nil
	}
	resolved := t.procFastPath(req, path[0])
	if response, handled := t.tryProcUUIDOpen(req.ID, fd, cntr, resolved, req.Data.Args[0], req.Data.Args[2], 0); handled {
		return response, nil
	}
	if response, handled := t.tryProcHostUTSOpen(req, fd, cntr, resolved, req.Data.Args[2], 0); handled {
		return response, nil
	}
	if response, handled := t.tryProcVirtualOpen(req, fd, cntr, resolved, req.Data.Args[2], 0); handled {
		return response, nil
	}
	return t.createContinueResponse(req.ID), nil
}
