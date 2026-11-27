package implementations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/w7panel/sysrunc/fs/domain"
)

func TestHashsizeFallbackUsesContainerCache(t *testing.T) {
	buckets := filepath.Join(t.TempDir(), "nf_conntrack_buckets")
	if err := os.WriteFile(buckets, []byte("262144\n"), 0600); err != nil {
		t.Fatal(err)
	}
	node := qdiscTestNode{path: filepath.Join(t.TempDir(), "hashsize")}
	a := &qdiscTestContainer{data: make(map[string][]byte)}
	b := &qdiscTestContainer{data: make(map[string][]byte)}
	read := func(c *qdiscTestContainer) string {
		t.Helper()
		req := &domain.HandlerRequest{Container: c, Data: make([]byte, 32)}
		n, err := readHashsizeWithoutHost(node, req, buckets)
		if err != nil {
			t.Fatal(err)
		}
		return string(req.Data[:n])
	}
	if got := read(a); got != "262144\n" {
		t.Fatalf("initial hashsize = %q", got)
	}
	if err := a.SetData(node.Path(), 0, []byte("4096\n")); err != nil {
		t.Fatal(err)
	}
	if got := read(a); got != "4096\n" {
		t.Fatalf("cached hashsize = %q", got)
	}
	if got := read(b); got != "262144\n" {
		t.Fatalf("hashsize leaked to second container: %q", got)
	}
	if err := os.Remove(buckets); err != nil {
		t.Fatal(err)
	}
	if got := read(a); got != "4096\n" {
		t.Fatalf("cached hashsize lost after fallback disappeared: %q", got)
	}
	if err := os.WriteFile(buckets, []byte("bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHashsizeWithoutHost(node, &domain.HandlerRequest{Container: &qdiscTestContainer{data: make(map[string][]byte)}, Data: make([]byte, 32)}, buckets); err == nil {
		t.Fatal("invalid bucket count accepted")
	}
}
