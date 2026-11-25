package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/runtime-spec/specs-go"
)

const (
	fuseDevicePath  = "/dev/fuse"
	fuseDeviceMajor = int64(10)
	fuseDeviceMinor = int64(229)
)

// ensureFuseDevice adds host /dev/fuse to FS-enabled system containers. The
// Kubernetes Pod does not need to declare a hostPath volume for this device.
func ensureFuseDevice(spec *specs.Spec, source string) error {
	if spec.Linux == nil {
		spec.Linux = &specs.Linux{}
	}
	hasMount := false
	for _, mount := range spec.Mounts {
		if filepath.Clean(mount.Destination) == fuseDevicePath {
			hasMount = true
			break
		}
	}
	if !hasMount {
		info, err := os.Stat(source)
		if err != nil {
			return fmt.Errorf("FS mode requires host %s: %w", source, err)
		}
		if info.Mode()&os.ModeCharDevice == 0 {
			return fmt.Errorf("FS mode requires host %s to be a character device", source)
		}
		spec.Mounts = append(spec.Mounts, specs.Mount{
			Destination: fuseDevicePath,
			Source:      source,
			Type:        "bind",
			Options:     []string{"rbind", "rprivate"},
		})
	}
	hasDevice := false
	for _, device := range spec.Linux.Devices {
		if filepath.Clean(device.Path) == fuseDevicePath {
			hasDevice = true
			break
		}
	}
	if !hasDevice {
		mode := os.FileMode(0o666)
		spec.Linux.Devices = append(spec.Linux.Devices, specs.LinuxDevice{
			Path: fuseDevicePath, Type: "c", Major: fuseDeviceMajor, Minor: fuseDeviceMinor,
			FileMode: &mode,
		})
	}
	if spec.Linux.Resources == nil {
		spec.Linux.Resources = &specs.LinuxResources{}
	}
	for _, rule := range spec.Linux.Resources.Devices {
		if rule.Allow && rule.Type == "c" && rule.Major != nil && rule.Minor != nil &&
			*rule.Major == fuseDeviceMajor && *rule.Minor == fuseDeviceMinor &&
			strings.Contains(rule.Access, "r") && strings.Contains(rule.Access, "w") {
			return nil
		}
	}
	major, minor := fuseDeviceMajor, fuseDeviceMinor
	spec.Linux.Resources.Devices = append(spec.Linux.Resources.Devices, specs.LinuxDeviceCgroup{
		Allow: true, Type: "c", Major: &major, Minor: &minor, Access: "rwm",
	})
	return nil
}
