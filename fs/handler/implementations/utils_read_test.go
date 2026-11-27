package implementations

import (
	"context"
	"io"
	"testing"

	"github.com/w7panel/sysrunc/fs/domain"
)

type seededVirtualWorker struct{ value []byte }

func (w *seededVirtualWorker) ReadVirtualSysctl(context.Context, string, string) ([]byte, error) {
	return append([]byte(nil), w.value...), nil
}

func (w *seededVirtualWorker) SetVirtualSysctl(_ context.Context, _, _ string, data []byte) error {
	w.value = append([]byte(nil), data...)
	return nil
}

type sysctlBackingNode struct {
	domain.IOnodeIface
	value []byte
}

func (n sysctlBackingNode) Name() string { return "overcommit_memory" }
func (n sysctlBackingNode) Path() string { return "/proc/sys/vm/overcommit_memory" }
func (n sysctlBackingNode) Open() error  { return nil }
func (n sysctlBackingNode) Close() error { return nil }
func (n sysctlBackingNode) ReadAt(dst []byte, offset int64) (int, error) {
	if offset >= int64(len(n.value)) {
		return 0, io.EOF
	}
	num := copy(dst, n.value[offset:])
	return num, nil
}

func TestHostBackedVirtualSysctlSeedsFullValueAfterShortRead(t *testing.T) {
	worker := &seededVirtualWorker{}
	SetVirtualSysctlWorker(worker)
	defer SetVirtualSysctlWorker(nil)
	container := &qdiscTestContainer{data: make(map[string][]byte)}
	node := sysctlBackingNode{value: []byte("2\n")}
	handler := &ProcSysVm{HandlerBase: domain.HandlerBase{
		Service: qdiscTestService{}, EmuResourceMap: ProcSysVm_Handler.EmuResourceMap,
	}}
	first := &domain.HandlerRequest{Container: container, Data: make([]byte, 1)}
	num, err := readCntrData(handler, node, first)
	if err != nil || num != 1 || string(first.Data[:num]) != "2" || string(worker.value) != "2\n" {
		t.Fatalf("short read = %q, worker = %q, err = %v", first.Data[:num], worker.value, err)
	}
	full := &domain.HandlerRequest{Container: container, Data: make([]byte, 16)}
	num, err = readCntrData(handler, node, full)
	if err != nil || string(full.Data[:num]) != "2\n" {
		t.Fatalf("full cached read = %q, err = %v", full.Data[:num], err)
	}
}

func TestHostBackedVirtualSysctlUsesProcWorkerCache(t *testing.T) {
	const path = "/proc/sys/vm/overcommit_memory"
	worker := &virtualQdiscWorker{}
	SetVirtualSysctlWorker(worker)
	defer SetVirtualSysctlWorker(nil)
	container := &qdiscTestContainer{data: map[string][]byte{path: []byte("1\n")}}
	node := qdiscTestNode{path: path}
	handler := &ProcSysVm{HandlerBase: domain.HandlerBase{Service: qdiscTestService{}}}
	request := &domain.HandlerRequest{Container: container, Data: make([]byte, 16)}
	n, err := readCntrData(handler, node, request)
	if err != nil || string(request.Data[:n]) != "1\n" || string(worker.value) != "1\n" {
		t.Fatalf("worker-backed read = %q, worker = %q, err = %v", request.Data[:n], worker.value, err)
	}
}

func TestReadAvailableAtContinuesAfterShortRead(t *testing.T) {
	const contents = "virtual-sysctl\n"
	var offsets []int64
	read := func(dst []byte, offset int64) (int, error) {
		offsets = append(offsets, offset)
		if offset >= int64(len(contents)) {
			return 0, io.EOF
		}
		end := int(offset) + 3
		if end > len(contents) {
			end = len(contents)
		}
		return copy(dst, contents[offset:end]), nil
	}
	buf := make([]byte, 32)
	n, err := readAvailableAt(read, buf, 0)
	if err != io.EOF || string(buf[:n]) != contents {
		t.Fatalf("read = %q, %v; want %q, EOF", buf[:n], err, contents)
	}
	if len(offsets) < 3 || offsets[1] != 3 {
		t.Fatalf("short read did not advance offset: %v", offsets)
	}
}

func TestReadAvailableAtRejectsZeroProgress(t *testing.T) {
	buf := make([]byte, 1)
	n, err := readAvailableAt(func([]byte, int64) (int, error) { return 0, nil }, buf, 0)
	if n != 0 || err != io.ErrNoProgress {
		t.Fatalf("read = %d, %v; want 0, ErrNoProgress", n, err)
	}
}

func TestWriteOnceAtRejectsShortSysctlWrite(t *testing.T) {
	data := []byte("123\n")
	calls := 0
	err := writeOnceAt(func(got []byte, offset int64) (int, error) {
		calls++
		if offset != 0 || string(got) != string(data) {
			t.Fatalf("write = %q at %d", got, offset)
		}
		return 2, nil
	}, data, 0)
	if err != io.ErrShortWrite || calls != 1 {
		t.Fatalf("write result = %v after %d calls; want ErrShortWrite after one call", err, calls)
	}
}
