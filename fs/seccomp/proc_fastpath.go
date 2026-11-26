package seccomp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	"/proc/swaps":  true,
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

// These UTS files are read-only and have the same contents in every namespace.
var procHostUTSFiles = map[string]bool{
	"/proc/sys/kernel/ostype": true, "/proc/sys/kernel/osrelease": true,
	"/proc/sys/kernel/version": true,
}

func procPlainReadFlags(flags, resolve uint64) bool {
	return resolve == 0 && flags&^uint64(unix.O_CLOEXEC|unix.O_LARGEFILE) == 0
}

// Only this read-only virtual proc file currently has a worker-backed reader.
// Other paths continue into the kernel and retain the FUSE implementation.
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
	for _, masked := range paths {
		if masked == "" {
			continue
		}
		masked = strings.TrimRight(masked, "/")
		if masked == "/" || masked == procRandomDir ||
			strings.HasPrefix(procRandomDir, masked+"/") ||
			strings.HasPrefix(masked, procRandomDir+"/") {
			return true
		}
	}
	return false
}

// Directory injection must preserve the virtual UUID file on a subsequent
// openat(dirfd, "uuid"). Resolve only this single relative component; any
// traversal or other relative pathname is left to the kernel.
func (t *syscallTracer) procFastPath(req *sysRequest, path string) string {
	if path == "" || path == "." || path == ".." || strings.Contains(path, "/") {
		return path
	}
	if path != "uuid" && !procVirtualFiles["/proc/"+path] && !procVirtualFiles["/proc/pressure/"+path] && !procVirtualFiles["/sys/devices/system/cpu/"+path] && !procKernelReadOnlyFiles["/proc/sys/kernel/"+path] && !procHostUTSFiles["/proc/sys/kernel/"+path] {
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
		if path == "uuid" {
			return procUUIDPath
		}
	}
	if parent == "/proc" && procVirtualFiles["/proc/"+path] {
		return "/proc/" + path
	}
	if parent == "/proc/pressure" && procVirtualFiles[parent+"/"+path] {
		return parent + "/" + path
	}
	if parent == "/sys/devices/system/cpu" && procVirtualFiles[parent+"/"+path] {
		return parent + "/" + path
	}
	if parent == "/proc/sys/kernel" && procHostUTSFiles[parent+"/"+path] {
		return parent + "/" + path
	}
	if parent == "/proc/sys/kernel" && procKernelReadOnlyFiles[parent+"/"+path] {
		return parent + "/" + path
	}
	return path
}

func (t *syscallTracer) tryProcHostUTSOpen(req *sysRequest, notifyFD int32, cntr domain.ContainerIface, path string, flags, resolve uint64) (*sysResponse, bool) {
	if !procHostUTSFiles[path] || !procPlainReadFlags(flags, resolve) || procPathMasked(path, cntr.ProcMaskPaths()) {
		return nil, false
	}
	data, err := os.ReadFile(path)
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
	if service == nil || (!procVirtualFiles[path] && !procKernelReadOnlyFiles[path]) {
		return nil, false
	}
	var handler domain.HandlerIface
	var ok bool
	if procKernelReadOnlyFiles[path] {
		node := service.IOService().NewIOnode(filepath.Base(path), path, os.FileMode(0444))
		handler, ok = service.LookupHandler(node)
		ok = ok && handler.GetPath() == "/proc/sys/kernel"
	} else {
		handler, ok = service.FindHandler(path)
	}
	return handler, ok && handler.GetEnabled()
}

// Read the existing virtual proc handler directly. Its ResourceOwner is shared
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
	req := &domain.HandlerRequest{Pid: pid, Container: cntr, ResourceOwner: owner}
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
	if !procPlainReadFlags(flags, resolve) || procPathMasked(path, cntr.ProcMaskPaths()) {
		return nil, false
	}
	if _, ok := t.procVirtualHandler(path); !ok {
		return nil, false
	}
	var data []byte
	var err error
	if path == "/proc/diskstats" && t.service.diskstatsReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.diskstatsReader.ReadDiskstats(ctx, cntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/swaps" && t.service.swapsReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.swapsReader.ReadSwaps(ctx, cntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/cpuinfo" && t.service.cpuinfoReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.cpuinfoReader.ReadCPUInfo(ctx, cntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/meminfo" && t.service.meminfoReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.meminfoReader.ReadMemInfo(ctx, cntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/uptime" && t.service.uptimeReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.uptimeReader.ReadUptime(ctx, cntr.ID(), req.Pid)
		cancel()
	} else if path == "/proc/stat" && t.service.statReader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		data, err = t.service.statReader.ReadStat(ctx, cntr.ID(), req.Pid)
		cancel()
	}
	if data == nil || err != nil {
		data, err = t.readVirtualProc(cntr, req.Pid, path)
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
	fd, err := unix.MemfdCreate(name, unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return -1, err
	}
	defer unix.Close(fd)
	if err := writeProcMemfd(fd, data); err != nil {
		return -1, err
	}
	if err := unix.Fchmod(fd, 0444); err != nil {
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
// Use the native proc metadata only for exact global proc paths. All other
// paths, flags, and masked resources retain the kernel/FUSE implementation.
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
		if _, ok := t.procVirtualHandler(target); (!ok && !procHostUTSFiles[target]) || procPathMasked(target, cntr.ProcMaskPaths()) {
			return t.createContinueResponse(req.ID), nil
		}
	}
	var stat unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, target, 0, int(req.Data.Args[3]), &stat); err != nil {
		return t.createContinueResponse(req.ID), nil
	}
	// The FUSE UUID inode advertises a fixed virtual size and read-only mode.
	if target == procUUIDPath {
		stat.Size = 1024
		stat.Mode = (stat.Mode & unix.S_IFMT) | 0444
	} else if procVirtualFiles[target] || procKernelReadOnlyFiles[target] {
		stat.Size = 4096
		if procKernelReadOnlyFiles[target] {
			stat.Size = 1024
		}
		stat.Mode = (stat.Mode & unix.S_IFMT) | 0444
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
	data, err := reader.ReadUUID(ctx, cntr.ID())
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
