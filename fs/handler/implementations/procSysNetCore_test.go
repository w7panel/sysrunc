package implementations

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"github.com/w7panel/sysrunc/fs/domain"
)

type qdiscTestContainer struct {
	domain.ContainerIface
	mu   sync.Mutex
	data map[string][]byte
}

func (c *qdiscTestContainer) Lock()      { c.mu.Lock() }
func (c *qdiscTestContainer) Unlock()    { c.mu.Unlock() }
func (c *qdiscTestContainer) ID() string { return "qdisc-test" }
func (c *qdiscTestContainer) Data(path string, offset int64, data *[]byte) (int, error) {
	value, ok := c.data[path]
	if !ok || offset >= int64(len(value)) {
		return 0, io.EOF
	}
	n := copy(*data, value[offset:])
	return n, nil
}

type virtualQdiscWorker struct{ value []byte }

func (w *virtualQdiscWorker) ReadVirtualSysctl(context.Context, string, string) ([]byte, error) {
	if w.value == nil {
		return []byte("fq_codel\n"), nil
	}
	return append([]byte(nil), w.value...), nil
}
func (w *virtualQdiscWorker) SetVirtualSysctl(_ context.Context, _, _ string, data []byte) error {
	w.value = append([]byte(nil), data...)
	return nil
}

func TestDefaultQdiscWorkerKeepsFullValueAfterShortRead(t *testing.T) {
	SetVirtualSysctlWorker(&virtualQdiscWorker{})
	defer SetVirtualSysctlWorker(nil)
	path := "/proc/sys/net/core/default_qdisc"
	container := &qdiscTestContainer{data: make(map[string][]byte)}
	node := qdiscTestNode{path: path}
	first := &domain.HandlerRequest{Container: container, Data: make([]byte, 1)}
	if n, err := readCntrDataWithoutHost(node, first, "fq_codel\n"); err != nil || n != 1 || string(first.Data[:n]) != "f" {
		t.Fatalf("short read = %q, %v", first.Data, err)
	}
	SetVirtualSysctlWorker(nil)
	full := &domain.HandlerRequest{Container: container, Data: make([]byte, 64)}
	if n, err := readCntrDataWithoutHost(node, full, "fq_codel\n"); err != nil || string(full.Data[:n]) != "fq_codel\n" {
		t.Fatalf("cached full value = %q, %v", full.Data[:n], err)
	}
}
func (c *qdiscTestContainer) SetData(path string, offset int64, data []byte) error {
	c.data[path] = append([]byte(nil), data...)
	return nil
}

type qdiscTestNode struct {
	domain.IOnodeIface
	path string
}

func (n qdiscTestNode) Name() string { return "default_qdisc" }
func (n qdiscTestNode) Path() string { return n.path }

type qdiscTestService struct{ domain.HandlerServiceIface }

func (qdiscTestService) IgnoreErrors() bool { return false }

func TestDefaultQdiscWithoutHostIsContainerLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "default_qdisc")
	node := qdiscTestNode{path: path}
	container := &qdiscTestContainer{data: make(map[string][]byte)}
	handler := &ProcSysNetCore{HandlerBase: domain.HandlerBase{Service: qdiscTestService{}}}
	read := func() string {
		t.Helper()
		req := &domain.HandlerRequest{Container: container, Data: make([]byte, 64)}
		n, err := handler.Read(node, req)
		if err != nil {
			t.Fatal(err)
		}
		return string(req.Data[:n])
	}
	if got := read(); got != "fq_codel\n" {
		t.Fatalf("initial qdisc = %q", got)
	}
	writeReq := &domain.HandlerRequest{Container: container, Data: []byte("fq\n")}
	if n, err := handler.Write(node, writeReq); err != nil || n != len(writeReq.Data) {
		t.Fatalf("write qdisc = %d, %v", n, err)
	}
	if got := read(); got != "fq\n" {
		t.Fatalf("container qdisc after write = %q", got)
	}
	if _, err := handler.Write(node, &domain.HandlerRequest{Container: container, Data: []byte("invalid\n")}); err == nil {
		t.Fatal("unsupported qdisc accepted")
	}
}

func TestNetCoreDirectoryKeepsVirtualFileMetadata(t *testing.T) {
	h := &ProcSysNetCore{HandlerBase: domain.HandlerBase{
		Service:        neighborDirService{},
		EmuResourceMap: ProcSysNetCore_Handler.EmuResourceMap,
	}}
	entries, err := h.ReadDirAll(qdiscTestNode{}, &domain.HandlerRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "default_qdisc" {
			want := ProcSysNetCore_Handler.EmuResourceMap[entry.Name()]
			if entry.Mode() != want.Mode || entry.Size() != want.Size || entry.IsDir() {
				t.Fatalf("default_qdisc directory metadata = mode %v size %d isDir %v", entry.Mode(), entry.Size(), entry.IsDir())
			}
			return
		}
	}
	t.Fatal("default_qdisc missing from directory")
}
