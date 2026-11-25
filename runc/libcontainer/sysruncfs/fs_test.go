package sysruncfs

import (
	"path/filepath"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestAddSeccompTrapsPreservesOtherRules(t *testing.T) {
	spec := &specs.Spec{Linux: &specs.Linux{Seccomp: &specs.LinuxSeccomp{
		DefaultAction: specs.ActErrno,
		Syscalls: []specs.LinuxSyscall{
			{Names: []string{"mount", "read"}, Action: specs.ActAllow},
			{Names: []string{"openat", "openat2", "statx", "getdents64"}, Action: specs.ActAllow},
		},
	}}}
	if err := AddSeccompTraps(spec); err != nil {
		t.Fatal(err)
	}
	if spec.Linux.Seccomp.ListenerPath != "/run/sysrunc/sysfs-seccomp.sock" {
		t.Fatal("missing FS seccomp listener")
	}
	seen := map[string]specs.LinuxSeccompAction{}
	for _, rule := range spec.Linux.Seccomp.Syscalls {
		for _, name := range rule.Names {
			seen[name] = rule.Action
		}
	}
	for _, name := range []string{"mount", "umount2", "sysinfo", "openat", "openat2", "statx", "getdents64"} {
		if seen[name] != specs.ActNotify {
			t.Fatalf("%s action = %s, want notify", name, seen[name])
		}
	}
	if seen["read"] != specs.ActAllow {
		t.Fatal("unrelated seccomp rule changed")
	}
}

func TestAddSeccompTrapsKeepsDeniedReadSyscalls(t *testing.T) {
	spec := &specs.Spec{Linux: &specs.Linux{Seccomp: &specs.LinuxSeccomp{
		DefaultAction: specs.ActErrno,
		Syscalls: []specs.LinuxSyscall{
			{Names: []string{"openat"}, Action: specs.ActErrno},
			{Names: []string{"statx"}, Action: specs.ActAllow, Args: []specs.LinuxSeccompArg{{Index: 0, Value: 1, Op: specs.OpEqualTo}}},
		},
	}}}
	if err := AddSeccompTraps(spec); err != nil {
		t.Fatal(err)
	}
	for _, rule := range spec.Linux.Seccomp.Syscalls {
		for _, name := range rule.Names {
			if (name == "openat" || name == "statx") && rule.Action == specs.ActNotify {
				t.Fatalf("restricted %s was changed to notify", name)
			}
		}
	}
}

func TestAddSeccompTrapsKeepsDeniedStatxAndGetdents(t *testing.T) {
	spec := &specs.Spec{Linux: &specs.Linux{Seccomp: &specs.LinuxSeccomp{
		DefaultAction: specs.ActAllow,
		Syscalls: []specs.LinuxSyscall{
			{Names: []string{"statx"}, Action: specs.ActErrno},
			{Names: []string{"getdents64"}, Action: specs.ActErrno},
		},
	}}}
	if err := AddSeccompTraps(spec); err != nil {
		t.Fatal(err)
	}
	seen := map[string]specs.LinuxSeccompAction{}
	for _, rule := range spec.Linux.Seccomp.Syscalls {
		for _, name := range rule.Names {
			seen[name] = rule.Action
		}
	}
	if seen["statx"] != specs.ActErrno || seen["getdents64"] != specs.ActErrno {
		t.Fatalf("denied syscall actions changed: %v", seen)
	}
}

func TestAddMountsUsesContainerDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "container")
	spec := &specs.Spec{Root: &specs.Root{Readonly: true}, Linux: &specs.Linux{}, Mounts: []specs.Mount{{Destination: "/proc/cpuinfo", Source: "old"}}}
	AddMounts(spec, root)
	count := 0
	for _, mount := range spec.Mounts {
		if mount.Destination == "/proc/cpuinfo" {
			count++
			if len(mount.Options) != 1 || mount.Options[0] != "rbind" {
				t.Fatalf("file bind options = %v, want rbind without propagation", mount.Options)
			}
			if mount.Source != filepath.Join(root, "proc/cpuinfo") {
				t.Fatalf("source = %q", mount.Source)
			}
		}
	}
	if count != 1 {
		t.Fatalf("cpuinfo mounts = %d, want 1", count)
	}
}
