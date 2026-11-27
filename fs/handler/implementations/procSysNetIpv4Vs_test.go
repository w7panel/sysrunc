package implementations

import (
	"testing"

	"github.com/w7panel/sysrunc/fs/domain"
)

func TestIPVSVirtualDirectoryFileMetadata(t *testing.T) {
	h := &ProcSysNetIpv4Vs{HandlerBase: domain.HandlerBase{
		Service:        neighborDirService{},
		EmuResourceMap: ProcSysNetIpv4Vs_Handler.EmuResourceMap,
	}}
	entries, err := h.ReadDirAll(qdiscTestNode{}, &domain.HandlerRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "conntrack" {
			want := ProcSysNetIpv4Vs_Handler.EmuResourceMap[entry.Name()]
			if entry.Mode() != want.Mode || entry.Size() != want.Size || entry.IsDir() {
				t.Fatalf("IPVS conntrack directory metadata = mode %v size %d isDir %v", entry.Mode(), entry.Size(), entry.IsDir())
			}
			return
		}
	}
	t.Fatal("IPVS conntrack missing from directory")
}
