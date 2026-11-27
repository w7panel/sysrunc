package main

import (
	"testing"

	"github.com/w7panel/sysrunc/snapshotter/cmd/sysrunc-snapshotter/version"
)

func TestParseArgsRequiresContainerdSocket(t *testing.T) {
	_, _, _, ok := parseArgs([]string{"sysrunc-snapshotter", "--socket", "/run/sock", "--root", "/root"})
	if ok {
		t.Fatal("parseArgs accepted missing containerd socket")
	}
}

func TestParseArgsReturnsContainerdSocket(t *testing.T) {
	address, root, socket, ok := parseArgs([]string{
		"sysrunc-snapshotter",
		"--socket", "/run/sock",
		"--root", "/root",
		"--containerd-socket", "/run/k3s/containerd/containerd.sock",
	})
	if !ok {
		t.Fatal("parseArgs rejected valid args")
	}
	if address != "/run/sock" || root != "/root" || socket != "/run/k3s/containerd/containerd.sock" {
		t.Fatalf("args = %q %q %q", address, root, socket)
	}
}

func TestVersionFlagShape(t *testing.T) {
	if version.Version == "" || version.Revision == "" {
		t.Fatal("version metadata must be printable")
	}
}
