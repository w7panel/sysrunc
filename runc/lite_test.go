package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestRootfsAnnotation(t *testing.T) {
	spec := &specs.Spec{Annotations: map[string]string{liteRootfsAnnotation: "broken"}}
	if !liteRootfsEnabled(spec) {
		t.Fatal("rootfs annotation must enable runtime device setup")
	}
	if err := addLiteSpecialMounts(spec, "container"); err == nil || !strings.Contains(err.Error(), liteRootfsAnnotation) {
		t.Fatalf("invalid rootfs annotation: %v", err)
	}
	spec.Annotations[liteRootfsAnnotation] = "false"
	if liteRootfsEnabled(spec) {
		t.Fatal("disabled rootfs annotation enabled runtime device setup")
	}
}

func TestNestedSystemSpecRestoresSysboxSecurity(t *testing.T) {
	for _, uid := range []uint32{0, 1000} {
		spec := &specs.Spec{
			Process: &specs.Process{
				User:            specs.User{UID: uid},
				ApparmorProfile: "cri-default",
				Capabilities:    &specs.LinuxCapabilities{Bounding: []string{"CAP_CHOWN"}},
			},
			Linux: &specs.Linux{
				MaskedPaths:   []string{"/proc/kcore", "/proc/sys", "/other"},
				ReadonlyPaths: []string{"/proc/sys", "/other"},
			},
		}
		applyNestedSystemSecurity(spec, []string{"CAP_NET_ADMIN", "CAP_SYS_ADMIN", "CAP_SYS_PTRACE"})
		if spec.Process.ApparmorProfile != "" {
			t.Fatalf("uid %d: AppArmor profile retained", uid)
		}
		if got := spec.Process.Capabilities.Bounding; len(got) != 3 {
			t.Fatalf("uid %d: bounding capabilities = %v", uid, got)
		}
		if got := spec.Process.Capabilities.Effective; (uid == 0 && len(got) != 3) || (uid != 0 && len(got) != 0) {
			t.Fatalf("uid %d: effective capabilities = %v", uid, got)
		}
		if len(spec.Linux.MaskedPaths) != 1 || spec.Linux.MaskedPaths[0] != "/other" || len(spec.Linux.ReadonlyPaths) != 1 || spec.Linux.ReadonlyPaths[0] != "/other" {
			t.Fatalf("uid %d: proc restrictions retained: masked=%v readonly=%v", uid, spec.Linux.MaskedPaths, spec.Linux.ReadonlyPaths)
		}
	}
}

func TestDisabledRootfsAnnotationDoesNotCreateSpecialMounts(t *testing.T) {
	for _, raw := range []string{"", "false"} {
		spec := &specs.Spec{Annotations: map[string]string{liteRootfsAnnotation: raw}}
		if liteRootfsEnabled(spec) {
			t.Fatalf("annotation %q enabled rootfs", raw)
		}
		if err := addLiteSpecialMounts(spec, "container"); err != nil {
			t.Fatalf("annotation %q: %v", raw, err)
		}
		if len(spec.Mounts) != 0 {
			t.Fatalf("annotation %q created mounts", raw)
		}
	}
}

func TestLiteWritableCgroupOnlyChangesCgroupMount(t *testing.T) {
	spec := &specs.Spec{Mounts: []specs.Mount{
		{Type: "cgroup", Source: "cgroup", Destination: "/sys/fs/cgroup", Options: []string{"nosuid", "ro"}},
		{Type: "bind", Source: "/host", Destination: "/data", Options: []string{"ro"}},
	}}
	liteWritableCgroup(spec)
	if got := spec.Mounts[0].Options[1]; got != "rw" {
		t.Fatalf("cgroup mount remains %q", got)
	}
	if got := spec.Mounts[1].Options[0]; got != "ro" {
		t.Fatalf("unrelated bind mount changed to %q", got)
	}
}

func TestLiteSpecialBindMountMapsUserNamespace(t *testing.T) {
	linux := &specs.Linux{
		UIDMappings: []specs.LinuxIDMapping{{ContainerID: 0, HostID: 33357824, Size: 65536}},
		GIDMappings: []specs.LinuxIDMapping{{ContainerID: 0, HostID: 33357824, Size: 65536}},
	}
	mount, err := liteSpecialBindMount(linux, "/host/pvc/special", "/var/lib/rancher/k3s")
	if err != nil {
		t.Fatal(err)
	}
	if len(mount.UIDMappings) != 1 || len(mount.GIDMappings) != 1 || mount.Options[1] != "rshared" || mount.Options[2] != "ridmap" {
		t.Fatalf("special mount is not recursively idmapped: %#v", mount)
	}
	if _, err := liteSpecialBindMount(&specs.Linux{UIDMappings: linux.UIDMappings}, "/host/pvc/special", "/var/lib/rancher/k3s"); err == nil {
		t.Fatal("expected incomplete UID/GID mappings to be rejected")
	}
	plain, err := liteSpecialBindMount(nil, "/host/pvc/special", "/var/lib/rancher/k3s")
	if err != nil || len(plain.UIDMappings) != 0 || len(plain.Options) != 2 || plain.Options[1] != "rshared" {
		t.Fatalf("plain special mount = %#v, %v", plain, err)
	}
}

func TestReplaceLiteSpecialMountRemovesOriginalVolumeMount(t *testing.T) {
	spec := &specs.Spec{Mounts: []specs.Mount{
		{Source: "/kubelet/volume", Destination: "/var/lib/kubelet", Type: "bind", Options: []string{"rbind", "rprivate"}},
		{Source: "/other", Destination: "/other", Type: "bind"},
	}}
	replacement := specs.Mount{Source: "/pvc/special", Destination: "/var/lib/kubelet", Type: "bind", Options: []string{"rbind", "rshared"}}
	replaceLiteSpecialMount(spec, replacement)
	if len(spec.Mounts) != 2 || spec.Mounts[0].Destination != "/other" || spec.Mounts[1].Source != replacement.Source {
		t.Fatalf("special mount did not replace original volume mount: %#v", spec.Mounts)
	}
}

func TestDirEmptyIgnoresLostFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lost+found"), 0o700); err != nil {
		t.Fatal(err)
	}
	empty, err := dirEmpty(dir)
	if err != nil || !empty {
		t.Fatalf("dirEmpty() = %v, %v; want true, nil", empty, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	empty, err = dirEmpty(dir)
	if err != nil || empty {
		t.Fatalf("dirEmpty() = %v, %v; want false, nil", empty, err)
	}
}

func TestInitLiteVolumesWithoutAnnotation(t *testing.T) {
	podsDir, rootfs := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootfs, "data", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootfs, "data", "nested", "image.txt"), []byte("image"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(podsDir, "pod-uid", "volumes", "kubernetes.io~csi", "data", "mount")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := &specs.Spec{
		Root: &specs.Root{Path: rootfs},
		Annotations: map[string]string{
			liteContainerName: "app",
			liteSandboxUID:    "pod-uid",
		},
		Mounts: []specs.Mount{{Source: source, Destination: "/data", Type: "bind", Options: []string{"rw"}}},
	}
	if err := initLiteVolumesAt(spec, podsDir); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, filepath.Join(source, "nested", "image.txt"))); got != "image" {
		t.Fatalf("initialized content = %q", got)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "image.txt"), []byte("persistent"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := initLiteVolumesAt(spec, podsDir); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, filepath.Join(source, "nested", "image.txt"))); got != "persistent" {
		t.Fatalf("existing content overwritten: %q", got)
	}
}

func TestDetectLitePVCSourceSkipsNonPVCAndValidatesSubPath(t *testing.T) {
	podsDir := t.TempDir()
	emptyDir := filepath.Join(podsDir, "pod-uid", "volumes", "kubernetes.io~empty-dir", "cache")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := detectLitePVCSourceAt(emptyDir, "pod-uid", "app", podsDir); ok {
		t.Fatal("emptyDir must not be treated as PVC")
	}
	if err := os.MkdirAll(filepath.Join(podsDir, "pod-uid", "volumes", "kubernetes.io~csi", "data", "mount"), 0o755); err != nil {
		t.Fatal(err)
	}
	subpath := filepath.Join(podsDir, "pod-uid", "volume-subpaths", "data", "app", "0")
	if err := os.MkdirAll(subpath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := detectLitePVCSourceAt(subpath, "pod-uid", "app-123", podsDir); !ok {
		t.Fatal("CSI subPath must be detected")
	}
}

func TestDetectLitePVCSourceAcceptsK3sLocalPath(t *testing.T) {
	podsDir := t.TempDir()
	source := filepath.Join(podsDir, "pod-uid", "volumes", "kubernetes.io~local-volume", "pvc-uid")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := detectLitePVCSourceAt(source, "pod-uid", "app", podsDir); !ok || got != source {
		t.Fatalf("local-path PVC = %q, %v; want %q, true", got, ok, source)
	}
}

func TestCopyDirPreservesSymlinkAndMode(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "dst")
	if err := os.Mkdir(filepath.Join(src, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(src, "nested", "file")
	if err := os.WriteFile(file, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nested/file", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.Stat(filepath.Join(dst, "nested", "file"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(dst, "nested", "file"))) != "payload" || copied.Mode().Perm() != info.Mode().Perm() {
		t.Fatalf("copied file content or mode mismatch: %q %o", mustRead(t, filepath.Join(dst, "nested", "file")), copied.Mode().Perm())
	}
	link, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil || link != "nested/file" {
		t.Fatalf("copied link = %q, %v", link, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
