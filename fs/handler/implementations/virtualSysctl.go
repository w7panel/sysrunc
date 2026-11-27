package implementations

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/w7panel/sysrunc/fs/domain"
)

type virtualSysctlWorker interface {
	ReadVirtualSysctl(context.Context, string, string) ([]byte, error)
	SetVirtualSysctl(context.Context, string, string, []byte) error
}

var virtualSysctlBackend struct {
	sync.RWMutex
	worker virtualSysctlWorker
}

// SetVirtualSysctlWorker connects FUSE and seccomp-backed reads to the
// registered per-container proc worker.
func SetVirtualSysctlWorker(worker virtualSysctlWorker) {
	virtualSysctlBackend.Lock()
	virtualSysctlBackend.worker = worker
	virtualSysctlBackend.Unlock()
}

func currentVirtualSysctlWorker() virtualSysctlWorker {
	virtualSysctlBackend.RLock()
	defer virtualSysctlBackend.RUnlock()
	return virtualSysctlBackend.worker
}

func readVirtualSysctlFromWorker(cntr domain.ContainerIface, path string, offset int64, out *[]byte) (int, bool) {
	worker := currentVirtualSysctlWorker()
	if worker == nil || offset < 0 {
		return 0, false
	}
	// A restored manager may have cached writes from before proc worker startup.
	// Seed that state once; the supervisor skips unchanged, acknowledged values.
	cached := make([]byte, 1024)
	if n, err := cntr.Data(path, 0, &cached); n > 0 && (err == nil || errors.Is(err, io.EOF)) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = worker.SetVirtualSysctl(ctx, cntr.ID(), path, append([]byte(nil), cached[:n]...))
		cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	data, err := worker.ReadVirtualSysctl(ctx, cntr.ID(), path)
	cancel()
	if err != nil || data == nil {
		return 0, false
	}
	if err := cntr.SetData(path, 0, data); err != nil {
		return 0, false
	}
	if offset >= int64(len(data)) {
		return 0, true
	}
	end := int(offset) + len(*out)
	if end > len(data) {
		end = len(data)
	}
	*out = data[offset:end]
	return len(*out), true
}

func syncVirtualSysctlToWorker(cntr domain.ContainerIface, path string) {
	worker := currentVirtualSysctlWorker()
	if worker == nil {
		return
	}
	data := make([]byte, 1024)
	n, err := cntr.Data(path, 0, &data)
	if n <= 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = worker.SetVirtualSysctl(ctx, cntr.ID(), path, append([]byte(nil), data[:n]...))
	cancel()
}

// WorkerVirtualSysctl lists the container-cached virtual files whose values
// belong to the proc worker. The handler still validates writes and performs
// any required host-side operation before publishing a new cached value.
func WorkerVirtualSysctl(path string) bool {
	switch path {
	case "/proc/sys/kernel/pid_max", "/proc/sys/kernel/domainname", "/proc/sys/kernel/hostname",
		"/proc/sys/kernel/kptr_restrict", "/proc/sys/kernel/dmesg_restrict",
		"/proc/sys/kernel/panic", "/proc/sys/kernel/panic_on_oops",
		"/proc/sys/kernel/sysrq", "/proc/sys/kernel/printk",
		"/proc/sys/kernel/yama/ptrace_scope",
		"/proc/sys/fs/file-max", "/proc/sys/fs/nr_open",
		"/proc/sys/fs/protected_hardlinks", "/proc/sys/fs/protected_symlinks",
		"/proc/sys/net/core/default_qdisc", "/proc/sys/net/core/somaxconn",
		"/proc/sys/net/ipv4/neigh/default/gc_thresh1",
		"/proc/sys/net/ipv4/neigh/default/gc_thresh2",
		"/proc/sys/net/ipv4/neigh/default/gc_thresh3",
		"/proc/sys/net/ipv4/vs/conntrack", "/proc/sys/net/ipv4/vs/conn_reuse_mode",
		"/proc/sys/net/ipv4/vs/expire_nodest_conn", "/proc/sys/net/ipv4/vs/expire_quiescent_template",
		"/proc/sys/net/netfilter/nf_conntrack_max",
		"/proc/sys/net/netfilter/nf_conntrack_generic_timeout",
		"/proc/sys/net/netfilter/nf_conntrack_tcp_be_liberal",
		"/proc/sys/net/netfilter/nf_conntrack_tcp_timeout_established",
		"/proc/sys/net/netfilter/nf_conntrack_tcp_timeout_close_wait",
		"/proc/sys/net/unix/max_dgram_qlen",
		"/proc/sys/vm/overcommit_memory", "/proc/sys/vm/mmap_min_addr",
		"/sys/module/nf_conntrack/parameters/hashsize":
		return true
	default:
		return false
	}
}

// VirtualSysctlDefault returns the initial value of a sysctl that the nested
// kernel may omit but the Sysrunc proc view exposes per container.
func VirtualSysctlDefault(path string) ([]byte, bool) {
	switch path {
	case "/proc/sys/net/core/default_qdisc":
		return []byte("fq_codel\n"), true
	case "/proc/sys/net/ipv4/neigh/default/gc_thresh1":
		return []byte("128\n"), true
	case "/proc/sys/net/ipv4/neigh/default/gc_thresh2":
		return []byte("512\n"), true
	case "/proc/sys/net/ipv4/neigh/default/gc_thresh3":
		return []byte("1024\n"), true
	default:
		return nil, false
	}
}
