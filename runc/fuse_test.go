package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestEnsureFuseDeviceAddsMountAndAccess(t *testing.T) {
	spec := &specs.Spec{}
	for i := 0; i < 2; i++ {
		if err := ensureFuseDevice(spec, "/dev/null"); err != nil {
			t.Fatal(err)
		}
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Destination != fuseDevicePath || spec.Mounts[0].Source != "/dev/null" {
		t.Fatalf("unexpected FUSE mount: %+v", spec.Mounts)
	}
	if spec.Linux == nil || len(spec.Linux.Devices) != 1 || len(spec.Linux.Resources.Devices) != 1 {
		t.Fatalf("missing or duplicate FUSE access: %+v", spec.Linux)
	}
	device := spec.Linux.Devices[0]
	if device.Path != fuseDevicePath || device.Type != "c" || device.Major != fuseDeviceMajor ||
		device.Minor != fuseDeviceMinor || device.FileMode == nil || *device.FileMode != 0o666 {
		t.Fatalf("unexpected FUSE device: %+v", device)
	}
	rule := spec.Linux.Resources.Devices[0]
	if !rule.Allow || rule.Type != "c" || rule.Major == nil || rule.Minor == nil ||
		*rule.Major != fuseDeviceMajor || *rule.Minor != fuseDeviceMinor || rule.Access != "rwm" {
		t.Fatalf("unexpected FUSE cgroup rule: %+v", rule)
	}
}

func TestEnsureFuseDevicePreservesExplicitMount(t *testing.T) {
	spec := &specs.Spec{Mounts: []specs.Mount{{Source: "/explicit/fuse", Destination: fuseDevicePath, Type: "bind"}}}
	if err := ensureFuseDevice(spec, "/missing/fuse"); err != nil {
		t.Fatal(err)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Source != "/explicit/fuse" {
		t.Fatalf("explicit FUSE mount changed: %+v", spec.Mounts)
	}
	if len(spec.Linux.Devices) != 1 || len(spec.Linux.Resources.Devices) != 1 {
		t.Fatal("explicit FUSE mount lacks device access")
	}
}

func TestEnsureFuseDeviceRejectsMissingOrNonDevice(t *testing.T) {
	for _, source := range []string{filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		if err := ensureFuseDevice(&specs.Spec{}, source); err == nil {
			t.Fatalf("expected %q to be rejected", source)
		}
	}
	if info, err := os.Stat("/dev/null"); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("test requires /dev/null as a character device")
	}
}
