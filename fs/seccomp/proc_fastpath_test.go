package seccomp

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	libseccomp "github.com/seccomp/libseccomp-golang"
	"github.com/w7panel/sysrunc/fs/domain"
	"github.com/w7panel/sysrunc/fs/handler/implementations"
	"github.com/w7panel/sysrunc/fs/process"
	"golang.org/x/sys/unix"
)

type snapshotReplacingHandler struct {
	domain.HandlerIface
	data []byte
}

func (h snapshotReplacingHandler) Read(_ domain.IOnodeIface, req *domain.HandlerRequest) (int, error) {
	req.Data = h.data
	return len(h.data), io.EOF
}

func TestReadProcHandlerSnapshotUsesReplacedData(t *testing.T) {
	handler := snapshotReplacingHandler{data: []byte("65536\n")}
	data, err := readProcHandlerSnapshot(handler, nil, &domain.HandlerRequest{})
	if err != nil || string(data) != "65536\n" {
		t.Fatalf("snapshot = %q, %v; want cached handler data", data, err)
	}
}

type literalProcPathParser string

type namedProcContainer struct {
	domain.ContainerIface
	id string
}

func (c namedProcContainer) ID() string { return c.id }

type sharedProcFuseState struct {
	domain.FuseServerServiceIface
	state domain.ContainerIface
}

func (s sharedProcFuseState) StateContainer(string) domain.ContainerIface { return s.state }

type sharedProcCSS struct {
	domain.ContainerStateServiceIface
	fss domain.FuseServerServiceIface
}

func (s sharedProcCSS) FuseServerService() domain.FuseServerServiceIface { return s.fss }

func TestProcFastPathUsesSharedFuseState(t *testing.T) {
	serving := namedProcContainer{id: "app"}
	shared := namedProcContainer{id: "pod-state"}
	tracer := &syscallTracer{service: &SyscallMonitorService{css: sharedProcCSS{
		fss: sharedProcFuseState{state: shared},
	}}}
	if got := tracer.procStateContainer(serving); got.ID() != shared.ID() {
		t.Fatalf("state container = %s, want %s", got.ID(), shared.ID())
	}
	tracer.service.css = nil
	if got := tracer.procStateContainer(serving); got.ID() != serving.ID() {
		t.Fatalf("unavailable FUSE state = %s, want serving container", got.ID())
	}
}

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
		"/proc/slabinfo", "/proc/swaps", "/proc/uptime", "/proc/loadavg",
	} {
		if !procVirtualFiles[path] {
			t.Errorf("missing direct proc reader for %s", path)
		}
	}
	for _, path := range []string{"/proc/sys", "/proc/uptime/child"} {
		if procVirtualFiles[path] {
			t.Errorf("unexpected direct proc reader for %s", path)
		}
	}
	if !procPathMasked("/proc/meminfo", []string{"/proc"}) ||
		procPathMasked("/proc/meminfo", []string{"/proc/meminfo-extra"}) {
		t.Fatal("proc mask path boundary is wrong")
	}
	if !procPathMasked("/sys/devices/system/cpu/online", []string{"/sys/devices/system/cpu"}) ||
		procPathMasked("/sys/devices/system/cpu/online", []string{"/sys/devices/system/cpu/online-extra"}) {
		t.Fatal("sysfs mask path boundary is wrong")
	}
}

func TestVirtualRelativeComponent(t *testing.T) {
	for _, tc := range []struct {
		path string
		name string
		ok   bool
	}{
		{"meminfo", "meminfo", true},
		{"./meminfo", "meminfo", true},
		{"./hostname", "hostname", true},
		{"../hostname", "../hostname", false},
		{"./../hostname", "../hostname", false},
		{"./subdir/file", "subdir/file", false},
		{"/proc/meminfo", "/proc/meminfo", false},
		{"./", "", false},
	} {
		name, ok := virtualRelativeComponent(tc.path)
		if name != tc.name || ok != tc.ok {
			t.Errorf("relative component %q = (%q, %t), want (%q, %t)", tc.path, name, ok, tc.name, tc.ok)
		}
	}
}

func TestWritableSysctlReadSelection(t *testing.T) {
	resources := map[string]map[string]*domain.EmuResource{
		"/proc/sys/kernel":         implementations.ProcSysKernel_Handler.EmuResourceMap,
		"/proc/sys/kernel/yama":    implementations.ProcSysKernelYama_Handler.EmuResourceMap,
		"/proc/sys/fs":             implementations.ProcSysFs_Handler.EmuResourceMap,
		"/proc/sys/net/core":       implementations.ProcSysNetCore_Handler.EmuResourceMap,
		"/proc/sys/net/ipv4":       implementations.ProcSysNetIpv4_Handler.EmuResourceMap,
		"/proc/sys/net/ipv4/vs":    implementations.ProcSysNetIpv4Vs_Handler.EmuResourceMap,
		"/proc/sys/net/ipv4/neigh": implementations.ProcSysNetIpv4Neigh_Handler.EmuResourceMap,
		"/proc/sys/net/netfilter":  implementations.ProcSysNetNetfilter_Handler.EmuResourceMap,
		"/proc/sys/net/unix":       implementations.ProcSysNetUnix_Handler.EmuResourceMap,
		"/proc/sys/vm":             implementations.ProcSysVm_Handler.EmuResourceMap,
	}
	for path, parent := range virtualSnapshotFiles {
		if !strings.HasPrefix(path, parent+"/") || !virtualRelativeFile(filepath.Base(path)) {
			t.Fatalf("invalid plain-read snapshot path %s", path)
		}
		resource := resources[parent][strings.TrimPrefix(path, parent+"/")]
		if resource == nil || resource.Mode.Perm() != 0644 {
			t.Fatalf("%s must remain world-readable in FUSE before ADDFD can bypass its open", path)
		}
	}
	for _, path := range []string{
		"/sys/devices/virtual/dmi/id/product_uuid",
		"/sys/module/nf_conntrack/parameters/hashsize",
	} {
		if virtualSnapshotFiles[path] != "" {
			t.Fatalf("owner-only path selected: %s", path)
		}
	}
	for _, name := range []string{"hostname", "domainname"} {
		if virtualSnapshotFiles["/proc/sys/kernel/"+name] != "/proc/sys/kernel" {
			t.Fatalf("container-backed UTS path %s omitted", name)
		}
	}
	if procPlainReadFlags(unix.O_WRONLY, 0) || procPlainReadFlags(unix.O_RDWR, 0) {
		t.Fatal("write opens must retain FUSE handling")
	}
}

func TestIPVSSnapshotRequiresBackingDirectory(t *testing.T) {
	const path = "/proc/sys/net/ipv4/vs/conn_reuse_mode"
	info, err := os.Stat("/proc/sys/net/ipv4/vs")
	want := err == nil && info.IsDir()
	if got := virtualSnapshotAvailable(path); got != want {
		t.Fatalf("IPVS snapshot available = %v, want %v", got, want)
	}
	if !virtualSnapshotAvailable("/proc/sys/kernel/hostname") {
		t.Fatal("unrelated virtual snapshots must not depend on IPVS")
	}
}

func TestEmulatedFileReadPathCoverage(t *testing.T) {
	resources := map[string]map[string]*domain.EmuResource{
		"/proc/sys/fs":                        implementations.ProcSysFs_Handler.EmuResourceMap,
		"/proc/sys/kernel":                    implementations.ProcSysKernel_Handler.EmuResourceMap,
		"/proc/sys/kernel/random":             implementations.ProcSysKernelRandom_Handler.EmuResourceMap,
		"/proc/sys/kernel/yama":               implementations.ProcSysKernelYama_Handler.EmuResourceMap,
		"/proc/sys/net/core":                  implementations.ProcSysNetCore_Handler.EmuResourceMap,
		"/proc/sys/net/ipv4":                  implementations.ProcSysNetIpv4_Handler.EmuResourceMap,
		"/proc/sys/net/ipv4/neigh":            implementations.ProcSysNetIpv4Neigh_Handler.EmuResourceMap,
		"/proc/sys/net/ipv4/vs":               implementations.ProcSysNetIpv4Vs_Handler.EmuResourceMap,
		"/proc/sys/net/netfilter":             implementations.ProcSysNetNetfilter_Handler.EmuResourceMap,
		"/proc/sys/net/unix":                  implementations.ProcSysNetUnix_Handler.EmuResourceMap,
		"/proc/sys/vm":                        implementations.ProcSysVm_Handler.EmuResourceMap,
		"/sys/devices/virtual/dmi/id":         implementations.SysDevicesVirtualDmiId_Handler.EmuResourceMap,
		"/sys/module/nf_conntrack/parameters": implementations.SysModuleNfconntrackParameters_Handler.EmuResourceMap,
	}
	for parent, entries := range resources {
		for name, resource := range entries {
			if !resource.Enabled || resource.Kind != domain.FileEmuResource {
				continue
			}
			path := filepath.Join(parent, name)
			if path == procUUIDPath || procKernelReadOnlyFiles[path] || virtualSnapshotFiles[path] != "" {
				continue
			}
			if _, ok := virtualOwnerFiles[path]; ok {
				continue
			}
			t.Errorf("emulated file %s has no exact read path", path)
		}
	}
}

func TestProcHostUTSFileSelection(t *testing.T) {
	for _, name := range []string{"ostype", "osrelease", "version"} {
		path := "/proc/sys/kernel/" + name
		if !procHostUTSFiles[path] || !procPlainReadFlags(unix.O_RDONLY|unix.O_CLOEXEC, 0) {
			t.Fatalf("missing read-only UTS fast path for %s", path)
		}
		if !procPathMasked(path, []string{"/proc/sys"}) {
			t.Fatalf("mask does not cover %s", path)
		}
	}
	for _, path := range []string{"/proc/sys/kernel/hostname", "/proc/sys/kernel/domainname", "/proc/sys/kernel/version/child", "/proc/sys/kernel/../kernel/ostype"} {
		if procHostUTSFiles[path] {
			t.Fatalf("unsafe UTS fast path for %s", path)
		}
	}
	for _, flags := range []uint64{unix.O_WRONLY, unix.O_PATH, unix.O_NONBLOCK, unix.O_DIRECTORY} {
		if procPlainReadFlags(flags, 0) {
			t.Fatalf("accepted unsupported flags %#x", flags)
		}
	}
	if procPlainReadFlags(unix.O_RDONLY, RESOLVE_IN_ROOT) {
		t.Fatal("accepted resolve restriction")
	}
}

func TestOwnerOnlyVirtualFilesKeepOwnerPermissions(t *testing.T) {
	for path, expected := range map[string]os.FileMode{
		"/proc/sys/fs/protected_hardlinks":             0600,
		"/proc/sys/fs/protected_symlinks":              0600,
		"/sys/devices/virtual/dmi/id/product_uuid":     0400,
		"/sys/module/nf_conntrack/parameters/hashsize": 0600,
	} {
		owner, ok := virtualOwnerFiles[path]
		if !ok || owner.mode != expected || owner.parent != filepath.Dir(path) {
			t.Fatalf("owner-only path %s = %+v, %v", path, owner, ok)
		}
		fd, err := makeReadOnlyProcFDWithMode("test-owner", []byte("value\n"), owner.mode)
		if err != nil {
			t.Fatal(err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			unix.Close(fd)
			t.Fatal(err)
		}
		unix.Close(fd)
		if os.FileMode(stat.Mode).Perm() != expected {
			t.Fatalf("injected %s mode = %04o, want %04o", path, os.FileMode(stat.Mode).Perm(), expected)
		}
	}
	if !procPlainReadFlags(unix.O_RDONLY, 0) || procPlainReadFlags(unix.O_WRONLY, 0) {
		t.Fatal("owner-only fast path must accept reads but leave writes to FUSE")
	}
}

func TestProcFSUIDUsesFilesystemUID(t *testing.T) {
	uid, err := procFSUID([]byte("Name:\ttest\nUid:\t1000\t1001\t1002\t1003\n"))
	if err != nil || uid != 1003 {
		t.Fatalf("fsuid = %d, %v; want 1003", uid, err)
	}
	for _, status := range []string{"Uid:\t0\t0\t0\n", "Uid:\t0\t0\t0\tbad\n", "Name:\ttest\n"} {
		if _, err := procFSUID([]byte(status)); err == nil {
			t.Fatalf("accepted invalid status %q", status)
		}
	}
}

func TestProcOwnerReadableSameUserNamespace(t *testing.T) {
	tracer := &syscallTracer{service: &SyscallMonitorService{prs: process.NewProcessService()}}
	if got := tracer.procOwnerReadable(uint32(os.Getpid())); got != (os.Geteuid() == 0) {
		t.Fatalf("same user namespace owner access = %v for euid %d", got, os.Geteuid())
	}
	if tracer.procOwnerReadable(0) {
		t.Fatal("accepted missing tracee PID")
	}
}

func TestProcHostUTSOpenUnsupportedFlagsContinue(t *testing.T) {
	tracer := &syscallTracer{service: &SyscallMonitorService{}, memParser: literalProcPathParser("/proc/sys/kernel/ostype")}
	req := &sysRequest{ID: 43, Data: libseccomp.ScmpNotifData{Args: []uint64{0, 1, unix.O_WRONLY}}}
	response, err := tracer.processOpenat(req, 0, nil)
	if err != nil || response == nil || response.Flags != libseccomp.NotifRespFlagContinue {
		t.Fatalf("unsupported open: response = %+v, err = %v; want continue", response, err)
	}
}
