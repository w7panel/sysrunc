package seccomp

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"

	fusefs "bazil.org/fuse/fs"
	libseccomp "github.com/seccomp/libseccomp-golang"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"github.com/w7panel/sysrunc/fs/domain"
)

// linux_dirent64 has a 19-byte header followed by a NUL-terminated name.
// The FUSE server uses the same eight-byte record alignment and assigns each
// entry's next byte offset as its cookie.
const linuxDirent64Header = 19

var virtualGetdentsFastpathLogged sync.Map

const procPressureDir = "/proc/pressure"

var virtualDirectories = map[string]bool{
	"/proc/pressure":                      true,
	"/proc/sys":                           true,
	"/proc/sys/fs":                        true,
	"/proc/sys/kernel":                    true,
	"/proc/sys/kernel/random":             true,
	"/proc/sys/kernel/yama":               true,
	"/proc/sys/net":                       true,
	"/proc/sys/net/core":                  true,
	"/proc/sys/net/ipv4":                  true,
	"/proc/sys/net/ipv4/vs":               true,
	"/proc/sys/net/ipv4/neigh":            true,
	"/proc/sys/net/ipv4/neigh/default":    true,
	"/proc/sys/net/netfilter":             true,
	"/proc/sys/net/unix":                  true,
	"/proc/sys/vm":                        true,
	"/sys/kernel":                         true,
	"/sys/kernel/config":                  true,
	"/sys/kernel/debug":                   true,
	"/sys/kernel/security":                true,
	"/sys/kernel/tracing":                 true,
	"/sys/devices/virtual":                true,
	"/sys/devices/virtual/dmi":            true,
	"/sys/devices/virtual/dmi/id":         true,
	"/sys/module/nf_conntrack/parameters": true,
}

type virtualDirEntry struct{ info os.FileInfo }

func (entry virtualDirEntry) Name() string               { return entry.info.Name() }
func (entry virtualDirEntry) IsDir() bool                { return entry.info.IsDir() }
func (entry virtualDirEntry) Info() (os.FileInfo, error) { return entry.info, nil }
func (entry virtualDirEntry) Type() os.FileMode {
	if entry.info.IsDir() {
		return os.ModeDir
	}
	return entry.info.Mode().Type()
}

func (t *syscallTracer) virtualDirectoryEntries(cntr domain.ContainerIface, pid uint32, path string) ([]os.DirEntry, error) {
	service := t.service.procHandlers
	if service == nil {
		return nil, syscall.ENOENT
	}
	node := service.IOService().NewIOnode(filepath.Base(path), path, os.ModeDir)
	handler, ok := service.LookupHandler(node)
	if !ok || !handler.GetEnabled() {
		return nil, syscall.ENOENT
	}
	cntr = t.procStateContainer(cntr)
	owner := service.AcquireResourceOwner(cntr)
	defer service.ReleaseResourceOwner(owner)
	process := t.service.prs.ProcessCreate(pid, 0, 0)
	req := &domain.HandlerRequest{Pid: pid, Uid: process.Uid(), Gid: process.Gid(), Container: cntr, ResourceOwner: owner}
	files, err := handler.ReadDirAll(node, req)
	if err != nil {
		return nil, err
	}
	entries := make([]os.DirEntry, 0, len(files))
	for _, file := range files {
		entries = append(entries, virtualDirEntry{file})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func direntRecordLength(name string) int {
	return (linuxDirent64Header + len(name) + 1 + 7) &^ 7
}

func direntType(mode os.FileMode) byte {
	switch {
	case mode.IsDir():
		return unix.DT_DIR
	case mode.IsRegular():
		return unix.DT_REG
	case mode&os.ModeSymlink != 0:
		return unix.DT_LNK
	default:
		return unix.DT_UNKNOWN
	}
}

// encodeRandomDirents mirrors the exact directory listing produced by the
// existing ReadDirAll handler: sorted names, no dot entries, generated FUSE
// inode numbers, and byte-offset cookies. Unknown offsets and short buffers
// remain on the kernel/FUSE path.
func encodeRandomDirents(entries []os.DirEntry, parentInode uint64, offset int64, count uint64) ([]byte, int64, bool) {
	if offset < 0 {
		return nil, 0, false
	}
	position := int64(0)
	started := offset == 0
	var data []byte
	for _, entry := range entries {
		name := entry.Name()
		recordLen := direntRecordLength(name)
		if recordLen > 0xffff {
			return nil, 0, false
		}
		next := position + int64(recordLen)
		if !started {
			if position != offset {
				position = next
				continue
			}
			started = true
		}
		if uint64(len(data)+recordLen) > count {
			if len(data) == 0 {
				return nil, 0, false
			}
			return data, position, true
		}
		info, err := entry.Info()
		if err != nil {
			return nil, 0, false
		}
		record := make([]byte, recordLen)
		binary.LittleEndian.PutUint64(record[0:8], fusefs.GenerateDynamicInode(parentInode, name))
		binary.LittleEndian.PutUint64(record[8:16], uint64(next))
		binary.LittleEndian.PutUint16(record[16:18], uint16(recordLen))
		record[18] = direntType(info.Mode())
		copy(record[linuxDirent64Header:], name)
		data = append(data, record...)
		position = next
	}
	if !started && position != offset {
		return nil, 0, false
	}
	return data, position, true
}

// processGetdents64 uses the tracee's original FUSE directory fd. A duplicated
// pidfd_getfd descriptor shares its cursor, so later getdents64, dup, and
// lseek calls continue with the same cookie. No host directory fd is injected
// into the container; relative pathname operations stay on its FUSE mount.
func (t *syscallTracer) processGetdents64(req *sysRequest, notifyFD int32, cntr domain.ContainerIface) (*sysResponse, error) {
	targetFD := int32(req.Data.Args[0])
	if targetFD < 0 {
		return t.createContinueResponse(req.ID), nil
	}
	// The kernel receives count as unsigned int. A raw syscall can still put
	// nonzero high bits in the seccomp argument register; treating all 64 bits
	// as the buffer length could write beyond the caller's actual request.
	if req.Data.Args[2]>>32 != 0 {
		return t.createContinueResponse(req.ID), nil
	}
	process := t.service.prs.ProcessCreate(req.Pid, 0, 0)
	path, err := process.GetFd(targetFD)
	if err != nil || !virtualDirectories[path] {
		return t.createContinueResponse(req.ID), nil
	}
	if procDirMasked(path, cntr.ProcMaskPaths()) {
		return t.createContinueResponse(req.ID), nil
	}
	pidfd, err := unix.PidfdOpen(int(req.Pid), 0)
	if err != nil {
		return t.createContinueResponse(req.ID), nil
	}
	defer unix.Close(pidfd)
	dirfd, err := unix.PidfdGetfd(pidfd, int(targetFD), 0)
	if err != nil {
		return t.createContinueResponse(req.ID), nil
	}
	defer unix.Close(dirfd)
	var fsStat unix.Statfs_t
	var fileStat unix.Stat_t
	if unix.Fstatfs(dirfd, &fsStat) != nil || fsStat.Type != unix.FUSE_SUPER_MAGIC ||
		unix.Fstat(dirfd, &fileStat) != nil || fileStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return t.createContinueResponse(req.ID), nil
	}
	offset, err := unix.Seek(dirfd, 0, unix.SEEK_CUR)
	if err != nil {
		return t.createContinueResponse(req.ID), nil
	}
	entries, err := t.virtualDirectoryEntries(cntr, req.Pid, path)
	if err != nil {
		return t.createContinueResponse(req.ID), nil
	}
	data, next, ok := encodeRandomDirents(entries, fileStat.Ino, offset, req.Data.Args[2])
	if !ok {
		return t.createContinueResponse(req.ID), nil
	}
	if err := libseccomp.NotifIDValid(libseccomp.ScmpFd(notifyFD), req.ID); err != nil {
		return nil, err
	}
	if len(data) != 0 {
		if err := t.memParser.WriteSyscallBytesArgs(req.Pid, []memParserDataElem{{req.Data.Args[1], len(data), data}}); err != nil {
			return t.createErrorResponse(req.ID, syscall.EFAULT), nil
		}
		if _, err := unix.Seek(dirfd, next, unix.SEEK_SET); err != nil {
			return t.createContinueResponse(req.ID), nil
		}
	}
	logged, _ := virtualGetdentsFastpathLogged.LoadOrStore(path, &sync.Once{})
	logged.(*sync.Once).Do(func() {
		logrus.Infof("getdents64 fast path served %s through seccomp notify", path)
	})
	return t.createSuccessResponseWithRetValue(req.ID, uint64(len(data))), nil
}
