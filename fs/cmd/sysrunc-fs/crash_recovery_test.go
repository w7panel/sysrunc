package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/w7panel/sysrunc/fs/state"
)

func TestDetachCrashedHostMountsRejectsUncheckpointedData(t *testing.T) {
	root := t.TempDir()
	known := filepath.Join(root, "known")
	if err := os.Mkdir(known, 0700); err != nil {
		t.Fatal(err)
	}
	if err := detachCrashedHostMounts(root, &state.RestartState{Containers: []state.RestartContainer{{ID: "known"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(known); !os.IsNotExist(err) {
		t.Fatalf("checkpointed empty mountpoint remains: %v", err)
	}
	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "data"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := detachCrashedHostMounts(root, &state.RestartState{}); err == nil {
		t.Fatal("accepted uncheckpointed data")
	}
	if data, err := os.ReadFile(filepath.Join(other, "data")); err != nil || string(data) != "keep" {
		t.Fatalf("uncheckpointed data changed: %q, %v", data, err)
	}
}

func TestCheckpointProcessIdentity(t *testing.T) {
	pid := uint32(os.Getpid())
	start, err := state.ProcessStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &state.RestartState{Containers: []state.RestartContainer{{PID: pid, StartTime: start}}}
	if live, err := hasLiveCheckpointContainers(checkpoint); err != nil || !live {
		t.Fatalf("current process not recognized: live=%t err=%v", live, err)
	}
	checkpoint.Containers[0].StartTime++
	if live, err := hasLiveCheckpointContainers(checkpoint); err != nil || live {
		t.Fatalf("reused PID accepted: live=%t err=%v", live, err)
	}
}
