package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w7panel/sysrunc/fs/state"
)

func TestReadFSRestartMigratesLegacyCheckpoint(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "shm", "checkpoint.json")
	legacy := filepath.Join(dir, "run", "checkpoint.json")
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	old := &state.RestartState{Version: 1, BootID: strings.TrimSpace(string(boot)), Clean: true}
	if err := state.WriteRestart(legacy, old); err != nil {
		t.Fatal(err)
	}
	got, err := readFSRestart(primary, legacy, true)
	if err != nil || got == nil || !got.Clean {
		t.Fatalf("legacy checkpoint = %+v, %v", got, err)
	}
	newState := *old
	newState.Clean = false
	if err := state.WriteRestart(primary, &newState); err != nil {
		t.Fatal(err)
	}
	got, err = readFSRestart(primary, legacy, true)
	if err != nil || got == nil || got.Clean {
		t.Fatalf("primary checkpoint = %+v, %v", got, err)
	}
}
