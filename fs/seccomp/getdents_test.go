package seccomp

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	fusefs "bazil.org/fuse/fs"
	"golang.org/x/sys/unix"
)

func TestEncodeRandomDirentsPreservesFuseCookies(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"uuid", "boot_id"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0444); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	const parentInode = 1218751057
	first, next, ok := encodeRandomDirents(entries, parentInode, 0, 32)
	if !ok || len(first) != 32 || next != 32 ||
		binary.LittleEndian.Uint64(first[0:8]) != fusefs.GenerateDynamicInode(parentInode, "boot_id") ||
		binary.LittleEndian.Uint64(first[8:16]) != 32 ||
		binary.LittleEndian.Uint16(first[16:18]) != 32 || first[18] != unix.DT_REG ||
		string(first[19:26]) != "boot_id" || first[26] != 0 {
		t.Fatalf("first dirent = %v, next=%d, ok=%v", first, next, ok)
	}
	second, next, ok := encodeRandomDirents(entries, parentInode, 32, 24)
	if !ok || len(second) != 24 || next != 56 ||
		binary.LittleEndian.Uint64(second[8:16]) != 56 ||
		string(second[19:23]) != "uuid" {
		t.Fatalf("second dirent = %v, next=%d, ok=%v", second, next, ok)
	}
	eof, next, ok := encodeRandomDirents(entries, parentInode, 56, 8192)
	if !ok || len(eof) != 0 || next != 56 {
		t.Fatalf("EOF = %v, next=%d, ok=%v", eof, next, ok)
	}
	if _, _, ok := encodeRandomDirents(entries, parentInode, 1, 8192); ok {
		t.Fatal("accepted a non-cookie directory offset")
	}
	if _, _, ok := encodeRandomDirents(entries, parentInode, 0, 31); ok {
		t.Fatal("accepted a buffer too small for the next record")
	}
}
