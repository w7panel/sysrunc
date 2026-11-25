package fuse

import (
	"os"
	"testing"
)

func TestSameUserNamespace(t *testing.T) {
	if !sameUserNamespace(uint32(os.Getpid())) {
		t.Fatal("current process must share its own user namespace")
	}
	if sameUserNamespace(0) {
		t.Fatal("missing process must not be treated as the current user namespace")
	}
}
