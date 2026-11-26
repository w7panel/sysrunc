package sysruncfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/w7panel/sysrunc/ipc/sysruncFsGrpc"
	unixIpc "github.com/w7panel/sysrunc/ipc/unix"
)

const EnabledAnnotation = "sysrunc.io/fs"
const TickerAnnotation = "sysrunc.io/ticker"

func Enabled(annotations map[string]string) bool {
	return annotations[EnabledAnnotation] == "true"
}

func PreRegister(spec *specs.Spec, id string) (string, error) {
	mountpoint, err := sysruncFsGrpc.GetMountpoint()
	if err != nil {
		return "", fmt.Errorf("get sysrunc-fs mountpoint: %w", err)
	}
	data := &sysruncFsGrpc.ContainerData{Id: id}
	if spec.Linux != nil {
		for _, ns := range spec.Linux.Namespaces {
			if ns.Type == specs.NetworkNamespace && ns.Path != "" {
				data.Netns = ns.Path
			}
		}
	}
	if err := sysruncFsGrpc.SendContainerPreRegistration(data); err != nil {
		return "", fmt.Errorf("pre-register sysrunc-fs container: %w", err)
	}
	return filepath.Join(mountpoint, id), nil
}

func AddMounts(spec *specs.Spec, sourceRoot string) {
	paths := []string{
		"proc/cpuinfo", "proc/diskstats", "proc/meminfo", "proc/stat",
		"proc/slabinfo", "proc/sys", "proc/swaps", "proc/uptime", "proc/loadavg",
		"sys/devices/system/cpu/online", "sys/devices/system/cpu/present",
		"sys/kernel", "sys/devices/virtual", "sys/module/nf_conntrack/parameters",
	}
	for _, path := range paths {
		dest := "/" + path
		for i := 0; i < len(spec.Mounts); i++ {
			if spec.Mounts[i].Destination == dest {
				spec.Mounts = append(spec.Mounts[:i], spec.Mounts[i+1:]...)
				i--
			}
		}
		spec.Mounts = append(spec.Mounts, specs.Mount{
			Destination: dest, Source: filepath.Join(sourceRoot, path),
			Type: "bind", Options: []string{"rbind"},
		})
		if spec.Root != nil && spec.Root.Readonly && spec.Linux != nil {
			spec.Linux.ReadonlyPaths = append(spec.Linux.ReadonlyPaths, dest)
		}
	}
}

func AddSeccompTraps(spec *specs.Spec) error {
	if spec.Linux == nil {
		spec.Linux = &specs.Linux{}
	}
	if spec.Linux.Seccomp == nil {
		spec.Linux.Seccomp = &specs.LinuxSeccomp{DefaultAction: specs.ActAllow}
	}
	if spec.Linux.Seccomp.ListenerPath != "" {
		return fmt.Errorf("sysrunc-fs conflicts with OCI seccomp listener %q", spec.Linux.Seccomp.ListenerPath)
	}
	// The FS worker emulates these syscalls. Remove earlier rules for the same
	// names so an OCI profile's errno action cannot shadow notification.
	traps := map[string]bool{"mount": true, "umount2": true, "sysinfo": true}
	// Read-path interception must not turn a denied or argument-restricted OCI
	// syscall into an unconditional allow via NOTIF_CONTINUE.
	for _, name := range []string{"openat", "openat2", "statx", "getdents64"} {
		traps[name] = canNotifyReadSyscall(spec.Linux.Seccomp, name)
	}
	// getdents64 keeps the tracee's FUSE directory fd. The notification path
	// only handles an exact safe directory and continues all other requests.
	var rules []specs.LinuxSyscall
	for _, rule := range spec.Linux.Seccomp.Syscalls {
		var keep []string
		for _, name := range rule.Names {
			if !traps[name] {
				keep = append(keep, name)
			}
		}
		if len(keep) != 0 {
			rule.Names = keep
			rules = append(rules, rule)
		}
	}
	for name := range traps {
		if !traps[name] {
			continue
		}
		rules = append(rules, specs.LinuxSyscall{Names: []string{name}, Action: specs.ActNotify})
	}
	spec.Linux.Seccomp.Syscalls = rules
	spec.Linux.Seccomp.ListenerPath = "/run/sysrunc/sysfs-seccomp.sock"
	return nil
}

func canNotifyReadSyscall(profile *specs.LinuxSeccomp, name string) bool {
	found := false
	for _, rule := range profile.Syscalls {
		for _, syscallName := range rule.Names {
			if syscallName != name {
				continue
			}
			found = true
			if rule.Action != specs.ActAllow || len(rule.Args) != 0 {
				return false
			}
		}
	}
	return found || profile.DefaultAction == specs.ActAllow
}

func Register(id string, pid int, startTime uint64, hostname string, uid, gid, size int, readonly, masked []string, ticker bool) error {
	data := &sysruncFsGrpc.ContainerData{
		Id: id, InitPid: int32(pid), Hostname: hostname,
		UidFirst: int32(uid), UidSize: int32(size), GidFirst: int32(gid), GidSize: int32(size),
		InitStartTime: startTime, Ticker: ticker,
	}
	for _, path := range readonly {
		if strings.HasPrefix(path, "/proc") {
			data.ProcRoPaths = append(data.ProcRoPaths, path)
		}
	}
	for _, path := range masked {
		// The same field also carries sysfs masks so the seccomp read fast
		// path cannot expose a file hidden by the OCI maskedPaths policy.
		if path == "/proc" || strings.HasPrefix(path, "/proc/") ||
			path == "/sys" || strings.HasPrefix(path, "/sys/") {
			data.ProcMaskPaths = append(data.ProcMaskPaths, path)
		}
	}
	cgroup, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return err
	}
	data.CgroupData = string(cgroup)
	data.Pidns, err = os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", pid))
	if err != nil {
		return err
	}
	return sysruncFsGrpc.SendContainerRegistration(data)
}

func SendCreationTime(id string, t time.Time) error {
	return sysruncFsGrpc.SendContainerUpdate(&sysruncFsGrpc.ContainerData{Id: id, Ctime: t})
}

func Unregister(id string) error {
	return sysruncFsGrpc.SendContainerUnregistration(&sysruncFsGrpc.ContainerData{Id: id})
}

func SendSeccompInit(id string, pid, fd int) error {
	conn, err := unixIpc.Connect("/run/sysrunc/sysfs-seccomp.sock")
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := unixIpc.SendSeccompInitMsg(conn, int32(pid), id, int32(fd)); err != nil {
		return err
	}
	return unixIpc.RecvSeccompInitAckMsg(conn)
}
