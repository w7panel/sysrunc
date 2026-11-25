package main

import (
	"path/filepath"

	"github.com/opencontainers/runtime-spec/specs-go"
)

// Nested kubelet probes /dev/kmsg even when running in a user namespace.
// The host kernel log is not exposed to the container; sys-container Pods
// receive the harmless /dev/null device at that path, as with Sysbox.
func ensureKmsgShim(spec *specs.Spec) {
	for _, mount := range spec.Mounts {
		if filepath.Clean(mount.Destination) == "/dev/kmsg" {
			return
		}
	}
	spec.Mounts = append(spec.Mounts, specs.Mount{
		Destination: "/dev/kmsg",
		Source:      "/dev/null",
		Type:        "bind",
		Options:     []string{"bind", "rprivate"},
	})
}
