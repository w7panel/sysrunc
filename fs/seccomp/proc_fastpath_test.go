package seccomp

import (
	"io"
	"os"
	"testing"

	libseccomp "github.com/seccomp/libseccomp-golang"
	"golang.org/x/sys/unix"
)

type literalProcPathParser string

func (p literalProcPathParser) ReadSyscallStringArgs(uint32, []memParserDataElem) ([]string, error) {
	return []string{string(p)}, nil
}

func (literalProcPathParser) ReadSyscallBytesArgs(uint32, []memParserDataElem) ([]string, error) {
	return nil, nil
}

func (literalProcPathParser) WriteSyscallBytesArgs(uint32, []memParserDataElem) error {
	return nil
}

func TestProcRandomDirectoryOpenFallsBackToFuse(t *testing.T) {
	// Injecting a host proc directory fd allows relative traversal outside the
	// virtual mount. A directory open must therefore continue in the kernel.
	tracer := &syscallTracer{
		service:   &SyscallMonitorService{},
		memParser: literalProcPathParser(procRandomDir),
	}
	req := &sysRequest{
		ID:   42,
		Data: libseccomp.ScmpNotifData{Args: []uint64{0, 1, unix.O_RDONLY | unix.O_DIRECTORY}},
	}
	resp, err := tracer.processOpenat(req, 0, nil)
	if err != nil || resp == nil || resp.Flags != libseccomp.NotifRespFlagContinue {
		t.Fatalf("random directory open: response = %+v, err = %v; want continue", resp, err)
	}
}

func TestProcUUIDFastPath(t *testing.T) {
	for _, tc := range []struct {
		path    string
		flags   uint64
		resolve uint64
		want    bool
	}{
		{procUUIDPath, unix.O_RDONLY, 0, true},
		{procUUIDPath, unix.O_RDONLY | unix.O_CLOEXEC, 0, true},
		{procUUIDPath, unix.O_WRONLY, 0, false},
		{procUUIDPath, unix.O_PATH, 0, false},
		{procUUIDPath, unix.O_NONBLOCK, 0, false},
		{procUUIDPath, unix.O_DIRECTORY, 0, false},
		{procUUIDPath, unix.O_RDONLY, RESOLVE_IN_ROOT, false},
		{"/proc/meminfo", unix.O_RDONLY, 0, false},
		{"/proc/sys/kernel/random/uuid/child", unix.O_RDONLY, 0, false},
	} {
		if got := procUUIDFastPath(tc.path, tc.flags, tc.resolve); got != tc.want {
			t.Errorf("procUUIDFastPath(%q, %#x, %#x) = %v, want %v", tc.path, tc.flags, tc.resolve, got, tc.want)
		}
	}
}

func TestReadOnlyProcFD(t *testing.T) {
	fd, err := makeReadOnlyProcFD("sysrunc-test", []byte("virtual proc\n"))
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "proc-test")
	defer file.Close()
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		t.Fatalf("fd flags=%#x, err=%v", flags, err)
	}
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "virtual proc\n" {
		t.Fatalf("read=%q, err=%v", data, err)
	}
	if _, err := file.Write([]byte("change")); err == nil {
		t.Fatal("read-only proc fd accepted a write")
	}
}

func TestProcUUIDMasked(t *testing.T) {
	for _, tc := range []struct {
		paths []string
		want  bool
	}{
		{[]string{"/proc/sys"}, true},
		{[]string{procUUIDPath}, true},
		{[]string{"/proc/sys/kernel/random/uuid-other"}, false},
		{[]string{"/proc/kcore"}, false},
	} {
		if got := procUUIDMasked(tc.paths); got != tc.want {
			t.Errorf("procUUIDMasked(%q) = %v, want %v", tc.paths, got, tc.want)
		}
	}
}

func TestProcRandomDirMasked(t *testing.T) {
	for _, tc := range []struct {
		paths []string
		want  bool
	}{
		{[]string{"/proc/sys"}, true},
		{[]string{procUUIDPath}, true},
		{[]string{"/proc/sys/kernel/random/other"}, true},
		{[]string{"/proc/sys/kernel/random-other"}, false},
		{[]string{"/proc/kcore"}, false},
	} {
		if got := procRandomDirMasked(tc.paths); got != tc.want {
			t.Errorf("procRandomDirMasked(%q) = %v, want %v", tc.paths, got, tc.want)
		}
	}
}

func TestProcVirtualFileSelection(t *testing.T) {
	for _, path := range []string{
		"/proc/cpuinfo", "/proc/diskstats", "/proc/meminfo", "/proc/stat",
		"/proc/swaps", "/proc/uptime", "/proc/loadavg",
	} {
		if !procVirtualFiles[path] {
			t.Errorf("missing direct proc reader for %s", path)
		}
	}
	for _, path := range []string{"/proc/slabinfo", "/proc/sys", "/proc/uptime/child"} {
		if procVirtualFiles[path] {
			t.Errorf("unexpected direct proc reader for %s", path)
		}
	}
	if !procPathMasked("/proc/meminfo", []string{"/proc"}) ||
		procPathMasked("/proc/meminfo", []string{"/proc/meminfo-extra"}) {
		t.Fatal("proc mask path boundary is wrong")
	}
}
