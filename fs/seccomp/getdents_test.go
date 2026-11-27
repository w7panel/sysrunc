package seccomp

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	fusefs "bazil.org/fuse/fs"
	"github.com/w7panel/sysrunc/fs/domain"
	"github.com/w7panel/sysrunc/fs/handler/implementations"
	"golang.org/x/sys/unix"
)

func TestEncodeRandomDirentsPreservesFuseCookies(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"uuid", "boot_id"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0444); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	const parentInode = 1218751057
	first, next, ok := encodeRandomDirents(entries, parentInode, 0, 32)
	if !ok || len(first) != 32 || next != 32 ||
		binary.LittleEndian.Uint64(first[0:8]) != fusefs.GenerateDynamicInode(parentInode, "boot_id") ||
		binary.LittleEndian.Uint64(first[8:16]) != 32 ||
		binary.LittleEndian.Uint16(first[16:18]) != 32 || first[18] != unix.DT_REG ||
		string(first[19:26]) != "boot_id" || first[26] != 0 {
		t.Fatalf("first dirent = %v, next=%d, ok=%v", first, next, ok)
	}
	second, next, ok := encodeRandomDirents(entries, parentInode, 32, 24)
	if !ok || len(second) != 24 || next != 56 ||
		binary.LittleEndian.Uint64(second[8:16]) != 56 ||
		string(second[19:23]) != "uuid" {
		t.Fatalf("second dirent = %v, next=%d, ok=%v", second, next, ok)
	}
	eof, next, ok := encodeRandomDirents(entries, parentInode, 56, 8192)
	if !ok || len(eof) != 0 || next != 56 {
		t.Fatalf("EOF = %v, next=%d, ok=%v", eof, next, ok)
	}
	if _, _, ok := encodeRandomDirents(entries, parentInode, 1, 8192); ok {
		t.Fatal("accepted a non-cookie directory offset")
	}
	if _, _, ok := encodeRandomDirents(entries, parentInode, 0, 31); ok {
		t.Fatal("accepted a buffer too small for the next record")
	}
}

func TestVirtualDirectorySelectionAndMasks(t *testing.T) {
	for _, path := range []string{procPressureDir, procRandomDir, "/proc/sys/fs", "/proc/sys/net/ipv4/neigh/default", "/sys/kernel", "/sys/devices/virtual/dmi/id"} {
		if !virtualDirectories[path] {
			t.Fatalf("missing virtual directory %s", path)
		}
	}
	if virtualDirectories["/proc/sys/unknown"] || virtualDirectories["/sys/kernel/unknown"] {
		t.Fatal("unmanaged directory selected")
	}
	if !procDirMasked(procPressureDir, []string{procPressureDir + "/cpu"}) {
		t.Fatal("masked virtual child must retain FUSE listing")
	}
	if procDirMasked(procPressureDir, []string{procPressureDir + "-other"}) {
		t.Fatal("unrelated mask disabled fast path")
	}
}

func TestEmulatedDirectoryFastPathCoverage(t *testing.T) {
	handlers := []struct {
		path      string
		resources map[string]*domain.EmuResource
	}{
		{"/proc/sys", implementations.ProcSys_Handler.EmuResourceMap},
		{"/proc/sys/fs", implementations.ProcSysFs_Handler.EmuResourceMap},
		{"/proc/sys/kernel", implementations.ProcSysKernel_Handler.EmuResourceMap},
		{"/proc/sys/kernel/random", implementations.ProcSysKernelRandom_Handler.EmuResourceMap},
		{"/proc/sys/kernel/yama", implementations.ProcSysKernelYama_Handler.EmuResourceMap},
		{"/proc/sys/net/core", implementations.ProcSysNetCore_Handler.EmuResourceMap},
		{"/proc/sys/net/ipv4", implementations.ProcSysNetIpv4_Handler.EmuResourceMap},
		{"/proc/sys/net/ipv4/neigh", implementations.ProcSysNetIpv4Neigh_Handler.EmuResourceMap},
		{"/proc/sys/net/ipv4/vs", implementations.ProcSysNetIpv4Vs_Handler.EmuResourceMap},
		{"/proc/sys/net/netfilter", implementations.ProcSysNetNetfilter_Handler.EmuResourceMap},
		{"/proc/sys/net/unix", implementations.ProcSysNetUnix_Handler.EmuResourceMap},
		{"/proc/sys/vm", implementations.ProcSysVm_Handler.EmuResourceMap},
		{"/sys/kernel", implementations.SysKernel_Handler.EmuResourceMap},
		{"/sys/devices/virtual", implementations.SysDevicesVirtual_Handler.EmuResourceMap},
		{"/sys/devices/virtual/dmi", implementations.SysDevicesVirtualDmi_Handler.EmuResourceMap},
		{"/sys/devices/virtual/dmi/id", implementations.SysDevicesVirtualDmiId_Handler.EmuResourceMap},
		{"/sys/module/nf_conntrack/parameters", implementations.SysModuleNfconntrackParameters_Handler.EmuResourceMap},
	}
	for _, handler := range handlers {
		for name, resource := range handler.resources {
			if resource.Enabled && resource.Kind == domain.DirEmuResource {
				path := filepath.Join(handler.path, name)
				if !virtualDirectories[path] {
					t.Errorf("enabled emulated directory %s has no getdents64 fast path", path)
				}
			}
		}
	}
}
