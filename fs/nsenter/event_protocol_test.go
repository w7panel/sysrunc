package nsenter

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/w7panel/sysrunc/fs/domain"
	"github.com/w7panel/sysrunc/runc/libcontainer"
)

func TestReadNsenterPID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    pid
		wantErr bool
	}{
		{"current nsexec protocol", "\x00{\"stage1_pid\":101,\"stage2_pid\":102}\n", pid{Pid: 102, PidFirstChild: 101}, false},
		{"missing handshake", "{\"stage1_pid\":101,\"stage2_pid\":102}\n", pid{}, true},
		{"missing pid", "\x00{\"stage1_pid\":101}\n", pid{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readNsenterPID(strings.NewReader(tt.input))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("readNsenterPID() = %+v, %v; want %+v, error=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestBootstrapWireMatchesLibcontainer(t *testing.T) {
	if bootstrapInitMsg != int(libcontainer.InitMsg) ||
		bootstrapNsPathsAttr != libcontainer.NsPathsAttr ||
		bootstrapCloneFlagsAttr != libcontainer.CloneFlagsAttr {
		t.Fatal("nsenter bootstrap constants differ from libcontainer")
	}
	ours := []interface{ Serialize() []byte }{
		bootstrapByteMsg{Type: bootstrapNsPathsAttr, Value: []byte("user:/proc/42/ns/user")},
		bootstrapInt32Msg{Type: bootstrapCloneFlagsAttr, Value: 0x20000},
	}
	upstream := []interface{ Serialize() []byte }{
		&libcontainer.Bytemsg{Type: libcontainer.NsPathsAttr, Value: []byte("user:/proc/42/ns/user")},
		&libcontainer.Int32msg{Type: libcontainer.CloneFlagsAttr, Value: 0x20000},
	}
	for i := range ours {
		if !bytes.Equal(ours[i].Serialize(), upstream[i].Serialize()) {
			t.Fatalf("bootstrap message %d differs from libcontainer", i)
		}
	}
}

func TestNamespacePathsSkipsCurrentUserNamespace(t *testing.T) {
	namespaces := []domain.NStype{domain.NStypeUser, domain.NStypeMount}
	event := &NSenterEvent{Pid: uint32(os.Getpid()), Namespace: &namespaces}
	paths, err := event.namespacePaths()
	if err != nil {
		t.Fatal(err)
	}
	want := "mnt:/proc/" + strconv.Itoa(os.Getpid()) + "/ns/mnt"
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("namespacePaths() = %v, want [%s]", paths, want)
	}
}

func TestNamespacePathsEntersCgroupBeforeUser(t *testing.T) {
	namespaces := []domain.NStype{domain.NStypeUser, domain.NStypePid, domain.NStypeCgroup}
	event := &NSenterEvent{Pid: uint32(os.Getpid()), Namespace: &namespaces}
	paths, err := event.namespacePaths()
	if err != nil {
		t.Fatal(err)
	}
	prefix := "cgroup:/proc/" + strconv.Itoa(os.Getpid()) + "/ns/cgroup"
	if len(paths) != 2 || paths[0] != prefix {
		t.Fatalf("namespacePaths() = %v, want cgroup before other namespaces", paths)
	}
}
