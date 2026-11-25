package main

import (
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestEnsureKmsgShim(t *testing.T) {
	spec := &specs.Spec{}
	ensureKmsgShim(spec)
	ensureKmsgShim(spec)
	if len(spec.Mounts) != 1 || spec.Mounts[0].Destination != "/dev/kmsg" || spec.Mounts[0].Source != "/dev/null" {
		t.Fatalf("unexpected /dev/kmsg shim: %#v", spec.Mounts)
	}
	explicit := &specs.Spec{Mounts: []specs.Mount{{Destination: "/dev/kmsg", Source: "/test/kmsg"}}}
	ensureKmsgShim(explicit)
	if len(explicit.Mounts) != 1 || explicit.Mounts[0].Source != "/test/kmsg" {
		t.Fatalf("existing /dev/kmsg mount replaced: %#v", explicit.Mounts)
	}
}
