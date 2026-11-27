package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/w7panel/sysrunc/libs/capability"
)

const (
	liteRootfsAnnotation          = "sysrunc.io/rootfs-rw-layer"
	liteNestedContainerAnnotation = "sysrunc.io/nested-system-container"
	liteContainerName             = "io.kubernetes.cri.container-name"
	liteSandboxUID                = "io.kubernetes.cri.sandbox-uid"
	liteHandoffDir                = "/run/sysrunc/rootfs-pvc-handoff"
	liteKubeletPodsDir            = "/var/lib/kubelet/pods"
)

type liteRootfsEntry struct {
	Name, VolumeName, Path  string
	PersistentSpecialMounts bool     `json:"persistentSpecialMounts"`
	SpecialPath             []string `json:"specialPath"`
}
type liteHandoff struct{ SnapshotKey, PodUID, ContainerName, VolumeName, PVCMountPath string }

func prepareLiteSpec(spec *specs.Spec, id string) error {
	if err := initLiteVolumes(spec); err != nil {
		return err
	}
	if err := addLiteSpecialMounts(spec, id); err != nil {
		return err
	}
	if spec.Annotations[liteNestedContainerAnnotation] == "true" {
		return prepareNestedSystemSpec(spec)
	}
	return nil
}

// prepareNestedSystemSpec restores the system-container OCI settings that
// Sysbox applied before invoking runc. Rootfs intent identifies this path;
// ordinary Sysrunc containers retain their requested security settings.
func prepareNestedSystemSpec(spec *specs.Spec) error {
	if spec.Process == nil || spec.Linux == nil {
		return fmt.Errorf("nested system container requires process and linux specs")
	}
	runtimeCaps, err := nestedRuntimeCaps()
	if err != nil {
		return err
	}
	applyNestedSystemSecurity(spec, runtimeCaps)
	return nil
}

func nestedRuntimeCaps() ([]string, error) {
	caps, err := capability.NewPid2(0)
	if err != nil {
		return nil, fmt.Errorf("read runtime capabilities: %w", err)
	}
	if err := caps.Load(); err != nil {
		return nil, fmt.Errorf("load runtime capabilities: %w", err)
	}
	runtimeCaps := strings.FieldsFunc(caps.StringCap(capability.EFFECTIVE, capability.OCI_STRING), func(r rune) bool { return r == ',' || r == ' ' })
	return runtimeCaps, nil
}

func applyNestedSystemSecurity(spec *specs.Spec, runtimeCaps []string) {
	applyNestedSystemProcessSecurity(spec.Process, runtimeCaps)
	spec.Linux.MaskedPaths = removeNestedPaths(spec.Linux.MaskedPaths, "/proc", "/proc/sys", "/proc/kcore", "/proc/kallsyms", "/proc/kmsg")
	spec.Linux.ReadonlyPaths = removeNestedPaths(spec.Linux.ReadonlyPaths, "/proc", "/proc/sys")
}

func applyNestedSystemProcessSecurity(process *specs.Process, runtimeCaps []string) {
	if process.Capabilities == nil {
		process.Capabilities = &specs.LinuxCapabilities{}
	}
	process.Capabilities.Bounding = append([]string(nil), runtimeCaps...)
	process.Capabilities.Effective = nil
	process.Capabilities.Inheritable = nil
	process.Capabilities.Permitted = nil
	process.Capabilities.Ambient = nil
	if process.User.UID == 0 {
		process.Capabilities.Effective = append([]string(nil), runtimeCaps...)
		process.Capabilities.Inheritable = append([]string(nil), runtimeCaps...)
		process.Capabilities.Permitted = append([]string(nil), runtimeCaps...)
		process.Capabilities.Ambient = append([]string(nil), runtimeCaps...)
	}
	process.ApparmorProfile = ""
}

func prepareNestedSystemProcess(process *specs.Process) error {
	runtimeCaps, err := nestedRuntimeCaps()
	if err != nil {
		return err
	}
	applyNestedSystemProcessSecurity(process, runtimeCaps)
	return nil
}

func removeNestedPaths(paths []string, allowed ...string) []string {
	result := paths[:0]
	for _, path := range paths {
		remove := false
		for _, candidate := range allowed {
			if path == candidate {
				remove = true
				break
			}
		}
		if !remove {
			result = append(result, path)
		}
	}
	return result
}

func initLiteVolumes(spec *specs.Spec) error {
	return initLiteVolumesAt(spec, liteKubeletPodsDir)
}

func initLiteVolumesAt(spec *specs.Spec, podsDir string) error {
	if spec.Root == nil || spec.Root.Path == "" {
		return nil
	}
	name := spec.Annotations[liteContainerName]
	podUID := spec.Annotations[liteSandboxUID]
	if name == "" || podUID == "" {
		return nil
	}
	rootfs, err := filepath.Abs(spec.Root.Path)
	if err != nil {
		return fmt.Errorf("resolve container rootfs: %w", err)
	}
	for _, mount := range spec.Mounts {
		if mount.Type != "bind" || liteMountReadOnly(mount) {
			continue
		}
		source, ok := detectLitePVCSourceAt(mount.Source, podUID, name, podsDir)
		if !ok {
			continue
		}
		relative := strings.TrimPrefix(filepath.Clean(mount.Destination), string(filepath.Separator))
		if relative == "." || relative == "" {
			continue
		}
		imagePath, err := liteImageDir(rootfs, relative)
		if err != nil {
			return fmt.Errorf("validate image volume path %s: %w", mount.Destination, err)
		}
		info, err := os.Lstat(imagePath)
		if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect image volume path %s: %w", mount.Destination, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("image volume path %s is a symlink", mount.Destination)
		}
		if err := seedLitePVC(imagePath, source, spec.Linux); err != nil {
			return fmt.Errorf("initialize PVC mount %s: %w", mount.Destination, err)
		}
	}
	return nil
}

func liteMountReadOnly(mount specs.Mount) bool {
	for _, option := range mount.Options {
		if option == "ro" {
			return true
		}
	}
	return false
}

// detectLitePVCSource accepts only recognized PVC kubelet paths for the current Pod.
// This comprises all CSI drivers (kubernetes.io~csi) and K3s local-path
// (kubernetes.io~local-volume).
// It intentionally skips emptyDir, projected volumes, hostPath, and unknown
// storage plugins because runc-lite has no Kubernetes API client.
func detectLitePVCSource(source, podUID, containerName string) (string, bool) {
	return detectLitePVCSourceAt(source, podUID, containerName, liteKubeletPodsDir)
}

func detectLitePVCSourceAt(source, podUID, containerName, podsDir string) (string, bool) {
	cleanSource, err := filepath.Abs(source)
	if err != nil {
		return "", false
	}
	podRoot := filepath.Join(podsDir, podUID)
	volumeRoots := []string{
		filepath.Join(podRoot, "volumes", "kubernetes.io~csi"),
		// k3s local-path exposes dynamically provisioned PVCs through the
		// in-tree local-volume path. It has the same per-Pod, kubelet-owned
		// layout as CSI and is safe to initialize without an API client.
		filepath.Join(podRoot, "volumes", "kubernetes.io~local-volume"),
	}
	for _, directRoot := range volumeRoots {
		if rel, err := filepath.Rel(directRoot, cleanSource); err == nil {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) == 1 && parts[0] != "" {
				return cleanSource, true
			}
			if len(parts) == 2 && parts[0] != "" && parts[1] == "mount" {
				return cleanSource, true
			}
		}
	}
	subpathRoot := filepath.Join(podRoot, "volume-subpaths")
	if rel, err := filepath.Rel(subpathRoot, cleanSource); err == nil {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return "", false
		}
		containerMatches := parts[1] == containerName || strings.HasPrefix(containerName, parts[1]+"-")
		volumeMatches := strings.HasPrefix(parts[0], "pvc-")
		if !volumeMatches {
			for _, directRoot := range volumeRoots {
				if _, err = os.Stat(filepath.Join(directRoot, parts[0], "mount")); err == nil {
					volumeMatches = true
					break
				}
				if _, err = os.Stat(filepath.Join(directRoot, parts[0])); err == nil {
					volumeMatches = true
					break
				}
			}
		}
		if containerMatches && volumeMatches {
			return cleanSource, true
		}
	}
	return "", false
}

func addLiteSpecialMounts(spec *specs.Spec, id string) error {
	raw, annotation := liteRootfsAnnotationValue(spec)
	if !liteRootfsEnabled(spec) {
		return nil
	}
	var entries []liteRootfsEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return fmt.Errorf("decode %s: %w", annotation, err)
	}
	name := spec.Annotations[liteContainerName]
	for _, entry := range entries {
		if entry.Name != name || !entry.PersistentSpecialMounts {
			continue
		}
		handoff, err := loadLiteHandoff(id)
		if err != nil {
			return err
		}
		if handoff.ContainerName != name || handoff.VolumeName != entry.VolumeName || handoff.SnapshotKey != id {
			return fmt.Errorf("persistent special handoff does not match container")
		}
		if filepath.IsAbs(entry.Path) || entry.Path == ".." || strings.HasPrefix(entry.Path, "../") || strings.Contains("/"+entry.Path+"/", "/../") {
			return fmt.Errorf("invalid persistent special layer path %q", entry.Path)
		}
		spec.Annotations[liteNestedContainerAnnotation] = "true"
		liteWritableCgroup(spec)
		root := filepath.Join(handoff.PVCMountPath, filepath.Clean(entry.Path), "special")
		if _, err := liteImageDir(handoff.PVCMountPath, filepath.Join(entry.Path, "special")); err != nil {
			return fmt.Errorf("validate persistent special root: %w", err)
		}
		if err := os.MkdirAll(root, 0755); err != nil {
			return err
		}
		lock, err := openLiteDirLock(filepath.Join(root, ".sysrunc-special.lock"))
		if err != nil {
			return err
		}
		paths := []string{"/var/lib/docker", "/var/lib/kubelet", "/var/lib/k0s", "/var/lib/rancher/k3s", "/var/lib/rancher/rke2", "/var/lib/buildkit", "/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs"}
		paths = append(paths, entry.SpecialPath...)
		for _, dest := range paths {
			if !filepath.IsAbs(dest) || dest == "/" || strings.Contains("/"+strings.Trim(dest, "/")+"/", "/../") || strings.Contains("/"+strings.Trim(dest, "/")+"/", "/./") {
				lock.Close()
				return fmt.Errorf("invalid special mount path %q", dest)
			}
			relative := strings.TrimPrefix(filepath.Clean(dest), "/")
			src := filepath.Join(root, relative)
			if _, err := liteImageDir(root, filepath.Dir(relative)); err != nil {
				lock.Close()
				return fmt.Errorf("validate special mount source %s: %w", dest, err)
			}
			if err := os.MkdirAll(filepath.Dir(src), 0755); err != nil {
				lock.Close()
				return err
			}
			if _, err := liteImageDir(root, filepath.Dir(relative)); err != nil {
				lock.Close()
				return fmt.Errorf("validate special mount source %s: %w", dest, err)
			}
			imagePath, err := liteImageDir(spec.Root.Path, relative)
			if err != nil {
				lock.Close()
				return err
			}
			if err := seedLiteSpecial(imagePath, src, spec.Linux); err != nil {
				lock.Close()
				return fmt.Errorf("initialize special mount %s: %w", dest, err)
			}
			mount, err := liteSpecialBindMount(spec.Linux, src, dest)
			if err != nil {
				lock.Close()
				return err
			}
			replaceLiteSpecialMount(spec, mount)
		}
		if err := lock.Close(); err != nil {
			return err
		}
	}
	return nil
}

func liteRootfsAnnotationValue(spec *specs.Spec) (string, string) {
	return spec.Annotations[liteRootfsAnnotation], liteRootfsAnnotation
}

func liteRootfsEnabled(spec *specs.Spec) bool {
	raw, _ := liteRootfsAnnotationValue(spec)
	raw = strings.TrimSpace(raw)
	return raw != "" && raw != "false"
}

func replaceLiteSpecialMount(spec *specs.Spec, replacement specs.Mount) {
	dest := filepath.Clean(replacement.Destination)
	mounts := spec.Mounts[:0]
	for _, mount := range spec.Mounts {
		if filepath.Clean(mount.Destination) != dest {
			mounts = append(mounts, mount)
		}
	}
	spec.Mounts = append(mounts, replacement)
}

// liteWritableCgroup lets the cgroup manager delegate the nested hierarchy.
func liteWritableCgroup(spec *specs.Spec) {
	for i := range spec.Mounts {
		m := &spec.Mounts[i]
		if m.Type != "cgroup" || filepath.Clean(m.Destination) != "/sys/fs/cgroup" {
			continue
		}
		for j, option := range m.Options {
			if option == "ro" {
				m.Options[j] = "rw"
			}
		}
	}
}

func liteSpecialBindMount(linux *specs.Linux, src, dest string) (specs.Mount, error) {
	// Kubelet and CSI drivers bind submounts below these persistent paths.
	// Propagate those mounts within the nested K3s mount namespace.
	mount := specs.Mount{Source: src, Destination: dest, Type: "bind", Options: []string{"rbind", "rshared"}}
	if linux == nil || (len(linux.UIDMappings) == 0 && len(linux.GIDMappings) == 0) {
		return mount, nil
	}
	if len(linux.UIDMappings) == 0 || len(linux.GIDMappings) == 0 {
		return mount, fmt.Errorf("persistent special mount %s requires both UID and GID mappings", dest)
	}
	// The PVC is owned by the host. Map the whole special tree into the
	// K3s Server's user namespace so root can write it.
	mount.Options = append(mount.Options, "ridmap")
	mount.UIDMappings = append([]specs.LinuxIDMapping{}, linux.UIDMappings...)
	mount.GIDMappings = append([]specs.LinuxIDMapping{}, linux.GIDMappings...)
	return mount, nil
}

func loadLiteHandoff(id string) (liteHandoff, error) {
	sum := sha256.Sum256([]byte(id))
	path := filepath.Join(liteHandoffDir, hex.EncodeToString(sum[:])+".json")
	f, err := os.Open(path)
	if err != nil {
		return liteHandoff{}, fmt.Errorf("open persistent special handoff: %w", err)
	}
	defer f.Close()
	var h liteHandoff
	if err := json.NewDecoder(f).Decode(&h); err != nil {
		return h, err
	}
	return h, nil
}

func dirEmpty(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	for {
		names, err := f.Readdirnames(32)
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		for _, n := range names {
			if n != "lost+found" {
				return false, nil
			}
		}
	}
}

func copyDir(src, dst string) error { return copyLiteDir(src, dst, nil) }
