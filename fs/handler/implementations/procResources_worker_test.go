package implementations

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/w7panel/sysrunc/fs/domain"
)

type snapshotTestContainer struct{ domain.ContainerIface }

func (snapshotTestContainer) ID() string { return "snapshot-test" }

type snapshotTestWorker struct {
	path  string
	data  []byte
	err   error
	calls int
}

func (w *snapshotTestWorker) read() ([]byte, error) {
	w.calls++
	return w.data, w.err
}

func (w *snapshotTestWorker) ReadCPUInfo(context.Context, string, uint32) ([]byte, error) {
	return w.read()
}
func (w *snapshotTestWorker) ReadDiskstats(context.Context, string, uint32) ([]byte, error) {
	return w.read()
}
func (w *snapshotTestWorker) ReadSwaps(context.Context, string, uint32) ([]byte, error) {
	return w.read()
}
func (w *snapshotTestWorker) ReadMemInfo(context.Context, string, uint32) ([]byte, error) {
	return w.read()
}
func (w *snapshotTestWorker) ReadUptime(context.Context, string, uint32) ([]byte, error) {
	return w.read()
}
func (w *snapshotTestWorker) ReadStat(context.Context, string, uint32) ([]byte, error) {
	return w.read()
}
func (w *snapshotTestWorker) ReadResource(_ context.Context, _ string, _ uint32, path string) ([]byte, error) {
	w.path = path
	return w.read()
}

func TestReadOnlyResourceUsesProcWorkerAndFallsBack(t *testing.T) {
	worker := &snapshotTestWorker{data: []byte("worker\n")}
	SetProcSnapshotWorker(worker)
	defer SetProcSnapshotWorker(nil)
	container := snapshotTestContainer{}
	for _, tc := range []struct {
		path       string
		skipWorker bool
		workerErr  error
		want       string
		wantCalls  int
	}{
		{"/proc/meminfo", false, nil, "worker\n", 1},
		{"/proc/pressure/cpu", false, nil, "worker\n", 1},
		{"/proc/slabinfo", false, errors.New("unavailable"), "fallback\n", 1},
		{"/proc/meminfo", true, nil, "fallback\n", 0},
	} {
		worker.calls, worker.path, worker.err = 0, "", tc.workerErr
		h := newReadOnlyResource("test", tc.path, func(*domain.HandlerRequest) ([]byte, error) {
			return []byte("fallback\n"), nil
		})
		req := &domain.HandlerRequest{Pid: uint32(os.Getpid()), Container: container, SkipWorker: tc.skipWorker}
		data, err := h.snapshotData(req)
		if err != nil || string(data) != tc.want || worker.calls != tc.wantCalls {
			t.Fatalf("%s skip=%v: data=%q err=%v calls=%d", tc.path, tc.skipWorker, data, err, worker.calls)
		}
		if tc.path == "/proc/pressure/cpu" && worker.path != tc.path {
			t.Fatalf("resource path = %q, want %q", worker.path, tc.path)
		}
	}
}
