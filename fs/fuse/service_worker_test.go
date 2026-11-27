package fuse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type failingMountpointWorker struct{ err error }

func (w failingMountpointWorker) PrepareMountpoint(context.Context, string) error { return w.err }
func (w failingMountpointWorker) RemoveMountpoint(context.Context, string) error  { return w.err }

func TestPrepareMountpointDoesNotBypassFailedWorker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "container")
	want := errors.New("FUSE worker unavailable")
	service := NewFuseServerService()
	service.SetMountpointWorker(failingMountpointWorker{err: want})
	if err := service.prepareMountpoint(path); !errors.Is(err, want) {
		t.Fatalf("prepare error = %v, want %v", err, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("manager created mountpoint after worker failure: %v", err)
	}
}
