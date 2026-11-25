package specconv

import (
	"strings"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestIDMappedBindMountConversion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		options   []string
		recursive bool
	}{
		{"idmap option", []string{"bind", "idmap"}, false},
		{"ridmap option", []string{"rbind", "ridmap"}, true},
		{"OCI mappings", []string{"bind"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mount := specs.Mount{Source: "/tmp", Destination: "/data", Options: tc.options}
			if tc.name == "OCI mappings" {
				mount.UIDMappings = []specs.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}}
				mount.GIDMappings = []specs.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}}
			}
			got, err := createLibcontainerMount("/", mount)
			if err != nil {
				t.Fatal(err)
			}
			if !got.IDMapped || got.IDMapRecursive != tc.recursive || got.Device != "bind" {
				t.Fatalf("unexpected idmapped mount: %+v", got)
			}
			if got.Data != "" {
				t.Fatalf("idmap leaked into filesystem data: %q", got.Data)
			}
		})
	}
}

func TestIDMappedMountRequiresContainerMapping(t *testing.T) {
	base := Example()
	base.Root.Path = "/"
	base.Linux.Namespaces = append(base.Linux.Namespaces, specs.LinuxNamespace{Type: specs.UserNamespace})
	base.Linux.UIDMappings = []specs.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}}
	base.Linux.GIDMappings = []specs.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}}
	bind := specs.Mount{
		Source: "/tmp", Destination: "/data", Options: []string{"bind"},
		UIDMappings: base.Linux.UIDMappings, GIDMappings: base.Linux.GIDMappings,
	}
	base.Mounts = append(base.Mounts, bind)
	if _, err := CreateLibcontainerConfig(&CreateOpts{Spec: base, CgroupName: "test"}); err != nil {
		t.Fatalf("matching idmap was rejected: %v", err)
	}
	base.Mounts[len(base.Mounts)-1].UIDMappings = []specs.LinuxIDMapping{{ContainerID: 0, HostID: 200000, Size: 65536}}
	if _, err := CreateLibcontainerConfig(&CreateOpts{Spec: base, CgroupName: "test"}); err == nil ||
		!strings.Contains(err.Error(), "requires the container's UID/GID mappings") {
		t.Fatalf("unexpected error for unrelated idmap: %v", err)
	}
}

func TestIDMappedNonBindMountRejected(t *testing.T) {
	_, err := createLibcontainerMount("/", specs.Mount{
		Source: "tmpfs", Destination: "/data", Type: "tmpfs", Options: []string{"idmap"},
	})
	if err == nil {
		t.Fatal("expected a non-bind idmap mount to be rejected")
	}
}
