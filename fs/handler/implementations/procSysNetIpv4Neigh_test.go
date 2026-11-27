package implementations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/w7panel/sysrunc/fs/domain"
)

type neighborDirPassThrough struct{ domain.PassthroughHandlerIface }

func (neighborDirPassThrough) ReadDirAll(domain.IOnodeIface, *domain.HandlerRequest) ([]os.FileInfo, error) {
	return nil, nil
}

type neighborDirService struct{ domain.HandlerServiceIface }

func (neighborDirService) GetPassThroughHandler() domain.PassthroughHandlerIface {
	return neighborDirPassThrough{}
}

func TestNeighborVirtualDirectoryEntryTypes(t *testing.T) {
	root := t.TempDir()
	h := &ProcSysNetIpv4Neigh{HandlerBase: domain.HandlerBase{
		Path: root, Service: neighborDirService{},
		EmuResourceMap: ProcSysNetIpv4Neigh_Handler.EmuResourceMap,
	}}
	for _, tc := range []struct {
		parent, name string
		isDir        bool
		mode         os.FileMode
	}{
		{root, "default", true, 0555},
		{filepath.Join(root, "default"), "gc_thresh1", false, 0644},
		{filepath.Join(root, "default"), "gc_thresh2", false, 0644},
		{filepath.Join(root, "default"), "gc_thresh3", false, 0644},
	} {
		entries, err := h.ReadDirAll(qdiscTestNode{path: tc.parent}, &domain.HandlerRequest{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range entries {
			if entry.Name() != tc.name {
				continue
			}
			found = true
			if entry.IsDir() != tc.isDir || entry.Mode().Perm() != tc.mode {
				t.Errorf("%s: mode=%v isDir=%v", tc.name, entry.Mode(), entry.IsDir())
			}
		}
		if !found {
			t.Errorf("%s missing from %s", tc.name, tc.parent)
		}
	}
}

func TestNeighborThresholdWithoutHostIsContainerLocal(t *testing.T) {
	root := t.TempDir()
	h := &ProcSysNetIpv4Neigh{HandlerBase: domain.HandlerBase{
		Path: root, Service: qdiscTestService{},
		EmuResourceMap: ProcSysNetIpv4Neigh_Handler.EmuResourceMap,
	}}
	a := &qdiscTestContainer{data: make(map[string][]byte)}
	b := &qdiscTestContainer{data: make(map[string][]byte)}
	for _, tc := range []struct{ name, initial string }{
		{"gc_thresh1", "128\n"}, {"gc_thresh2", "512\n"}, {"gc_thresh3", "1024\n"},
	} {
		node := qdiscTestNode{path: filepath.Join(root, "default", tc.name)}
		read := func(c *qdiscTestContainer) string {
			t.Helper()
			req := &domain.HandlerRequest{Container: c, Data: make([]byte, 64)}
			n, err := h.Read(node, req)
			if err != nil {
				t.Fatal(err)
			}
			return string(req.Data[:n])
		}
		if got := read(a); got != tc.initial {
			t.Fatalf("%s initial = %q", tc.name, got)
		}
		writeReq := &domain.HandlerRequest{Container: a, Data: []byte("2048\n")}
		if n, err := h.Write(node, writeReq); err != nil || n != len(writeReq.Data) {
			t.Fatalf("%s write = %d, %v", tc.name, n, err)
		}
		if got := read(a); got != "2048\n" {
			t.Fatalf("%s after write = %q", tc.name, got)
		}
		if got := read(b); got != tc.initial {
			t.Fatalf("%s leaked to second container: %q", tc.name, got)
		}
	}
}
