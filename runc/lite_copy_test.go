package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSeedLitePVCRetriesInterruptedCopy(t *testing.T) {
	image, pvc, bin := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(image, "image-file"), []byte("complete"), 0640); err != nil {
		t.Fatal(err)
	}
	realPath := os.Getenv("PATH")
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte("#!/bin/sh\nfor last; do :; done\nprintf partial > \"$last/image-file\"\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+realPath)
	if err := seedLitePVC(image, pvc, nil); err == nil {
		t.Fatal("interrupted copy unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(pvc, liteVolumeInitMarker)); err != nil {
		t.Fatalf("missing retry marker: %v", err)
	}
	if err := os.Setenv("PATH", realPath); err != nil {
		t.Fatal(err)
	}
	if err := seedLitePVC(image, pvc, nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(pvc, "image-file")); err != nil || string(data) != "complete" {
		t.Fatalf("retried copy = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(pvc, liteVolumeInitMarker)); !os.IsNotExist(err) {
		t.Fatalf("retry marker remains: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pvc, "image-file"), []byte("user-data"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := seedLitePVC(image, pvc, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(pvc, "image-file")); string(data) != "user-data" {
		t.Fatalf("existing PVC data overwritten: %q", data)
	}
}

func TestSeedLitePVCSerializesFirstWriters(t *testing.T) {
	image, pvc := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(image, "file"), []byte("image"), 0644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- seedLitePVC(image, pvc, nil)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(pvc, "file")); err != nil || string(data) != "image" {
		t.Fatalf("initialized data = %q, %v", data, err)
	}
}

func TestSeedLiteSpecialPublishesOnlyCompleteCopy(t *testing.T) {
	image, root, bin := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(image, "file"), []byte("image"), 0644); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "special")
	realPath := os.Getenv("PATH")
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+realPath)
	if err := seedLiteSpecial(image, destination, nil); err == nil {
		t.Fatal("failed copy unexpectedly published")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("partial special directory published: %v", err)
	}
	if err := os.Setenv("PATH", realPath); err != nil {
		t.Fatal(err)
	}
	if err := seedLiteSpecial(image, destination, nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "file")); err != nil || string(data) != "image" {
		t.Fatalf("special data = %q, %v", data, err)
	}
}

func TestCopyLiteDirPreservesHardlinkAndXattr(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	file := filepath.Join(source, "file")
	if err := os.WriteFile(file, []byte("data"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(source, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(file, "user.sysrunc-test", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	if err := copyLiteDir(source, destination, nil); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(filepath.Join(destination, "file"))
	b, _ := os.Stat(filepath.Join(destination, "hardlink"))
	if !os.SameFile(a, b) || a.Mode().Perm() != 0640 {
		t.Fatal("copy lost hardlink or mode")
	}
	buf := make([]byte, 20)
	n, err := unix.Getxattr(filepath.Join(destination, "file"), "user.sysrunc-test", buf)
	if err != nil || strings.TrimSpace(string(buf[:n])) != "value" {
		t.Fatalf("copy lost xattr: %q, %v", buf[:n], err)
	}
}

func TestLiteImageDirRejectsSymlinkAncestor(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := liteImageDir(root, "linked/data"); err == nil {
		t.Fatal("accepted symlink ancestor")
	}
	if _, err := liteImageDir(root, "../outside"); err == nil {
		t.Fatal("accepted path escaping rootfs")
	}
}
